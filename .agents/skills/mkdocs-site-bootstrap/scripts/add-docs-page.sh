#!/usr/bin/env bash
# add-docs-page.sh — Create a new docs/ page and insert it into mkdocs.yml nav.
#
# Bash 3.2 compatible. Requires `yq` (mikefarah/yq v4+) for nav editing.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SKILL_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ASSETS="$SKILL_DIR/assets"

usage() {
  cat <<'EOF'
Usage: add-docs-page.sh [OPTIONS]

Create docs/<section>/<slug>.md from a template and insert a nav entry into
mkdocs.yml under the matching section heading.

Options:
  --section NAME      Section name (must match an existing top-level nav section
                      heading in mkdocs.yml, e.g. "Workflows", "Reference").
                      Use "_root" to add at top level (no section).
  --title TITLE       Display title (e.g., "My new workflow"). Required.
  --slug SLUG         Filename slug without .md (default: derived from --title).
  --template PATH     Page template (default: skill's assets/page.md.template).
  --target-dir DIR    Repo root (default: walk up from CWD looking for mkdocs.yml).
  --lang LANG         Create the page only for LANG (e.g. zh-TW). Skips the
                      default-language file and the nav-insertion step
                      (mkdocs-static-i18n auto-discovers language siblings).
                      If omitted, creates the default-language page plus
                      *.<LANG>.md stubs for every non-default language listed
                      in .skills/preferences.yaml.
  --dry-run           Print actions without writing.
  --force             Overwrite existing page file.
  --help, -h          Show this help and exit.

Examples:
  add-docs-page.sh --section Workflows --title "My new workflow"
  add-docs-page.sh --section Reference --title "API schema" --slug api-schema
  add-docs-page.sh --section _root --title "About" --slug about
  add-docs-page.sh --section _root --title "About" --slug about --lang zh-TW

Exit codes:
  0  success (idempotent: re-running with same slug is a no-op)
  1  invalid arguments
  2  mkdocs.yml not found
  3  refusing to overwrite (use --force)
  4  yq error or section not found in nav
EOF
}

log()  { printf '%s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit "${2:-1}"; }

SECTION=""
TITLE=""
SLUG=""
TEMPLATE=""
TARGET=""
LANG_ONLY=""
DRY_RUN=0
FORCE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --section)     SECTION="${2:-}"; shift 2 ;;
    --title)       TITLE="${2:-}"; shift 2 ;;
    --slug)        SLUG="${2:-}"; shift 2 ;;
    --template)    TEMPLATE="${2:-}"; shift 2 ;;
    --target-dir)  TARGET="${2:-}"; shift 2 ;;
    --lang)        LANG_ONLY="${2:-}"; shift 2 ;;
    --dry-run)     DRY_RUN=1; shift ;;
    --force)       FORCE=1; shift ;;
    --help|-h)     usage; exit 0 ;;
    -*)            die "unknown flag: $1 (try --help)" 1 ;;
    *)             die "unexpected positional argument: $1" 1 ;;
  esac
done

[ -n "$SECTION" ] || die "--section is required (use _root for top-level)" 1
[ -n "$TITLE" ]   || die "--title is required" 1

command -v yq >/dev/null 2>&1 || die "yq not found in PATH (install: brew install yq)" 4

# Resolve target.
if [ -z "$TARGET" ]; then
  cur="$(pwd)"
  while [ "$cur" != "/" ]; do
    if [ -f "$cur/mkdocs.yml" ]; then TARGET="$cur"; break; fi
    cur="$(dirname "$cur")"
  done
  [ -n "$TARGET" ] || die "could not find mkdocs.yml walking up from $(pwd)" 2
fi
[ -f "$TARGET/mkdocs.yml" ] || die "no mkdocs.yml in $TARGET" 2

# Derive slug from title if not given.
if [ -z "$SLUG" ]; then
  SLUG=$(printf '%s' "$TITLE" | tr '[:upper:]' '[:lower:]' | tr -cs '[:alnum:]' '-' | sed 's/^-//; s/-$//')
fi
case "$SLUG" in
  ''|'.'|'..'|*/*|*\\*|*[!A-Za-z0-9._-]*)
    die "--slug must be one safe filename segment (letters, digits, dot, underscore, hyphen)" 1
    ;;
esac
if [ "$SECTION" != "_root" ]; then
  case "$SECTION" in
    '.'|'..'|*/*|*\\*) die "--section must be a nav heading, not a path" 1 ;;
  esac
fi

# Compute paths.
if [ "$SECTION" = "_root" ]; then
  REL_PATH="${SLUG}.md"
else
  # Section dir = lowercased section name (matches our convention).
  SECTION_DIR=$(printf '%s' "$SECTION" | tr '[:upper:]' '[:lower:]' | tr ' ' '-')
  REL_PATH="${SECTION_DIR}/${SLUG}.md"
