# Contributing to BaaS

Thanks for your interest in contributing! BaaS (Browser as a Service) drives a real Chrome instance behind an HTTP API for browser automation and document conversion. This guide explains how to report bugs, suggest features, and submit pull requests.

> **Heads up:** the project is under active development and breaking changes can happen. For larger changes, please open an issue to discuss first.

## Ways to contribute

- **Report bugs** by opening an issue
- **Suggest features** or improvements
- **Improve documentation** (README, API docs, examples)
- **Fix bugs or add features** via pull requests

## Related repositories

- [agent-factory-ui](https://github.com/Ursa-Minor-Beta/agent-factory-ui): the web UI client. UI issues and PRs belong there.
- [agent-factory-docker-api-ui](https://github.com/Ursa-Minor-Beta/agent-factory-docker-api-ui): one-shot Docker deployment of API + UI.

## Reporting bugs

Search existing issues first to avoid duplicates. A good bug report includes:

- What you expected and what actually happened
- Steps to reproduce (endpoint, request body, browser program if applicable)
- Your environment: OS, Docker version, Go version, browser version
- Error messages from logs or API responses
- For browser automation issues: the page URL and a minimal program that reproduces it

**Security vulnerabilities:** do not open a public issue. Follow [SECURITY.md](SECURITY.md) and report privately.

## Suggesting features

Open an issue describing the problem you want to solve and your proposed approach. Mention which part of the system it touches (browser automation, document conversion, API, async sessions, etc.).

## Development setup

**Prerequisites:** Go 1.26 or newer, Docker (for MongoDB), and optionally Chrome/Chromium for local browser testing.

1. **Fork** the repository on GitHub.
2. **Clone** your fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/baas.git
   cd baas
   ```
3. **Add the upstream remote:**
   ```bash
   git remote add upstream https://github.com/Ursa-Minor-Beta/baas.git
   ```
4. **Start the database:**
   ```bash
   docker compose up -d mongodb
   ```
5. **Configure environment:**
   ```bash
   cp .env.example .env
   # Edit .env: set API_KEY and BROWSER_EXECUTABLE (path to your Chrome)
   ```
6. **Run from source:**
   ```bash
   set -a && . ./.env && set +a
   go run ./cmd/baas
   ```

See the README for full details on running with Docker or building the base image.

## Making changes

1. Sync with upstream and create a branch from `main`:
   ```bash
   git fetch upstream
   git checkout -b my-change upstream/main
   ```
2. Make your changes. Keep each pull request focused on one logical change.
3. Before pushing, make sure the project builds and tests pass:
   ```bash
   go build ./...
   go test ./...
   ```
4. If you changed an endpoint or DTO, regenerate Swagger docs:
   ```bash
   go run github.com/swaggo/swag/cmd/swag init --parseDependency -g ./cmd/baas/main.go -o internal/docs
   ```
5. Update the README if you change behavior or add new features.

## Code guidelines

- The project is written in **Go**. Follow standard Go conventions and idioms.
- Format with `gofumpt`. Lint configuration is in `.golangci.yml`.
- Fix all lint errors; don't disable rules without a good reason.
- **Security matters.** Be careful with user input, avoid injection vulnerabilities, and never log sensitive data like API keys.
- Browser programs run in a sandboxed JavaScript interpreter — keep the attack surface minimal when adding new actions.
- Don't add dependencies unless necessary, and mention why in the PR.
- Tests that need Chrome, Docker, or Pandoc skip themselves when those are unavailable. Add similar skip logic if your test has external dependencies.

## Commit messages

- Use the imperative mood: "Add PDF extraction endpoint", not "Added..."
- Keep the first line under 72 characters
- Reference related issues, e.g. `Fixes #123`

## Submitting a pull request

1. Push your branch to your fork:
   ```bash
   git push origin my-change
   ```
2. Open a pull request against the `main` branch of `Ursa-Minor-Beta/baas`.
3. Describe what changed and why, link the related issue, and note how you tested it.
4. Make sure CI checks pass.
5. Respond to review feedback by pushing follow-up commits to the same branch; the PR updates automatically.

**Tips for faster reviews**

- Small, focused PRs get reviewed sooner.
- Explain the "why", not just the "what".
- Include example requests/responses for API changes.
- If adding a new browser action, include a test program demonstrating it.
- Breaking changes to the API should be called out clearly in the PR description.

## Review process

A maintainer will review your PR and may request changes or explain why a change doesn't fit the project's direction. Please don't take that personally; it's about keeping the project coherent. Once approved and passing CI, a maintainer will merge it.

## License

BaaS is licensed under the [Apache License 2.0](LICENSE). By submitting a contribution, you agree that it is licensed under the same terms, as described in Section 5 of the license.

## Questions?

Open an issue or start a discussion in the repository. Thanks for helping improve BaaS!
