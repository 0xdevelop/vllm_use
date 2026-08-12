# vllm-use

[简体中文](README.zh-CN.md) · [Configuration](docs/configuration.md) · [HTTP API](docs/api.md) · [MCP](docs/MCP.md)

`vllm-use` is a small, native Go control plane for one local vLLM process. It registers local and Hugging Face models, supervises vLLM, exposes authenticated OpenAI/Anthropic-compatible gateway routes, serves a scoped MCP management endpoint, and embeds a Chinese-first Web Admin in the same binary. Phase 1 is aimed at a trusted single-host operator—not a cluster scheduler or hosted multi-tenant platform.

```text
 browser / API clients / MCP clients
              │
        ┌─────▼────────── vllm-use (one Go binary) ─────────┐
        │ Web Admin  │ admin API │ auth/scopes │ MCP tools  │
        │            └──────┬────┴──────┬──────┘            │
        │ SQLite + model registry   runtime supervisor      │
        └──────────────┬───────────────┬────────────────────┘
                 hf download       native vLLM process
                                         │
                              OpenAI/Anthropic upstream
```

## Implemented in Phase 1

- Linux process supervision for one native `vllm serve` process: start, stop, restart/switch, readiness polling, PID, exit state, and a bounded in-memory log tail.
- SQLite model registry; managed-root local model discovery; Hugging Face registration and bounded-concurrency downloads through `hf`.
- NVIDIA inventory via `nvidia-smi` (an empty list is returned when unavailable).
- Hashed, named API keys with enable/disable/delete and scopes: `inference`, `admin.read`, `admin.write`, `mcp.read`, `mcp.runtime`, `mcp.models`, `mcp.admin`.
- A narrow transparent reverse proxy for `/v1/models`, chat/completions, completions, Responses, embeddings, and Anthropic messages/count-tokens. It preserves streaming and replaces client credentials with an optional upstream credential.
- Stateless MCP Streamable HTTP at `POST /mcp`, protocol `2026-07-28`; see [MCP details](docs/MCP.md).
- Embedded responsive Web Admin: Dashboard, Models, Runtime, GPU, API Keys, MCP, Logs, and Settings. Production needs no Bun or Node.

Current limitations: Linux-only runtime supervision; NVIDIA-only GPU discovery; one runtime; no containers, distributed scheduling, users/RBAC, TLS, remote model catalog browsing, automatic vLLM installation, live application of persisted settings, resumable interrupted downloads, or recovery of in-memory runtime/download logs after restart. A Hugging Face registration does not download files until a download operation is started; a linked download does restore its persisted relationship and marks an interrupted model/job canceled.

## Requirements and build

- Go 1.25 or newer
- Linux, Python/vLLM, and the `vllm` executable for runtime control
- Hugging Face `hf` CLI for downloads
- NVIDIA driver utilities for GPU telemetry
- Bun 1.3.3 or newer only when rebuilding/testing the Web Admin

The checked-in `web/dist` is an intentionally minimal bootstrap embedded by Go;
it is not a generated copy of the current React UI:

```bash
go build -o vllm-use ./cmd/vllm-use
./vllm-use
```

To rebuild the UI first:

```bash
cd web
bun install
bun run lint
bun run typecheck
bun run test
bun run build
cd ..
go test ./...
go build -o vllm-use ./cmd/vllm-use
```

For frontend development, run the Go service on `127.0.0.1:8080`, then `cd web && bun run dev`. Vite proxies `/api`, `/mcp`, and `/v1` to the Go process.

## First run and configuration

Defaults are loopback-only: HTTP `127.0.0.1:8080`, vLLM upstream `http://127.0.0.1:8000`, and private data under the OS user configuration directory (`~/.config/vllm-use` on typical Linux systems). On first run without `VLLM_USE_ADMIN_TOKEN`, a mode-`0600` `admin-bootstrap.token` is created. Its secret is not logged; paste its contents into Web Admin. Replace/manage access with scoped API keys and protect this file.

```bash
./vllm-use \
  -listen 127.0.0.1:8080 \
  -data-dir "$PWD/.data" \
  -db "$PWD/.data/vllm-use.db" \
  -models-dir "$PWD/.data/models" \
  -vllm vllm -hf hf \
  -upstream http://127.0.0.1:8000
```

All data paths must be absolute. Local models and download destinations must resolve inside `models-dir`. See [configuration](docs/configuration.md) for every flag/environment variable and security implications.

## Client endpoints

Use a key with `inference` scope as `Authorization: Bearer …`.

- OpenAI clients: base URL `http://127.0.0.1:8080/v1`; supported paths include `/responses`, `/chat/completions`, `/completions`, `/embeddings`, and `/models`. Codex-style Responses clients can point their OpenAI-compatible base URL here when the selected vLLM model supports the request.
- Claude Code/Anthropic-compatible clients: base URL `http://127.0.0.1:8080`, with `/v1/messages` and `/v1/messages/count_tokens`. Compatibility is limited to what the configured vLLM upstream actually implements; `vllm-use` does not translate payloads.
- MCP clients: `POST http://127.0.0.1:8080/mcp` with a separately scoped MCP key and required protocol header.

The gateway validates the client key, strips client `Authorization` and `X-API-Key`, optionally adds `VLLM_USE_UPSTREAM_API_KEY`, then transparently proxies the allowed path. It does not emulate unsupported model capabilities or schemas.

## Security and development

Keep the default loopback binding unless a trusted TLS reverse proxy, firewall, strong token, and exact MCP origins are configured. Secrets are not returned by settings APIs; new API-key secrets are shown once. Request metadata includes remote addresses and paths. Read [SECURITY.md](SECURITY.md) before exposing the service.

Run `go test ./...` for backend/embed tests. Frontend checks are the Bun commands above; Vitest and React Testing Library cover authentication and API-contract behavior. Contributions follow [CONTRIBUTING.md](CONTRIBUTING.md). The project is licensed under the existing [BSD 3-Clause License](LICENSE).

## Roadmap

Likely later work includes persisted runtime profiles, richer download progress/recovery, configurable aliases, broader telemetry, and hardened remote operation. These are directions, not current capabilities.
