# BaaS — Browser as a Service

BaaS drives a real Chrome instance behind an HTTP API. You POST a small
JavaScript-like program describing what to do in the browser; BaaS runs it and
returns the page, a screenshot, cookies, a downloaded file, or Markdown
extracted from whatever the page served you.

It also parses documents without a browser: PDF, DOCX, XLSX, PPTX, CSV and
plain text all convert to Markdown through the same API.

- **Browser automation** — navigate, click, type, wait, upload, download,
  screenshot, run arbitrary JS, all through ChromeDP.
- **LLM-assisted actions** — `llmClick('the login button')`,
  `llmLogin(user, pass)`, `llmText('one-time code')`, for pages whose selectors
  you do not know in advance. Backed by OpenAI, Azure OpenAI or Ollama.
- **Sync and async sessions** — one-shot programs, or a long-lived session you
  send commands to over time.
- **Readability extraction** — article text, tables and metadata from a URL.
- **Document conversion** — Office formats and PDF to Markdown, PDF to images.
- **Proxy support** — route browser traffic through an HTTP or SOCKS5 upstream.

## Requirements

MongoDB is required and must be a **replica set**: the async session message bus
is built on change streams and will not start against a standalone node. A
single-node replica set is fine, and the Compose file sets one up for you.

Everything else — Chrome, ChromeDriver, Pandoc, Node.js, the Python document
tooling — lives in the container image. Running the binary directly on a host
means installing those yourself.

## Run it in a container

Build the base image once. It carries Chrome, a Pandoc fork, Node.js and the
document-processing dependencies, and takes a while because Pandoc is compiled
from source:

```bash
docker build -f base.Dockerfile -t baas-base:local .
```

Then configure and start the stack:

```bash
cp .env.example .env
$EDITOR .env          # at minimum set API_KEY
docker compose up --build
```

That brings up MongoDB as a single-node replica set and BaaS on
`http://localhost:8090`. Check it:

```bash
curl -H "Authorization: Bearer $API_KEY" http://localhost:8090/api/status
```

To build only the service image, against your own copy of the base:

```bash
docker build -t baas:local --build-arg BASE_IMAGE=baas-base:local .
```

Set `ENABLE_VNC=true` plus either `VNC_PASSWORD` or `SE_VNC_NO_PASSWORD=1` and
you can watch the browser work on port 5900.

## Run it from source

```bash
cp .env.example .env
$EDITOR .env          # set API_KEY, MONGO_URI and BROWSER_EXECUTABLE
set -a && . ./.env && set +a
go run ./cmd/baas
```

`go build ./...` and `go test ./...` both work on a bare checkout. Tests that
need Chrome, Docker or Pandoc skip themselves with a message naming what is
missing.

There is also a CLI client in `cmd/baas-cli`, which talks to a running instance
at `BAAS_URL` (default `http://localhost:8090`) using `BAAS_API_KEY`.

## First request

Every call carries `Authorization: Bearer <API_KEY>`.

```bash
curl -X POST http://localhost:8090/api/process \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d '{
    "program": "var status = navigateStatus(\"https://example.com\"); if (status != 200) { throw \"got \" + status; } waitReady(\"body\"); outerHtml(\"body\");",
    "timeout": "30s"
  }'
```

The program is evaluated by an embedded JavaScript interpreter with browser
actions bound as functions. `GET /api/async/actions` returns the full list with
signatures. `program-generator-sp.txt` is the system prompt used to have a model
write these programs, and doubles as a description of the dialect.

## API

