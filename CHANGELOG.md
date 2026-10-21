# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Zorven Net: WireGuard-based port sharing between devices (shares, nodes, `zorven up/down/autostart`, local control socket, panel Shares page).
- MCP server (remote endpoint and `zorven mcp` over stdio) and an OAuth 2.1 authorization server for custom connectors; connected-apps tab and connect card in the panel.
- TCP and UDP tunnel metrics (connections, rejections, bytes, durations, RTT/loss), monthly usage metering, tunnel connection-quality view.
- Authoritative DNS verification for custom domains, two-step tunnel picker, custom 404 pages, `/updates` path (old `/guncellemeler` redirects), Go SDK.
- Remote screen "screen2" mode: binary tile protocol (only changed 64x64 tiles, JPEG/PNG/solid per region, adaptive quality/fps), canvas compositing in the panel; legacy MJPEG/H.264 kept for old clients.
- Per-viewer screen settings: shared capture with a server-side tile store; each viewer gets its own fps and one of two quality layers without affecting others.
- Multi-viewer screen sessions, server-enforced view-only, remote cursor overlay, text clipboard sync (Windows), Ctrl+Alt+Del and shortcut menu, input status badge (applied / blocked by an elevated window / unsupported).
- File transfer inside a screen session (drag and drop upload, download from an allowed folder; size limits, safe names, no overwrite, sha256 check).
- Remote input on Linux (X11 XTEST) and macOS capture/input without cgo (purego; macOS 15+ capture needs ScreenCaptureKit, not included yet).
- Updates / changelog pages with RSS, admin editor, JSON import/export, and opt-in announcement emails (consent at signup, one-time banner, safe unsubscribe page, RFC 8058 one-click, resumable sending).
- MTA-STS mode and max_age configurable via env; the policy id is logged at startup.
- Reserved port pool across several public IPs (TCP_BIND_IP1/2, ZORVEN_TCP_BIND_IPS, ZORVEN_TCP_HOSTS in the compose file).
- Panel language selector is a dropdown.

### Changed
- Remote screen control (input, clipboard, files, shortcuts) is limited to tenant owners and admins; members can only watch. The decision is made when the screen ticket is issued.

### Fixed
- Cancelling a browser/visitor request no longer drops the whole agent WebSocket connection (server and client, with regression tests).
- CSP blocked blob: images and media, which left MJPEG/H.264 screen sharing black.
- Markdown links with nested parentheses; a rejected link no longer leaks characters into the output.

### Added
- Request body capture (opt-in): stored in Postgres (`request_captures`), gzip + AES-GCM with `ZORVEN_SECRET_KEY`, body cap and cache size via env, optional tablespace (`ZORVEN_CAPTURE_TABLESPACE`), pruned by retention (`ZORVEN_CAPTURE_RETENTION_DAYS` upper bound).
- Reserved TCP/UDP port pool (`ZORVEN_TCP_PORT_MIN/MAX`, `ZORVEN_TCP_BIND_IPS`): allocations are (IP, port) pairs, one pool per IP, quarantine before a freed port is reused; port mode and SNI mode both remain.
- Visitor sessions for protected tunnels: per-tunnel TTL (15 min - 30 days), optional sliding renewal with a 7-day absolute cap, "end all sessions" / per-identity revocation, events in `tunnel_access_events`.
- Custom domains: apex domains get A-record instructions (CNAME is not allowed at the apex), subdomains get CNAME or A as alternatives, wildcard hint when a root domain is added; DNS check asks the authoritative name servers first (no stale resolver cache) and reports pointed / apex / wildcard.
- `GET /api/v1/my-ip` returns the caller's public IP.
- Second, offline recovery key for signed client updates, with a key loss / leak runbook.

### Changed
- Content-Security-Policy is enforced by default (`ZORVEN_CSP_MODE=report` to roll back); `unsafe-eval` only on the API reference page.
- Unauthenticated `/api/v1/status` no longer reveals the server version or the connected client count.

### Fixed
- TCP tunnels: the client registers a stream before acknowledging it, so the first data packet of a new connection is no longer dropped under concurrency.
- `scripts/build-clients.sh` no longer deletes `dist/desktop.json` before signing it.

