# API 通信与执行架构

本文描述当前产品的管理控制面与推理数据面。历史模板中的 JSON-RPC、WebSocket、gRPC、用户 JWT/Auth 和通用 Task/Policy 不属于 vllm-use。

## 管理控制面

管理能力只有两种 Adapter：

- `/api/*`：供 React Web Admin 和运维客户端使用的资源化 HTTP 路由。
- `POST /mcp`：MCP `2026-07-28` stateless Streamable HTTP，使用官方 Go SDK。

两者都必须完成 Bearer 认证，并将授权信息写入 context，然后进入同一条调用链：

```text
Adapter → api_executer.ExecuteAbility/APIExecuter
        → api_supported_methods.Method
        → SupportedMethod.Execute
        → ability_<domain>
```

HTTP Adapter 把资源路由映射为注册方法名和 `arguments`；MCP Adapter 原生接收 `tools/call → name → arguments`。Adapter 不得直接持有或调用域 Service 来绕过注册表和 scope 门禁。

`SupportedMethod` 保存 `Name`、`Description`、`InputSchema`、`Scope`、`Public` 和 `Execute`。`api_executer` 负责方法查找与 fail-closed scope 校验；Ability 负责严格解码参数和业务执行。`test` 是唯一公开方法，但 `/mcp` 外层仍要求 API key。

当前 scope：

- 管理 HTTP：bootstrap admin token，或 `admin.read` / `admin.write`。
- MCP：`mcp.read`、`mcp.models`、`mcp.runtime`、`mcp.admin`；`mcp.admin` 可调用全部 MCP tools。

MCP 还要求 `Mcp-Protocol-Version: 2026-07-28`，并使用 Go 标准库跨源保护；可信浏览器 Origin 只能由显式配置加入。未显式配置管理 token 时，服务仅以原子排他创建方式生成 `0600` 的 bootstrap 凭据文件；重启读取时拒绝符号链接、非普通文件、控制字符和异常长度，并收紧遗留的过宽权限。

## 产品 Ability

- `ability_model`：扫描、登记、查询和删除模型。删除与 runtime 启动、停止、重启、切换共用同一生命周期门禁：正在按注册 ID 运行的模型，以及通过底层直接启动且实际路径相同的模型，都拒绝删除；门禁会一直持有到删除提交结束，避免“检查后启动”的并发竞态。删除本地文件时先把模型目录原子移动到模型根目录下的私有隔离区，再提交 SQLite 删除；进程若在两步之间退出，下次启动以 SQLite 为权威恢复未提交删除的目录，或清理已提交删除的隔离目录。未知隔离区内容会被保留并导致启动失败，避免误删运维人员数据。
- `ability_download`：通过宿主机 Hugging Face CLI 下载、重试、取消、查询日志。HTTP/MCP 只能携带任务 `id`、已登记 Hugging Face 模型的 `model_id` 和一次性 token；服务从 SQLite 读取 repository/revision，并把目标目录固定派生为 `<models-dir>/<model_id>`，客户端不能注入仓库或主机路径。服务会先在同一 SQLite 事务中持久化任务和模型的 `downloading` 状态，再启动宿主机 CLI；任一持久化步骤失败都会回滚且不会发布内存任务或启动下载进程。CLI 退出成功后还会校验目标是模型根目录内真实可读的目录并计量文件大小，校验通过才在同一事务中将任务及对应模型记录转为 `succeeded` / `ready`，避免数据库故障留下成功任务与仍在下载模型的分裂状态；终态持久化失败会显式标记内存任务失败，并使服务关闭返回错误，而不是假报持久化成功。
- `ability_runtime`：构造受约束参数，监督唯一宿主机 vLLM 进程并切换活动模型。`runtime.switch` 以 SQLite 中的 `model_id` 为权威，只允许切换到状态为 `ready` 且具有受管本地路径的模型；启动前会重新确认登记路径仍是模型根目录内的真实目录，拒绝缺失、普通文件、被替换的符号链接或逃逸路径，调用方不能用 `options.model` 绕过模型登记状态。切换会接管并停止此前通过底层 `runtime.start` 直接启动的进程；重启会先完整校验替换参数，非法配置不会停止健康进程。运行状态只在当前进程确由模型注册表启动时返回对应 `active_model_id`，直接启动或重启不会沿用过期关联。Web Admin 的启动/切换入口只选择这些就绪模型。常用 vLLM flags 使用类型化字段，`extra_args` 仅接受结构化的合法 flag 名和值，不能通过值注入另一项 `--flag` 或覆盖保留参数。
- `ability_gpu`：读取真实 `nvidia-smi` 状态。
- `ability_api_key`：创建、列出、启停和删除带 scope 的 API key。
- `ability_settings`：保存、删除非敏感设置与读取最近 Gateway 请求元数据。HTTP Web Admin 和 MCP 都通过统一注册方法删除设置；token、password、credential、API key 等敏感键会被拒绝写入，凭据只能来自环境变量或 CLI flags，升级时会清理旧版 SQLite 中的敏感设置行。

