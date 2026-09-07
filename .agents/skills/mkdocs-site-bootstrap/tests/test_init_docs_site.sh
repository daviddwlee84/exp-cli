#!/usr/bin/env bash
# Regression tests for MkDocs bootstrap root discovery and existing-doc handling.
# Bash 3.2 compatible (stock macOS).

set -u

TESTS_DIR="$(cd "$(dirname "$0")" && pwd)"
SKILL_DIR="$(cd "$TESTS_DIR/.." && pwd)"
REPO_ROOT="$(cd "$SKILL_DIR/../../.." && pwd)"
INIT="$SKILL_DIR/scripts/init-docs-site.sh"
ADD_PAGE="$SKILL_DIR/scripts/add-docs-page.sh"
ADD_LANGUAGE="$SKILL_DIR/scripts/add-language.sh"
PREFERENCES="$SKILL_DIR/scripts/check-preferences.sh"
BASE="$(mktemp -d /tmp/test-mkdocs-bootstrap.XXXXXX)"
BASE="$(cd "$BASE" && pwd -P)"
FAKE_BIN="$BASE/bin"
SYSTEM_PATH="/bin:/usr/bin:$PATH"
PASS_COUNT=0
FAIL_COUNT=0

trap 'rm -rf "$BASE"' EXIT

printf 'bash-under-test: %s (%s)\n' "$BASH_VERSION" "$BASH"
if [ "$(uname -s)" = "Darwin" ] && [ "${BASH_VERSINFO[0]}" != "3" ]; then
  printf 'error: Darwin compatibility suite must run with /bin/bash 3.x\n' >&2
  exit 1
fi

pass() { PASS_COUNT=$((PASS_COUNT + 1)); printf '  PASS %s\n' "$1"; }
fail() { FAIL_COUNT=$((FAIL_COUNT + 1)); printf '  FAIL %s\n' "$1"; }
contains() { printf '%s\n' "$1" | grep -Fq -- "$2"; }

assert_contains() {
  local name="$1" value="$2" expected="$3"
  if contains "$value" "$expected"; then pass "$name"; else
    fail "$name"
    printf '    expected output to contain: %s\n' "$expected" >&2
  fi
}

assert_line() {
  local name="$1" value="$2" expected="$3"
  if printf '%s\n' "$value" | grep -Fqx -- "$expected"; then pass "$name"; else
    fail "$name"
    printf '    expected exact output line: %s\n' "$expected" >&2
  fi
}

assert_file() {
  local name="$1" path="$2"
  if [ -f "$path" ]; then pass "$name"; else fail "$name"; fi
}

assert_no_file() {
  local name="$1" path="$2"
  if [ ! -e "$path" ]; then pass "$name"; else fail "$name"; fi
}

fingerprint_tree() {
  local root="$1"
  find "$root" -type f -print | LC_ALL=C sort | while IFS= read -r path; do
    printf '%s  %s\n' "$(git hash-object --no-filters "$path")" "${path#"$root"/}"
  done
}

run_init() {
  local target="$1" mode="$2"
  shift 2
  "$INIT" \
    --target-dir "$target" \
    --site-name "Fixture Docs" \
    --site-url "https://example.test/docs/" \
    --repo-slug "owner/repo" \
    --existing "$mode" \
    --no-workflow \
    "$@"
}

validate_config() {
  local config="$1" mode="$2"
  shift 2
  uv run --project "$REPO_ROOT" --extra docs python - "$config" "$mode" "$@" <<'PY'
import sys
from pathlib import Path

import yaml

config = Path(sys.argv[1])
mode = sys.argv[2]
expected = sys.argv[3:]
data = yaml.safe_load(config.read_text(encoding="utf-8"))

plugins = data["plugins"]
llmstxt = next(item["llmstxt"] for item in plugins if isinstance(item, dict) and "llmstxt" in item)
guides = llmstxt.get("sections", {}).get("Guides", [])
has_copy_to_llm = any(isinstance(item, dict) and "copy-to-llm" in item for item in plugins)

if mode == "fresh":
    assert data["nav"] == [{"Home": "index.md"}, {"Getting Started": "getting-started.md"}]
    assert guides == ["index.md", "getting-started.md"]
    assert has_copy_to_llm
elif mode == "skip":
    assert "nav" not in data
    assert guides == expected
    assert not has_copy_to_llm
elif mode == "wrap":
    assert data["nav"] == expected
    assert guides == expected
    assert not has_copy_to_llm
else:
    raise AssertionError(f"unknown mode: {mode}")
PY
}

