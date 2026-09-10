# Security Policy

## Reporting a vulnerability

Please **do not** open a public issue for security vulnerabilities.

Instead, report them privately via GitHub's
[Security Advisories](https://github.com/tkodcumpeg4/zorven/security/advisories/new)
or by email to the maintainer. We aim to acknowledge reports within a few days
and will coordinate a fix and disclosure timeline with you.

## Scope

Zorven tunnels arbitrary HTTP traffic and can expose remote terminal and screen
sharing. Remote terminal and screen sharing are disabled by default and must be
explicitly enabled per client. Always run the server behind TLS.
