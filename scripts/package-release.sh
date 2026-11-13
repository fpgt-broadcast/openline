#!/usr/bin/env bash
# Builds all release files into dist/release/.
#   scripts/package-release.sh <version> <path-to-opus.dll> [firmware-build-dir]
#   Optional: PORTAUDIO_DLL=<path to a Windows portaudio DLL> (sound card outputs)
# Used by .github/workflows/release.yml; also runnable locally.
set -euo pipefail

VERSION="${1:?usage: package-release.sh <version> <opus.dll> [firmware-build-dir]}"
OPUS_DLL="${2:?path to a Windows opus.dll}"
FW_BUILD="${3:-}"

cd "$(dirname "$0")/.."
OUT=dist/release
mkdir -p "$OUT"
find "$OUT" -mindepth 1 -delete

make release VERSION="$VERSION"

# Server everywhere; bridge for macOS/Linux as plain binaries.
cp dist/openline-server-* "$OUT"/
cp dist/openline-bridge-darwin-* dist/openline-bridge-linux-* "$OUT"/

# Windows bridge: zip with opus.dll, example config and double-click helpers.
STAGE=$(mktemp -d)
mkdir -p "$STAGE/openline-bridge"
cp dist/openline-bridge-windows-amd64.exe "$STAGE/openline-bridge/openline-bridge.exe"
cp "$OPUS_DLL" "$STAGE/openline-bridge/opus.dll"
if [ -n "${PORTAUDIO_DLL:-}" ]; then cp "$PORTAUDIO_DLL" "$STAGE/openline-bridge/portaudio.dll"; fi
cp packaging/windows/README.txt packaging/windows/run-bridge.bat packaging/windows/list-ndi-sources.bat packaging/windows/list-sound-cards.bat "$STAGE/openline-bridge/"
go run ./cmd/openline-bridge -init | sed 's/$/\r/' > "$STAGE/openline-bridge/bridge.example.json"
(cd "$STAGE" && python3 - "$OLDPWD/$OUT/openline-bridge-windows-amd64.zip" <<'EOF'
import os, sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk("openline-bridge"):
        for f in sorted(files):
            p = os.path.join(root, f)
            z.write(p, p)
EOF
)
find "$STAGE" -mindepth 1 -delete && rmdir "$STAGE"

# Beltpack firmware (optional): one merged image to flash at 0x0, plus parts.
if [ -n "$FW_BUILD" ] && [ -f "$FW_BUILD/openline-beltpack.bin" ]; then
  (cd "$FW_BUILD" && python -m esptool --chip esp32s3 merge_bin -o "$OLDPWD/$OUT/openline-beltpack-esp32s3-$VERSION.bin" @flash_args)
fi

(cd "$OUT" && sha256sum -- * > SHA256SUMS.txt)
ls -la "$OUT"