build_fixture() {
  local target="$1"
  uv run --project "$REPO_ROOT" --extra docs mkdocs build \
    --strict \
    --config-file "$target/mkdocs.yml" \
    --site-dir "$target/site" >/dev/null
}

mkdir -p "$FAKE_BIN"
printf '#!/bin/sh\nexit 97\n' > "$FAKE_BIN/yq"
chmod +x "$FAKE_BIN/yq"

# Linked-worktree root discovery from a nested directory.
PRIMARY="$BASE/primary"
WORKTREE="$BASE/linked-worktree"
mkdir -p "$PRIMARY"
git -C "$PRIMARY" init -q -b main
printf 'base\n' > "$PRIMARY/base.txt"
git -C "$PRIMARY" add base.txt
git -C "$PRIMARY" -c core.hooksPath=/dev/null \
  -c user.name=test -c user.email=test@example.com commit -q -m init
git -C "$PRIMARY" worktree add -q -b linked "$WORKTREE"
mkdir -p "$WORKTREE/nested/deep" "$WORKTREE/.skills"
printf 'fixture: linked-worktree\n' > "$WORKTREE/.skills/preferences.yaml"

OUTPUT=$(
  cd "$WORKTREE/nested/deep" &&
  GIT_CONFIG_GLOBAL=/dev/null "$INIT" --dry-run --site-name Test \
    --site-url https://example.test/ --repo-slug owner/repo --no-workflow 2>&1
)
assert_line "init resolves linked-worktree root" "$OUTPUT" "Target: $WORKTREE"

OUTPUT=$(
  cd "$WORKTREE/nested/deep" &&
  PATH="$FAKE_BIN:$SYSTEM_PATH" GIT_CONFIG_GLOBAL=/dev/null "$PREFERENCES" --list
)
assert_contains "preferences resolve linked-worktree root" "$OUTPUT" "fixture: linked-worktree"

# Outside Git, both scripts keep the invocation directory fallback.
OUTSIDE="$BASE/outside/nested"
mkdir -p "$OUTSIDE/.skills"
printf 'fixture: outside-git\n' > "$OUTSIDE/.skills/preferences.yaml"
OUTPUT=$(
  cd "$OUTSIDE" &&
  GIT_CONFIG_GLOBAL=/dev/null "$INIT" --dry-run --site-name Test \
    --site-url https://example.test/ --repo-slug owner/repo --no-workflow 2>&1
)
assert_line "init falls back outside Git" "$OUTPUT" "Target: $OUTSIDE"
OUTPUT=$(
  cd "$OUTSIDE" &&
  PATH="$FAKE_BIN:$SYSTEM_PATH" GIT_CONFIG_GLOBAL=/dev/null "$PREFERENCES" --list
)
assert_contains "preferences fall back outside Git" "$OUTPUT" "fixture: outside-git"

# Hidden ancestors must not make visible docs look empty.
HIDDEN="$BASE/.hidden/target"
mkdir -p "$HIDDEN/docs"
printf '# Existing\n' > "$HIDDEN/docs/existing.md"
run_init "$HIDDEN" wrap >/dev/null
assert_contains "hidden ancestor preserves existing docs" "$(cat "$HIDDEN/docs/existing.md")" "# Existing"
if validate_config "$HIDDEN/mkdocs.yml" wrap existing.md; then pass "hidden ancestor keeps existing-doc mode"; else fail "hidden ancestor keeps existing-doc mode"; fi

