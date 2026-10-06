# Open core: what is open and what is commercial

Zorven is **open core**. The tunneling product is complete and unlimited in this
repository (AGPL-3.0). Only the layer needed to *sell* Zorven as a multi-tenant
service, plus a few enterprise-management features, is commercial and lives in a
separate, private codebase.

## Feature matrix

| Area | Open (this repo) | Commercial (Enterprise) |
|---|---|---|
| Tunnels: HTTP, WebSocket, SSE, raw TCP, UDP, SNI passthrough | yes | |
| Custom domains with automatic ACME TLS, mutual TLS, IP allow-lists | yes | |
| Policy engine (deny, redirect, set header, rate limit, require mTLS), WAF, webhook signature verification | yes | |
| Secret Vault | yes | |
| Path-based routing, visitor access control (Basic / OAuth) | yes | |
| Load balancing, replicas, health checks | yes | |
| Private network (`zorven connect`, `zorven forward`, subnets) | yes | |
| Request inspector, replay, replay diff, metrics, alerts, abuse reports | yes | |
| Multi-tenant model: organizations, projects, members, invitations, API tokens | yes | |
| Webmail (inbound + outbound, DKIM/SPF/DMARC), remote terminal, screen sharing | yes | |
| CLI, desktop app, Go / Node.js / Python SDKs, REST API | yes | |
| Dashboard (English + Turkish) | yes | billing and plan screens |
| Self-hosting stack (Docker Compose, install scripts) | yes | |
| Billing, plan and subscription enforcement, trials, add-on requests | | yes |
| Multi-node high availability (clustering) | | yes |
| OIDC single sign-on (SSO) | | yes |
| Audit log | | yes |
| Log export to external systems (SIEM sinks) | | yes |
| Zero-trust access conditions (2FA, source-network conditions) | | yes |
| Service accounts | | yes |
| Plan-based log retention | | yes |

Email/password and GitHub/Google sign-in are part of the open core.

## How the core runs unlimited

The core never enforces plans or quotas. It talks to an `Entitlements` interface
(`server/entitlements`), and this repository ships a **no-op implementation**:

- every resource limit (clients, tunnels, domains, members, projects) is unlimited,
- every feature check (API access, IP allow-lists, custom domains, policy engine,
  Secret Vault, private network, advanced load balancing, advanced UDP, agent
  configuration) succeeds,
- screen-sharing slots are granted without a cap and monthly traffic is not metered
  against a limit.

Other commercial seams are likewise replaced by simple defaults in the open build:

- **Audit sink:** audit calls are a no-op.
- **Clustering:** the server runs as a single node.
- **Log retention:** a fixed, configurable period. Set `ZORVEN_LOG_RETENTION_DAYS`
  (default `30`; `0` disables pruning). Request logs older than that are deleted
  in the background.
- **Device access:** every member of an organization can reach the organization's
  devices; owners and admins manage tags and sensitive settings.

A commercial deployment replaces the no-op entitlements with a real implementation
and adds the extra services; the open code paths are identical.

## Database migrations

Migration numbers are shared between the open and commercial builds so both can
operate on the same database. Where a migration belongs to a commercial feature,
the open build ships an empty placeholder with the same number.

## Licensing

The open core is licensed under the GNU AGPL v3 (see [LICENSE](../LICENSE)).
If you run a modified version as a network service you must offer its source to
the users of that service. Commercial features are available under a separate
license; contact the maintainers.
