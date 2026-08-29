BINARY  := exp
PREFIX  ?= $(HOME)/.local

ifeq ($(origin VERSION), undefined)
VERSION := $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || printf '%s' dev)
else
# Freeze an override's raw bytes so Make syntax in untrusted metadata is data.
override VERSION := $(value VERSION)
endif

export BINARY
export PREFIX
export VERSION

.PHONY: all build install fmt fmt-check vet test test-race test-build-portability clean

all: fmt-check vet test-race build

# cmd/go parses -ldflags itself. Quote the complete -X field only when VERSION
# contains whitespace; an unquoted field safely carries quote characters as data.
build:
	@version_arg='github.com/daviddwlee84/exp-cli/internal/cli.Version='"$${VERSION}"; \
	case "$${VERSION}" in \
		*[[:space:]]*) \
			case "$${VERSION}" in \
				*\"*) \
					case "$${VERSION}" in \
						*\'*) printf '%s\n' 'VERSION cannot contain whitespace together with both quote characters' >&2; exit 2 ;; \
						*) version_arg="'$${version_arg}'" ;; \
					esac ;; \
				*) version_arg="\"$${version_arg}\"" ;; \
			esac ;; \
	esac; \
	go build -trimpath -ldflags="-s -w -X $${version_arg}" -o "$${BINARY}" ./cmd/exp

install: build
	install -d "$${PREFIX}/bin"
	install -m 0755 "$${BINARY}" "$${PREFIX}/bin/$${BINARY}"

fmt:
	gofmt -w cmd internal

fmt-check:
	@files="$$(gofmt -l cmd internal)"; \
	if [ -n "$$files" ]; then \
		printf '%s\n' "$$files" >&2; \
		exit 1; \
	fi

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

test-build-portability:
	./scripts/test-build-portability.sh

clean:
	rm -f "$${BINARY}"
	go clean -testcache
