# Configuration

`vllm-use` reads defaults, then command-line flags. The environment variables below supply secrets or list values; flags do not cover every internal timeout in Phase 1.

| Flag / environment | Default | Meaning |
| --- | --- | --- |
| `-listen` | `127.0.0.1:8080` | Manager HTTP address |
| `-data-dir` | OS config dir + `vllm-use` | Private state root; absolute |
| `-db` | `<data-dir>/vllm-use.db` | SQLite file; absolute |
| `-models-dir` | `<data-dir>/models` | Managed model root; absolute |
| `-vllm` | `vllm` | vLLM executable |
| `-hf` | `hf` | Hugging Face CLI executable |
| `-hf-home` / `VLLM_USE_HF_HOME` | inherited environment | Optional absolute Hugging Face cache/config directory; the flag wins |
| `-max-download-workers` / `VLLM_USE_MAX_DOWNLOAD_WORKERS` | `2` | Concurrent downloads, from 1 through 64; the flag wins |
| `-upstream` | `http://127.0.0.1:8000` | vLLM gateway target |
| `-admin-token` / `VLLM_USE_ADMIN_TOKEN` | generated file | Full admin bootstrap credential; the flag wins |
| `VLLM_USE_UPSTREAM_API_KEY` | empty | Credential sent to vLLM after client credentials are stripped |
| `-mcp-allowed-origins` / `VLLM_USE_MCP_ALLOWED_ORIGINS` | loopback only | Comma-separated exact HTTP(S) browser origins; the flag wins |

Configured directories and the SQLite file are forced to private permissions where supported. The generated `admin-bootstrap.token` is mode `0600`. Back up the database carefully: it contains hashed API-key material and operational metadata. Downloads and local registrations are constrained to the resolved models root; deleting files is opt-in. Download destinations are passed with `hf download --local-dir`; they are never used as `HF_HOME`. When HF home is unset, the child inherits any ambient `HF_HOME` unchanged. Download tokens are supplied only through the child environment and are not persisted or placed in command arguments.

Settings written through `/api/settings` are generic persisted key/value metadata. Secret values are write-only through the API. Phase 1 does **not** bind these rows to flags or live runtime behavior; use flags/environment and restart the manager for process configuration.

For remote access, retain a loopback listener behind a local TLS reverse proxy when possible. If binding elsewhere, use a high-entropy admin token, network access controls, TLS, and exact MCP origins. There is no built-in TLS or rate limiter.

## Data layout

Typical Linux defaults:

```text
~/.config/vllm-use/
├── admin-bootstrap.token
├── vllm-use.db
└── models/
```

SQLite persists models, keys, settings, download snapshots, and request metadata. Active process state and log buffers remain in memory.
