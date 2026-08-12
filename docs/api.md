# HTTP API overview

All `/api/*` routes require `Authorization: Bearer <token>`. The bootstrap admin token has full access. Issued keys need `admin.read` for GET/HEAD and `admin.write` for mutations (`admin.write` also satisfies reads). JSON errors have `{ "error": "message" }`; request bodies are limited to 1 MiB and reject unknown fields.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Unauthenticated process health |
| GET/POST | `/api/models`, `/api/models/scan` | List and discover managed model directories |
| GET/DELETE | `/api/models/{id}` | Read/delete a registration (`?files=true` opts into managed file deletion) |
| POST | `/api/models/huggingface` | `{repository, revision}` registration |
| POST | `/api/models/local` | `{name, path}` registration inside models root |
| GET/POST | `/api/downloads` | List/start jobs; start body uses `{id, repository, destination, token?}` |
| GET/POST | `/api/downloads/{id}[/{logs,cancel,retry}]` | Inspect/control a job |
| GET | `/api/gpus`, `/api/system`, `/api/dashboard` | Hardware/process/dashboard summaries |
| GET/POST | `/api/runtime`; `/api/runtime/{start,restart,switch,stop,status,logs}` | Runtime state/control |
| GET/POST/DELETE | `/api/keys` and `/api/keys/{id}/{enable,disable}` | Key lifecycle; create secret is returned once |
| GET | `/api/mcp` | MCP protocol/transport and bounded recent request metadata |
| GET | `/api/requests/recent?limit=100` | Gateway request metadata (1–500; invalid values use 50) |
| GET/PUT | `/api/settings` | Persist/display generic settings; secret values are redacted |

Admin API resources consistently use snake_case JSON fields. The typed WebUI contract in `web/src/api.ts` mirrors this wire format; request bodies reject fields outside the documented contract.

Inference routes require an `inference` key. Allowed routes are `GET /v1/models`, POST to `/v1/models`, `/v1/chat/completions`, `/v1/completions`, `/v1/responses`, `/v1/embeddings`, `/v1/messages`, `/v1/messages/count_tokens`, and child `/v1/responses/*` operations. Other paths/methods are rejected. Proxy behavior is transparent; no OpenAI↔Anthropic payload translation is performed.

The WebUI is served at `/` with history fallback. `/api`, `/mcp`, and `/v1` namespaces are reserved and never swallowed by SPA fallback.
