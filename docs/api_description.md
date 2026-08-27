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

MCP 还要求 `Mcp-Protocol-Version: 2026-07-28`，并使用 Go 标准库跨源保护；可信浏览器 Origin 只能由显式配置加入。

## 产品 Ability

- `ability_model`：扫描、登记、查询和删除模型。
- `ability_download`：通过宿主机 Hugging Face CLI 下载、重试、取消、查询日志。
- `ability_runtime`：构造受约束参数，监督唯一宿主机 vLLM 进程并切换活动模型。
- `ability_gpu`：读取真实 `nvidia-smi` 状态。
- `ability_api_key`：创建、列出、启停和删除带 scope 的 API key。
- `ability_settings`：保存非敏感设置与读取最近 Gateway 请求元数据。

SQLite 是持久化真相源。下载进程和 vLLM runtime 由宿主机进程组管理；不存在 MySQL Worker 或通用异步任务框架。

## 推理数据面

`/v1/*` 是独立的推理 Gateway Adapter，校验 `inference` scope 后反向代理到配置的 vLLM upstream。它支持：

- OpenAI Chat Completions、Completions、Responses、Embeddings 和 Models 端点。
- Anthropic Messages 与 token counting 兼容端点。
- SSE 流式透传、请求取消传播、模型 alias 重写、可选上游凭据注入。
- 只记录非敏感请求元数据，并以 API key ID（不含 secret）标记已认证请求，便于审计和撤销分析；客户端 Authorization/X-API-Key 不转发给 upstream；服务优雅退出时会在关闭 SQLite 前等待已接收的审计写入完成。

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