SQLite 是持久化真相源。下载进程和 vLLM runtime 由宿主机进程组管理；已受理的下载不依赖 HTTP/MCP 请求 context，服务退出时会停止接收请求、取消并等待下载状态落库后再关闭 SQLite。不存在 MySQL Worker 或通用异步任务框架。

## 推理数据面

`/v1/*` 是独立的推理 Gateway Adapter，校验 `inference` scope 后反向代理到配置的 vLLM upstream。upstream 配置只能是无凭据、无路径/查询/fragment 的 loopback HTTP(S) origin，确保数据面连接的是本机受管 vLLM，而不是被误配成任意远端代理。它支持：

- OpenAI Chat Completions、Completions、Responses、Embeddings 和 Models 端点。
- Anthropic Messages 与 token counting 兼容端点。
- SSE 流式透传、请求取消传播、模型 alias 重写（重写时保留大整数等 JSON 数值的原始精度）、可选上游凭据注入。所有 POST 推理请求统一限制为 16 MiB，不能通过省略或伪装 `Content-Type` 绕过。
- 只记录非敏感且有长度上限的请求元数据，并以 API key ID（不含 secret）标记已认证请求，便于审计和撤销分析；每条记录有服务端生成的唯一 `audit_id`，客户端 `X-Request-ID` 仅作为可重复的关联字段，超过 128 字节或含非可见 ASCII 时会替换为服务端 ID，重复值不会覆盖、丢弃或混淆审计事件；模型名等审计字段按 UTF-8 边界截断但不会改写原推理请求；客户端 Authorization/X-API-Key 不转发给 upstream；服务优雅退出时会在关闭 SQLite 前等待已接收的审计写入完成。

Gateway 不执行业务管理 Ability，也不伪造推理结果。upstream 不可用时返回明确的 502。

## HTTP 与单二进制边界

单一 listener 默认 `127.0.0.1:8080`：

- `GET /healthz` 无需认证，只说明管理进程存活。
- `/api/*`、`/mcp`、`/v1/*` 按上述规则认证。
- 其余 GET 路由由嵌入的 React SPA 处理；后端命名空间不会回落为 SPA 假成功。

生产 Web 资源来自 `web/dist` 的 Go embed。运行不依赖 Node/Bun 或额外静态文件服务。

## 错误语义

- HTTP 管理 Adapter 使用 HTTP 状态表达路由、认证、参数与业务错误。
- MCP 的已注册 tool 业务结果使用 `CallToolResult`，并显式输出 `isError`；协议损坏和未知 tool 使用 MCP/JSON-RPC 协议错误。
- Gateway 保留 upstream 正常响应；认证、路由、请求体限制和 upstream 不可用使用兼容 JSON error。
- 无 vLLM、Hugging Face CLI 或 `nvidia-smi` 时返回真实失败/空状态，不得合成成功数据。
