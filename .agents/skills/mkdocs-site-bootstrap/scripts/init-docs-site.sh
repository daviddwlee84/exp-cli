#!/usr/bin/env bash
# init-docs-site.sh — Scaffold a MkDocs Material docs site in the current repo.
#
# Bash 3.2 compatible. See SKILL.md for the full workflow including consent
# gates and existing-docs handling (which the agent does, not this script).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SKILL_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ASSETS="$SKILL_DIR/assets"

usage() {
  cat <<'EOF'
Usage: init-docs-site.sh [OPTIONS]

Scaffold mkdocs.yml, pyproject.toml (docs extras), .github/workflows/docs.yml,
and a starter docs/ tree in the current repo (or --target-dir).

Options:
  --site-name NAME         Display name for the site (required).
  --site-description DESC  One-paragraph site description (default: derived from --site-name).
  --site-url URL           Public URL (e.g., https://owner.github.io/repo/) (required).
  --repo-slug owner/repo   GitHub slug (required).
  --repo-name NAME         Python package-ish name for pyproject.toml (default: repo basename).
  --target-dir DIR         Repo root (default: current Git worktree root, or CWD
                           when outside Git).
  --existing skip|wrap     How to handle existing docs/ (default: skip — leave as-is,
                           omit nav for MkDocs auto-navigation).
                           wrap: add Markdown paths to nav alphabetically.
  --no-workflow            Don't create .github/workflows/docs.yml.
  --no-skeleton            Don't create the docs/ skeleton (use existing docs/ as-is).
  --social                 Enable the Material social plugin (OG/Twitter preview
                           cards). Off by default: it needs system Cairo/Pango
                           (and mkdocs-material[imaging]), so a plain scaffold
                           stays dependency-free. See SKILL.md § Social cards.
  --dry-run                Print actions without writing.
  --force                  Overwrite existing files (mkdocs.yml, pyproject.toml).
  --help, -h               Show this help and exit.

Examples:
  init-docs-site.sh \
    --site-name "My Project" \
    --site-url https://owner.github.io/repo/ \
    --repo-slug owner/repo

  init-docs-site.sh --dry-run --site-name X --site-url https://x.io/ --repo-slug a/b

Exit codes:
  0  success
  1  invalid arguments
  2  target dir not found
  3  refusing to overwrite (use --force)
  4  template missing
EOF
}

log()  { printf '%s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit "${2:-1}"; }

# ensure_gitignore <pattern> <comment> — idempotently append to $TARGET/.gitignore
ensure_gitignore() {
  if [ "$DRY_RUN" = "1" ]; then
    log "[dry-run] would ensure $1 is in $TARGET/.gitignore"
  elif ! grep -qxF "$1" "$TARGET/.gitignore" 2>/dev/null; then
    printf '\n# %s\n%s\n' "$2" "$1" >> "$TARGET/.gitignore"
    log "Added $1 to .gitignore ($2)."
  fi
}

SITE_NAME=""
SITE_DESC=""
SITE_URL=""
REPO_SLUG=""
REPO_NAME=""
TARGET=""
EXISTING="skip"
NO_WORKFLOW=0
NO_SKELETON=0
SOCIAL=0
DRY_RUN=0
FORCE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --site-name)        SITE_NAME="${2:-}"; shift 2 ;;
    --site-description) SITE_DESC="${2:-}"; shift 2 ;;
    --site-url)         SITE_URL="${2:-}"; shift 2 ;;
    --repo-slug)        REPO_SLUG="${2:-}"; shift 2 ;;
    --repo-name)        REPO_NAME="${2:-}"; shift 2 ;;
    --target-dir)       TARGET="${2:-}"; shift 2 ;;
    --existing)         EXISTING="${2:-}"; shift 2 ;;
    --no-workflow)      NO_WORKFLOW=1; shift ;;
    --no-skeleton)      NO_SKELETON=1; shift ;;
    --social)           SOCIAL=1; shift ;;
    --dry-run)          DRY_RUN=1; shift ;;
    --force)            FORCE=1; shift ;;
    --help|-h)          usage; exit 0 ;;
    -*)                 die "unknown flag: $1 (try --help)" 1 ;;
    *)                  die "unexpected positional argument: $1" 1 ;;
  esac