DOTFILES_ONLY="$BASE/dotfiles-only"
mkdir -p "$DOTFILES_ONLY/docs"
printf 'jekyll marker\n' > "$DOTFILES_ONLY/docs/.nojekyll"
run_init "$DOTFILES_ONLY" skip >/dev/null
assert_file "dotfiles-only docs still receive starter skeleton" "$DOTFILES_ONLY/docs/index.md"
assert_contains "existing dotfile remains untouched" "$(cat "$DOTFILES_ONLY/docs/.nojekyll")" "jekyll marker"
if validate_config "$DOTFILES_ONLY/mkdocs.yml" fresh; then pass "dotfiles do not select existing-doc mode"; else fail "dotfiles do not select existing-doc mode"; fi

# Refuse symlinked docs before --force can write through them.
SYMLINK_ROOT="$BASE/symlink-root"
SYMLINK_EXTERNAL="$BASE/symlink-root-external"
mkdir -p "$SYMLINK_ROOT" "$SYMLINK_EXTERNAL"
printf '# External\n' > "$SYMLINK_EXTERNAL/index.md"
ln -s "$SYMLINK_EXTERNAL" "$SYMLINK_ROOT/docs"
if run_init "$SYMLINK_ROOT" wrap --force >/dev/null 2>&1; then fail "symlinked docs root is rejected"; else pass "symlinked docs root is rejected"; fi
assert_contains "symlinked docs root remains untouched" "$(cat "$SYMLINK_EXTERNAL/index.md")" "# External"

SYMLINK_PAGE="$BASE/symlink-page"
SYMLINK_PAGE_EXTERNAL="$BASE/symlink-page-external.md"
mkdir -p "$SYMLINK_PAGE/docs"
printf '# External Page\n' > "$SYMLINK_PAGE_EXTERNAL"
ln -s "$SYMLINK_PAGE_EXTERNAL" "$SYMLINK_PAGE/docs/index.md"
if run_init "$SYMLINK_PAGE" wrap --force >/dev/null 2>&1; then fail "symlinked docs page is rejected"; else pass "symlinked docs page is rejected"; fi
assert_contains "symlinked docs page remains untouched" "$(cat "$SYMLINK_PAGE_EXTERNAL")" "# External Page"

# Traversal errors fail closed before any scaffold is written.
LOCKED="$BASE/locked"
mkdir -p "$LOCKED/docs/private"
printf '# Private\n' > "$LOCKED/docs/private/page.md"
chmod 000 "$LOCKED/docs/private"
if run_init "$LOCKED" wrap >/dev/null 2>&1; then fail "unreadable docs fail closed"; else pass "unreadable docs fail closed"; fi
chmod 700 "$LOCKED/docs/private"
assert_no_file "unreadable docs create no config" "$LOCKED/mkdocs.yml"
assert_contains "unreadable docs remain untouched" "$(cat "$LOCKED/docs/private/page.md")" "# Private"

# A malformed later marker must not leave an earlier block half-rendered.
BROKEN_SKILL="$BASE/broken-skill"
BROKEN_TARGET="$BASE/broken-target"
cp -R "$SKILL_DIR" "$BROKEN_SKILL"
rm -rf "$BROKEN_SKILL/tests"
sed -i.bak '/__STARTER_LLMSTXT_END__/d' "$BROKEN_SKILL/assets/mkdocs.yml.template"
rm -f "$BROKEN_SKILL/assets/mkdocs.yml.template.bak"
mkdir -p "$BROKEN_TARGET/docs"
printf '# Existing\n' > "$BROKEN_TARGET/docs/existing.md"
if "$BROKEN_SKILL/scripts/init-docs-site.sh" --target-dir "$BROKEN_TARGET" \
  --site-name Test --site-url https://example.test/ --repo-slug owner/repo \
  --existing wrap --no-workflow >/dev/null 2>&1; then
  fail "malformed markers fail closed"
else
  pass "malformed markers fail closed"
fi
if grep -Fq '__STARTER_NAV_BEGIN__' "$BROKEN_TARGET/mkdocs.yml" && \
   grep -Fq -- '- Home: index.md' "$BROKEN_TARGET/mkdocs.yml"; then
  pass "marker rendering is atomic"
else
  fail "marker rendering is atomic"
fi

