# Zorven

**Self-hosted, open-source reverse proxy and secure tunneling platform — a Cloudflare Tunnel / ngrok alternative you fully control.**

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8.svg)](https://go.dev)

Zorven exposes a local service (`localhost:8000`) on a stable public domain
(`https://api.yourdomain.com`) without port forwarding, a static IP, or opening
firewall ports. The client opens a single **outbound**, encrypted tunnel to the
server; inbound public requests are routed back through it to your local service.

It works behind CGNAT, dynamic IPs, and restrictive firewalls — anywhere a plain
outbound HTTPS connection is allowed.

---

## Why Zorven

- **You own the whole stack.** Run the server on your own VPS; no third party sees your traffic.
- **Outbound-only client.** No inbound ports, no NAT/firewall changes on the client side.
- **Stable public URLs.** Automatic per-tunnel hostnames plus your own custom domains.
- **More than HTTP.** Live request logs, remote terminal (PTY), and screen sharing over the same tunnel.
- **Batteries included.** Multi-tenant dashboard, team members, self-hosted webmail (SPF/DKIM/DMARC), API tokens, IP allow-lists.
- **One-command client.** `zorven 8080` — ngrok-style.

## Features

- HTTP and WebSocket tunneling with streaming request/response bodies
- Automatic scoped hostnames (`myapp--tenant.yourdomain.com`) and custom domains with ACME TLS
- Web dashboard (Vue/Nuxt) with live traffic, tunnels, clients, domains, tokens, and team management
- CLI client and cross-platform desktop app (Windows/macOS/Linux)
- Remote terminal and screen sharing (off by default; opt-in per client for security)
- Self-hosted webmail: inbound receiver + outbound sender with DKIM signing
- Multi-tenant data model, GitHub OAuth / email auth, API tokens, per-tunnel IP allow-lists
- Postgres-backed; ships with a Docker Compose stack for one-command self-hosting

## Architecture

```
                       Internet
                          |
                   (public HTTPS)
                          v
   +------------------------------------------+
   |            Zorven Server (VPS)            |
   |  ingress proxy  -  tunnel hub  -  API     |
   |  dashboard  -  webmail  -  Postgres       |
   +------------------------------------------+
                          ^
                (outbound WSS tunnel)
                          |
   +------------------------------------------+
   |        Zorven Client (your machine)       |
   |     forwards to http://localhost:8000     |
   +------------------------------------------+
```

The client dials `wss://<server>/_tunnel/v1/connect`, authenticates with a client
token, and keeps a persistent multiplexed connection. The server terminates
public TLS, resolves the hostname to a tunnel, and forwards the request down the
tunnel to the client's local target.

## Quick start

### Use the client (against your own server)

```bash
# Linux/macOS
curl -fsSL https://<your-server>/install.sh | sh
```

```powershell
# Windows (PowerShell)
irm https://<your-server>/install.ps1 | iex
```

Then expose a local port:

```bash
zorven authtoken zrv_live_...   # one-time, token from the dashboard
zorven 8080                     # exposes http://localhost:8080
```

Check status any time:

```bash
zorven status
```

Remote terminal and screen sharing are **disabled by default**. Enable per run:

```bash
zorven 8080 --enable-terminal --enable-screen
```

### Self-host the server

Requirements: a VPS with a public IP, a domain with wildcard DNS
(`yourdomain.com` and `*.yourdomain.com`) pointing to it, and Docker.

```bash
git clone https://github.com/tkodcumpeg4/zorven.git
cd zorven/deploy
cp .env.example .env      # fill in PLATFORM_DOMAIN, secrets, ACME_EMAIL, ...
docker compose up -d
```

The server obtains TLS certificates via ACME automatically and serves the
dashboard, API, and tunnel endpoint on the same origin. See `deploy/` for the
full Compose stack (server, Postgres, mail relay).

## Build from source

Zorven is a Go workspace (`go.work`) with four modules plus a Nuxt dashboard.

```bash
# Server + client + shared
cd server && go build ./...
cd ../client && go build -o zorven .

# Dashboard (embedded into the server binary)
cd web && npm install && npm run build
bash scripts/build-dashboard.sh    # copies build output into server/webdist
```

Go 1.23+ and Node 20+ are required.

## Project structure

| Path | What |
|------|------|
| `server/` | Tunnel server, ingress proxy, HTTP API, webmail, Postgres store |
| `client/` | CLI tunnel agent (`zorven`) |
| `desktop/` | Cross-platform desktop app (Wails) |
| `shared/` | Wire protocol shared by client and server |
| `web/` | Nuxt dashboard (embedded into the server via `go:embed`) |
| `deploy/` | Docker Compose self-hosting stack |

## Open core

Zorven is **open core**. This repository contains the complete tunneling
product under the **AGPL-3.0** license: server, clients, dashboard, webmail,
remote access — fully functional and unlimited for self-hosting, with no feature
gates or quotas.

Commercial extensions for running Zorven as a paid, multi-tenant service —
plan-based quota enforcement, billing, SSO/SAML, audit logging, and multi-node
high availability — are distributed separately under a commercial license and
are **not** part of this repository. The core defines the integration seams as
interfaces; without the commercial layer everything runs unlimited.

## Contributing

Issues and pull requests are welcome. By contributing you agree that your
contributions are licensed under the AGPL-3.0. Please open an issue to discuss
substantial changes before starting.

## License

Copyright (C) 2026 the Zorven authors.

This program is free software: you can redistribute it and/or modify it under
the terms of the **GNU Affero General Public License, version 3**, as published
by the Free Software Foundation. See [LICENSE](LICENSE) for the full text.

The AGPL requires that if you run a modified version of Zorven as a network
service, you must make the corresponding source of your modified version
available to its users.
