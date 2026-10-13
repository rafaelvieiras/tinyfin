# Security Policy

Tinyfin is a network-facing media server: it handles user credentials, access
tokens and file paths. We take security reports seriously.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately through GitHub's [private vulnerability
reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
(the **Security** tab → **Report a vulnerability**), or by email to
**hello@rafaelvieiras.com**.

Please include:

- A description of the issue and its impact.
- Steps to reproduce (a proof of concept if possible).
- The affected version or commit and the deployment shape (container, binary).

We aim to acknowledge reports within a few days and will keep you updated as we
work on a fix. Once a fix is released, we are happy to credit you unless you
prefer to stay anonymous.

## Scope notes

- Behaviors where Tinyfin deliberately differs from the reference server for
  security reasons are recorded as `divergence` entries in the behavior contract
  (`docs/contract/`). A divergence is intended, not a bug.
- Never include real credentials, tokens or media paths in a report; redact them.
