# vllm-use

[English](README.md) · [配置](docs/configuration.md) · [HTTP API](docs/api.md) · [MCP](docs/MCP.md)

`vllm-use` 是一个轻量的原生 Go 单机 vLLM 控制面：登记本地/Hugging Face 模型，监督一个 vLLM 进程，提供带权限的 OpenAI/Anthropic 兼容网关、MCP 管理端点，以及嵌入同一二进制的中文 Web 管理台。Phase 1 面向可信单机管理员，不是集群调度器或多租户平台。

```text
浏览器 / API 客户端 / MCP 客户端
                 │
        ┌────────▼──── vllm-use 单一 Go 程序 ────────┐
        │ WebUI │ 管理 API │ 密钥权限 │ MCP 工具      │
        │ SQLite/模型登记       vLLM 进程监督          │
        └──────────┬──────────────┬───────────────────┘
                hf 下载       原生 vLLM → 模型推理
```

## 已实现

- Linux 上启动、停止、重启一个原生 `vllm serve`；就绪检查、PID、退出状态与最多 1000 行内存日志。
- SQLite 模型登记、`models-dir` 扫描、本地模型和 Hugging Face 下载（调用 `hf`，默认最多两个并发）。
- 通过 `nvidia-smi` 读取 NVIDIA GPU；不可用时显示空列表。
- 哈希保存的命名 API key，可启停/删除；支持推理、管理和细分 MCP scope。
- 白名单 `/v1` 透明反向代理，支持 models、chat/completions、completions、Responses、embeddings、messages 与 count_tokens；保留流式响应。
- `POST /mcp` 无状态 Streamable HTTP（协议 `2026-07-28`）及八页面嵌入式 WebUI。

限制：运行监督仅支持 Linux、仅检测 NVIDIA、只能运行一个 vLLM；没有 Docker、集群、多用户 RBAC、TLS、在线模型商店或 vLLM 自动安装；数据库 settings 不会动态改变进程配置；重启后内存日志不恢复。登记 Hugging Face 模型不等于下载。

## 构建与启动

需要 Go 1.25+。运行模型需要 Linux、Python/vLLM；下载需要 `hf`；GPU 状态需要 NVIDIA 工具。只有开发 WebUI 才需要 Bun 1.3.3+，生产二进制不需要 Bun/Node。

```bash
# 使用仓库内已有 web/dist
go test ./...
go build -o vllm-use ./cmd/vllm-use
./vllm-use

# 修改 WebUI 后
cd web
bun install
bun run lint && bun run typecheck && bun run test && bun run build
cd .. && go build -o vllm-use ./cmd/vllm-use
```

开发时先启动 8080 端口的 Go 服务，再执行 `cd web && bun run dev`；Vite 会代理 `/api`、`/mcp`、`/v1`。

首次启动若未设置 `VLLM_USE_ADMIN_TOKEN`，程序在私有数据目录创建权限 `0600` 的 `admin-bootstrap.token`，日志只显示路径。把文件内容输入 WebUI。所有数据路径必须是绝对路径，本地模型和下载目标必须位于 `models-dir`。完整参数见[配置文档](docs/configuration.md)。

推理 key 需要 `inference` scope：OpenAI/Codex Responses 客户端可把 base URL 指向 `http://127.0.0.1:8080/v1`；Claude Code/Anthropic 客户端可使用主机 base URL 和 `/v1/messages`。本项目不转换协议内容，实际兼容能力取决于 vLLM 上游和模型。MCP 使用独立 MCP scope key，详见 [MCP 文档](docs/MCP.md)。

默认仅监听回环地址。远程暴露前必须阅读 [SECURITY.md](SECURITY.md)，配置可信 TLS 反代、防火墙、强令牌和精确 MCP origin。HTTP 端点见 [API 文档](docs/api.md)。贡献方式见 [CONTRIBUTING.md](CONTRIBUTING.md)。许可证是现有的 [BSD 3-Clause](LICENSE)。

后续方向包括持久运行配置、更完整的下载恢复/进度、别名与遥测；这些尚未实现。
