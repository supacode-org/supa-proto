#!/usr/bin/env bash
set -euo pipefail

COMPONENT="${1:-all}"
case "$COMPONENT" in server|client|all) ;; *) echo "Usage: $0 [server|client|all]" >&2; exit 2;; esac
if [[ "$(uname -s)" != "Linux" ]]; then echo "Only Linux is supported." >&2; exit 1; fi
if [[ "$EUID" -ne 0 ]]; then echo "Run as root (for example: sudo $0 $COMPONENT)." >&2; exit 1; fi
if ! command -v systemctl >/dev/null || [[ ! -d /run/systemd/system ]]; then echo "systemd is required." >&2; exit 1; fi

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_DIR="/usr/local/lib/supaproto"
CONFIG_DIR="/etc/supaproto"
STATE_DIR="/var/lib/supaproto"
SERVICE_USER="supaproto"
TEMP_DIR="$(mktemp -d)"
GO_BINARY=""
cleanup() { rm -rf "$TEMP_DIR"; }
trap cleanup EXIT

need_server=false
need_client=false
[[ "$COMPONENT" == "server" || "$COMPONENT" == "all" ]] && need_server=true
[[ "$COMPONENT" == "client" || "$COMPONENT" == "all" ]] && need_client=true

if command -v go >/dev/null 2>&1; then
  GO_BINARY="$(command -v go)"
else
  case "$(uname -m)" in
    x86_64) GO_ARCH="amd64" ;;
    aarch64|arm64) GO_ARCH="arm64" ;;
    *) echo "Unsupported Linux architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  SUPAPROTO_GO_VERSION="${SUPAPROTO_GO_VERSION:-1.24.3}"
  GO_ARCHIVE="go${SUPAPROTO_GO_VERSION}.linux-${GO_ARCH}.tar.gz"
  echo "Go is unavailable; downloading temporary ${GO_ARCHIVE}..."
  curl -fL --retry 3 --output "$TEMP_DIR/go.tgz" "https://go.dev/dl/${GO_ARCHIVE}"
  curl -fL --retry 3 --output "$TEMP_DIR/go.tgz.sha256" "https://dl.google.com/go/${GO_ARCHIVE}.sha256"
  EXPECTED_SHA="$(awk '{print $1}' "$TEMP_DIR/go.tgz.sha256")"
  ACTUAL_SHA="$(sha256sum "$TEMP_DIR/go.tgz" | awk '{print $1}')"
  [[ "$EXPECTED_SHA" == "$ACTUAL_SHA" ]] || { echo "Go archive checksum mismatch." >&2; exit 1; }
  tar -C "$TEMP_DIR" -xzf "$TEMP_DIR/go.tgz"
  GO_BINARY="$TEMP_DIR/go/bin/go"
fi

mkdir -p "$TEMP_DIR/bin"
if "$need_server"; then "$GO_BINARY" build -trimpath -ldflags="-s -w" -o "$TEMP_DIR/bin/supaproto-server" "$ROOT_DIR/cmd/supaproto-server"; fi
if "$need_client"; then "$GO_BINARY" build -trimpath -ldflags="-s -w" -o "$TEMP_DIR/bin/supaproto-client" "$ROOT_DIR/cmd/supaproto-client"; fi

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
fi
install -d -m 0755 "$INSTALL_DIR"
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$STATE_DIR"
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$STATE_DIR/logs"
install -d -m 0750 -o root -g "$SERVICE_USER" "$CONFIG_DIR"

install_component() {
  local name="$1"
  local example="$2"
  install -m 0755 "$TEMP_DIR/bin/$name" "$INSTALL_DIR/$name"
  if [[ ! -e "$CONFIG_DIR/$name.json" ]]; then
    install -m 0640 -o root -g "$SERVICE_USER" "$example" "$CONFIG_DIR/$name.json"
    if [[ "$name" == "supaproto-server" ]]; then
      sed -i "s#\"logs/server.log\"#\"$STATE_DIR/logs/server.log\"#" "$CONFIG_DIR/$name.json"
    else
      sed -i "s#\"logs/client.log\"#\"$STATE_DIR/logs/client.log\"#" "$CONFIG_DIR/$name.json"
    fi
    echo "Created $CONFIG_DIR/$name.json. Replace its placeholder secret before starting."
  else
    chown root:"$SERVICE_USER" "$CONFIG_DIR/$name.json"
    chmod 0640 "$CONFIG_DIR/$name.json"
    echo "Preserved existing $CONFIG_DIR/$name.json"
  fi
}

write_unit() {
  local name="$1"
  cat > "/etc/systemd/system/$name.service" <<UNIT
[Unit]
Description=Supa Proto $name
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$CONFIG_DIR
ExecStart=$INSTALL_DIR/$name -config $CONFIG_DIR/$name.json
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=full
ReadWritePaths=$STATE_DIR

[Install]
WantedBy=multi-user.target
UNIT
}

if "$need_server"; then
  install_component supaproto-server "$ROOT_DIR/configs/server.example.json"
  write_unit supaproto-server
fi
if "$need_client"; then
  install_component supaproto-client "$ROOT_DIR/configs/client.example.json"
  write_unit supaproto-client
fi
systemctl daemon-reload

echo "Installation complete. Services were installed but NOT enabled or started."
echo "Edit the JSON files under $CONFIG_DIR, then start explicitly:"
if [[ "$COMPONENT" == "all" ]]; then
  echo "  systemctl enable --now supaproto-server supaproto-client"
else
  echo "  systemctl enable --now supaproto-$COMPONENT"
fi
