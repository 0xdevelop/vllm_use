# vllm-use

A lightweight, native Go manager for local vLLM runtimes. Phase 1 provides the
secure backend foundation: SQLite storage, model registry, downloader and GPU
adapters, Linux runtime supervision, an inference reverse proxy package, API
key hashing, and an admin service API.

Build with `go build ./cmd/vllm-use`. The service listens on `127.0.0.1:8080`
by default and stores private state below the user's config directory.

MCP 2026-07-28 is intentionally not exposed yet. At implementation time the
official Go SDK's newest stable release is v1.6.1; 2026-07-28 support requires
the prerelease v1.7 line. A later tranche should add `/mcp` once a stable SDK
release satisfying that requirement is available.