fi
ABS_PATH="$TARGET/docs/$REL_PATH"
PAGE_DIR="$(dirname "$ABS_PATH")"
if [ -L "$TARGET/docs" ]; then
  die "refusing symlinked docs directory: $TARGET/docs" 3
elif [ -e "$TARGET/docs" ] && [ ! -d "$TARGET/docs" ]; then
  die "docs path is not a directory: $TARGET/docs" 3
elif [ -L "$PAGE_DIR" ]; then
  die "refusing symlinked page directory: $PAGE_DIR" 3
elif [ -e "$PAGE_DIR" ] && [ ! -d "$PAGE_DIR" ]; then
  die "page directory path is not a directory: $PAGE_DIR" 3
elif [ -L "$ABS_PATH" ]; then
  die "refusing symlinked page: $ABS_PATH" 3
fi

[ -n "$TEMPLATE" ] || TEMPLATE="$ASSETS/page.md.template"
[ -f "$TEMPLATE" ] || die "template not found: $TEMPLATE" 4

STUB_TEMPLATE="$ASSETS/translation-stub.md.template"
MKDOCS="$TARGET/mkdocs.yml"
MKDOCS_TMP=""

cleanup() {
  [ -z "$MKDOCS_TMP" ] || rm -f "$MKDOCS_TMP"
}
trap cleanup EXIT HUP INT TERM

# Helper: write a stub for a non-default language at <base>.<LANG>.md.
write_lang_stub() {
  local base_path="$1" lang="$2"
  local stub_path
  case "$lang" in
    ''|*[!A-Za-z0-9_-]*) die "unsafe language code: $lang" 1 ;;
  esac
  stub_path="${base_path%.md}.${lang}.md"
  [ ! -L "$stub_path" ] || die "refusing symlinked language stub: $stub_path" 3
  if [ -e "$stub_path" ] && [ "$FORCE" = "0" ]; then
    log "Stub already exists: $stub_path (use --force to overwrite)"
    return 0
  fi
  [ -f "$STUB_TEMPLATE" ] || die "stub template missing: $STUB_TEMPLATE" 4
  if [ "$DRY_RUN" = "1" ]; then
    log "[dry-run] write stub $stub_path"
    return 0
  fi
  mkdir -p "$(dirname "$stub_path")"
  sed -e "s|{{PAGE_TITLE}}|${TITLE}|g" \
      -e "s|{{LANG}}|${lang}|g" \
      "$STUB_TEMPLATE" > "$stub_path"
  log "Created stub: $stub_path"
}

# --- --lang shortcut: only create the language stub, skip default + nav ---
if [ -n "$LANG_ONLY" ] && [ "$SECTION" != "_root" ]; then
  LANG_SECTION_INDEX=$(SEC="$SECTION" yq 'env(SEC) as $s | (.nav | to_entries | map(select(.value | tag == "!!map") | select(.value | has($s))) | .[0].key)' \
    "$MKDOCS" 2>/dev/null || echo "null")
  if [ "$LANG_SECTION_INDEX" = "null" ] || [ -z "$LANG_SECTION_INDEX" ]; then
    die "section '$SECTION' not found at top of nav: in $MKDOCS" 4
  fi
fi
if [ -n "$LANG_ONLY" ]; then
  write_lang_stub "$ABS_PATH" "$LANG_ONLY"
  printf '{"page":"%s","lang":"%s","status":"stub_created"}\n' \
    "${REL_PATH%.md}.${LANG_ONLY}.md" "$LANG_ONLY"
  exit 0
fi

# Read and preflight configured language stubs before changing either the page
# or mkdocs.yml, so a bad preference or symlink cannot leave partial state.
PREFS_FILE="$TARGET/.skills/preferences.yaml"
STUBS_CREATED=0
LANGUAGES_LIST=""
if [ -f "$PREFS_FILE" ]; then
  LANGUAGES_LIST=$(yq -r '.mkdocs_site_bootstrap.languages[]?' "$PREFS_FILE" 2>/dev/null | tr '\n' ' ' || true)
fi
if [ -n "$LANGUAGES_LIST" ]; then
  DEFAULT_LANG=""
  for loc in $LANGUAGES_LIST; do
    [ -z "$loc" ] && continue
    if [ -z "$DEFAULT_LANG" ]; then DEFAULT_LANG="$loc"; continue; fi
    case "$loc" in ''|*[!A-Za-z0-9_-]*) die "unsafe language code: $loc" 1 ;; esac
    STUB_PATH="${ABS_PATH%.md}.${loc}.md"
    [ ! -L "$STUB_PATH" ] || die "refusing symlinked language stub: $STUB_PATH" 3
    [ -f "$STUB_TEMPLATE" ] || die "stub template missing: $STUB_TEMPLATE" 4
  done
fi

# Resolve and validate the config update before writing a page. Existing-docs
# skip mode intentionally omits nav; a root page remains auto-discovered rather
# than creating a one-entry explicit nav that hides every existing page.
NAV_HAS_PATH=$(REL_PATH="$REL_PATH" yq -r \
  '[.nav[] | .. | select(tag == "!!str")] | any_c(. == strenv(REL_PATH))' \
  "$MKDOCS" 2>/dev/null || echo "false")
