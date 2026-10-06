# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
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

### Changed
- Entitlements remain a no-op (everything allowed, unlimited), extended with the new feature checks.
- Panel: billing, plan-upgrade, and throttle screens removed; dashboard shows unlimited usage.
- `deploy/docker-compose.yml` and `.env.example` simplified for self-hosting (no cluster or analytics services, placeholders only).
- Test fixtures no longer contain real hosts or addresses.

### Removed
- Clustering flags and services, service accounts, SSO, audit log, log sinks, zero-trust conditions, add-on requests, Pro trial.
