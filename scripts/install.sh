#!/bin/sh
# Tandem installer: fetches the latest release binary for this machine.
#   curl -fsSL https://raw.githubusercontent.com/mherzog4/tandem/main/scripts/install.sh | sh
set -eu

REPO="mherzog4/tandem"
BIN_DIR="${TANDEM_BIN_DIR:-/usr/local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  darwin | linux) ;;
  *) echo "unsupported OS: $os (Windows: use WSL)" >&2; exit 1 ;;
esac

url="https://github.com/$REPO/releases/latest/download/tandem-$os-$arch"
echo "downloading $url"
tmp=$(mktemp)
curl -fsSL "$url" -o "$tmp"
chmod +x "$tmp"

dest="$BIN_DIR/tandem"
if [ -w "$BIN_DIR" ]; then
  mv "$tmp" "$dest"
else
  echo "installing to $dest (sudo required)"
  sudo mv "$tmp" "$dest"
fi
echo "installed: $("$dest" --version)"
