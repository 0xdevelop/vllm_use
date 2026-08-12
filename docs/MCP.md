# MCP management endpoint

`vllm-use` exposes management operations at `POST /mcp`. This is a stateless,
sessionless Streamable HTTP endpoint targeting MCP protocol version
`2026-07-28`. It uses the official
`github.com/modelcontextprotocol/go-sdk/mcp` package pinned to v1.7.0.

## Transport

- Send `Content-Type: application/json` and an `Accept` value containing both
  `application/json` and `text/event-stream`.
- Send `Mcp-Protocol-Version: 2026-07-28`. The same version must be present in
  the request `_meta` as required by the protocol.
- Responses are request-scoped JSON or SSE. There is no permanent GET SSE
  endpoint. GET and every method other than POST return `405` with
  `Allow: POST`.
- `Mcp-Session-Id` is never issued and any request containing it is rejected.
- When supplied, `Mcp-Method` and the applicable `Mcp-Name` must match the
  JSON-RPC body. The SDK returns MCP header-mismatch error `-32020` when they do
  not.
- Disconnecting the HTTP request cancels the request and active tool context.
  A successfully accepted background model download owns its own lifetime and
  is subsequently controlled with `models.download_cancel`.

The SDK v1.7.0 release explicitly supports `2026-07-28`, including stateless
Streamable HTTP, standardized header/body validation, request-scoped streams,
and cancellation propagation. Consequently this implementation needs no
protocol compatibility shim and does not claim any behavior beyond the SDK's
support. The release implements the specification snapshot identified by the
SDK release notes; later changes to the dated specification are not implied.

## Network and Origin policy

The safe default accepts absent Origin headers for non-browser clients, or
HTTP(S) Origin headers whose host is loopback (`localhost`, a subdomain of
`.localhost`, `127.0.0.0/8`, or `::1`). The HTTP Host must also be loopback.
This prevents a browser or DNS rebinding attack from reaching a default local
manager through an attacker-controlled name.

Additional exact origins can be configured with the comma-separated
`VLLM_USE_MCP_ALLOWED_ORIGINS` environment variable or the
`-mcp-allowed-origins` flag. An entry includes scheme, host, and optional port,
for example `https://manager.example:8443`; it also authorizes that Host.
Paths, credentials, query strings, fragments, and non-HTTP schemes are invalid.
Put a public deployment behind TLS and list only its exact trusted origins.

## Authentication and authorization

Every POST independently requires `Authorization: Bearer <API key>`. The MCP
middleware depends on a token-verifier interface; storage and key issuance
remain the responsibility of the existing API-key manager. Admin bootstrap
tokens are intentionally not accepted at `/mcp`.

Scopes are checked again at tool execution:

| Scope | Operations |
| --- | --- |
| `mcp.read` | model, runtime, GPU, download, and system reads |
| `mcp.runtime` | runtime start, stop, restart, and switch |
| `mcp.models` | model registration, download/cancel, and deletion |
| `mcp.admin` | all MCP operations |

A key must have at least one MCP scope to enter the endpoint. Having a write
scope does not implicitly grant read access; use multiple scopes where needed.

## Tools

| Tool | Required scope | Behavior |
| --- | --- | --- |
| `models.list`, `models.get` | `mcp.read` | Read sanitized registry metadata |
| `models.register` | `mcp.models` | Register Hugging Face or managed local model |
| `models.download` | `mcp.models` | Start a Hugging Face download; optional `model_id` links a registration and optional `revision` is passed structurally |
| `models.download_cancel` | `mcp.models` | Cancel a download |
| `models.delete` | `mcp.models` | Delete registration and optionally managed files |
| `runtime.status` | `mcp.read` | Read runtime state |
| `runtime.start`, `runtime.stop`, `runtime.restart`, `runtime.switch` | `mcp.runtime` | Control runtime |
| `gpu.list`, `gpu.status` | `mcp.read` | Read NVIDIA inventory/status |
| `downloads.list`, `downloads.status` | `mcp.read` | Read sanitized download state |
| `system.status` | `mcp.read` | Read process/platform status |

Tool annotations explicitly describe read-only, destructive, idempotent, and
open-world behavior. `models.delete` is destructive and requires non-empty
`id` plus an identical `confirm_model_id`; `delete_files` must be explicitly
true to remove managed files. Registry and download results omit local paths,
download logs, secrets, and raw internal errors. The authenticated admin API's
`GET /api/mcp` reports endpoint status and bounded recent request metadata.