NAV_EXPR=""
if [ "$NAV_HAS_PATH" = "true" ]; then
  NAV_STATUS="already_exists"
elif [ "$SECTION" = "_root" ]; then
  NAV_TYPE=$(yq -r '.nav | type' "$MKDOCS" 2>/dev/null || echo "invalid")
  case "$NAV_TYPE" in
    '!!null') NAV_STATUS="auto_discovered" ;;
    '!!seq')
      NAV_STATUS="created"
      NAV_EXPR='(.nav += [{strenv(TITLE): strenv(REL_PATH)}])'
      ;;
    *) die "nav must be omitted or a list in $MKDOCS" 4 ;;
  esac
else
  IDX=$(SEC="$SECTION" yq 'env(SEC) as $s | (.nav | to_entries | map(select(.value | tag == "!!map") | select(.value | has($s))) | .[0].key)' \
    "$MKDOCS" 2>/dev/null || echo "null")
  if [ "$IDX" = "null" ] || [ -z "$IDX" ]; then
    EXISTING_SECTIONS=$(yq '[.nav[] | select(tag == "!!map") | keys | .[0]] | join(",")' \
      "$MKDOCS" 2>/dev/null || echo "?")
    die "section '$SECTION' not found at top of nav: in $MKDOCS (existing: $EXISTING_SECTIONS)" 4
  fi
  NAV_STATUS="created"
  NAV_EXPR='(.nav[env(IDX)][] += [{strenv(TITLE): strenv(REL_PATH)}])'
fi

HAS_LLMSTXT=$(yq -r '[.plugins[]? | select(tag == "!!map") | select(has("llmstxt"))] | length > 0' \
  "$MKDOCS" 2>/dev/null || echo "false")
if [ "$HAS_LLMSTXT" = "true" ]; then
  LLMSTXT_EXPR='with(.plugins[] | select(has("llmstxt")); .llmstxt.sections.Guides += [strenv(REL_PATH)] | .llmstxt.sections.Guides |= unique)'
else
  LLMSTXT_EXPR='.'
fi
if [ -n "$NAV_EXPR" ]; then
  CONFIG_EXPR="$NAV_EXPR | $LLMSTXT_EXPR"
else
  CONFIG_EXPR="$LLMSTXT_EXPR"
fi

if [ "$DRY_RUN" = "0" ]; then
  MKDOCS_TMP="${MKDOCS}.tmp.$$"
  IDX="${IDX:-}" TITLE="$TITLE" REL_PATH="$REL_PATH" \
    yq "$CONFIG_EXPR" "$MKDOCS" > "$MKDOCS_TMP" || {
      rm -f "$MKDOCS_TMP"
      MKDOCS_TMP=""
      die "yq failed to prepare nav and llmstxt update" 4
    }
fi

# --- 1. Create the default-language page ---
if [ -e "$ABS_PATH" ] && [ "$FORCE" = "0" ]; then
  log "Page already exists: $ABS_PATH (use --force to overwrite)"
  log "Skipping page creation; will still ensure nav entry."
else
  if [ "$DRY_RUN" = "1" ]; then
    log "[dry-run] mkdir -p $(dirname "$ABS_PATH"); cp $TEMPLATE → $ABS_PATH"
  else
    mkdir -p "$(dirname "$ABS_PATH")"
    cp "$TEMPLATE" "$ABS_PATH"
    sed -i.bak \
      -e "s|{{PAGE_TITLE}}|${TITLE}|g" \
      "$ABS_PATH"
    rm -f "${ABS_PATH}.bak"
    log "Created: $ABS_PATH"
  fi
fi

# --- 2. Commit the prevalidated nav + llmstxt update ---
if [ "$DRY_RUN" = "1" ]; then
  log "[dry-run] update nav/llmstxt in $MKDOCS (nav status: $NAV_STATUS)"
else
  mv "$MKDOCS_TMP" "$MKDOCS"
  MKDOCS_TMP=""
  log "Updated nav/llmstxt in: $MKDOCS (nav status: $NAV_STATUS)"
fi

# --- 3. Create *.<LANG>.md stubs for every non-default language ---
if [ -n "$LANGUAGES_LIST" ]; then
  DEFAULT_LANG=""
  for loc in $LANGUAGES_LIST; do
    [ -z "$loc" ] && continue
    if [ -z "$DEFAULT_LANG" ]; then DEFAULT_LANG="$loc"; continue; fi
    write_lang_stub "$ABS_PATH" "$loc"
    STUBS_CREATED=$((STUBS_CREATED + 1))
  done
fi

printf '{"page":"%s","section":"%s","status":"%s","lang_stubs":%d}\n' \
  "$REL_PATH" "$SECTION" "$NAV_STATUS" "$STUBS_CREATED"
