# 项目协作约定

完整强制契约见 `AGENTS.md`。动手前还应按范围阅读 `README.md`、`docs/api_description.md` 和相关源码。

## 当前产品事实

- vllm-use 是宿主机原生、SQLite 持久化、单 vLLM 进程的管理与推理服务；禁止 Docker。
- 保留 project_template_go 的 `setupApp`、gtbox、统一日志、run mode、应用元数据和研发机制，但不得恢复其 MySQL/Auth/JSON-RPC/WebSocket/gRPC/Async Task 示例。
- 管理链固定为 Adapter → `api_executer` → `api_supported_methods` → Ability。
- 产品 Ability 域为 model、download、runtime、gpu、api_key、settings；认证由管理 token/API key scopes 在 Adapter 与执行器门禁完成，不存在用户注册/JWT 域。
- 单一 HTTP listener 默认 `127.0.0.1:8080`：`/api/*` 管理 API、`/mcp` stateless MCP、`/v1/*` OpenAI/Anthropic 推理 Gateway、`/` React Web Admin。
- Web Admin 生产资源由 Go embed 进入单二进制；数据落 SQLite；vLLM、Hugging Face CLI 和 `nvidia-smi` 都调用宿主机真实程序。

## 执行纪律

- 每轮先 fetch 并确认 `main`/`origin/main`；保护已有改动。
- 不为 Ability 复制 Adapter 业务逻辑；新增方法由直属 Ability 注册并经统一执行器调用。
- `docs/api_methods.md` 只能由 `gen_api_docs.sh` 生成。
- 临时文件只放 `tmp/`；不新增产品内临时工具 main。
- 后端闭环执行 test、race、vet、build；前端变更追加 Bun frozen install、lint、typecheck、test、build；运行链变更追加真实单二进制 HTTP/MCP/embed 冒烟。
- 完成后清理、提交、推送 main 并核对远端。发布仅用 `git_tag.sh`。
