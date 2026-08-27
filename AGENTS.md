# vllm-use 项目契约

本仓库是可公开开源的宿主机原生 vLLM 管理与推理服务。以下约束优先于 project_template_go 中已裁剪的示例域；禁止恢复 MySQL、用户注册/Auth、JSON-RPC、WebSocket、gRPC、通用异步 Task/Policy 或 Docker 示例。

## 产品架构

- 保留 project_template_go 的 `setupApp`、gtbox、统一日志、run mode、应用元数据、生成/测试/发布机制；产品运行时由 `app.Run` 组合。
- 管理调用链固定为 **Adapter → `api_executer` → `api_supported_methods` → Ability**。Adapter 只做认证、协议/路由映射和响应编码；业务规则、参数解码和执行不得复制到 Adapter。
- `api_executer.ExecuteAbility` 是受保护管理能力的唯一业务执行入口，统一执行 scope 门禁并从注册表调用 `Execute`。不得引入第二套执行器、业务 switch、反射扫描、隐藏 `init()` 注册或跨协议返回分发。
- `ability` 父包是方法装配入口；产品域固定为 `ability_model`、`ability_download`、`ability_runtime`、`ability_gpu`、`ability_api_key`、`ability_settings`。新增子域必须由直属父包显式加载。
- `api_supported_methods` 只保存协议无关的方法元数据、scope、InputSchema 和 Execute，不引用具体 Ability。
- 管理 Adapter 当前为同一 HTTP listener 上的 `/api/*` 与 `/mcp`。MCP 使用官方 Go SDK、stateless Streamable HTTP、协议版本 `2026-07-28`。不得把已经裁剪的 JSON-RPC、WebSocket 或 gRPC 示例恢复为产品接口。
- 推理 Gateway 位于同一 listener 的 `/v1/*`，代理宿主机 vLLM，支持 OpenAI 与 Anthropic 兼容端点、流式透传和 API key scope 校验；它不是管理 Ability 的旁路。
- Web Admin 使用 React，生产资源由 Go `embed` 打入同一二进制；不得新增独立前端服务作为发布依赖。

## 宿主机运行与数据

- 只管理一个宿主机原生 vLLM 进程；使用进程组完成启动、就绪检查、停止、重启和模型切换。禁止 Docker、容器编排和伪造的进程/GPU 状态。
- 持久化固定为 SQLite（`db/sqlite`）；模型、下载、API key、非敏感设置和请求元数据共用该 store。CGO 可用，但不得把产品迁回 MySQL/GORM 模板示例。
- GPU 信息来自真实 `nvidia-smi`；不可用时返回明确的空结果或错误，不得构造设备数据。
- 模型下载使用宿主机 Hugging Face CLI；token、API key、bootstrap token 和上游凭据不得写日志或明文返回（创建时的一次性 key secret 除外）。
- 默认只监听 `127.0.0.1:8080`。管理 API 必须使用 bootstrap admin token 或具备对应 scope 的 API key；MCP 还必须通过协议版本和跨源保护。
- 配置权威是 `ability_settings.Config`、环境变量和 CLI flags。`example_files/*.yaml` 仅供 gtbox Debug/模板加载保持合法，不得重新承载已裁剪产品配置。

## 文档与生成物

- `README.md` 是公开入口，`docs/api_description.md` 描述当前管理/推理接口边界。
- `docs/api_methods.md` 由根目录 `gen_api_docs.sh` 从当前方法注册表生成，禁止手改。修改注册方法或生成器后必须重新生成并检查不含已裁剪域。
- 不保留已裁剪示例域的“未来计划”文档，避免后续代理误恢复旧架构。
- 项目根 `tmp/` 只存可删除、可再生成的缓存和冒烟产物；源码和续做状态不得依赖它。

## 工具、测试与发布

- 禁止 Docker。生成工具只允许根目录 `gen_*.sh` + `//go:build ignore` 单文件；不得新增会编进产品的临时 main/探针。
- 每个闭环至少执行 `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...` 和 `git diff --check`。
- 涉及前端时必须在 `web/` 执行 Bun frozen install、lint、typecheck、test、production build，再验证 Go embed。
- 涉及运行链时必须构建并启动真实单二进制，验证 `/healthz`、受保护管理 API、MCP 和 embed 页面；无 vLLM/GPU 时只验证可真实完成的失败路径和其他链路。
- 临时进程和产物必须清理，提交前复查工作区。闭环后提交并推送 `main`，再确认 `origin/main` 指向该提交。
- 发布只能运行仓库 `git_tag.sh`；禁止手改版本、手工 tag 或手工 release。