# Fresh scaffold keeps the starter pages and starter configuration.
FRESH="$BASE/fresh"
mkdir -p "$FRESH"
run_init "$FRESH" skip >/dev/null
assert_file "fresh scaffold creates index" "$FRESH/docs/index.md"
assert_file "fresh scaffold creates getting started" "$FRESH/docs/getting-started.md"
if validate_config "$FRESH/mkdocs.yml" fresh; then pass "fresh config keeps starter entries"; else fail "fresh config keeps starter entries"; fi
if build_fixture "$FRESH"; then pass "fresh scaffold builds strictly"; else fail "fresh scaffold builds strictly"; fi
assert_no_file "fresh scaffold does not publish snippets" "$FRESH/site/_snippets"

MARKER_NAME="$BASE/marker-name"
mkdir -p "$MARKER_NAME"
"$INIT" --target-dir "$MARKER_NAME" --site-name '__STARTER_COPY_TO_LLM_BEGIN__' \
  --site-url https://example.test/ --repo-slug owner/repo --no-workflow >/dev/null
assert_file "marker-like site name does not break rendering" "$MARKER_NAME/docs/index.md"
if [ "$(yq -r '.site_name' "$MARKER_NAME/mkdocs.yml")" = "__STARTER_COPY_TO_LLM_BEGIN__" ]; then
  pass "marker matching is anchored to template comments"
else
  fail "marker matching is anchored to template comments"
fi

# --no-skeleton also removes starter references when no docs exist yet.
NO_SKELETON="$BASE/no-skeleton"
mkdir -p "$NO_SKELETON"
"$INIT" --target-dir "$NO_SKELETON" --site-name "Fixture Docs" \
  --site-url "https://example.test/docs/" --repo-slug "owner/repo" \
  --no-skeleton --no-workflow >/dev/null
assert_no_file "no-skeleton creates no docs directory" "$NO_SKELETON/docs"
if validate_config "$NO_SKELETON/mkdocs.yml" skip; then
  pass "no-skeleton removes starter references"
else
  fail "no-skeleton removes starter references"
fi

# Existing trees without Markdown keep a valid empty llmstxt section.
HTML_ONLY="$BASE/html-only"
mkdir -p "$HTML_ONLY/docs"
printf '<h1>Static</h1>\n' > "$HTML_ONLY/docs/index.html"
BEFORE="$(fingerprint_tree "$HTML_ONLY/docs")"
run_init "$HTML_ONLY" wrap >/dev/null
if validate_config "$HTML_ONLY/mkdocs.yml" wrap; then pass "HTML-only config remains valid"; else fail "HTML-only config remains valid"; fi
if build_fixture "$HTML_ONLY"; then pass "HTML-only scaffold builds strictly"; else fail "HTML-only scaffold builds strictly"; fi
AFTER_BUILD="$(fingerprint_tree "$HTML_ONLY/docs")"
if [ "$BEFORE" = "$AFTER_BUILD" ]; then pass "HTML-only build preserves existing docs"; else fail "HTML-only build preserves existing docs"; fi

# Skip preserves existing docs and omits explicit navigation.
SKIP="$BASE/skip"
mkdir -p "$SKIP/docs/nested"
printf '# Alpha\n' > "$SKIP/docs/alpha.md"
printf '# Beta Notes\n' > "$SKIP/docs/nested/Beta notes.md"
printf 'static text\n' > "$SKIP/docs/notes.txt"
BEFORE="$(fingerprint_tree "$SKIP/docs")"
run_init "$SKIP" skip >/dev/null
AFTER="$(fingerprint_tree "$SKIP/docs")"
if [ "$BEFORE" = "$AFTER" ]; then pass "skip preserves existing docs byte-for-byte"; else fail "skip preserves existing docs byte-for-byte"; fi
assert_no_file "skip creates no starter page" "$SKIP/docs/getting-started.md"
if validate_config "$SKIP/mkdocs.yml" skip alpha.md "nested/Beta notes.md"; then pass "skip omits nav and refreshes llmstxt paths"; else fail "skip omits nav and refreshes llmstxt paths"; fi
if build_fixture "$SKIP"; then pass "skip scaffold builds strictly"; else fail "skip scaffold builds strictly"; fi
AFTER_BUILD="$(fingerprint_tree "$SKIP/docs")"
if [ "$BEFORE" = "$AFTER_BUILD" ]; then pass "skip build preserves existing docs"; else fail "skip build preserves existing docs"; fi

