# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
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
