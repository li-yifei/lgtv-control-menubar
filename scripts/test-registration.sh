#!/bin/sh
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT
swiftc -swift-version 5 \
  "$ROOT/Sources/LGTVControl/Models.swift" \
  "$ROOT/Sources/LGTVControl/WebOSClient.swift" \
  "$ROOT/Tests/RegistrationChecks.swift" \
  -o "$TEST_DIR/registration-checks"
"$TEST_DIR/registration-checks"
