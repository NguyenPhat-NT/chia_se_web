#!/bin/sh
set -eu

# Use the existing checkout: cloud tasks are isolated; do not create a worktree.
GUIWEB_REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GUIWEB_WORKSPACE=${GUIWEB_WORKSPACE:-/workspace}
GUIWEB_GO_ROOT=${GUIWEB_GO_ROOT:-$GUIWEB_WORKSPACE/toolchains/go}
GUIWEB_GO_VERSION=go1.27.2
GUIWEB_GO_ARCHIVE=go1.27.2.linux-amd64.tar.gz
GUIWEB_GO_SHA256=ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5

for GUIWEB_COMMAND in curl sha256sum tar mktemp; do
  command -v "$GUIWEB_COMMAND" >/dev/null 2>&1 || {
    printf 'Required command is missing: %s\n' "$GUIWEB_COMMAND" >&2
    exit 1
  }
done

if [ -e "$GUIWEB_GO_ROOT" ]; then
  if [ ! -x "$GUIWEB_GO_ROOT/bin/go" ]; then
    printf 'Refusing to overwrite existing path: %s\n' "$GUIWEB_GO_ROOT" >&2
    exit 1
  fi
  GUIWEB_FOUND_VERSION=$("$GUIWEB_GO_ROOT/bin/go" version)
  case "$GUIWEB_FOUND_VERSION" in
    "go version $GUIWEB_GO_VERSION "*) ;;
    *) printf 'Existing SDK differs from pinned SDK: %s\nChoose another GUIWEB_GO_ROOT; existing files were kept.\n' "$GUIWEB_FOUND_VERSION" >&2; exit 1 ;;
  esac
else
  [ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ] || {
    printf 'Automatic SDK installation supports Linux amd64 only. Use a verified Go 1.27.2 SDK and set GUIWEB_GO_ROOT.\n' >&2
    exit 1
  }
  GUIWEB_GO_PARENT=$(dirname -- "$GUIWEB_GO_ROOT")
  mkdir -p "$GUIWEB_GO_PARENT"
  GUIWEB_GO_TMP=$(mktemp -d "$GUIWEB_GO_PARENT/.guiweb-go.XXXXXX")
  trap 'rm -rf -- "$GUIWEB_GO_TMP"' EXIT HUP INT TERM
  curl --fail --location --show-error --silent --proto '=https' --proto-redir '=https' \
    "https://go.dev/dl/$GUIWEB_GO_ARCHIVE" -o "$GUIWEB_GO_TMP/$GUIWEB_GO_ARCHIVE"
  printf '%s  %s\n' "$GUIWEB_GO_SHA256" "$GUIWEB_GO_TMP/$GUIWEB_GO_ARCHIVE" | sha256sum --check --status
  tar -xzf "$GUIWEB_GO_TMP/$GUIWEB_GO_ARCHIVE" -C "$GUIWEB_GO_TMP"
  [ ! -e "$GUIWEB_GO_ROOT" ] || {
    printf 'SDK destination appeared during installation; refusing to overwrite it.\n' >&2
    exit 1
  }
  mv "$GUIWEB_GO_TMP/go" "$GUIWEB_GO_ROOT"
fi

export PATH="$GUIWEB_GO_ROOT/bin:$PATH"
export GOCACHE="${GUIWEB_GO_CACHE:-$GUIWEB_WORKSPACE/.cache/guiweb-go}"
export GOPATH="${GUIWEB_GO_PATH:-$GUIWEB_WORKSPACE/.cache/guiweb-gopath}"
export GOTOOLCHAIN=local
mkdir -p "$GOCACHE" "$GOPATH"
cd "$GUIWEB_REPO"
[ -f go.mod ] && [ -f go.sum ] || {
  printf 'The checkout must contain its committed go.mod and go.sum.\n' >&2
  exit 1
}
go version
go list -mod=readonly -deps ./... >/dev/null
printf 'Go and pinned dependencies are ready. Build with ./scripts/build.sh.\n'
