#!/bin/sh
# Cross-compile claude-mini release binaries into ./dist for every supported
# platform. Pure-Go (CGO disabled) so it builds from any host — Windows, macOS,
# or Linux. Upload everything in ./dist as assets on the GitHub Release.
#
# Usage:  sh build.sh
set -e

OUT="dist"
rm -rf "$OUT"
mkdir -p "$OUT"

# os/arch pairs to build. Asset names follow the convention the installers
# expect: claude-mini-<os>-<arch>[.exe]
TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

for t in $TARGETS; do
    os="${t%/*}"
    arch="${t#*/}"
    name="claude-mini-${os}-${arch}"
    ext=""
    [ "$os" = "windows" ] && ext=".exe"
    echo "  building ${name}${ext}"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
        go build -trimpath -ldflags "-s -w" -o "${OUT}/${name}${ext}" .
done

echo
echo "Done. Release assets are in ./${OUT}:"
ls -1 "$OUT"
