#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

IDENTITY="${LGTV_CODESIGN_IDENTITY:-LG TV Control Dev}"
if [ "$IDENTITY" = - ] || ! security find-identity -p codesigning -v | grep -Fq -- "$IDENTITY"; then
  echo "Code signing identity '$IDENTITY' is unavailable. Run ./scripts/setup-codesign.sh or set LGTV_CODESIGN_IDENTITY." >&2
  exit 1
fi

exec go tool mygo dev -sign "$IDENTITY" "$@"
