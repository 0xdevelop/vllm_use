# vllm-use

vllm-use 是一个可自托管、宿主机原生的 vLLM 管理与推理服务。它用一个 Go 二进制提供 React Web Admin、SQLite 状态、vLLM 单进程监督、模型下载、GPU 观测、API key 管理、OpenAI/Anthropic 兼容 Gateway 与 MCP 管理接口。

## 运行

```bash
go run . --listen 127.0.0.1:8080
```

首次运行会在私有数据目录创建 `admin-bootstrap.token`（日志只报告路径，不输出 secret）。默认数据目录是用户配置目录下的 `vllm-use`，可通过 CLI flags 和 `VLLM_USE_*` 环境变量调整。

主要入口：

- `/`：React Web Admin
- `/healthz`：进程健康检查
- `/api/*`：受保护的管理 HTTP Adapter
- `/mcp`：受保护的 stateless Streamable HTTP MCP
- `/v1/*`：受保护的 OpenAI/Anthropic 兼容推理 Gateway

服务直接调用宿主机 `vllm`、`hf` 和 `nvidia-smi`，不使用 Docker。缺少这些程序时不会伪造运行、下载或 GPU 结果。

## 架构

管理能力统一走：

```text
HTTP / MCP Adapter → APIExecuter → Supported Methods Registry → Ability
```

产品域为 model、download、runtime、gpu、api_key、settings，持久化使用 SQLite。项目保留 project_template_go 的 setupApp、gtbox 日志/run mode 与应用元数据机制，但不包含模板的 MySQL、用户 Auth、JSON-RPC、WebSocket、gRPC 或通用异步任务示例。

## 开发与文档

- 项目契约：[`AGENTS.md`](AGENTS.md)
- API 架构：[`docs/api_description.md`](docs/api_description.md)
- Ability 方法清单：[`docs/api_methods.md`](docs/api_methods.md)，由 `./gen_api_docs.sh` 生成，禁止手改
- Web Admin：`web/`，使用 Bun；生产构建输出由 Go embed 打入二进制

常规后端质量门：`go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`。发布只能使用 `./git_tag.sh`。