done

[ -n "$SITE_NAME" ] || die "--site-name is required (try --help)" 1
[ -n "$SITE_URL" ]  || die "--site-url is required" 1
[ -n "$REPO_SLUG" ] || die "--repo-slug is required (e.g. owner/repo)" 1
case "$EXISTING" in skip|wrap) ;; *) die "--existing must be 'skip' or 'wrap'" 1 ;; esac

[ -n "$SITE_DESC" ] || SITE_DESC="Documentation for $SITE_NAME"

# Resolve target dir.
if [ -z "$TARGET" ]; then
  if TARGET="$(git rev-parse --show-toplevel 2>/dev/null)"; then
    :
  else
    TARGET="$(pwd)"
  fi
fi
[ -d "$TARGET" ] || die "target dir not found: $TARGET" 2

[ -n "$REPO_NAME" ] || REPO_NAME="$(basename "$TARGET")"

log "Target: $TARGET"
log "Site:   $SITE_NAME @ $SITE_URL"
log "Repo:   $REPO_SLUG"
log "Social: $([ "$SOCIAL" = "1" ] && echo "on (OG cards; needs Cairo/Pango)" || echo "off (pass --social to enable)")"

# --- helper: substitute {{VAR}} placeholders ---
substitute() {
  local file="$1"
  if [ "$DRY_RUN" = "1" ]; then
    log "[dry-run] would substitute placeholders in $file"
    return 0
  fi
  sed -i.bak \
    -e "s|{{SITE_NAME}}|${SITE_NAME}|g" \
    -e "s|{{SITE_DESCRIPTION}}|${SITE_DESC}|g" \
    -e "s|{{SITE_URL}}|${SITE_URL}|g" \
    -e "s|{{REPO_SLUG}}|${REPO_SLUG}|g" \
    -e "s|{{REPO_NAME}}|${REPO_NAME}|g" \
    "$file"
  rm -f "${file}.bak"
}

# --- helper: copy template, refusing to overwrite unless --force ---
copy_template() {
  local src="$1" dst="$2"
  [ -f "$src" ] || die "template missing: $src" 4
  if [ -e "$dst" ] && [ "$FORCE" = "0" ]; then
    die "exists, refusing to overwrite: $dst (use --force)" 3
  fi
  if [ "$DRY_RUN" = "1" ]; then
    log "[dry-run] cp $src → $dst"
    return 0
  fi
  mkdir -p "$(dirname "$dst")"
  cp "$src" "$dst"
}

# --- helper: expand a __SOCIAL_*__ marker line ---
# With --social, replace the marker line with the snippet file's contents
# (snippets are pre-indented for their target). Without --social, delete the
# marker line. Keeps every other line — and its comments — untouched.
expand_marker() {
  local file="$1" marker="$2" snippet="$3"
  [ -f "$file" ] || return 0
  if [ "$DRY_RUN" = "1" ]; then
    log "[dry-run] expand $marker in $file ($([ "$SOCIAL" = "1" ] && echo "insert $snippet" || echo "delete marker"))"
    return 0
  fi
  if [ "$SOCIAL" = "1" ]; then
    [ -f "$snippet" ] || die "social snippet missing: $snippet" 4
    awk -v marker="$marker" -v repl="$snippet" '
      index($0, marker) { while ((getline line < repl) > 0) print line; close(repl); next }
      { print }
    ' "$file" > "${file}.tmp" && mv "${file}.tmp" "$file"
  else
    awk -v marker="$marker" '!index($0, marker)' "$file" > "${file}.tmp" && mv "${file}.tmp" "$file"
  fi
}

