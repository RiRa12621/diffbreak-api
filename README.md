# DiffBreak (Go backend)

DiffBreak is a small Go HTTP service that inspects GitHub releases/commits between two tags and asks a local Ollama model to return a structured risk analysis. It exposes:

- `GET /detect` to list tags for a repository
- `POST /api/analyze` to generate upgrade risk analysis
- `GET /metrics` for Prometheus metrics

## Requirements

- Go 1.22+ (the module declares `go 1.25`)
- Ollama running locally at `http://localhost:11434`
- GitHub token (optional but recommended to reduce rate limiting)

The service calls the Ollama model `qwen2.5:7b` and expects it to return strict JSON matching the frontend contract.

## Running locally

Build and run:

```bash
go build -o diffbreak ./
./diffbreak -llm http://localhost:11434 -port 8080 -interface 0.0.0.0 -github <GITHUB_TOKEN>
```

Or run directly:

```bash
go run . -llm http://localhost:11434 -port 8080 -interface 0.0.0.0 -github <GITHUB_TOKEN>
```

### Flags

- `-llm` (string): Ollama base URL. Default `http://localhost:11434`.
- `-port` (string): Port to listen on. Default `8080`.
- `-interface` (string): Interface to bind. Default `0.0.0.0`.
- `-github` (string): GitHub access token (optional, reduces rate limiting).

## API

### `GET /detect`

Query params:

- `repo` (required): `https://github.com/owner/repo`

Response:

```json
{
  "repo": { "url": "...", "owner": "", "name": "", "provider": "github" },
  "tags": ["v1.0.0", "v1.1.0"],
  "defaultFrom": "",
  "defaultTo": ""
}
```

### `POST /api/analyze`

Request body:

```json
{
  "repoUrl": "https://github.com/owner/repo",
  "fromTag": "v1.13.0",
  "toTag": "v1.17.0",
  "mode": "fast",
  "limits": { "maxReleases": 30 }
}
```

Validation rules:

- `repoUrl`, `fromTag`, `toTag` are required
- `mode` must be `fast` or `deep`
- `maxReleases` is clamped to `1..60`
- `repoUrl` must be `https://github.com/owner/repo`

Response (200):

```json
{
  "risk": { "level": "low|medium|high", "score": 0, "confidence": "low|medium|high", "reasons": [] },
  "summary": { "highlights": [], "grouped": [ { "title": "...", "items": ["..."] } ] },
  "breakers": [ { "title": "...", "severity": "low|medium|high", "reason": "...", "evidence": [ { "label": "...", "url": "..." } ] } ],
  "behaviorChanges": [ { "title": "...", "reason": "...", "evidence": [ { "label": "...", "url": "..." } ] } ],
  "upgradeSteps": [ { "step": "...", "why": "...", "evidence": [ { "label": "...", "url": "..." } ] } ],
  "evidence": [ { "label": "...", "url": "...", "kind": "release|pr|compare|commit" } ],
  "meta": { "repo": { "url": "..." }, "fromTag": "...", "toTag": "...", "generatedAt": "RFC3339" }
}
```

Error response (non-2xx):

```json
{ "error": "..." }
```

## Metrics

Prometheus metrics are exposed at `GET /metrics`:

- `http_requests_total{handler,method,status}`
- `http_request_duration_seconds{handler,method,status}`
- `github_requests_total{operation,status}`
- `github_request_duration_seconds{operation,status}`
- `ollama_requests_total{status}`
- `ollama_request_duration_seconds{status}`

## Docker

A multi-stage Dockerfile is provided, with Alpine as the runtime image.

Build:

```bash
docker build -t diffbreak .
```

Multi-arch build with buildx:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t diffbreak .
```

Run:

```bash
docker run --rm -p 8080:8080 diffbreak \
  -llm http://host.docker.internal:11434 \
  -port 8080 \
  -interface 0.0.0.0 \
  -github <GITHUB_TOKEN>
```

On Linux, `host.docker.internal` may require:

```bash
docker run --add-host=host.docker.internal:host-gateway --rm -p 8080:8080 diffbreak ...
```

## Notes

- `mode=fast` uses release notes and commit titles.
- `mode=deep` also includes changed file paths and commit shas in titles.
- The server enforces a 60s request timeout for `/api/analyze` and returns `504` with JSON error on timeout.
