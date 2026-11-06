#!/usr/bin/env bash
set -euo pipefail

COMPONENT="${1:-all}"
ASSUME_YES=false
if [[ "$COMPONENT" == "--yes" ]]; then COMPONENT=all; ASSUME_YES=true; fi
[[ "${2:-}" == "--yes" ]] && ASSUME_YES=true
case "$COMPONENT" in server|client|all) ;; *) echo "Usage: $0 [server|client|all] [--yes]" >&2; exit 2;; esac
if [[ -n "${2:-}" && "${2:-}" != "--yes" ]] || [[ -n "${3:-}" ]]; then echo "Usage: $0 [server|client|all] [--yes]" >&2; exit 2; fi
if [[ "$(uname -s)" != "Linux" ]]; then echo "Only Linux is supported." >&2; exit 1; fi
if [[ "$EUID" -ne 0 ]]; then echo "Run as root." >&2; exit 1; fi
if ! command -v systemctl >/dev/null; then echo "systemctl is required." >&2; exit 1; fi

INSTALL_DIR="/usr/local/lib/supaproto"
CONFIG_DIR="/etc/supaproto"
STATE_DIR="/var/lib/supaproto"

if ! "$ASSUME_YES"; then
  echo "This removes Supa Proto $COMPONENT service files, binary, configuration, and its default log."
  read -r -p "Type DELETE to continue: " CONFIRM
  [[ "$CONFIRM" == "DELETE" ]] || { echo "Cancelled."; exit 0; }
fi

remove_component() {
  local component="$1"
  local name="supaproto-$component"
  systemctl disable --now "$name.service" 2>/dev/null || true
  rm -f "/etc/systemd/system/$name.service"
  rm -f "$INSTALL_DIR/$name" "$CONFIG_DIR/$name.json" "$STATE_DIR/logs/$component.log"
}

if [[ "$COMPONENT" == "server" || "$COMPONENT" == "all" ]]; then remove_component server; fi
if [[ "$COMPONENT" == "client" || "$COMPONENT" == "all" ]]; then remove_component client; fi
systemctl daemon-reload

if [[ "$COMPONENT" == "all" ]]; then
  rm -rf "$INSTALL_DIR" "$CONFIG_DIR" "$STATE_DIR"
else
  rmdir "$INSTALL_DIR" "$CONFIG_DIR" "$STATE_DIR/logs" "$STATE_DIR" 2>/dev/null || true
fi
echo "Supa Proto $COMPONENT installation has been removed. Source directories were not touched."