list_docs_files() {
  [ -d "$TARGET/docs" ] || return 0
  (
    cd "$TARGET/docs"
    find . \
      \( -type d \( -path './.*' -o -path '*/.*' -o -name '__pycache__' -o -name 'node_modules' \) -prune \) -o \
      \( -type f -not -name '.*' -print \) |
      sed 's|^\./||' | LC_ALL=C sort
  )
}

find_docs_symlink() {
  [ -d "$TARGET/docs" ] || return 0
  (
    cd "$TARGET/docs"
    find . \
      \( -type d \( -path './.*' -o -path '*/.*' -o -name '__pycache__' -o -name 'node_modules' \) -prune \) -o \
      \( -type l -print -quit \)
  )
}

list_markdown_paths() {
  local path
  [ -n "$DOCS_FILES" ] || return 0
  printf '%s\n' "$DOCS_FILES" | while IFS= read -r path; do
    case "$path" in
      _snippets/*|*/_snippets/*) ;;
      *.md|*.markdown) printf '%s\n' "$path" ;;
    esac
  done
}

render_starter_blocks() {
  local file="$1" mode="$2" nav="$3" llmstxt="$4" copy_to_llm="$5" tmp rc
  tmp="${file}.tmp.$$"
  if awk -v mode="$mode" -v nav="$nav" -v llmstxt="$llmstxt" -v copy_to_llm="$copy_to_llm" '
    function emit(path, line) {
      while ((getline line < path) > 0) print line
      close(path)
    }
    BEGIN {
      block = ""
      nav_begin = nav_end = 0
      llmstxt_begin = llmstxt_end = 0
      copy_begin = copy_end = 0
      invalid = 0
    }
    /^[[:space:]]*# __STARTER_NAV_BEGIN__[[:space:]]*$/ {
      if (block != "") invalid = 1
      nav_begin++
      block = "nav"
      if (mode == "replace") emit(nav)
      next
    }
    /^[[:space:]]*# __STARTER_NAV_END__[[:space:]]*$/ {
      if (block != "nav") invalid = 1
      nav_end++
      block = ""
      next
    }
    /^[[:space:]]*# __STARTER_LLMSTXT_BEGIN__[[:space:]]*$/ {
      if (block != "") invalid = 1
      llmstxt_begin++
      block = "llmstxt"
      if (mode == "replace") emit(llmstxt)
      next
    }
    /^[[:space:]]*# __STARTER_LLMSTXT_END__[[:space:]]*$/ {
      if (block != "llmstxt") invalid = 1
      llmstxt_end++
      block = ""
      next
    }
    /^[[:space:]]*# __STARTER_COPY_TO_LLM_BEGIN__[[:space:]]*$/ {
      if (block != "") invalid = 1
      copy_begin++
      block = "copy"
      if (mode == "replace") emit(copy_to_llm)
      next
    }
    /^[[:space:]]*# __STARTER_COPY_TO_LLM_END__[[:space:]]*$/ {
      if (block != "copy") invalid = 1
      copy_end++
      block = ""
      next
    }
    block == "" || mode == "keep" { print }
    END {
      if (invalid || block != "" ||
          nav_begin != 1 || nav_end != 1 ||
          llmstxt_begin != 1 || llmstxt_end != 1 ||
          copy_begin != 1 || copy_end != 1) exit 2
    }
  ' "$file" > "$tmp"; then
    mv "$tmp" "$file"
  else
    rc=$?
    rm -f "$tmp"
    return "$rc"
  fi
}

configure_existing_docs() {
  local file="$1" mode="$2" paths nav llmstxt copy_to_llm path escaped rc
  paths="$(mktemp "$TARGET/.mkdocs-paths.XXXXXX")"
  nav="$(mktemp "$TARGET/.mkdocs-nav.XXXXXX")"
  llmstxt="$(mktemp "$TARGET/.mkdocs-llmstxt.XXXXXX")"
  copy_to_llm="$(mktemp "$TARGET/.mkdocs-copy-to-llm.XXXXXX")"

  if list_markdown_paths > "$paths"; then
    :
  else
    rc=$?
    rm -f "$paths" "$nav" "$llmstxt" "$copy_to_llm"
    return "$rc"
  fi

  : > "$nav"
  if [ "$mode" = "wrap" ]; then
    if [ -s "$paths" ]; then printf 'nav:\n' >> "$nav"; else printf 'nav: []\n' >> "$nav"; fi
  fi

  printf '      sections:\n' > "$llmstxt"
  if [ -s "$paths" ]; then
    printf '        Guides:\n' >> "$llmstxt"
  else
    printf '        Guides: []\n' >> "$llmstxt"
  fi
  : > "$copy_to_llm"

  while IFS= read -r path; do
    escaped="$(printf '%s' "$path" | sed "s/'/''/g")"
    if [ "$mode" = "wrap" ]; then printf "  - '%s'\n" "$escaped" >> "$nav"; fi
    printf "          - '%s'\n" "$escaped" >> "$llmstxt"
  done < "$paths"

  if render_starter_blocks "$file" replace "$nav" "$llmstxt" "$copy_to_llm"; then
    rm -f "$paths" "$nav" "$llmstxt" "$copy_to_llm"
  else
    rc=$?
    rm -f "$paths" "$nav" "$llmstxt" "$copy_to_llm"
    return "$rc"
  fi
}

DOCS_FILES=""
DOCS_SYMLINK=""
if [ -L "$TARGET/docs" ]; then
  die "refusing symlinked docs directory: $TARGET/docs" 3
elif [ -e "$TARGET/docs" ] && [ ! -d "$TARGET/docs" ]; then
  die "docs path is not a directory: $TARGET/docs" 3
elif [ -d "$TARGET/docs" ]; then
  if DOCS_SYMLINK="$(find_docs_symlink)"; then
    [ -z "$DOCS_SYMLINK" ] || die "refusing symlink inside docs/: $DOCS_SYMLINK" 3
  else
    die "could not inspect docs/ for symlinks" 3
  fi
  if DOCS_FILES="$(list_docs_files)"; then
    :
  else
    die "could not inspect existing docs/ content" 3
  fi
fi

if [ -n "$DOCS_FILES" ]; then
  DOCS_MODE="$EXISTING"
elif [ "$NO_SKELETON" = "1" ]; then
  DOCS_MODE="no-skeleton"
else
  DOCS_MODE="fresh"
fi

# --- 1. mkdocs.yml ---
copy_template "$ASSETS/mkdocs.yml.template" "$TARGET/mkdocs.yml"
substitute "$TARGET/mkdocs.yml"
expand_marker "$TARGET/mkdocs.yml" "__SOCIAL_PLUGIN__" "$ASSETS/social/mkdocs-plugin.yml"
if [ "$DRY_RUN" = "1" ]; then
  case "$DOCS_MODE" in
    skip|wrap) log "[dry-run] would configure existing docs with --existing=$DOCS_MODE" ;;
    no-skeleton) log "[dry-run] would remove starter integrations because --no-skeleton is set" ;;
    fresh) log "[dry-run] would keep starter nav, llmstxt, and copy-to-llm integrations" ;;
  esac
else
  case "$DOCS_MODE" in
    skip|wrap)
      configure_existing_docs "$TARGET/mkdocs.yml" "$DOCS_MODE" || \
        die "failed to configure existing docs in $TARGET/mkdocs.yml" 4
      ;;
    no-skeleton)
      configure_existing_docs "$TARGET/mkdocs.yml" skip || \
        die "failed to remove starter integrations from $TARGET/mkdocs.yml" 4
      ;;
    fresh)
      render_starter_blocks "$TARGET/mkdocs.yml" keep /dev/null /dev/null /dev/null || \
        die "failed to finalize starter configuration in $TARGET/mkdocs.yml" 4
      ;;
  esac
fi

# --- 2. pyproject.toml ---
if [ -e "$TARGET/pyproject.toml" ]; then
  log "Note: $TARGET/pyproject.toml already exists; not modifying it."
  log "      Add this to your [project.optional-dependencies]:"
  log "        docs = [\"mkdocs>=1.6\", \"mkdocs-material>=9.5\","
  if [ "$SOCIAL" = "1" ]; then
    log "                \"mkdocs-material[imaging]>=9.5\",  # social/OG cards"
  fi
  log "                \"mkdocs-llmstxt>=0.2\", \"mkdocs-copy-to-llm>=0.1\","
  log "                \"pymdown-extensions>=10.7\"]"
  if [ "$SOCIAL" = "1" ]; then
    log "      The social plugin also needs system Cairo/Pango — see the"
    log "      'social cards' step in docs-workflow.yml.template."
  fi
else
  copy_template "$ASSETS/pyproject.toml.template" "$TARGET/pyproject.toml"
  substitute "$TARGET/pyproject.toml"
  expand_marker "$TARGET/pyproject.toml" "__SOCIAL_IMAGING__" "$ASSETS/social/pyproject-dep.txt"
fi

# --- 3. .github/workflows/docs.yml ---
if [ "$NO_WORKFLOW" = "0" ]; then
  copy_template "$ASSETS/docs-workflow.yml.template" "$TARGET/.github/workflows/docs.yml"
  expand_marker "$TARGET/.github/workflows/docs.yml" "__SOCIAL_CI__" "$ASSETS/social/ci-steps.yml"
fi

# --- 3b. .gitignore: build output (always) + social plugin cache (--social) ---
# `mkdocs build` writes the rendered site to ./site/ (default site_dir) every
# run — a generated artifact that must never be committed.
ensure_gitignore '/site/' 'mkdocs build output (regenerated every build)'
if [ "$SOCIAL" = "1" ]; then
  ensure_gitignore '/.cache/' 'mkdocs social plugin card+font cache (regenerated every build)'
fi

# --- 4. docs/ skeleton ---
case "$DOCS_MODE" in
  skip)
    log "Note: docs/ already has content. Honoring --existing=skip:"
    log "  Skipping skeleton creation (existing files left alone)."
    log "  mkdocs.yml omits nav and copy-to-llm so files remain untouched."
    ;;
  wrap)
    log "Note: docs/ already has content. Honoring --existing=wrap:"
    log "  Skipping skeleton creation (existing files left alone)."
    log "  mkdocs.yml lists page paths alphabetically and omits copy-to-llm."
    ;;
  no-skeleton)
    log "Skipping docs skeleton and starter integrations (--no-skeleton)."
    ;;
  fresh)
    if [ "$DRY_RUN" = "1" ]; then
      log "[dry-run] would copy docs-skeleton/* → $TARGET/docs/"
    else
      mkdir -p "$TARGET/docs/_snippets" "$TARGET/docs/assets/copy-to-llm"
      copy_template "$ASSETS/docs-skeleton/index.md" "$TARGET/docs/index.md"
      substitute "$TARGET/docs/index.md"
      copy_template "$ASSETS/docs-skeleton/getting-started.md" "$TARGET/docs/getting-started.md"
      substitute "$TARGET/docs/getting-started.md"
      copy_template "$ASSETS/docs-skeleton/_snippets/README.md" "$TARGET/docs/_snippets/README.md"
      cp "$ASSETS/docs-skeleton/assets/copy-to-llm/"* "$TARGET/docs/assets/copy-to-llm/"
    fi
    ;;
esac

if [ "$DRY_RUN" = "1" ]; then
  log "Dry run complete."
  exit 0
fi

# Structured success output.
printf '{"target":"%s","site_name":"%s","site_url":"%s","social":%s,"next_steps":[' \
  "$TARGET" "$SITE_NAME" "$SITE_URL" "$([ "$SOCIAL" = "1" ] && echo true || echo false)"
printf '"uv sync --extra docs",'
printf '"uv run mkdocs build --strict",'
printf '"git add and commit the new files",'
printf '"Run enable-pages.sh to deploy to GitHub Pages"]}\n'
