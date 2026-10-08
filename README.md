# Zorven

**Self-hosted, open-source reverse proxy and secure tunneling platform — a Cloudflare Tunnel / ngrok alternative you fully control.**

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)

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
- **More than HTTP.** Raw TCP, UDP, SNI passthrough, live request logs, remote terminal (PTY), and screen sharing over the same tunnel.
- **Edge gateway built in.** Policy engine, WAF, webhook signature verification, mTLS, and load balancing in the tunnel server itself.
- **Batteries included.** Multi-tenant dashboard, projects, team invitations, self-hosted webmail (SPF/DKIM/DMARC), API tokens, IP allow-lists.
- **One-command client.** `zorven 8080` — ngrok-style.

## Features

**Tunneling**

- HTTP, WebSocket, and SSE tunneling with streaming request/response bodies
- Raw TCP and UDP tunnels (reserved public ports, including advanced UDP options), SNI-based TLS passthrough on :443
- Automatic scoped hostnames (`myapp--tenant.yourdomain.com`) and custom domains with ACME TLS
- Path-based routing: send different URL prefixes of one hostname to different tunnels
- Ephemeral tunnels with an automatic time-to-live (`zorven ephemeral 8080`)
- Mutual TLS (client-certificate) per tunnel, per-tunnel IP allow-lists, visitor access control (Basic auth / Google & GitHub OAuth)
- Branded visitor sign-in pages for protected tunnels (password form and "Sign in with Google/GitHub"), with the Zorven session cookies never forwarded to your service
- Web door for raw TCP/UDP tunnels: visitors sign in on a web page and their IP is temporarily allowed to reach the port; grants can be revoked at any time

**Edge gateway**

- Policy engine: `deny`, `redirect`, `set_header`, `rate_limit`, `require_mtls` rules with match conditions (path, method, geo/IP-list conditions)
- Built-in WAF (OWASP-lite ruleset, bring-your-own patterns)
- Webhook signature verification (GitHub, Stripe, GitLab, Shopify, Slack) so forged callbacks never reach your service
- Secret Vault: AES-256-GCM encrypted secrets referenced from policies and webhook configs
- Load balancing across replicas (round-robin, weighted, least-connections, latency) with active health checks and automatic failover

**Private network**

- Zorven Network: publish private resources (`db.internal:5432`, whole subnets) without exposing them to the internet
- `zorven connect` (local SOCKS5 gateway, optional site-to-site gateway mode) and `zorven forward` (port-forward to a private or reserved-port tunnel)

**Observability**

- Live request log, request inspector with replay and replay diff, per-tunnel metrics
- Rejected-request log: requests the ingress turned away (IP filter, policy, WAF, rate limit, offline client ...) are recorded with a reason, with a badge and filter in the panel
- Access statistics per tunnel: sign-in successes/failures by method and provider, unique identities and IPs, web-door grants
- Alerts on error-rate thresholds with e-mail notification, abuse reporting for visitors

**Platform**

- Web dashboard (Vue/Nuxt, English + Turkish) with tunnels, clients, devices, domains, policies, secrets, tokens, projects, and team management
- Multi-tenant data model: organizations, projects, members, e-mail invitations, API tokens (scoped, rotatable)
- REST API with OpenAPI spec; SDKs for Go, Node.js, and Python
- CLI client, background service install (`zorven service ...`), self-updating binaries, and a cross-platform desktop app (Windows/macOS/Linux)
- Remote terminal and screen sharing (off by default; opt-in per client for security)
- Self-hosted webmail: inbound receiver + outbound sender with DKIM signing, folders, and external sending for every tenant
- Mail apps: use your Zorven mailbox from Gmail, Outlook, Apple Mail, or Thunderbird over IMAP (993) and authenticated SMTP submission (587/465) with revocable app passwords and autoconfig / Autodiscover / `.mobileconfig`
- Client version reporting: clients report their real version and the panel flags outdated ones
- Configurable log retention (`ZORVEN_LOG_RETENTION_DAYS`, default 30)
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
full Compose stack (server, Postgres, dashboard auth service, mail relay).

## Build from source

Zorven is a Go workspace (`go.work`) with four modules plus a Nuxt dashboard, and SDKs under `sdk/`.

```bash
# Server + client + shared
cd server && go build ./...
cd ../client && go build -o zorven .

# Dashboard (embedded into the server binary)
cd web && npm install && npm run build
bash scripts/build-dashboard.sh    # copies build output into server/webdist
```

Go 1.25+ and Node 20+ are required.

## Project structure

| Path | What |
|------|------|
| `server/` | Tunnel server, ingress proxy, policy/WAF engine, HTTP API, webmail, Postgres store |
| `client/` | CLI tunnel agent (`zorven`) |
| `desktop/` | Cross-platform desktop app (Wails) |
| `shared/` | Wire protocol shared by client and server |
| `sdk/` | Node.js and Python SDKs (the Go SDK lives in `client/sdk`) |
| `web/` | Nuxt dashboard (embedded into the server via `go:embed`) |
| `deploy/` | Docker Compose self-hosting stack |

## Open core

Zorven is **open core**. This repository contains the complete tunneling
product under the **AGPL-3.0** license: server, clients, SDKs, dashboard, webmail,
remote access — fully functional and unlimited for self-hosting, with no feature
gates or quotas. See [docs/OPEN-CORE.md](docs/OPEN-CORE.md) for the exact split.

## Enterprise

Running Zorven as a paid, multi-tenant service needs a few extras that are
distributed separately under a commercial license and are **not** part of this
repository:

- Billing, plan and subscription enforcement (including trials and add-on requests)
- Multi-node high availability (clustering)
- OIDC single sign-on (SSO)
- Audit log
- Log export to external systems (SIEM sinks)
- Zero-trust access conditions (2FA and source-network conditions)
- Service accounts (machine identities)
- Plan-based log retention
- Per-plan short-name quota and suspension of names after a plan downgrade
- Device tags (metadata labels on devices)
- Platform plan management (changing a tenant's plan; plan labels in the platform console)
- Hosted marketing site and billing screens

The core defines the integration seams as interfaces; without the commercial
layer everything runs unlimited on a single node.

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
