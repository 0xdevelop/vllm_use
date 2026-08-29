> 本文件由 `gen_api_docs.sh` 从方法注册表生成（v0.0.1，共 31 个方法），禁止手改；新增方法后重新执行生成。

# 1. 统一调用方式

管理方法统一注册在 Supported Methods Registry，并由 APIExecuter 完成 scope 门禁后调用
Ability。MCP Adapter 使用 `tools/call → name → arguments`；资源化 HTTP Adapter
把 `/api/*` 路由映射为相同的方法名和 arguments。下面是 MCP 调用示例：

```json
{
  "jsonrpc": "2.0",
  "id": "request-id",
  "method": "tools/call",
  "params": {
    "name": "models.list",
    "arguments": {}
  }
}
```

MCP 已注册 tool 的业务结果统一返回 `CallToolResult`，并显式输出 `isError`；
协议损坏或未知 tool 使用 MCP/JSON-RPC 协议错误。HTTP Adapter 使用 HTTP 状态表达认证、
参数和业务错误，但仍执行同一个注册项。

**方法节怎么读**：每个方法节给出方法语义、`arguments` **传参举例**（必填字段的
实际请求形态，占位值按真实值替换）与 `arguments` **JSON Schema**（机器可校验的
约束说明——`required` 数组表示「哪些字段必填」，`properties` 内是各字段
类型与长度约束；**schema 本身不是请求体的一部分，不要照抄进请求**）。

# 2. 业务错误码

| error_code | error_msg |
| ---------- | --------- |
| 0 | 成功 |
| 10001 | method not found |
| 10002 | method is not supported |
| 10003 | invalid arguments |
| 10004 | permission denied |

# 3. test

检查统一 API 调用链是否可用

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "type": "object"
}
```

# 4. Models

## 4.1. models.list

列出已注册模型

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

## 4.2. models.scan

扫描模型目录

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

## 4.3. models.get

读取模型

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

## 4.4. models.register_huggingface

注册 Hugging Face 模型

`arguments` 传参举例（仅含必填字段）：

```json
{
  "repository": "<repository>"
}
```

可选字段：`revision`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "repository": {
      "type": "string"
    },
    "revision": {
      "type": "string"
    }
  },
  "required": [
    "repository"
  ],
  "type": "object"
}
```

## 4.5. models.register_local

注册本地模型

`arguments` 传参举例（仅含必填字段）：

```json
{
  "name": "<name>",
  "path": "<path>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "name": {
      "type": "string"
    },
    "path": {
      "type": "string"
    }
  },
  "required": [
    "name",
    "path"
  ],
  "type": "object"
}
```

## 4.6. models.delete

删除模型

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

可选字段：`files`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "files": {
      "type": "boolean"
    },
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

# 5. Gpu

## 5.1. gpu.list

列出 NVIDIA GPU 状态

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

# 6. Downloads

## 6.1. downloads.list

列出下载任务

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

## 6.2. downloads.start

启动模型下载

异步方法：受理后返回 `task_id`，进度与结果经任务查询方法读取。

`arguments` 传参举例（仅含必填字段）：

```json
{}
```

可选字段：`destination`、`id`、`model_id`、`repository`、`revision`、`token`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "destination": {
      "type": "string"
    },
    "id": {
      "type": "string"
    },
    "model_id": {
      "type": "string"
    },
    "repository": {
      "type": "string"
    },
    "revision": {
      "type": "string"
    },
    "token": {
      "type": "string"
    }
  },
  "type": "object"
}
```

## 6.3. downloads.status

读取下载状态

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

## 6.4. downloads.logs

读取下载日志

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

## 6.5. downloads.cancel

取消下载

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

## 6.6. downloads.retry

重试下载

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

可选字段：`token`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "id": {
      "type": "string"
    },
    "token": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

# 7. Runtime

## 7.1. runtime.status

读取 vLLM Runtime 状态

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

## 7.2. runtime.start

启动 vLLM Runtime

`arguments` 传参举例（仅含必填字段）：

```json
{
  "options": "<options>"
}
```

可选字段：`health_url`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "health_url": {
      "type": "string"
    },
    "options": {
      "type": "object"
    }
  },
  "required": [
    "options"
  ],
  "type": "object"
}
```

## 7.3. runtime.restart

重启 vLLM Runtime

`arguments` 传参举例（仅含必填字段）：

```json
{
  "options": "<options>"
}
```

可选字段：`health_url`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "health_url": {
      "type": "string"
    },
    "options": {
      "type": "object"
    }
  },
  "required": [
    "options"
  ],
  "type": "object"
}
```

## 7.4. runtime.switch

切换活动模型

`arguments` 传参举例（仅含必填字段）：

```json
{
  "model_id": "<model_id>",
  "options": "<options>"
}
```

可选字段：`health_url`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "health_url": {
      "type": "string"
    },
    "model_id": {
      "type": "string"
    },
    "options": {
      "type": "object"
    }
  },
  "required": [
    "model_id",
    "options"
  ],
  "type": "object"
}
```

## 7.5. runtime.stop

停止 vLLM Runtime

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

# 8. Api_keys

## 8.1. api_keys.create

创建 API Key

`arguments` 传参举例（仅含必填字段）：

```json
{
  "scopes": "<scopes>"
}
```

可选字段：`name`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "name": {
      "type": "string"
    },
    "scopes": {
      "items": {
        "type": "string"
      },
      "type": "array"
    }
  },
  "required": [
    "scopes"
  ],
  "type": "object"
}
```

## 8.2. api_keys.list

列出 API Key

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

## 8.3. api_keys.enable

启用 API Key

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

## 8.4. api_keys.disable

禁用 API Key

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

## 8.5. api_keys.delete

删除 API Key

`arguments` 传参举例（仅含必填字段）：

```json
{
  "id": "<id>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "id": {
      "type": "string"
    }
  },
  "required": [
    "id"
  ],
  "type": "object"
}
```

# 9. Settings

## 9.1. settings.list

读取设置

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

## 9.2. settings.update

更新非敏感设置（凭据必须通过环境变量或 CLI flags 提供）

`arguments` 传参举例（仅含必填字段）：

```json
{
  "settings": "<settings>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "settings": {
      "items": {
        "additionalProperties": false,
        "properties": {
          "key": {
            "maxLength": 128,
            "minLength": 1,
            "type": "string"
          },
          "value": {
            "maxLength": 65536,
            "type": "string"
          }
        },
        "required": [
          "key",
          "value"
        ],
        "type": "object"
      },
      "type": "array"
    }
  },
  "required": [
    "settings"
  ],
  "type": "object"
}
```

## 9.3. settings.delete

删除非敏感设置

`arguments` 传参举例（仅含必填字段）：

```json
{
  "key": "<key>"
}
```

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "key": {
      "maxLength": 128,
      "minLength": 1,
      "type": "string"
    }
  },
  "required": [
    "key"
  ],
  "type": "object"
}
```

# 10. Requests

## 10.1. requests.recent

读取最近请求

`arguments` 传参举例（仅含必填字段）：

```json
{}
```

可选字段：`limit`。

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {
    "limit": {
      "maximum": 500,
      "minimum": 1,
      "type": "integer"
    }
  },
  "type": "object"
}
```

# 11. System

## 11.1. system.get

读取系统信息

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

# 12. Mcp

## 12.1. mcp.status

读取 MCP 状态

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```

# 13. Dashboard

## 13.1. dashboard.get

读取管理面板摘要

`arguments` JSON Schema（约束说明，非请求体；`required` 数组 = 必填字段清单）：

```json
{
  "additionalProperties": false,
  "properties": {},
  "type": "object"
}
```
