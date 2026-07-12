#!/bin/sh
# Cut a tandem release WITHOUT GitHub Actions: cross-compile all targets
# locally (Go needs no CI for this) and publish a GitHub Release. Creating
# a release and uploading assets uses zero Actions minutes.
#
# Usage: scripts/release.sh v0.1.0
set -eu

VERSION="${1:?usage: release.sh vX.Y.Z}"
case "$VERSION" in v*) ;; *) echo "version must start with v" >&2; exit 1 ;; esac

REPO="mherzog4/tandem"
OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

echo "building $VERSION → $OUT"
for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
  os="${target%/*}"; arch="${target#*/}"
  for bin in tandem relay; do
    name="$bin-$os-$arch"
    [ "$bin" = relay ] && name="tandem-relay-$os-$arch"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
      -trimpath -ldflags "-s -w -X main.version=${VERSION#v}" \
      -o "$OUT/$name" "./cmd/$bin"
  done
done
echo "built:"; ls -1 "$OUT"

# Tag if it doesn't exist yet.
if ! git rev-parse "$VERSION" >/dev/null 2>&1; then
  git tag "$VERSION"
  git push origin "$VERSION"
fi

# Publish (or update) the release with the binaries.
if gh release view "$VERSION" -R "$REPO" >/dev/null 2>&1; then
  gh release upload "$VERSION" "$OUT"/* -R "$REPO" --clobber
else
  gh release create "$VERSION" "$OUT"/* -R "$REPO" \
    --title "$VERSION" --generate-notes
fi
echo "released $VERSION"

# Refresh the Homebrew formula against the just-published binaries. If the
# tree is clean the change is committed and pushed; otherwise it is left
# for the caller to review.
echo "updating Homebrew formula"
if scripts/update-formula.sh "$VERSION" > HomebrewFormula/tandem.rb; then
  if [ -z "$(git status --porcelain HomebrewFormula/tandem.rb)" ]; then
    echo "formula already current"
  elif [ -n "$(git status --porcelain)" ] && [ "$(git status --porcelain | grep -vc 'HomebrewFormula/tandem.rb')" != "0" ]; then
    echo "working tree has other changes; commit HomebrewFormula/tandem.rb yourself"
  else
    git add HomebrewFormula/tandem.rb
    git commit -q -m "Homebrew: update formula to $VERSION"
    git push -q origin HEAD
    echo "formula updated to $VERSION"
  fi
fi
