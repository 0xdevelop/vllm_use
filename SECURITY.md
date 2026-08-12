# Security policy

## Reporting a vulnerability

Do not open a public issue containing exploit details, credentials, private model paths, or request logs. Use GitHub's private vulnerability reporting for this repository if enabled; otherwise contact the repository owner privately through the profile contact channel. Include affected revision, impact, reproduction, and suggested mitigation. Maintainers should acknowledge a report before coordinating disclosure; no fixed response SLA is promised for this Phase 1 project.

## Deployment model

`vllm-use` is designed for a trusted single host and listens on loopback by default. It does not provide TLS, multi-user isolation, a rate limiter, sandboxing of vLLM/model code, or distributed authorization. Do not expose it directly to an untrusted network.

- Put remote access behind a maintained TLS reverse proxy and firewall/VPN.
- Use independent, least-privilege keys. Protect and replace the bootstrap admin token; key secrets are shown once.
- Configure only exact trusted MCP origins. Admin API authentication does not replace browser/network controls.
- Treat model repositories and model code as untrusted supply-chain inputs. Review revisions and vLLM remote-code options before execution.
- Keep the private data directory, SQLite database, token file, models, logs, and backups readable only by the service account.
- Request metadata can include paths, model names, key IDs, and client addresses. Runtime/download logs can contain upstream output; do not paste secrets into options or logs.
- Hugging Face tokens are passed to the child download environment and are not returned, but exposure through the local process environment remains part of the host trust boundary.

Only managed-root paths may be scanned or deleted, and file deletion is explicit. API and MCP credentials use bearer authentication; inference client credentials are stripped before proxying and an optional dedicated upstream credential is substituted.

Security fixes are supported on the current mainline only until versioned releases define another policy.
