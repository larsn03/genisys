#!/usr/bin/env bash
# Build the primary Linux artifact (SPEC §19.1 / G13):
#   Genisys-<version>-x86_64.appimage
# Requires appimagetool (https://github.com/AppImage/appimagetool). The
# binary is fully static (CGO off) with UI + sandbox assets embedded, so
# the AppImage is just a launcher around it.
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
ARCH="${ARCH:-x86_64}"
GOARCH="${GOARCH:-amd64}"

# Homebrew pkg-config doesn't search distro dirs (dev-box quirk)
[ -d /usr/lib64/pkgconfig ] && PKG_CONFIG_PATH="${PKG_CONFIG_PATH:-}:/usr/lib64/pkgconfig"
export PKG_CONFIG_PATH

APPDIR="dist/AppDir"
OUT="dist/Genisys-${VERSION}-${ARCH}.appimage"

rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/share/icons/hicolor/256x256/apps"

# Primary artifact is the DESKTOP build (SPEC Q1 resolved): CGO on +
# Wails native window. Hosts need GTK4 + WebKitGTK 6.0 (standard on
# GNOME-based Fedora/Ubuntu/Debian); without them the binary degrades
# to the browser flow instead of failing.
CGO_ENABLED=1 GOOS=linux GOARCH="$GOARCH" go build -tags wails \
  -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
  -o "$APPDIR/usr/bin/genisys" ./cmd/genisys

cp packaging/genisys.desktop "$APPDIR/genisys.desktop"
cp packaging/icon.png "$APPDIR/usr/share/icons/hicolor/256x256/apps/genisys.png"
cp packaging/icon.png "$APPDIR/genisys.png"

# AppRun: the AppImage entry point. `serve` prints the tokenized URL and
# opens the default browser by itself (SPEC §19.1 launcher contract).
cat > "$APPDIR/AppRun" <<'EOF'
#!/usr/bin/env bash
here="$(dirname "$(readlink -f "$0")")"
exec "$here/usr/bin/genisys" "$@"   # no args → native window
EOF
chmod +x "$APPDIR/AppRun"

TOOL="${APPIAMETOOL:-appimagetool}"
if ! command -v "$TOOL" >/dev/null 2>&1; then
  echo "appimagetool not found — set APPIAMETOOL=/path/to/appimagetool" >&2
  echo "AppDir staged at $APPDIR (run appimagetool manually)." >&2
  exit 1
fi

VERSION="$VERSION" ARCH="$ARCH" "$TOOL" --no-appstream "$APPDIR" "$OUT"
echo "built: $OUT"