# Wrap preserves docs and writes deterministic path-only navigation.
WRAP="$BASE/wrap"
mkdir -p "$WRAP/docs/nested/.private" "$WRAP/docs/nested/_snippets" "$WRAP/docs/assets" "$WRAP/docs/_snippets"
printf '# Last\n' > "$WRAP/docs/z-last.md"
printf '# Alpha\n' > "$WRAP/docs/Alpha page.md"
printf '# Long extension\n' > "$WRAP/docs/Guide.markdown"
printf '# Beta\n' > "$WRAP/docs/nested/Beta notes.md"
printf '# Owner\n' > "$WRAP/docs/owner's guide.md"
printf 'draft\n' > "$WRAP/docs/.draft.md"
printf 'private\n' > "$WRAP/docs/nested/.private/secret.md"
printf 'nested include only\n' > "$WRAP/docs/nested/_snippets/private.md"
printf 'include only\n' > "$WRAP/docs/_snippets/private.md"
printf 'asset\n' > "$WRAP/docs/assets/readme.txt"
BEFORE="$(fingerprint_tree "$WRAP/docs")"
run_init "$WRAP" wrap >/dev/null
AFTER="$(fingerprint_tree "$WRAP/docs")"
if [ "$BEFORE" = "$AFTER" ]; then pass "wrap preserves existing docs byte-for-byte"; else fail "wrap preserves existing docs byte-for-byte"; fi
assert_no_file "wrap creates no starter page" "$WRAP/docs/getting-started.md"
if validate_config "$WRAP/mkdocs.yml" wrap \
  "Alpha page.md" "Guide.markdown" "nested/Beta notes.md" "owner's guide.md" "z-last.md"; then
  pass "wrap writes sorted path-only nav and llmstxt paths"
else
  fail "wrap writes sorted path-only nav and llmstxt paths"
fi
if build_fixture "$WRAP"; then pass "wrap scaffold builds strictly"; else fail "wrap scaffold builds strictly"; fi
assert_no_file "wrap scaffold does not publish root snippets" "$WRAP/site/_snippets"
assert_no_file "wrap scaffold does not publish nested snippets" "$WRAP/site/nested/_snippets"
AFTER_BUILD="$(fingerprint_tree "$WRAP/docs")"
if [ "$BEFORE" = "$AFTER_BUILD" ]; then pass "wrap build preserves existing docs"; else fail "wrap build preserves existing docs"; fi

# The documented page helper must preserve auto-nav and keep llmstxt current.
if command -v yq >/dev/null 2>&1 && yq --version 2>/dev/null | grep -Fqi 'mikefarah'; then
  if "$ADD_PAGE" --target-dir "$WRAP" --section Workflows --title Workflow \
    --slug workflow >/dev/null 2>&1; then
    fail "missing wrap section fails before page creation"
  else
    pass "missing wrap section fails before page creation"
  fi
  assert_no_file "failed section creates no page" "$WRAP/docs/workflows/workflow.md"
  if "$ADD_PAGE" --target-dir "$WRAP" --section Missing --title Translation \
    --slug translation --lang zh-TW >/dev/null 2>&1; then
    fail "language-only helper validates named section"
  else
    pass "language-only helper validates named section"
  fi
  assert_no_file "failed language section creates no stub" \
    "$WRAP/docs/missing/translation.zh-TW.md"

  "$ADD_PAGE" --target-dir "$SKIP" --section _root --title "Added Page" \
    --slug added >/dev/null
  if validate_config "$SKIP/mkdocs.yml" skip alpha.md "nested/Beta notes.md" added.md; then
    pass "root page preserves auto-nav and updates llmstxt"
  else
    fail "root page preserves auto-nav and updates llmstxt"
  fi

  "$ADD_PAGE" --target-dir "$WRAP" --section _root --title "New Root" \
    --slug new-root >/dev/null
  if uv run --project "$REPO_ROOT" --extra docs python - "$WRAP/mkdocs.yml" <<'PY'
