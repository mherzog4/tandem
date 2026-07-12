#!/bin/sh
# Tandem installer: fetches the latest release binary for this machine.
#   curl -fsSL https://raw.githubusercontent.com/mherzog4/tandem/main/scripts/install.sh | sh
#
# Installs to ~/.local/bin by default — no sudo. Override with
# TANDEM_BIN_DIR (e.g. TANDEM_BIN_DIR=/usr/local/bin, which needs sudo).
set -eu

REPO="mherzog4/tandem"
BIN_DIR="${TANDEM_BIN_DIR:-$HOME/.local/bin}"

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

mkdir -p "$BIN_DIR"
dest="$BIN_DIR/tandem"
if [ -w "$BIN_DIR" ]; then
  mv "$tmp" "$dest"
else
  echo "installing to $dest (sudo required — $BIN_DIR is not writable)"
  sudo mv "$tmp" "$dest"
fi
echo "installed: $("$dest" --version)  →  $dest"

# Nudge to add the dir to PATH if it isn't already there.
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    echo
    echo "note: $BIN_DIR is not on your PATH. Add it, then restart your shell:"
    echo "  echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.zshrc   # or ~/.bashrc"
    ;;
esac
