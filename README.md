# vllm-use

[![Quality](https://github.com/0xdevelop/vllm_use/actions/workflows/quality.yml/badge.svg)](https://github.com/0xdevelop/vllm_use/actions/workflows/quality.yml)

vllm-use 是一个可自托管、宿主机原生的 vLLM 管理与推理服务。它用一个 Go 二进制提供 React Web Admin、SQLite 状态、vLLM 单进程监督、模型下载、GPU 观测、API key 管理、OpenAI/Anthropic 兼容 Gateway 与 MCP 管理接口。

## 运行

```bash
go run . --listen 127.0.0.1:8080
```

首次运行会以原子、仅新建方式在私有数据目录创建 `admin-bootstrap.token`（权限 `0600`，日志只报告路径，不输出 secret）。后续启动会拒绝符号链接、非普通文件、控制字符和异常长度的凭据，并自动收紧过宽的文件权限。SQLite 数据库同样拒绝符号链接和非普通文件，既有数据库权限会收紧为 `0600`，避免配置路径意外指向并修改其他文件。数据、模型和显式 Hugging Face cache 目录也必须是真实目录而非符号链接，权限会通过已校验的目录描述符收紧为 `0700`，拒绝配置时不会误改链接目标。默认数据目录是用户配置目录下的 `vllm-use`。配置优先级为 **CLI flag > 环境变量 > 内置默认值**；仅设置 `data-dir` 时，数据库和模型目录会随之派生，显式配置的数据库或模型目录不会被覆盖。

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
| `--max-audit-records` | `VLLM_USE_MAX_AUDIT_RECORDS` | `10000`（设为 `0` 停止新增审计记录） |
| `--upstream` | `VLLM_USE_UPSTREAM` | `http://127.0.0.1:8000`（仅允许 loopback origin） |
| `--model-aliases` | `VLLM_USE_MODEL_ALIASES` | 空；逗号分隔的 `alias=upstream-model` 映射 |
| `--readiness-timeout` | `VLLM_USE_READINESS_TIMEOUT` | `2m` |
| `--shutdown-grace` | `VLLM_USE_SHUTDOWN_GRACE` | `10s` |
| `--health-interval` | `VLLM_USE_HEALTH_INTERVAL` | `200ms` |
| `--mcp-allowed-origins` | `VLLM_USE_MCP_ALLOWED_ORIGINS` | 空 |

管理 token 和可选上游凭据分别使用 `VLLM_USE_ADMIN_TOKEN` / `--admin-token` 与 `VLLM_USE_UPSTREAM_API_KEY` / `--upstream-api-key`。生产环境优先使用环境变量，避免 secret 出现在进程参数中。凭据必须是最多 4096 字节、无空白或逗号的可见 ASCII；认证只接受单个规范的 `Authorization: Bearer <token>`，Anthropic 端点也可改用单个 `X-API-Key`，重复、合并或同时提供两种凭据都会拒绝，避免代理链对歧义请求作出不同解释。scope 权限不会跨协议扩大：`mcp.admin` 只统管 MCP tools，不等价于管理 HTTP 的 `admin.read` / `admin.write`，也不授予 Gateway 的 `inference`。`upstream` 必须是指向本机 vLLM 的 loopback HTTP(S) origin，不能包含凭据、路径、查询或 fragment；Gateway 连接明确忽略宿主机 `HTTP_PROXY` / `HTTPS_PROXY`，避免本地推理流量和可选上游凭据被环境代理重定向。这避免把受保护 Gateway 配成任意远端代理。模型别名可配置为 `chat=org/production-chat,embed=org/embedding`；Gateway 只改写请求 JSON 中精确匹配的 `model` 值，审计仍保留客户端使用的别名。重复、空白、含控制字符或超过边界的映射会使启动失败。推理审计默认只保留最近 10000 条，插入与裁剪在同一 SQLite 事务中完成；设为 `0` 后不再写入新记录，但不会删除已有历史。非法的数值、时长、路径、地址、Origin、凭据或模型别名会让进程在启动阶段明确失败，而不会静默回退。

主要入口：

- `/`：React Web Admin
- `/healthz`：进程健康检查
- `/api/*`：受保护的管理 HTTP Adapter
- `/mcp`：受保护的 stateless Streamable HTTP MCP（请求必须精确携带且只携带一个 `Mcp-Protocol-Version: 2026-07-28`，缺失、重复、合并或不匹配时返回 400 并声明受支持版本）
- `/v1/*`：受保护的 OpenAI/Anthropic 兼容推理 Gateway

服务直接调用宿主机 `vllm`、`hf` 和 `nvidia-smi`，不使用 Docker。`GET /api/system` 与 MCP `system.get` 会按实际配置解析这三个宿主机命令，返回 `available`、`missing` 或 `error` 预检状态；`nvidia-smi` 存在时还会执行真实 GPU 查询并报告设备数量或驱动错误，Web Admin 的“设置”页直接展示该诊断。缺少 `nvidia-smi` 时 GPU 列表为空；驱动故障、权限错误等执行失败会明确返回错误，不会伪装成“没有 GPU”，其他宿主程序缺失时同样不会伪造运行或下载结果。本地模型只能登记 models 根目录下的独立真实子目录，名称限制为至多 256 字节、无控制字符的合法 UTF-8；Hugging Face 模型登记与下载共享 `owner/name`、revision 字符及长度校验，不会把宿主 CLI 必然拒绝的坐标先写入 SQLite。模型读取还会完整校验 ID、名称、类型、Hub 坐标、状态、受管路径、大小和时间关系，损坏记录会让当前管理请求明确失败，而不会作为可启动或可删除模型发布。下载启动前会拒绝目标目录或现有父路径中的符号链接以及非目录目标，防止预先植入的受管模型路径把 Hugging Face CLI 写入重定向到模型根目录之外；完成时还会再次解析并校验最终目录。超过并发 worker 上限的下载会以 `pending` 状态持久排队，并在 worker 释放后先把 `running` 与实际开始时间持久化，成功后才启动宿主机 CLI；该状态写入失败时任务会明确失败且不会产生未被数据库追踪的下载进程，也不会因为暂时繁忙而错误失败。排队任务在服务关闭或异常重启后会明确转为 `canceled`。重启恢复会校验持久化下载的 ID、模型关联、Hub 坐标、状态、进度、时间戳和有界日志，损坏记录会阻止服务监听而不会以未知状态进入内存。下载重试会重新从当前模型登记读取仓库、revision 和受管目标目录，而不信任历史任务快照；任务落库与模型从可下载状态切换为 `downloading` 在同一事务内完成，已经就绪或正在下载的模型不会被旧任务回退并覆盖。vLLM 就绪探测固定访问由已校验 runtime host/port 派生的 loopback `/health`，HTTP/MCP 调用方不能提供替代 URL；探测忽略宿主代理配置且不跟随重定向，防止越出本机进程边界。停止过程先向整个进程组发送 `SIGTERM`，宽限期后升级为 `SIGKILL`；等待会同时受调用方 deadline 和内部强杀回收上限约束，异常的宿主进程不会让管理服务关闭永久挂死。启动请求在就绪阶段被取消时会沿用同一 deadline 立即升级清理未就绪进程，不会继续等待完整的 shutdown grace。下载 token 只经 Hugging Face 子进程环境传递且受长度/控制字符校验；管理 token 与 Gateway 上游凭据会从 vLLM、Hugging Face 和 GPU 探测子进程环境中剥离，避免无关宿主进程继承控制面 secret。SQLite 设置只允许非敏感值；敏感键检测会忽略标点、Unicode 格式字符和符号等分隔符，避免用 `api-key`、`api<U+200B>key`、`client.secret` 等拼写绕过凭据边界，并在每次打开数据库时按同一规则清理旧版或外部写入的遗留记录。API key 使用固定的 `vu_` 加 48 位字母数字格式，随机字符采用无模偏差采样，认证会在 SQLite 查询和 scrypt 前拒绝异常长度或字符，并在 scrypt 前完整验证持久化的名称、前缀、salt/hash 长度、enabled 标记、scope 集合和时间戳；损坏记录不会部分认证或更新使用时间。认证写入最后使用时间前会释放查询游标，避免并发认证因小型连接池耗尽而相互等待。服务会持续排空 `hf` 与 vLLM 的 stdout/stderr，并按 UTF-8 边界截断超长单行，避免异常 CLI 输出卡死进程、撑大内存或无界占用 SQLite。删除模型文件采用同文件系统隔离后再提交 SQLite 的流程；若进程在删除中途退出，下次启动会依据数据库真相恢复未提交删除或清理已提交删除，不会把未知隔离内容当作可删除垃圾。

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

常规后端质量门：`go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`。

发布只能从干净的 `main` 运行 `./git_tag.sh`：脚本统一更新版本、生成 changelog/API 文档、提交并推送版本 tag。tag 触发的 Release workflow 会再次执行固定版本的 Bun 前端门禁、生成物校验、Go test/race/vet/build 和发布包内容校验，全部通过后才创建 GitHub Release；不得手改版本或手工创建 tag/release。
