#!/bin/sh
set -eu

ROOT="$(cd "$(dirname "$0")" && pwd)"
APP="$ROOT/build/LG TV Control.app"
CONTENTS="$APP/Contents"
MACOS="$CONTENTS/MacOS"
RESOURCES="$CONTENTS/Resources"
BIN="$ROOT/build/bin"
cd "$ROOT"
export GOOS=darwin GOARCH=arm64

rm -rf "$APP"
mkdir -p "$MACOS" "$RESOURCES" "$BIN"

cp Info.plist "$CONTENTS/Info.plist"
cp Assets/AppIcon.icns "$RESOURCES/AppIcon.icns"

go build -trimpath -ldflags="-s -w" -tags mygo_noinspector -o "$BIN/lgtv" ./cmd/lgtv-ui

LICENSES="$RESOURCES/Licenses"
mkdir -p "$LICENSES/Go"
cp LICENSE "$RESOURCES/LICENSE"
cp THIRD_PARTY_LICENSES.md "$RESOURCES/THIRD_PARTY_LICENSES.md"
GO_ROOT=$(go env GOROOT)
if [ -f "$GO_ROOT/LICENSE" ]; then
  cp "$GO_ROOT/LICENSE" "$LICENSES/Go/"
else
  cp "$GO_ROOT/../LICENSE" "$LICENSES/Go/"
fi
MODULES=$(go list -deps -tags mygo_noinspector -f '{{if and .Module (not .Module.Main)}}{{.Module.Path}}|{{.Module.Dir}}{{end}}' ./cmd/lgtv-ui)
printf '%s\n' "$MODULES" | sort -u | while IFS='|' read -r package dir; do
  [ -n "$package" ] || continue
  mkdir -p "$LICENSES/$package"
  for notice in "$dir"/LICENSE* "$dir"/NOTICE* "$dir"/COPYING* "$dir"/PATENTS; do
    [ -f "$notice" ] || continue
    cp "$notice" "$LICENSES/$package/"
  done
done

CLI_LICENSES="$ROOT/build/lgtv-LICENSES.txt"
{
  cat LICENSE
  find "$LICENSES" -type f | LC_ALL=C sort | while IFS= read -r notice; do
    printf '\n\n%s\n\n' "${notice#"$LICENSES"/}"
    cat "$notice"
  done
} > "$CLI_LICENSES"

CODESIGN_IDENTITY="${LGTV_CODESIGN_IDENTITY:-LG TV Control Dev}"
if ! security find-identity -p codesigning -v | grep -Fq -- "$CODESIGN_IDENTITY"; then
  echo "Warning: '$CODESIGN_IDENTITY' not found. Falling back to ad-hoc signing." >&2
  echo "Run ./scripts/setup-codesign.sh for stable Keychain access." >&2
  CODESIGN_IDENTITY=-
fi

APP_ID="io.github.li-yifei.lgtv-control"
codesign --force --sign "$CODESIGN_IDENTITY" --identifier "$APP_ID" "$BIN/lgtv"
cp "$BIN/lgtv" "$MACOS/LGTVControl"
codesign --force --sign "$CODESIGN_IDENTITY" --identifier "$APP_ID" "$APP"
codesign --verify --strict "$BIN/lgtv"
codesign --verify --strict "$APP"

ZIP="$ROOT/build/LG-TV-Control.app.zip"
rm -f "$ZIP"
(cd "$ROOT/build" && /usr/bin/ditto -c -k --keepParent "LG TV Control.app" "$ZIP")

echo "$APP"
echo "$ZIP"