### Added (earlier)
- Panel language defaults to the browser language on first visit (Turkish -> Turkish, anything else -> English); a saved choice always wins.
- Mail client auto-setup hosts (`autoconfig.mail.<platform>`, `autodiscover.mail.<platform>`) get their own ACME certificates, since the wildcard does not cover two-level names.
- Web door: IPv6 grants match the whole /64 prefix (privacy addresses rotate); IPv4 stays exact.
- Remote terminal: persistent sessions (the shell lives on the client for 30 minutes after a disconnect, 256 KB replay buffer, owner-bound), tabs that survive page changes and refreshes, search, links, WebGL rendering, copy/paste, automatic reconnect, and a shell picker (client-side shell discovery; Zorven zsh/bash profile with color and UTF-8 environment).
- Signed client updates: release manifests are verified with an embedded ed25519 public key, downgrades and same-version updates are refused; `keygen`/`sign` tools and a signing step in `scripts/build-clients.sh`.
- MTA-STS policy served at `mta-sts.<platform>/.well-known/mta-sts.txt` (`ZORVEN_MTA_STS_MODE`, default `testing`).
- Signed-session revocation: GitHub session cookies are re-evaluated against the database on every request (real member role, logout revokes older cookies).
- Branded visitor sign-in pages (Basic password form, OAuth) for protected tunnels; Zorven session cookies are stripped before requests reach the backend.
- Access statistics (`tunnel_access_events`, access-events API, per-tunnel stats tab in the panel).
- Web door for raw TCP/UDP tunnels (sign in on a web page to allow your IP for a limited time), with grant revocation and live disconnect.
- Mail apps: IMAP and authenticated SMTP submission, revocable app passwords, Thunderbird autoconfig / Outlook Autodiscover / Apple `.mobileconfig`, local delivery, raw message bytes, and mail folders (panel setup guide included).
- Rejected-request logging with a reason (`request_logs.reject_reason`), badge and filter in the panel.
- Clients report their real version; the panel shows an "update available" badge.
- Tenant-scoped names are the default hostname kind (`kind: scoped|short`); brand and reserved-name protection for short names and slugs.
- Policy engine (deny, redirect, set header, rate limit, require mTLS), WAF, and webhook signature verification.
- Secret Vault (AES-256-GCM) referenced from policies and webhook configs.
- Load balancing across replicas with health checks and failover.
- Raw TCP, UDP (advanced options), and SNI passthrough tunnels; per-tunnel mutual TLS; path-based routing.
- Private network: `zorven connect` / `zorven forward`, subnet routing.
- Request inspector with replay and replay diff, metrics, alerts, abuse reports.
- Projects, organizations, team invitations, scoped API tokens, REST conventions and OpenAPI.
- Ephemeral tunnels, Go / Node.js / Python SDKs, device settings and remote agent configuration.
- Configurable log retention via `ZORVEN_LOG_RETENTION_DAYS` (default 30).
- `docs/OPEN-CORE.md` describing the open/commercial split.

### Fixed
- Desktop: the embedded agent CLI no longer overwrites the desktop executable; desktop auto-update is disabled and the manifest only carries `windows/amd64` under the `+cli` key.
- Client: `zorven update` run by hand now loads the server settings and reports its result and errors.
- Hostnames: custom-domain duplicates of a verified or platform name return 409; FQDN lookup picks the authoritative row.
- Panel: a failed `nuxt generate` can no longer ship a stale build; `index.html` and `_nuxt` assets always come from the same build.

### Security
- CSRF: stricter Origin checks in production (loopback and plain-http origins rejected on a production platform domain); loopback trusted origins only outside production in the panel auth.
- Better Auth session cookies are signature-verified with `BETTER_AUTH_SECRET`; raw session tokens are no longer accepted as Bearer. Set the same secret for the server and the web service.
- A generated admin key is written to a 0600 file (`ZORVEN_ADMIN_KEY_FILE`, default `zorven-admin-key.txt`) instead of stdout/logs.
- TOTP codes cannot be replayed within their validity window; `get-session` no longer exposes the session token; `@nuxtjs/i18n` removed (vue-i18n 11 used directly), nodemailer 10.
- Content-Security-Policy no longer allows Google Fonts; the API reference page pins Scalar to a fixed version with SRI.
- Inspector: sensitive captured headers (Authorization, Cookie, Set-Cookie, X-Api-Key, ...) are masked for everyone except owners/admins; replay is restricted to owners/admins.
- Remote terminal and screen tickets are single-purpose (cross use refused), WebSocket exemption is limited to the real endpoints, and tickets are issued to owners/admins only.
- Members are read/create-only: security settings, deletions and token rotation require owner/admin; unknown roles are rejected.
- Install scripts validate token and server parameters; embedded values are single-quoted.
- Visitor and Basic-auth keys are derived with HKDF using purpose labels; production refuses to start without required secrets; open-redirect validator shared; visitor token purpose tag.
- Policy/WAF: rules that fail to compile fail closed (503), unknown secret references are rejected (422), WAF scans whole fields up to a 64 KB cap.
- Request-path validation and SSRF block list extended; `%00` in path parameters returns 404; failed device-approval attempts are rate limited; CSP (report-only) and Permissions-Policy headers on panel responses; Go toolchain pinned to 1.26.6 with a `govulncheck` CI step.

### Changed
- Entitlements remain a no-op (everything allowed, unlimited), extended with the new feature checks.
- Panel: billing, plan-upgrade, and throttle screens removed; dashboard shows unlimited usage.
- `deploy/docker-compose.yml` and `.env.example` simplified for self-hosting (no cluster or analytics services, placeholders only).
- Test fixtures no longer contain real hosts or addresses.
- Webmail: sending to external addresses is no longer limited to a paid plan.

### Removed
- Clustering flags and services, service accounts, SSO, audit log, log sinks, zero-trust conditions, add-on requests, Pro trial, device tags, platform plan-change endpoint and plan labels.
