#!/usr/bin/env bash
set -euo pipefail

# Builds the Go core for the native apps with gomobile.
#   scripts/build-mobile-core.sh [ios|android|all]
# gomobile and gobind come from the versions pinned in go.mod.

TARGET="${1:-all}"
case "$TARGET" in ios|android|all) ;; *) echo "Usage: $0 [ios|android|all]" >&2; exit 2;; esac

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$ROOT_DIR/apps/build"
BIN_DIR="$OUT_DIR/bin"
ANDROID_API=26

cd "$ROOT_DIR"
mkdir -p "$BIN_DIR"
go build -o "$BIN_DIR/gomobile" golang.org/x/mobile/cmd/gomobile
go build -o "$BIN_DIR/gobind" golang.org/x/mobile/cmd/gobind
export PATH="$BIN_DIR:$PATH"

if [[ "$TARGET" == "ios" || "$TARGET" == "all" ]]; then
  rm -rf "$OUT_DIR/SupaProtoCore.xcframework"
  gomobile bind -target=ios,iossimulator -iosversion=16.0 -trimpath -ldflags="-s -w" \
    -o "$OUT_DIR/SupaProtoCore.xcframework" ./mobile
  echo "Built $OUT_DIR/SupaProtoCore.xcframework"
fi

if [[ "$TARGET" == "android" || "$TARGET" == "all" ]]; then
  if [[ -z "${ANDROID_NDK_HOME:-}" && -z "${ANDROID_HOME:-}" ]]; then
    echo "Skipping Android: set ANDROID_HOME (with the NDK installed) or ANDROID_NDK_HOME." >&2
  else
    gomobile bind -target=android -androidapi "$ANDROID_API" -javapkg=org.supacode.protomobile.core -trimpath -ldflags="-s -w" \
      -o "$OUT_DIR/supaproto-core.aar" ./mobile
    echo "Built $OUT_DIR/supaproto-core.aar"
  fi
fi
