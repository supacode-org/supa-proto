#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG_PATH="${1:-$ROOT_DIR/.private/configs/client.json}"

if [[ ! -f "$CONFIG_PATH" ]]; then
  echo "Client config not found: $CONFIG_PATH" >&2
  echo "Create it from the public template:" >&2
  echo "  mkdir -p \"$ROOT_DIR/.private/configs\"" >&2
  echo "  cp \"$ROOT_DIR/configs/client.example.json\" \"$ROOT_DIR/.private/configs/client.json\"" >&2
  exit 1
fi

echo "Starting Supa Proto client in the foreground (not installed as a service)."
exec go run "$ROOT_DIR/cmd/supaproto-client" -config "$CONFIG_PATH"