| Method | Path | What it does |
|---|---|---|
| `GET` | `/api/status` | Health, version, X server and browser readiness |
| `GET` | `/api/docs` | Swagger UI |
| `POST` | `/api/process` | Run a browser program, synchronously |
| `POST` | `/api/readability` | Extract article content from a URL |
| `POST` | `/api/eke-extract` | Key-value extraction from a page |
| `POST` | `/api/parse` | Parse a document into structured content |
| `POST` | `/api/parse-to-markdown-kv` | Convert a document to Markdown |
| `POST` | `/api/render-markdown` | Render Markdown to HTML, PDF or DOCX |
| `POST` | `/api/extract-markdown` | Extract Markdown from a page |
| `POST` | `/api/pdf-to-images` | Rasterise a PDF |
| `POST` | `/api/async/start` | Open a long-lived browser session |
| `POST` | `/api/async/message` | Send a command to a session |
| `POST` | `/api/async/stop` | Close a session |
| `GET` | `/api/async/actions` | List available browser actions |
| `GET` | `/api/async/sessions` | List active sessions |
| `GET` | `/api/async/sessions/active` | Check whether a session is alive |
| `POST` | `/api/async/sessions/cleanup` | Drop expired sessions |

`examples/parse-to-markdown-kv.md` walks through the document endpoint in
detail.

## Configuration

`.env.example` documents every variable with its default. The ones worth
knowing up front:

| Variable | Description |
|---|---|
| `API_KEY` | Bearer token for the API. Required. |
| `MONGO_URI` | MongoDB replica set connection string. Required. |
| `PORT` | Listen port. |
| `BROWSER_EXECUTABLE` | Path to Chrome/Chromium. Preset in the image. |
| `LLM_CLIENT` | `openai`, `azure-openai` or `ollama`. Only for `llm*` actions. |
| `PROXY_HOST` | Upstream proxy, `http://` or `socks5://`. |
| `CHROME_EXTENSIONS_DIR` | Directory of unpacked extensions to load. |
| `STORAGE_SERVICE_URL` | Optional external store for generated files. |
| `CACHE_TTL` | Lifetime of cached readability results. |

`LOCAL_DEBUG`, `USE_RESPONSE_STREAM` and the two
`SIMPLE_CONTAINER_AWS_LAMBDA_*` variables come from the underlying service
framework, which can also run the same binary as a Lambda handler. Outside
Lambda they must be set as shown in `.env.example`; Compose already does.

### Storage service

Endpoints that hand back a generated PDF, DOCX, HTML file or image upload it
somewhere and return a link, rather than inlining it. `STORAGE_SERVICE_URL`
points at that somewhere. It has to answer:

```
POST {STORAGE_SERVICE_URL}/api/prepare?directory=&ttl_minutes=&file=
Authorization: Bearer {STORAGE_SERVICE_API_KEY}

-> 200 {"uploadURL": "...", "sessionID": "...", "link": "..."}
```

BaaS then PUTs the bytes to `uploadURL` and returns `link` to the caller. Leave
both variables unset and those endpoints report that storage is unconfigured;
every other endpoint works.

### Chrome extensions

`CHROME_EXTENSIONS_DIR` loads unpacked extensions from a directory — see
`profile/Extensions/README.md`. None ship with this repository. Extensions apply
only on the undetected-browser path; the plain headless path runs with
`--disable-extensions`.

## Exposing it

BaaS authenticates with a single static bearer token and drives a real browser
that will fetch whatever URL it is handed. Treat it as an internal service:

- Do not put it on a public address without something in front of it.
- Use a long random `API_KEY`.
- Constrain outbound traffic if the programs it runs are not fully trusted — a
  browser program can reach anything the container can reach.
- `ALLOWED_ORIGINS` and `ALLOWED_IPS` restrict ChromeDriver, not the API.

For a temporary tunnel during development, `ngrok http 8090` works; the API is
then reachable at `https://<subdomain>.ngrok.io/api`.

## Development

```bash
go build ./...
go test ./...
go run github.com/swaggo/swag/cmd/swag init --parseDependency -g ./cmd/baas/main.go -o internal/docs
```

Regenerate the Swagger output whenever you change an endpoint or a DTO.
Formatting is `gofumpt`; `.golangci.yml` holds the lint configuration.

## Licence

Apache 2.0 — see [LICENSE](LICENSE).
