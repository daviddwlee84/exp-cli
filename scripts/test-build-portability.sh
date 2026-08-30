#!/bin/sh
set -eu

repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/exp-build-portability.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

source_dir="$work/source"
mkdir -p "$source_dir/cmd/exp" "$source_dir/internal/cli"
cp "$repo/Makefile" "$source_dir/Makefile"
printf '%s\n' \
	'module github.com/daviddwlee84/exp-cli' \
	'' \
	'go 1.26.4' >"$source_dir/go.mod"
printf '%s\n' \
	'package cli' \
	'' \
	'var Version = "dev"' >"$source_dir/internal/cli/version.go"
printf '%s\n' \
	'package main' \
	'' \
	'import (' \
	'    "fmt"' \
	'    "os"' \
	'    "github.com/daviddwlee84/exp-cli/internal/cli"' \
	')' \
	'' \
	'func main() {' \
	'    if len(os.Args) == 4 && os.Args[1] == "skill" && os.Args[2] == "install" && os.Args[3] == "--link" {' \
	'        if marker := os.Getenv("EXP_SKILL_INSTALL_MARKER"); marker != "" {' \
	'            if err := os.WriteFile(marker, []byte("called\n"), 0o600); err != nil { panic(err) }' \
	'        }' \
	'        return' \
	'    }' \
	'    fmt.Printf("exp version %s\n", cli.Version)' \
	'}' >"$source_dir/cmd/exp/main.go"

git -C "$source_dir" init -q
git -C "$source_dir" config user.name "exp build regression"
git -C "$source_dir" config user.email "exp-build-regression@example.invalid"
git -C "$source_dir" add Makefile go.mod cmd internal
git -C "$source_dir" commit -qm "build regression fixture"

tag_marker=GIT_TAG_COMMAND_MUST_NOT_RUN
malicious_tag="v1'\";touch\${IFS}${tag_marker};true\${IFS}'"
git -C "$source_dir" tag "$malicious_tag"
(
	cd "$source_dir"
	unset VERSION
	make BINARY=exp-tag-test build
)
if [ -e "$source_dir/$tag_marker" ]; then
	printf '%s\n' "malicious Git tag executed shell content" >&2
	exit 1
fi
actual_tag_version=$("$source_dir/exp-tag-test" --version)
case "$actual_tag_version" in
	*"$tag_marker"*) ;;
	*)
		printf 'Git-derived version was not linked into the binary: %s\n' "$actual_tag_version" >&2
		exit 1
		;;
esac

version_marker=VERSION_OVERRIDE_COMMAND_MUST_NOT_RUN
malicious_version="v2';touch\${IFS}${version_marker};true\${IFS}'"
(
	cd "$source_dir"
	make BINARY=exp-version-test VERSION="$malicious_version" build
)
if [ -e "$source_dir/$version_marker" ]; then
	printf '%s\n' "VERSION override executed shell content" >&2
	exit 1
fi
actual_override_version=$("$source_dir/exp-version-test" --version)
case "$actual_override_version" in
	*"$version_marker"*) ;;
	*)
		printf 'VERSION override was not linked into the binary: %s\n' "$actual_override_version" >&2
		exit 1
		;;
esac

quoted_version='release "candidate" 2'
(
	cd "$source_dir"
	make BINARY=exp-quoted-test VERSION="$quoted_version" build
)
actual_quoted_version=$("$source_dir/exp-quoted-test" --version)
if [ "$actual_quoted_version" != "exp version $quoted_version" ]; then
	printf 'quoted VERSION override was not linked safely: %s\n' "$actual_quoted_version" >&2
	exit 1
fi

fake_linker="$source_dir/fake-linker"
linker_marker="$source_dir/FAKE_LINKER_MUST_NOT_RUN"
# shellcheck disable=SC2016 # Preserve expansion for the generated fake linker.
printf '%s\n' \
	'#!/bin/sh' \
	'touch "$(dirname "$0")/FAKE_LINKER_MUST_NOT_RUN"' \
	'exit 99' >"$fake_linker"
chmod 0755 "$fake_linker"
ldflag_payload='safe" -linkmode=external -extld=./fake-linker -X "github.com/daviddwlee84/exp-cli/internal/cli.Version=owned'
(
	cd "$source_dir"
	make BINARY=exp-ldflag-test VERSION="$ldflag_payload" build
)
if [ -e "$linker_marker" ]; then
	printf '%s\n' 'VERSION injected an executable linker option' >&2
	exit 1
fi
actual_ldflag_version=$("$source_dir/exp-ldflag-test" --version)
if [ "$actual_ldflag_version" != "exp version $ldflag_payload" ]; then
	printf 'linker-looking VERSION was not preserved as data: %s\n' "$actual_ldflag_version" >&2
	exit 1
fi

unrepresentable_version="release \"candidate' 3"
if (
	cd "$source_dir"
	make BINARY=exp-unrepresentable-test VERSION="$unrepresentable_version" build
) >"$work/unrepresentable-version.log" 2>&1; then
	printf '%s\n' 'VERSION with whitespace and both quote characters did not fail closed' >&2
	exit 1
fi
if ! grep -F 'VERSION cannot contain whitespace together with both quote characters' "$work/unrepresentable-version.log" >/dev/null; then
	printf '%s\n' 'VERSION rejection did not explain the unrepresentable linker value' >&2
	exit 1
fi

make_marker=MAKE_OVERRIDE_COMMAND_MUST_NOT_RUN
# shellcheck disable=SC2016 # The literal Make expression is the malicious input.
make_expression='$(shell touch MAKE_OVERRIDE_COMMAND_MUST_NOT_RUN)'
(
	cd "$source_dir"
	make BINARY=exp-make-test VERSION="$make_expression" build
)
if [ -e "$source_dir/$make_marker" ]; then
	printf '%s\n' "VERSION override executed Make syntax" >&2
	exit 1
fi
actual_make_version=$("$source_dir/exp-make-test" --version)
if [ "$actual_make_version" != "exp version $make_expression" ]; then
	printf 'raw VERSION override was not linked into the binary: %s\n' "$actual_make_version" >&2
	exit 1
fi

prefix="$work/prefix with spaces"
skill_install_marker="$work/installed-binary-skill-install-called"
(
	cd "$source_dir"
	EXP_SKILL_INSTALL_MARKER="$skill_install_marker" PREFIX="$prefix" VERSION=portability-test make BINARY=exp-install-test install
)
installed="$prefix/bin/exp-install-test"
if [ ! -x "$installed" ]; then
	printf 'expected executable was not installed at %s\n' "$installed" >&2
	exit 1
fi
if [ ! -f "$skill_install_marker" ]; then
	printf '%s\n' 'make install did not invoke the installed binary for skill install --link' >&2
	exit 1
fi
actual_version=$("$installed" --version)
if [ "$actual_version" != "exp version portability-test" ]; then
	printf 'installed executable has unexpected version metadata: %s\n' "$actual_version" >&2
	exit 1
fi
