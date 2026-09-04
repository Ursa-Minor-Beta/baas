# Repository guidelines

## Layout

- `cmd/baas/` — HTTP service entrypoint.
- `cmd/baas-cli/` — CLI client for a running instance.
- `internal/service/` — browser automation, HTTP handlers, format detection and
  document conversion. The bulk of the code.
- `internal/sessions/` — async session registry and lifecycle.
- `internal/llm/`, `internal/messagebus/`, `internal/proxy/`, `internal/socks5/`
  — LLM clients, the MongoDB-backed message bus, and proxy support.
- `pkg/dto/` — request and response types, shared with clients.
- `pkg/baasclient/` — Go client.
- `scripts/` — Node and Python helpers behind the Markdown, PDF and HTML
  endpoints.
- `profile/Extensions/` — where unpacked Chrome extensions go. Empty by design.

## Build and test

```bash
go build ./...
go test ./...
go run ./cmd/baas          # needs the environment from .env.example
```

Tests that need Chrome, a Docker daemon or Pandoc skip themselves and say which
one is missing, so a bare checkout is green. To run them for real, use the
container image or install the dependency locally.

Regenerate the Swagger output after changing any endpoint or DTO:

```bash
go run github.com/swaggo/swag/cmd/swag init --parseDependency -g ./cmd/baas/main.go -o internal/docs
```

## Style

- Go is formatted with `gofumpt`; imports are grouped per the `gci` section
  order in `.golangci.yml`.
- JavaScript and Python in `scripts/` use 2-space indentation and a 120-column
  width (`.editorconfig`, `.prettierrc`).
- Follow the existing naming: handlers live in `endpoints_*.go`, packages are
  lowercase single words.

## Tests

Tests belong next to the code as `*_test.go` and use `gomega` matchers. A test
that needs an external dependency must guard it with a skip that names the
dependency — see `requireChrome`, `requireDocker` and `requireBinary` in
`internal/service/browser_test.go`.

Do not commit fixtures captured from real sites you do not control, and do not
commit credentials, hostnames or account identifiers in test programs. Write
synthetic fixtures instead.

## Security

- Never commit secrets. `.env` is gitignored; `.env.example` documents the
  variables with empty values.
- The API is guarded by a single static bearer token and the browser will fetch
  any URL it is given. Assume it runs somewhere private.
- `.semgrep.yml` carries the repo-specific static analysis rules; run
  `semgrep scan --config .semgrep.yml` before sending a change that touches
  navigation or command execution.
