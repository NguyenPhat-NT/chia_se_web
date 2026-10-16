#!/bin/sh
set -eu

GUIWEB_REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GUIWEB_WORKSPACE=${GUIWEB_WORKSPACE:-/workspace}
GUIWEB_GO_ROOT=${GUIWEB_GO_ROOT:-$GUIWEB_WORKSPACE/toolchains/go}
GUIWEB_BUILD_VERSION=${GUIWEB_BUILD_VERSION:-dev}
case "$GUIWEB_BUILD_VERSION" in
  *[!A-Za-z0-9._+-]*) printf 'Invalid GUIWEB_BUILD_VERSION.\n' >&2; exit 1 ;;
esac
export PATH="$GUIWEB_GO_ROOT/bin:$PATH"
export GOCACHE="${GUIWEB_GO_CACHE:-$GUIWEB_WORKSPACE/.cache/guiweb-go}"
export GOPATH="${GUIWEB_GO_PATH:-$GUIWEB_WORKSPACE/.cache/guiweb-gopath}"
export GOTOOLCHAIN=local

[ -x "$GUIWEB_GO_ROOT/bin/go" ] || {
  printf 'Run ./scripts/install-dev.sh first, or set GUIWEB_GO_ROOT.\n' >&2
  exit 1
}
GUIWEB_FOUND_VERSION=$(go version)
case "$GUIWEB_FOUND_VERSION" in
  "go version go1.27.2 "*) ;;
  *) printf 'Go 1.27.2 is required; found %s\n' "$GUIWEB_FOUND_VERSION" >&2; exit 1 ;;
esac
for GUIWEB_COMMAND in sha256sum tar mktemp; do
  command -v "$GUIWEB_COMMAND" >/dev/null 2>&1 || {
    printf 'Required command is missing: %s\n' "$GUIWEB_COMMAND" >&2
    exit 1
  }
done
cd "$GUIWEB_REPO"
mkdir -p bin "$GOCACHE" "$GOPATH"
for GUIWEB_ARCH in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$GUIWEB_ARCH" \
    go build -mod=readonly -trimpath -ldflags="-s -w -X main.version=$GUIWEB_BUILD_VERSION" -o "bin/guiweb-linux-$GUIWEB_ARCH" ./cmd/guiweb
done
(cd bin && sha256sum guiweb-linux-amd64 guiweb-linux-arm64 > SHA256SUMS)
GUIWEB_PACKAGE_DIR=$(mktemp -d)
trap 'rm -rf -- "$GUIWEB_PACKAGE_DIR"' EXIT HUP INT TERM
cp bin/guiweb-linux-arm64 "$GUIWEB_PACKAGE_DIR/"
sed -n '/ guiweb-linux-arm64$/p' bin/SHA256SUMS > "$GUIWEB_PACKAGE_DIR/guiweb-arm64.sha256"
cp deploy/guiweb.init deploy/config.example.json deploy/tunnel.ingress.fragment.yml docs/OPENWRT.md docs/UPGRADE_0.1.3.md "$GUIWEB_PACKAGE_DIR/"
tar -czf bin/guiweb-openwrt-arm64.tar.gz -C "$GUIWEB_PACKAGE_DIR" guiweb-linux-arm64 guiweb-arm64.sha256 guiweb.init config.example.json tunnel.ingress.fragment.yml OPENWRT.md UPGRADE_0.1.3.md
printf 'Built Linux amd64 + arm64, checksums and bin/guiweb-openwrt-arm64.tar.gz.\n'
