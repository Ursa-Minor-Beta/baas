# Security Policy

> **Please do not report security vulnerabilities through public issues, discussions, or pull requests.**

Thank you for helping keep BaaS and its users safe.

## Supported versions

BaaS is under active development and does not yet publish versioned releases. Breaking changes may occur.

| Version                      | Supported |
| ---------------------------- | --------- |
| Latest commit on `main`      | Yes       |
| Older commits, forks, tags   | No        |

Security fixes are applied to `main` only. If you run BaaS, please keep your deployment up to date. Once tagged releases exist, this table will be updated.

## Reporting a vulnerability

Report them privately using either option:

- **GitHub private vulnerability reporting (preferred):** open the repository's [Security tab](https://github.com/Ursa-Minor-Beta/baas/security) and click **Report a vulnerability**.

Please include as much of the following as you can:

- The type of issue (e.g., command injection, authentication bypass, SSRF, sensitive data exposure)
- The affected component, endpoint, or source file
- The commit hash you tested against
- Step-by-step instructions to reproduce, ideally with a minimal proof of concept
- The impact and how an attacker could exploit it
- Your suggested fix, if you have one

## What to expect

- **Acknowledgment:** within 3 business days.
- **Assessment:** we will confirm the issue, determine severity, and may ask for more details.
- **Fix:** we will work on a fix and keep you updated on progress. Timelines depend on severity and complexity.
- **Disclosure:** once a fix is available, we will publish a GitHub security advisory. We follow coordinated disclosure and ask for up to 90 days before public disclosure.
- **Credit:** with your permission, we will credit you in the advisory.

## Scope

In scope (this repository, `Ursa-Minor-Beta/baas`):

- Authentication and authorization: API key validation, bearer token handling
- Server-side request forgery (SSRF) via browser automation or URL handling
- Command injection through browser programs or document processing
- Path traversal in file handling or document conversion
- Arbitrary code execution in the JavaScript interpreter or browser context
- Sensitive data exposure in API responses, logs, or error messages
- MongoDB injection or improper query construction
- Insecure handling of uploaded documents or generated files
- Proxy configuration bypass or credential leakage
- Container escape or privilege escalation

Out of scope:

- Vulnerabilities in third-party dependencies with no impact on BaaS (report those upstream, but let us know if we're affected)
- Vulnerabilities in separate Ursa-Minor-Beta repositories (please report them in those repositories)
- Weaknesses that require a deployment that ignores security best practices
- Issues that require physical access to the server
- Automated scanner output without a demonstrated impact
- Denial of service through resource exhaustion (long-running browser programs, large documents)

## Deployment hardening

If you deploy BaaS:

- Treat it as an internal service; do not expose it on a public address without a reverse proxy.
- Use a long, random `API_KEY` (at least 32 characters).
- Run the container with minimal privileges and consider network policies to restrict outbound traffic.
- MongoDB should not be publicly accessible; use authentication and TLS in production.
- If using `STORAGE_SERVICE_URL`, ensure that endpoint is also secured.
- Review browser programs before execution if they come from untrusted sources.
- Set `REQUIRE_DOCUMENT_TOOLS=true` only if your deployment genuinely needs document conversion.
- Monitor logs for unusual activity patterns.

## Good-faith research

We will not pursue legal action against researchers who act in good faith: test only against your own instances, avoid accessing other people's data, avoid degrading service for others, and report findings to us privately and promptly.