import sys
from pathlib import Path

import yaml

data = yaml.safe_load(Path(sys.argv[1]).read_text(encoding="utf-8"))
assert data["nav"][-1] == {"New Root": "new-root.md"}
llmstxt = next(item["llmstxt"] for item in data["plugins"] if isinstance(item, dict) and "llmstxt" in item)
assert "new-root.md" in llmstxt["sections"]["Guides"]
PY
  then
    pass "root page extends wrapped nav and llmstxt"
  else
    fail "root page extends wrapped nav and llmstxt"
  fi

  yq '.nav += [{"Guides": []}]' "$WRAP/mkdocs.yml" > "$WRAP/mkdocs.yml.tmp"
  mv "$WRAP/mkdocs.yml.tmp" "$WRAP/mkdocs.yml"
  "$ADD_PAGE" --target-dir "$WRAP" --section Guides --title "Guide Page" \
    --slug guide-page >/dev/null
  assert_file "mixed nav accepts an existing mapping section" "$WRAP/docs/guides/guide-page.md"
  if [ "$(yq '.nav[] | select(has("Guides")) | .Guides[-1]["Guide Page"]' "$WRAP/mkdocs.yml")" = "guides/guide-page.md" ]; then
    pass "mixed nav updates the original section index"
  else
    fail "mixed nav updates the original section index"
  fi

  NO_LLMSTXT="$BASE/no-llmstxt"
  mkdir -p "$NO_LLMSTXT/docs"
  printf 'site_name: No LLM\nnav: []\n' > "$NO_LLMSTXT/mkdocs.yml"
  "$ADD_PAGE" --target-dir "$NO_LLMSTXT" --section _root --title About \
    --slug about >/dev/null
  assert_file "page helper supports configs without llmstxt" "$NO_LLMSTXT/docs/about.md"
  if [ "$(yq 'has("plugins")' "$NO_LLMSTXT/mkdocs.yml")" = "false" ]; then
    pass "page helper preserves an omitted plugins key"
  else
    fail "page helper preserves an omitted plugins key"
  fi

  if "$ADD_PAGE" --target-dir "$NO_LLMSTXT" --section _root --title Escape \
    --slug ../escaped >/dev/null 2>&1; then
    fail "page helper rejects traversal slugs"
  else
    pass "page helper rejects traversal slugs"
  fi
  assert_no_file "traversal slug writes nothing outside docs" "$NO_LLMSTXT/escaped.md"

  ADD_SYMLINK_ROOT="$BASE/add-symlink-root"
  ADD_SYMLINK_EXTERNAL="$BASE/add-symlink-external"
  mkdir -p "$ADD_SYMLINK_ROOT" "$ADD_SYMLINK_EXTERNAL"
  printf 'site_name: Linked\nnav: []\n' > "$ADD_SYMLINK_ROOT/mkdocs.yml"
  ln -s "$ADD_SYMLINK_EXTERNAL" "$ADD_SYMLINK_ROOT/docs"
  if "$ADD_PAGE" --target-dir "$ADD_SYMLINK_ROOT" --section _root \
    --title Linked --slug linked >/dev/null 2>&1; then
    fail "page helper rejects symlinked docs root"
  else
    pass "page helper rejects symlinked docs root"
  fi
  assert_no_file "page helper does not write through docs symlink" \
    "$ADD_SYMLINK_EXTERNAL/linked.md"

  ADD_SYMLINK_PAGE="$BASE/add-symlink-page"
  ADD_SYMLINK_EXTERNAL_PAGE="$BASE/add-symlink-external-page.md"
  mkdir -p "$ADD_SYMLINK_PAGE/docs"
  printf 'site_name: Linked\nnav: []\n' > "$ADD_SYMLINK_PAGE/mkdocs.yml"
  printf '# External\n' > "$ADD_SYMLINK_EXTERNAL_PAGE"
  ln -s "$ADD_SYMLINK_EXTERNAL_PAGE" "$ADD_SYMLINK_PAGE/docs/linked.md"
  if "$ADD_PAGE" --target-dir "$ADD_SYMLINK_PAGE" --section _root \
    --title Linked --slug linked --force >/dev/null 2>&1; then
    fail "page helper rejects symlinked target page"
  else
    pass "page helper rejects symlinked target page"
  fi
  assert_contains "page helper preserves symlink target" \
    "$(cat "$ADD_SYMLINK_EXTERNAL_PAGE")" "# External"

  if "$ADD_PAGE" --target-dir "$NO_LLMSTXT" --section _root --title Unsafe \
    --slug unsafe --lang '../../../../escaped' >/dev/null 2>&1; then
    fail "page helper rejects traversal language codes"
  else
    pass "page helper rejects traversal language codes"
  fi

  PREF_STUB="$BASE/pref-stub-symlink"
  PREF_STUB_EXTERNAL="$BASE/pref-stub-external.md"
  mkdir -p "$PREF_STUB/docs" "$PREF_STUB/.skills"
  printf 'site_name: Pref Stub\nnav: []\n' > "$PREF_STUB/mkdocs.yml"
  printf 'mkdocs_site_bootstrap:\n  languages: [en, zh-TW]\n' > \
    "$PREF_STUB/.skills/preferences.yaml"
  printf '# External Stub\n' > "$PREF_STUB_EXTERNAL"
  ln -s "$PREF_STUB_EXTERNAL" "$PREF_STUB/docs/about.zh-TW.md"
  PREF_CONFIG_BEFORE="$(git hash-object --no-filters "$PREF_STUB/mkdocs.yml")"
  if "$ADD_PAGE" --target-dir "$PREF_STUB" --section _root --title About \
    --slug about --force >/dev/null 2>&1; then
    fail "configured stub symlink fails before writes"
  else
    pass "configured stub symlink fails before writes"
  fi
  assert_no_file "stub preflight creates no default page" "$PREF_STUB/docs/about.md"
  if [ "$PREF_CONFIG_BEFORE" = "$(git hash-object --no-filters "$PREF_STUB/mkdocs.yml")" ]; then
    pass "stub preflight preserves mkdocs config"
  else
    fail "stub preflight preserves mkdocs config"
  fi
  assert_contains "stub preflight preserves external target" \
    "$(cat "$PREF_STUB_EXTERNAL")" "# External Stub"

  LANGUAGE_TARGET="$BASE/language-markdown"
  mkdir -p "$LANGUAGE_TARGET/docs/guides/_snippets"
  printf '# Long Extension\n' > "$LANGUAGE_TARGET/docs/Guide.markdown"
  printf 'include only\n' > "$LANGUAGE_TARGET/docs/guides/_snippets/part.md"
  run_init "$LANGUAGE_TARGET" wrap >/dev/null
  "$ADD_LANGUAGE" --target-dir "$LANGUAGE_TARGET" --lang zh-TW \
    --remove-llmstxt >/dev/null
  assert_file "language helper preserves .markdown extension" \
    "$LANGUAGE_TARGET/docs/Guide.zh-TW.markdown"
  assert_file "language helper writes target preferences" \
    "$LANGUAGE_TARGET/.skills/preferences.yaml"
  assert_no_file "language helper skips nested snippets" \
    "$LANGUAGE_TARGET/docs/guides/_snippets/part.zh-TW.md"
else
  fail "mikefarah/yq is required for helper integration tests"
fi

# Dry-run does not create or alter files.
DRY="$BASE/dry-run"
mkdir -p "$DRY/docs"
printf '# Existing\n' > "$DRY/docs/existing.md"
BEFORE="$(fingerprint_tree "$DRY")"
"$INIT" --target-dir "$DRY" --dry-run --site-name Test \
  --site-url https://example.test/ --repo-slug owner/repo --existing wrap --no-workflow >/dev/null 2>&1
AFTER="$(fingerprint_tree "$DRY")"
if [ "$BEFORE" = "$AFTER" ]; then pass "dry-run leaves target unchanged"; else fail "dry-run leaves target unchanged"; fi
assert_no_file "dry-run creates no config" "$DRY/mkdocs.yml"

printf '\n%d passed, %d failed\n' "$PASS_COUNT" "$FAIL_COUNT"
[ "$FAIL_COUNT" -eq 0 ]
