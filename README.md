# Supa Proto

Supa Proto is a proxy protocol implementation built on TCP and JSON messages.
The client exposes a local SOCKS5 endpoint, the server connects to destination
addresses, and the two peers exchange handshake, control, and data frames over
a persistent newline-delimited JSON session.

The protocol uses readable JSON as its outer message format. Its connection
behavior is intentionally close to that of a conventional long-lived
application service: a session is established, state is exchanged, data flows
continuously, and the connection closes normally. Application data and session
authentication remain protected by the protocol layer; Supa Proto does not
depend on HTTP, TLS, or a CDN.

## Features

- A local SOCKS5 client and a remote TCP server.
- JSON as a stable, observable boundary for messages and control data.
- Multi-user support, shared-secret authentication, rotating one-time
  passwords, and session-level encryption.
- Optional SOCKS5 proxying for server-side outbound connections.
- Configurable JSON message layouts; both endpoints must use matching settings.
- A SOCKS5 listener bound to `127.0.0.1:8444` by default and a server TCP
  listener on port `8443` by default.

When server-side SOCKS5 egress is configured, `outbound.dns` may be `"local"`
(the default) or `"remote"`. Remote DNS sends domain names to the SOCKS5
server unchanged. It does not permit Supa Proto to apply its local private-IP
check to names, so use it only with an upstream proxy that enforces its own
destination policy.

## Performance trade-offs

JSON encoding, message validation, and session control add CPU work, byte
overhead, and some latency. As a result, Supa Proto is not intended to match
the maximum throughput of a minimal binary forwarding protocol. Its goal is a
stable, well-bounded, and diagnosable long-lived proxy session. Evaluate it
with load tests in the intended network environment before using it for
bandwidth-intensive or latency-sensitive workloads.

## Quick start

The public repository only includes configuration templates. Copy them into a
local private directory, then set your own server address and random secret:

```bash
mkdir -p .private/configs
cp configs/server.example.json .private/configs/server.json
cp configs/client.example.json .private/configs/client.json
chmod 700 .private .private/configs
chmod 600 .private/configs/*.json
```

Start the server and client in separate terminals:

```bash
./scripts/start-server.sh
./scripts/start-client.sh
```

Applications can then use the SOCKS5 proxy at `127.0.0.1:8444`. On Linux,
`scripts/install.sh` can install the components as systemd services. Review the example
configuration and replace every placeholder before starting a service.

## Performance modes

Both endpoints accept a `performance.mode` setting. `interactive` is the
default and preserves the smaller initial frame target used for responsive
traffic. `throughput` starts at the 64 KiB protocol frame limit and coalesces
short adjacent reads for up to 2 ms, reducing per-frame overhead for sustained
bulk transfers. Configure the same mode on both endpoints when comparing
performance characteristics.

```json
{"performance":{"mode":"throughput"}}
```

## Custom JSON layouts

Supa Proto can load a restricted, static Provider descriptor to customize its
JSON field names, field order, optional metadata, and bounded maintenance
messages. See [the Obfs Provider guide](docs/OBFS_PROVIDER.md) for the public
descriptor format and authoring workflow.

## Mobile apps

`apps/` contains native clients for Android (8.0+) and iOS (16+). They run the
same Go core as the command-line client inside a system VPN (Android
`VpnService`, iOS Packet Tunnel), so every app on the device is covered without
configuring a SOCKS5 proxy.

<p align="center">
  <img src="docs/images/app-home.png" width="300"
       alt="Home screen of the iOS app while connected: session.json card, one-minute traffic chart and connection counts">
</p>

- **Servers**: keep several servers, test their TCP latency, and import or share
  them as `supaproto://` links or QR codes.
- **Routing**: global proxy, direct connections for addresses in China
  (GeoIP CN), or imported rules (domain, suffix, keyword, IP range and GeoIP).
  Domain names are passed to the server unresolved whenever traffic is proxied.
- **DNS**: DoH for local lookups, with a plain bootstrap DNS server used only for
  the proxy server and DoH host names; each routing mode keeps its own settings.
- **Protection**: a kill switch blocks proxied traffic instead of falling back
  to direct connections, and flows are closed when the device switches
  networks.
- **Statistics**: live TCP/UDP connection counts, upload/download speeds, a
  one-minute traffic chart and upload/download totals kept across connections.
  On iOS the home screen also shows the memory used by the tunnel extension.

The interface is available in English and Traditional Chinese. See
[apps/README.md](apps/README.md) for build instructions.

## Repository layout

```text
apps/          Native client apps (Kotlin Multiplatform), see apps/README.md
cmd/           Go entry points for supaproto-client and supaproto-server
internal/      Go core: config, protocol, obfs, flow control, SOCKS5, DNS, routing
mobile/        gomobile API for the native apps (scripts/build-mobile-core.sh)
integration/   End-to-end Go tests
configs/       Public configuration templates
docs/          Protocol and Provider documentation
scripts/       Local start scripts and Linux systemd install/uninstall
```

## DISCLAIMER

FOR EDUCATIONAL PURPOSES IN COMPUTER NETWORKING ONLY. YOU ARE RESPONSIBLE FOR
ENSURING THAT YOUR USE COMPLIES WITH ALL APPLICABLE LOCAL LAWS AND REGULATIONS.
THE AUTHOR ACCEPTS NO LIABILITY FOR MISUSE.

本项目仅供计算机网络知识学习。你有责任确保使用行为符合当地所有适用的法律法规；因不当
使用造成的任何后果，作者概不负责。
