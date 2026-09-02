# vllm-use

[![Quality](https://github.com/0xdevelop/vllm_use/actions/workflows/quality.yml/badge.svg)](https://github.com/0xdevelop/vllm_use/actions/workflows/quality.yml)

vllm-use 是一个可自托管、宿主机原生的 vLLM 管理与推理服务。它用一个 Go 二进制提供 React Web Admin、SQLite 状态、vLLM 单进程监督、模型下载、GPU 观测、API key 管理、OpenAI/Anthropic 兼容 Gateway 与 MCP 管理接口。

## 运行

```bash
go run . --listen 127.0.0.1:8080
```

首次运行会以原子、仅新建方式在私有数据目录创建 `admin-bootstrap.token`（权限 `0600`，日志只报告路径，不输出 secret）。后续启动会拒绝符号链接、非普通文件、控制字符和异常长度的凭据，并自动收紧过宽的文件权限。默认数据目录是用户配置目录下的 `vllm-use`。配置优先级为 **CLI flag > 环境变量 > 内置默认值**；仅设置 `data-dir` 时，数据库和模型目录会随之派生，显式配置的数据库或模型目录不会被覆盖。

常用配置：

| CLI flag | 环境变量 | 默认值 |
| --- | --- | --- |
| `--listen` | `VLLM_USE_LISTEN` | `127.0.0.1:8080` |
| `--data-dir` | `VLLM_USE_DATA_DIR` | 用户配置目录下的 `vllm-use` |
| `--db` | `VLLM_USE_DATABASE`（兼容 `VLLM_USE_DB`） | `<data-dir>/vllm-use.db` |
| `--models-dir` | `VLLM_USE_MODELS_DIR` | `<data-dir>/models` |
| `--vllm` | `VLLM_USE_VLLM_BINARY` | `vllm` |
| `--hf` | `VLLM_USE_HF_CLI` | `hf` |
| `--hf-home` | `VLLM_USE_HF_HOME` | 继承宿主机 Hugging Face 配置 |
| `--max-download-workers` | `VLLM_USE_MAX_DOWNLOAD_WORKERS` | `2` |
| `--upstream` | `VLLM_USE_UPSTREAM` | `http://127.0.0.1:8000`（仅允许 loopback origin） |
| `--readiness-timeout` | `VLLM_USE_READINESS_TIMEOUT` | `2m` |
| `--shutdown-grace` | `VLLM_USE_SHUTDOWN_GRACE` | `10s` |
| `--health-interval` | `VLLM_USE_HEALTH_INTERVAL` | `200ms` |
| `--mcp-allowed-origins` | `VLLM_USE_MCP_ALLOWED_ORIGINS` | 空 |

管理 token 和可选上游凭据分别使用 `VLLM_USE_ADMIN_TOKEN` / `--admin-token` 与 `VLLM_USE_UPSTREAM_API_KEY` / `--upstream-api-key`。生产环境优先使用环境变量，避免 secret 出现在进程参数中。`upstream` 必须是指向本机 vLLM 的 loopback HTTP(S) origin，不能包含凭据、路径、查询或 fragment；这避免把受保护 Gateway 配成任意远端代理。非法的数值、时长、路径、地址或 Origin 会让进程在启动阶段明确失败，而不会静默回退。

主要入口：

- `/`：React Web Admin
- `/healthz`：进程健康检查
- `/api/*`：受保护的管理 HTTP Adapter
- `/mcp`：受保护的 stateless Streamable HTTP MCP
- `/v1/*`：受保护的 OpenAI/Anthropic 兼容推理 Gateway

服务直接调用宿主机 `vllm`、`hf` 和 `nvidia-smi`，不使用 Docker。缺少这些程序时不会伪造运行、下载或 GPU 结果。

## systemd 宿主机部署

发布包可用 `example_files/install_vllm-use.sh` 安装。安装脚本要求同目录存在可执行的 `vllm-use`、`vllm-use.service` 和 `vllm-use.env`，并且必须显式选择动作，不会扫描或删除解压目录：

```bash
sudo ./example_files/install_vllm-use.sh install
sudo systemctl status vllm-use.service
```

默认布局：

- 二进制：`/usr/local/bin/vllm-use`
- 私有环境文件：`/etc/vllm-use/vllm-use.env`（首次安装创建为 `0600`，更新不会覆盖）
- SQLite、模型和 bootstrap token：`/var/lib/vllm-use`
- Hugging Face cache：`/var/cache/vllm-use`
- 服务账户：无登录权限的 `vllm-use`，安装时按宿主机现有组加入 `video` / `render` 以访问 NVIDIA 设备

服务默认仍只监听 loopback，并启用 systemd 文件系统与提权防护。请在环境文件中按宿主机安装位置调整 `VLLM_USE_VLLM_BINARY` 和 `VLLM_USE_HF_CLI`；凭据不要写入 unit 或命令行。未设置 admin token 时，首次启动会把 bootstrap token 写入 `/var/lib/vllm-use/admin-bootstrap.token`。

更新与卸载分别使用：

```bash
sudo ./example_files/install_vllm-use.sh update
sudo ./example_files/install_vllm-use.sh uninstall
```

卸载只删除 unit 和程序文件，故意保留环境文件、服务账户、SQLite、模型与缓存，避免误删大模型和审计数据。

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
