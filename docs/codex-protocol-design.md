# ModelGate Codex 协议族（Responses API & Text Completions）支持设计方案

> 文档状态：设计草案 v0.2（细化稿）
> 适用范围：ModelGate 网关（`internal/gateway`）
> 内部规范协议：OpenAI ChatCompletions（`POST /v1/chat/completions`）

| 版本 | 日期 | 变更说明 |
| :--- | :--- | :--- |
| v0.1 | 2026-08 | 初稿：明确两个协议族及总体架构 |
| v0.2 | 2026-08 | 细化字段级转译、流式事件生命周期、配置扩展、错误映射、测试与实施计划 |

---

## 1. 背景与目标

Modelgate 网关目前支持两大标准 API 协议：
1. **OpenAI ChatCompletions 协议** (`/v1/chat/completions`)
2. **Anthropic Messages 协议** (`/v1/messages`)

随着 Codex CLI、OpenCode 以及各类 IDE 插件（如 Cursor、VS Code Continue、Copilot 兼容层）的普及，用户期望 ModelGate 网关能够原生支持 **Codex 协议族**。

Codex 协议族主要包含两大核心接口形态：
1. **OpenAI Responses API** (`POST /v1/responses`)：OpenAI 推出的新一代面向 Code Agent & CLI 工具的原生协议（Codex CLI / OpenCode 优先使用）。
2. **OpenAI Text Completions / FIM API** (`POST /v1/completions` 及 `/v1/engines/:engine/completions`)：用于 IDE 内联代码补全（Fill-In-The-Middle，光标前后代码填空）。

### 1.1 目标

在 ModelGate 网关的现有架构体系中，**扩展对 `/v1/responses` 与 `/v1/completions` 的原生支持**，实现与现有能力的无缝集成：

- 复用现有用户认证、API Key 管理、配额计费、负载均衡、健康检查、并发控制体系；
- 复用 `proxy.Protocol` 适配器架构与 `HandleProxyRequest` / `ExecuteCoreWorkflow` 核心工作流，不做核心代理层的侵入式改造；
- 复用 Raw Traffic Dump 四阶段诊断体系与访问日志中间件；
- 保持内部规范协议为 ChatCompletions，所有后端只与一种协议对接。

### 1.2 范围界定

**本期纳入范围：**

- `/v1/responses`：非流式与流式文本生成、工具调用（function calling）、推理内容（reasoning）透传、usage 统计。
- `/v1/completions` 与 `/v1/engines/:engine/completions`：非流式与流式文本补全、FIM 双模式转换、代码纯净过滤。

**本期明确不做（返回明确错误或忽略）：**

- Responses 的 `previous_response_id` 服务端状态会话（网关无状态，由客户端携带完整上下文）；
- `input_file` / 内置工具 `web_search`、`file_search`、`computer_use`、`local_shell` 的语义执行（可透传函数声明，但不代理其执行语义）；
- Text Completions 的多 `prompt` 数组、`n > 1`、`best_of`、`logit_bias` 等遗留参数；
- Responses 的 `text.format` 结构化输出仅做 `response_format` 映射，不承诺 JSON Schema 校验。

---

## 2. 协议规范与对比

### 2.1 四协议总览

| 维度 | OpenAI Chat 协议 | Anthropic 协议 | Codex Responses 协议 | Codex Text Completions 协议 |
| :--- | :--- | :--- | :--- | :--- |
| **标准接口** | `POST /v1/chat/completions` | `POST /v1/messages` | `POST /v1/responses` | `POST /v1/completions`<br/>`POST /v1/engines/:engine/completions` |
| **核心数据结构** | `messages: [{role, content}]` | `system` + `messages` | `instructions` + `input` | `prompt` + `suffix` |
| **主要应用场景** | Chat / WebUI / Agent | Claude Code / SDK | Codex CLI / OpenCode | IDE 内联代码自动补全 (FIM) |
| **响应格式 (`object`)** | `chat.completion` | `message` | `response` | `text_completion` |
| **系统指令位置** | `messages[0]` | 顶层 `system` | 顶层 `instructions` | 无（或注入 System Prompt） |
| **工具调用形态** | assistant 消息内 `tool_calls` | `tool_use` / `tool_result` 块 | 独立 `function_call` / `function_call_output` 输出项 | 不支持 |
| **推理内容形态** | `delta.reasoning_content` | `thinking` 块 | `reasoning` 输出项（summary） | 不支持 |
| **会话连续性** | 客户端全量上下文 | 客户端全量上下文 | 支持 `previous_response_id`（网关暂不支持） | 无状态 |
| **流式 SSE 规范** | 单行 `data: {...}` | 命名事件 (`event: content_block_delta`) | 命名事件 (`event: response.output_text.delta`) | 单行 `data: {"choices":[{"text":...}]}` |
| **Token 统计** | `usage`（prompt/completion） | `usage`（input/output） | `usage`（input/output + 明细） | `usage`（prompt/completion） |
| **错误格式** | `error: {type, message}` | `error: {type, message}` | `error: {type, message, code}` | 同 Chat 协议 |

### 2.2 客户端生态矩阵

| 客户端 | 默认协议 | ModelGate 接入方式 | 说明 |
| :--- | :--- | :--- | :--- |
| Codex CLI | Responses | `/v1/responses` | `config.toml` 中 `wire_api = "responses"` |
| OpenCode | Chat（可配 Responses） | `/v1/responses` 或 `/v1/chat/completions` | 新版支持 `wire_api = "responses"` |
| Cursor | Chat + Completions | `/v1/chat/completions` + `/v1/completions` | FIM 走 Text Completions 端点 |
| VS Code Continue | Chat + Completions | `/v1/chat/completions` + `/v1/completions` | Autocomplete 走 `useLegacyCompletionsEndpoint` |
| Copilot 兼容层 | Chat | `/v1/chat/completions` | 网关已支持 |

### 2.3 与既有协议的三个关键差异

1. **Responses 的输入是"条目"而非"消息"**：`input` 数组中的 `message` / `function_call` / `function_call_output` 需要各自归一化到 Chat 的消息形态，且工具 ID 前缀体系不同（`fc_` vs `call_`）。
2. **Responses 的流式是"命名事件 + 完整生命周期"**：客户端依赖 `response.created` → `response.output_item.added` → `response.output_text.delta` → `response.output_item.done` → `response.completed` 的事件序列解析一轮对话，转换器必须合成完整生命周期，不能只转发文本增量。
3. **Text Completions 是"纯文本"通道**：没有角色、没有工具，只有 `prompt`/`suffix` 与 `choices[].text`，需要为后端模型选择合适的 FIM 拼接方式。

---

## 3. 总体架构设计

ModelGate 采用 **`proxy.Protocol` 适配器架构**，将后端的内部统一协议收敛在 OpenAI `ChatCompletions` (`/v1/chat/completions`) 上，外部多协议通过 Gateway 适配层进行双向转换：

```
                              ┌──────────────────────────────────────────────┐
                              │                 Client Layer                 │
                              │  Codex CLI / OpenCode / IDE 插件 / SDK       │
                              └──────┬───────────────────────────────┬───────┘
                                     │ POST /v1/responses            │ POST /v1/completions
                                     ▼                               ▼
                              ┌──────────────┐                ┌──────────────┐
                              │  responses   │                │    codex     │
                              │   Gateway    │                │   Gateway    │
                              │ (Protocol +  │                │ (Protocol +  │
                              │  Converter)  │                │  Converter)  │
                              └──────┬───────┘                └──────┬───────┘
                                     │ (Implements proxy.Protocol)   │ (Implements proxy.Protocol)
                                     └───────────────┬───────────────┘
                                                     ▼
                              ┌──────────────────────────────────────────────┐
                              │   ModelGate Core Proxy & LB                  │
                              │   (internal/gateway/proxy)                   │
                              │   - 认证/配额/并发/负载均衡/健康检查           │
                              │   - 参数注入 / 上下文裁剪 / Token 估算         │
                              │   - Raw Traffic Dump 四阶段诊断               │
                              └──────────────────────┬───────────────────────┘
                                                     │ POST /v1/chat/completions
                                                     ▼
                              ┌──────────────────────────────────────────────┐
                              │   Backend LLM Provider Pool                  │
                              │ (Qwen-Coder/DeepSeek/GPT/Claude/GLM 等)       │
                              └──────────────────────────────────────────────┘
```

### 3.1 请求流转时序（以 Responses 非流式为例）

```
Client ── POST /v1/responses ──► Router
   │                              │
   │                              ├─ ProtocolInjectionMiddleware（注入 responses.Protocol）
   │                              ├─ ClientFilterMiddleware（UA 过滤）
   │                              ├─ ProxyAuthMiddleware（API Key / JWT）
   │                              ├─ ConcurrencyLimitMiddleware（全局并发）
   │                              ├─ AccessLogMiddleware（请求体捕获）
   │                              ▼
   │                     responses.Handler.HandleResponses
   │                              │ 1. 解析 ResponsesRequest
   │                              │ 2. 转译为 OpenAI Chat 请求体（Converter）
   │                              ▼
   │                     proxy.HandleProxyRequest / ExecuteCoreWorkflow
   │                              │ 3. 认证 + 配额检查
   │                              │ 4. 负载均衡选择后端（并发许可）
   │                              │ 5. 参数注入、max_tokens 裁剪、发送请求
   │                              │ 6. 后端返回 → Protocol.FormatResponse 转译为 Response
   │                              ▼
   └────────── JSON Response ◄────┘
```

---

## 4. 详细设计与转译机制

### 4.1 通用转译基础设施（两个适配器共用）

#### 4.1.1 流式行处理

`proxy.Protocol.FormatStreamLine(line, state)` 的输入是后端 SSE 的**单行文本**（可能是 `data: {...}\n`，也可能因为后端粘包而包含多个事件），输出是**零到多个**客户端事件文本。适配器必须：

- 兼容 `data: {...}` 与 `data:{...}` 两种前缀（复用 `proxy.ParseOpenAISSE` 的解析方式）；
- 处理 `[DONE]` 终止行；
- 通过 `state map[string]interface{}` 保存跨行的状态（如输出项 ID、是否已发 done 事件、代码围栏缓冲）；
- 返回 `contentText`（本次增量文本），供核心代理层在缺失 usage 时回退估算输出 Token。

#### 4.1.2 ID 与时间戳

- Responses 的 `response.id` 使用 `resp_` 前缀，输出项 ID 使用 `msg_` / `fc_` / `rs_` 前缀，由适配器在流开始时生成并缓存在 `state` 中；
- `created_at` 使用 Unix 秒级时间戳；
- Text Completions 的 `id` 使用 `cmpl-` 前缀（沿用 OpenAI 习惯）。

#### 4.1.3 Usage 提取与 Token 回退

- **非流式**：优先解析后端 `usage.prompt_tokens` / `usage.completion_tokens`（`FormatResponse` 返回值）；缺失时输入用转换后请求的本地估算，输出用转换后响应体估算（复用 `utils.EstimateTokens`）。
- **流式**：`FormatStreamLine` 返回值中携带 `preciseInputTokens` / `preciseOutputTokens`；缺失时核心代理层用累加的 `contentText` 估算。
- Responses 的 usage 需要**二次包装**：`input_tokens` / `output_tokens` / `total_tokens`，并尽量附带 `input_tokens_details.cached_tokens` 与 `output_tokens_details.reasoning_tokens`（后端不返回明细时省略）。

---

### 4.2 OpenAI Responses API 适配 (`POST /v1/responses`)

#### 4.2.1 端点与鉴权

- 路由：`POST /v1/responses`
- 中间件链与既有 OpenAI/Anthropic 网关一致（见 §7.1）；
- `extract` 函数：解析请求体 → 返回 `(modelID, isStream, openaiBody, err)`；`model` 缺失时返回 `invalid_request_error`。

#### 4.2.2 请求模型与字段支持策略

Responses 请求字段繁多，按四级策略处理：**直转（pass-through）**、**转换（convert）**、**忽略（ignore）**、**拒绝（reject）**。

| 字段 | 类型 | 策略 | 处理说明 |
| :--- | :--- | :--- | :--- |
| `model` | string | 直转 | 作为负载均衡选择键 |
| `instructions` | string | 转换 | 归一化为 `system` 消息 |
| `input` | string \| array | 转换 | 见 §4.2.3 输入项映射 |
| `max_output_tokens` | int | 转换 | 映射为 `max_tokens`（默认）或 `max_completion_tokens`（模型级配置，见 §5） |
| `temperature` / `top_p` | number | 直转 | 同名透传 |
| `stream` | bool | 直转 | 控制流式/非流式 |
| `tools` | array | 转换 | 扁平结构 → Chat `function` 包裹形式（兼容两种写法） |
| `tool_choice` | string/object | 转换 | `auto`/`none`/`required` 直传；对象形式规范化为 `{type, function:{name}}` |
| `parallel_tool_calls` | bool | 直转 | 同名透传 |
| `reasoning.effort` | string | 转换 | 展开为 Chat 顶层 `reasoning_effort`；`summary` 忽略 |
| `text.format` | object | 转换 | `json_object` / `json_schema` → `response_format`；`text` 忽略 |
| `store` / `previous_response_id` | - | 忽略 | 网关无状态；原始请求仍完整记录于访问日志 |
| `metadata` / `user` / `prompt_cache_key` | - | 忽略 | 可记入审计日志 |
| `truncation` / `include` | - | 忽略 | 仅影响官方内置能力，网关不消费 |
| `input` 中的不支持条目 | - | 拒绝 | `input_file`、`web_search_call` 等返回 400 |

#### 4.2.3 请求转译（Responses → ChatCompletions）

**核心思路**：将 Responses 的"指令 + 条目流"归一化为 Chat 的"系统消息 + 消息流"，确保转换后的请求体可以直接进入现有的 `OpenAIRequestHeader` 解析、Token 估算、`max_tokens` 裁剪管线。

```jsonc
// 客户端请求（Responses 格式）
{
  "model": "qwen3.5-coder",
  "instructions": "You are a coding agent.",
  "input": [
    {"type": "message", "role": "user",
     "content": [{"type": "input_text", "text": "Fix the bug in this file"}]}
  ],
  "max_output_tokens": 4096,
  "tools": [{"type": "function", "name": "shell", "description": "run a command",
             "parameters": {"type": "object", "properties": {}}}],
  "stream": false
}
```

```jsonc
// 网关转换后发给后端的请求（Chat 格式）
{
  "model": "qwen3.5-coder",
  "messages": [
    {"role": "system", "content": "You are a coding agent."},
    {"role": "user", "content": "Fix the bug in this file"}
  ],
  "max_tokens": 4096,
  "tools": [{"type": "function",
             "function": {"name": "shell", "description": "run a command",
                          "parameters": {"type": "object", "properties": {}}}}],
  "stream": false
}
```

**`input` 条目映射表：**

| Responses `input` 条目 | 转换后的 Chat 消息 |
| :--- | :--- |
| `message`（role: user/assistant/system/developer） | 同名 role 消息；`content` 为字符串或内容块数组 |
| `message` 内容块 `input_text` / `output_text` | 文本内容 |
| `message` 内容块 `input_image` | `image_url` 多模态内容块（data URL 原样透传） |
| `message` 内容块 `input_file` | 400 `invalid_request_error`（本期不支持文件输入） |
| `function_call`（name + arguments + id） | assistant 消息的 `tool_calls` 数组 |
| `function_call_output`（call_id + output） | `role: "tool"` 消息（`tool_call_id` = call_id，`content` = output） |
| `reasoning` | 忽略（推理条目不参与后端输入） |
| `computer_call` / `web_search_call` / `local_shell_call` | 400 `invalid_request_error` |

**工具 ID 前缀策略：**

- 请求方向（Responses → Chat）：`fc_*` 保留原样（后端普遍接受任意 tool_call_id）；
- 响应方向（Chat → Responses）：后端返回的 `call_*` / `toolu_*` / 任意 ID 统一保留为 `call_id`，同时为输出项生成 `fc_<hex>` 形式的 `item.id`，并在 `state` 中记录 `call_id ↔ item_id` 映射，供流式事件引用。

**`max_output_tokens` 映射策略：**

- 默认映射为 `max_tokens`（兼容 Qwen / DeepSeek / GLM 等绝大多数 OpenAI 兼容后端）；
- 模型配置 `responses.max_output_tokens_field: "max_completion_tokens"` 时映射为 `max_completion_tokens`（适用于仅接受该字段的模型）；
- 值小于 16 或为负时交由现有 `adjustMaxTokens` 逻辑兜底裁剪。

#### 4.2.4 非流式响应转译（ChatCompletions → Responses）

后端返回 `choices[].message.content` 与 `usage`，网关包装为：

```jsonc
// 网关返回给客户端的响应（Responses 格式）
{
  "id": "resp_01JXXXX",
  "object": "response",
  "created_at": 1755100000,
  "status": "completed",
  "model": "qwen3.5-coder",
  "output": [
    {
      "type": "message",
      "id": "msg_01JXXXX",
      "status": "completed",
      "role": "assistant",
      "content": [
        {"type": "output_text", "text": "Fixed. The bug was in `parse()`.",
         "annotations": []}
      ]
    }
  ],
  "output_text": "Fixed. The bug was in `parse()`.",
  "usage": {
    "input_tokens": 125,
    "output_tokens": 24,
    "total_tokens": 149
  }
}
```

要点：

- `status` 按后端完成情况取 `completed` / `failed` / `incomplete`；后端非 200 时由 `BuildErrorResponse` 走错误通道；
- 后端存在 `message.tool_calls` 时，输出项拆分为多个 `function_call` 条目（`type: "function_call"`, `name`, `arguments`，`call_id` 与 `id` 见 §4.2.3）；
- 后端存在 `message.reasoning_content` 时，额外输出 `reasoning` 条目（`type: "reasoning"`, `summary`）；
- `output_text` 为所有 `output_text` 文本的拼接（官方便捷字段，客户端依赖它拿纯文本）；
- `usage` 保留 `input_tokens` / `output_tokens` / `total_tokens`，可追加 `input_tokens_details` / `output_tokens_details`。

#### 4.2.5 流式响应转译（ChatCompletions SSE → Responses SSE）

Responses 流式协议要求**完整生命周期事件**。转换器内部维护一个输出项状态机，把后端的文本/推理/工具增量映射为命名事件：

```
后端 Chat SSE 增量                      客户端 Responses SSE 事件
─────────────────────                  ─────────────────────────────
data: {"choices":[]}                   event: response.created
（首个 chunk）                          event: response.in_progress
                                       event: response.output_item.added  (message)
                                       event: response.content_part.added (output_text)
data: {"delta":{"content":"Fixed"}}    event: response.output_text.delta
data: {"delta":{"reasoning_content":..}} event: response.reasoning_summary_text.delta
data: {"delta":{"tool_calls":[{...}]}} event: response.output_item.added  (function_call)
                                       event: response.function_call_arguments.delta
data: {"finish_reason":"stop"}         event: response.output_text.done
                                       event: response.content_part.done
                                       event: response.output_item.done
                                       event: response.completed
data: [DONE]                           （兜底补齐缺失的 done/completed）
```

**事件与数据格式：**

```text
event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_01JXXXX",
       "output_index":0,"content_index":0,"delta":"Fixed"}
```

**生命周期状态机（`state` 中持久化）：**

| 状态键 | 含义 |
| :--- | :--- |
| `response_id` | 本次流式响应的 `resp_*` ID |
| `item_id` | 当前 message 输出项 ID（`msg_*`） |
| `content_part_id` | 当前文本块 ID |
| `item_added` / `content_added` / `done_emitted` | 事件是否已发送（保证每个事件恰好一次） |
| `tool_items` | map[index] → function_call 输出项（含 `fc_*` ID、`call_id`） |
| `reasoning_items` | map[index] → reasoning 输出项 |
| `fence_buf` | 代码围栏过滤缓冲（Text Completions 使用） |

**关键规则：**

1. **首包触发**：读到后端第一个 chunk（含 `choices` 数组）时，依次发送 `response.created`、`response.in_progress`、`response.output_item.added`、`response.content_part.added`；`response.created` 的 `data` 中携带完整 `response` 对象（`status: "in_progress"`、`output: []`）。
2. **文本增量**：`delta.content` → `response.output_text.delta`，`delta` 字段为纯增量文本，不得拼接历史。
3. **推理增量**：`delta.reasoning_content` → `response.reasoning_summary_text.delta`（默认；模型配置可切换为 `reasoning_text` 事件族）。首个推理增量前先发 `response.output_item.added`（`type: "reasoning"`）。
4. **工具增量**：按 `delta.tool_calls[].index` 聚合；每个 index 首次出现时发送 `response.output_item.added`（`type: "function_call"`），随后 `arguments` 增量经 `response.function_call_arguments.delta` 转发；`finish_reason: "tool_calls"` 时补齐该条目的 `response.function_call_arguments.done` 与 `response.output_item.done`。
5. **收尾**：`finish_reason` 到达时发送文本块的 done 事件链（`output_text.done` → `content_part.done` → `output_item.done`）与 `response.completed`（`status: "completed"`，携带最终 response 对象与 usage）；`[DONE]` 行到达时若未发完成事件则兜底补齐。
6. **流中错误**：后端流中出错（非 200 或 JSON 解析失败）时发送 `response.failed`（`status: "failed"`、`error` 字段）与 `error` 事件，并结束流。
7. **SDK 模式兼容**：OpenAI 官方 SDK 解析 `data:` 行中的 `type` 字段，忽略 `event:` 行；因此网关始终输出 `event:` + `data:` 双行格式即可同时兼容原始 SSE 客户端与 SDK 客户端。
8. **心跳**：`PingMessage()` 返回 SSE 注释 `: ping\n\n`（注释行对两类客户端均无副作用）；首包 `response.created` 之前允许存在注释行。

#### 4.2.6 工具调用与推理内容

- 请求中携带 `function_call` / `function_call_output` 历史条目时，按 §4.2.3 还原为 assistant `tool_calls` + tool 消息，保证多轮 Agent 循环的上下文完整；
- 后端返回 `tool_calls` 后，`finish_reason` 映射为 `tool_calls`，客户端据此执行工具并把结果以 `function_call_output` 回传；
- 推理内容（`reasoning_content`）默认以 `reasoning` 输出项 + `response.reasoning_summary_text.*` 事件呈现；对只支持明文思考的 DeepSeek 等后端，注意**不要把推理文本混入 `output_text`**（避免污染补全结果与 Token 统计）。

#### 4.2.7 兼容性边界

- `previous_response_id`：忽略并返回**全新上下文**；文档与接入 FAQ 中说明客户端应使用全量 `input`；
- `store: true`：忽略（网关不落盘对话）；
- `text.format: {type: "json_schema", schema}`：映射 `response_format`；后端不支持时静默降级为普通文本并记 warning 日志；
- `include: ["reasoning.encrypted_content"]`：忽略（本期不输出加密推理）；
- 未知顶层字段：忽略，不回传错误（保证 SDK 兼容性）。

---

### 4.3 Codex Text Completions & FIM 适配 (`POST /v1/completions`)

#### 4.3.1 端点

- `POST /v1/completions`
- `POST /v1/engines/:engine/completions`（`engine` 路径参数在 body 缺失 `model` 时作为模型 ID）

#### 4.3.2 请求模型与字段支持策略

| 字段 | 类型 | 策略 | 处理说明 |
| :--- | :--- | :--- | :--- |
| `model` | string | 直转 | 缺失时取 `:engine` 路径参数 |
| `prompt` | string | 转换 | 单条字符串；数组返回 400 |
| `suffix` | string | 转换 | 存在即进入 FIM 模式（见 §4.3.3） |
| `max_tokens` | int | 直转 | 透传并参与上下文裁剪 |
| `temperature` / `top_p` / `stop` | - | 直转 | 同名透传 |
| `presence_penalty` / `frequency_penalty` | number | 直转 | 同名透传 |
| `stream` | bool | 直转 | 控制流式/非流式 |
| `echo` | bool | 转换 | 非流式/流式首包在文本前拼接 `prompt` |
| `n` / `best_of` | int | 拒绝/忽略 | `n > 1` 拒绝；`best_of > 1` 拒绝（与 `n > 1` 一致，返回 400）；`best_of == 1` 忽略 |
| `logprobs` | int | 忽略 | 响应中输出 `"logprobs": null` |
| `logit_bias` | object | 忽略 | 记 warning 日志 |
| `user` | - | 忽略 | 可记入审计日志 |

#### 4.3.3 FIM 双模式转换（核心场景：Fill-In-The-Middle）

请求包含 `prompt`（光标前代码）和可选的 `suffix`（光标后代码）。网关根据后端模型类型采取双模式：

**模式 A：代码专用大模型（原生 FIM 支持）**

```jsonc
// 转换后发给后端的请求
{
  "model": "qwen3.5-coder",
  "messages": [
    {"role": "user",
     "content": "<|fim_prefix|>" + prompt + "<|fim_suffix|>" + suffix + "<|fim_middle|>"}
  ],
  "stream": false
}
```

**模式 B：通用大模型（System Prompt 约束）**

```jsonc
{
  "model": "glm4.7",
  "messages": [
    {"role": "system",
     "content": "You are an inline code completion engine. Complete the code between <PREFIX> and <SUFFIX>. Output ONLY the missing code at the cursor position. Do NOT output markdown formatting like ```python, do NOT output explanations."},
    {"role": "user",
     "content": "<PREFIX>\n" + prompt + "\n</PREFIX>\n<SUFFIX>\n" + suffix + "\n</SUFFIX>"}
  ],
  "stream": false
}
```

**FIM 占位符对照表（默认值，均可通过模型配置覆盖）：**

| 模型家族 | prefix | suffix | middle | 判定关键字 |
| :--- | :--- | :--- | :--- | :--- |
| Qwen2.5-Coder | `<|fim_prefix|>` | `<|fim_suffix|>` | `<|fim_middle|>` | `qwen` + `coder` |
| DeepSeek-Coder | `<｜fim▁begin｜>` | `<｜fim▁end｜>` | `<｜fim▁hole｜>` | `deepseek` + `coder` |
| StarCoder | `<fim_prefix>` | `<fim_suffix>` | `<fim_middle>` | `starcoder` |
| CodeLlama | `▁<PRE>` | `<SUF>` | `<MID>` | `codellama` / `code-llama` |
| 通用模型 | - | - | - | 其余（走模式 B） |

**模式选择规则（`fim.mode`，见 §5）：**

- FIM 仅在 `fim.enabled: true` 时生效（缺省 false）；未启用时 `suffix` 忽略，退化为普通前缀补全；
- `auto`（enabled 后的默认模式）：`suffix` 非空且模型 ID 命中代码模型关键字 → 模式 A；否则 → 模式 B；
- `native`：强制模式 A（无论是否命中关键字）；
- `prompt`：强制模式 B；
- `disabled`：不启用 FIM，`suffix` 忽略，`prompt` 直接作为单条 user 消息。

**边界处理：**

- `suffix` 为空字符串：退化为普通前缀补全（仅 `prompt`，无 FIM 包装）；
- `suffix` 为 `\n` 等纯空白：视为空后缀，避免生成多余空行；
- 请求中同时出现 `prompt` 数组：400 `invalid_request_error`（本期仅支持单 prompt）。

#### 4.3.4 响应转译（ChatCompletions → Text Completions）

**非流式：**

```jsonc
{
  "id": "cmpl-01JXXXX",
  "object": "text_completion",
  "created": 1755100000,
  "model": "qwen3.5-coder",
  "choices": [
    {
      "text": "    return a + b",
      "index": 0,
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 42, "completion_tokens": 7, "total_tokens": 49}
}
```

- `choices[0].message.content` → `choices[0].text`（经代码纯净过滤器）；
- `finish_reason` 映射：`stop`→`stop`、`length`→`length`、`tool_calls`→`stop`（补全场景不应出现工具调用，出现则兜底）；
- `usage` 原样保留 `prompt_tokens` / `completion_tokens` / `total_tokens`。

**流式：**

```text
data: {"id":"cmpl-...","object":"text_completion","choices":[{"text":"    return ","index":0,"finish_reason":null}]}
data: {"id":"cmpl-...","object":"text_completion","choices":[{"text":"a + b","index":0,"finish_reason":null}]}
data: {"id":"cmpl-...","object":"text_completion","choices":[{"text":"","index":0,"finish_reason":"stop"}],"usage":{...}}
data: [DONE]
```

- `delta.content` → `choices[0].text`，逐包转发；
- 首包前需要发一条空文本占位行（`choices:[{text:"", finish_reason:null}]`）以建立流（与 OpenAI 行为一致）；
- 终止 chunk 携带 `finish_reason` 与 usage；`[DONE]` 原样透传；
- 缺失 usage 时核心代理层按累加文本估算输出 Token。

#### 4.3.5 代码纯净过滤器（Clean Code Filter）

针对代码补全场景，自动识别并过滤输出首尾的 Markdown Codeblock 标记，确保插入 IDE 的代码不引发语法错误：

- **非流式**：去除首部 ```` ```lang ```` 与尾部 ```` ``` ````（兼容 `~~~`），仅处理首尾边界，不触碰正文中的围栏；
- **流式**：使用 `fence_buf` 状态缓冲最近 3 个字符：
  - 文本流开头出现 ```` ``` ```` → 丢弃直到换行符；
  - 流结尾（`finish_reason`）出现尾部 ```` ``` ```` → 从最后一个 ```` ``` ```` 起截断；
  - 围栏跨 chunk 分片（如 `"``"` + ``"`"``）时通过缓冲拼接识别；
  - 正文中间的围栏（如代码内嵌 Markdown 示例）不处理。
- 默认**不裁剪首尾空白**（缩进/换行对补全语义有影响）；如需裁剪可通过模型配置开关（`fim.trim_whitespace`）。

#### 4.3.6 兼容性边界

- `prompt` 数组、`n > 1`、`best_of > 1`：返回 400，明确提示"Text Completions 网关仅支持单 prompt 单补全"；
- `logprobs`：不支持，恒返回 `null`（避免破坏主流 IDE 客户端）；
- 部分后端不支持 `suffix` 语义时（模式 B 后端拒绝 `suffix`），由网关在转换层完成 FIM 拼接，**后端永远只看到 Chat 格式**，不存在后缀泄漏。

---

### 4.4 错误处理与错误码映射

两个适配器均实现 `BuildErrorResponse(errType, message)`，核心代理层的错误统一映射为协议格式：

| 网关内部错误 | Responses 错误格式 | Completions 错误格式 | HTTP 状态 |
| :--- | :--- | :--- | :--- |
| 未认证 / 无效 Key | `authentication_error` | `authentication_error` | 401 |
| 请求体非法 / 模型缺失 / 参数不支持 | `invalid_request_error` | `invalid_request_error` | 400 |
| 配额 / 速率限制 | `rate_limit_error` | `rate_limit_error` | 429 |
| UA 被客户端过滤 | `permission_denied` | `permission_denied` | 403 |
| 后端不可用 / 网关内部错误 | `api_error` / `server_error` | `api_error` | 502/503/504 |

```jsonc
// Responses 错误响应
{"error": {"type": "invalid_request_error", "message": "...", "code": "invalid_parameter"}}

// Completions 错误响应（沿用 OpenAI 错误格式）
{"error": {"type": "invalid_request_error", "message": "...", "param": null, "code": null}}
```

流式请求中发生错误时，Responses 适配器发送 `response.failed` + `error` 事件后关闭流；Completions 适配器直接关闭流（客户端以 EOF 感知结束）。

---

## 5. 配置扩展

### 5.1 模型级配置

在 `config.yaml` 的 `models[]` 中新增两个可选字段（向后兼容，缺省时按默认行为运行）：

```yaml
models:
  - id: "qwen3.5-coder"
    name: "Qwen 3.5 Coder"
    enabled: true
    context_window: 32000
    fim:                                # Text Completions / FIM 行为（可选）
      enabled: true                     # 缺省 false（仅显式开启）
      mode: "auto"                      # auto | native | prompt | disabled
      prefix: "<|fim_prefix|>"          # 覆盖默认 FIM 占位符（可选）
      suffix: "<|fim_suffix|>"
      middle: "<|fim_middle|>"
      system_prompt: "..."              # 覆盖模式 B 的默认 System Prompt（可选）
      trim_whitespace: false            # 是否裁剪输出首尾空白（可选）
    responses:                          # Responses API 行为（可选）
      max_output_tokens_field: "max_tokens"   # max_tokens | max_completion_tokens
      reasoning_event: "summary"        # summary | text（推理增量事件族）
    backends:
      - id: "qwen3.5-coder-prod"
        base_url: "http://qwen-coder.internal:8000"
        model_name: "qwen3.5-coder-instruct"
        weight: 10
        enabled: true
```

设计要点：

- `fim.enabled` 单独开关，避免未配置时 IDE 流量意外进入 FIM 模式；
- `fim.mode` 与 `fim_tokens` 双轨：自动探测 + 显式覆盖，适配私有化部署的定制 tokenizer；
- `responses.max_output_tokens_field` 解决不同后端对 `max_tokens` / `max_completion_tokens` 的接受度差异；
- 新增配置由 `internal/config` 的模型结构体承载，热重载机制（`ReloadConfig`）天然生效。

### 5.2 客户端过滤规则

`internal/infra/utils/useragent.go` 增加 Codex CLI 识别规则：

```go
{keywords: []string{"codex"}, name: "Codex CLI",
 versionRe: regexp.MustCompile(`(?i)codex[/\s]?([0-9a-zA-Z.-]+)`)},
```

`config.yaml` 的 `client_filter.rules` 支持按需封禁 Codex CLI / OpenCode：

```yaml
client_filter:
  rules:
    - name: "Codex CLI"
      pattern: "codex"     # 默认不封禁（enabled: false），管理员按需开启
      enabled: false
```

---

## 6. 代码包与文件结构

```
internal/gateway/
├── responses/                 # [NEW] OpenAI Responses API 适配包
│   ├── handler.go             # HTTP 路由处理：POST /v1/responses
│   ├── models.go              # Requests/Responses 请求与响应模型（含内容块、输出项、usage、事件）
│   ├── converter.go           # Responses <-> ChatCompletions 双向转译器
│   ├── stream.go              # 流式生命周期状态机（SSE 事件合成）
│   ├── converter_test.go      # 请求/响应转换 + 事件生命周期单元测试
│   └── stream_test.go         # 流式边界（首包/收尾/错误/工具/推理）单元测试
├── codex/                     # [NEW] Codex Text Completions & FIM 适配包
│   ├── handler.go             # HTTP 路由处理：/v1/completions 与 /v1/engines/:engine/completions
│   ├── models.go              # Completions 请求/响应模型
│   ├── converter.go           # Completions <-> ChatCompletions 双向转译器与 FIM 拼接
│   ├── filter.go              # 代码纯净过滤器（非流式 + 流式状态机）
│   ├── converter_test.go      # FIM 转换、字段策略、usage 映射单元测试
│   └── filter_test.go         # 代码围栏剥离（含跨 chunk 分片）单元测试
└── proxy/                     # [EXISTING] 核心代理层（不改动）
    ├── protocol.go            # Protocol 接口（两个新包均实现）
    ├── handler.go             # HandleProxyRequest 泛化处理器
    ├── proxy.go               # ExecuteCoreWorkflow / 流式循环 / ParseOpenAISSE
    └── ...
```

### 6.1 Protocol 实现签名（与 anthropic 包同构）

```go
// responses.Protocol
type Protocol struct {
    ClientReq *ResponsesRequest // 保存原始请求，供响应转译使用
}

// 实现 proxy.Protocol：
//   FormatResponse(backendResp []byte) (clientResp []byte, in, out int, err error)
//   FormatStreamLine(line string, state map[string]interface{}) (string, int, int, string, error)
//   PingMessage() string
//   BuildErrorResponse(errType, message string) []byte
```

```go
// codex.Protocol
type Protocol struct {
    ClientReq *CompletionRequest
}

// 同样实现 proxy.Protocol 四个方法
```

Handler 侧与 `anthropic.Handler` 完全同构：

```go
func (h *Handler) HandleResponses(c *gin.Context) {
    var req ResponsesRequest
    h.proxy.HandleProxyRequest(c, &Protocol{ClientReq: &req},
        func(body []byte) (string, bool, []byte, error) {
            // 解析 → 校验 model → ConvertToOpenAI(&req)
            return req.Model, req.Stream, openaiBody, nil
        })
}
```

---

## 7. 中间件与系统集成

### 7.1 路由注册（`internal/server/server.go`）

在 `setupRoutes()` 中追加两个网关，中间件链与既有协议完全一致：

```go
// Responses 网关
responsesAuth := middleware.ProxyAuthMiddleware(s.apiKeyService, s.jwtManager, s.userStore)
responsesHandler := responses.NewHandler(s.proxyInstance, s.usageService)
responsesHandler.RegisterRoutes(r, responsesAuth, s.limiter,
    middleware.ClientFilterMiddleware(s.cfgManager))

// Codex Text Completions 网关
codexAuth := middleware.ProxyAuthMiddleware(s.apiKeyService, s.jwtManager, s.userStore)
codexHandler := codex.NewHandler(s.proxyInstance, s.usageService)
codexHandler.RegisterRoutes(r, codexAuth, s.limiter,
    middleware.ClientFilterMiddleware(s.cfgManager))
```

`RegisterRoutes` 内部结构（与 `anthropic` 一致）：

```go
v1 := r.Group("/v1")
v1.Use(middleware.ProtocolInjectionMiddleware(&Protocol{})) // 错误格式注入
v1.Use(clientFilter)                                        // UA 过滤
v1.Use(authMiddleware)                                      // API Key / JWT
{
    v1.POST("/responses", middleware.ConcurrencyLimitMiddleware(limiter),
        middleware.AccessLogMiddleware(usageService), h.HandleResponses)
}
```

### 7.2 认证 / 并发 / 客户端过滤

- `ProxyAuthMiddleware`：统一支持 Bearer API Key 与 JWT（含滑动过期刷新），错误格式由 `ProtocolInjectionMiddleware` 注入的协议适配器生成；
- `ConcurrencyLimitMiddleware`：全局并发限制，与既有接口共用；
- `ClientFilterMiddleware`：基于 UA 封禁规则，新增 Codex CLI 识别（§5.2）。

### 7.3 访问日志与审计（`internal/infra/middleware/access_log.go`）

扩展 `parseStreamResponse`，在现有 OpenAI Chat 分支、Anthropic 分支之外新增：

1. **Responses 分支**：解析 `data` JSON 中的 `type` 字段，提取 `response.output_text.delta` 的 `delta`、`response.function_call_arguments.delta` 的 `delta`、`response.reasoning_summary_text.delta` 的 `delta`，拼接为日志文本；
2. **Completions 分支**：`openaiStreamChoice` 增加 `text *string` 字段，提取 `choices[0].text`。

保证访问日志中的 Token 消耗量、响应内容记录完整准确；`log_payloads` 开关行为不变。

### 7.4 Raw Traffic Dump（四阶段诊断）

两个新适配器复用 `HandleProxyRequest` / `ExecuteCoreWorkflow` 内建的 Dump 逻辑，无需额外改造：

| 阶段 | 文件名 | 内容 |
| :--- | :--- | :--- |
| 1 | `1_client_request.json` | 客户端原始请求（Responses / Completions 格式） |
| 2 | `2_converted_request.json` | 转译后的 ChatCompletions 请求体 |
| 3 | `3_<status>_backend_response.txt` | 后端原始响应（含流式逐行） |
| 4 | `4_<status>_converted_response.txt` | 转译后的客户端响应 |

`raw_dumps: "error"` 时，仅在 400 及以上错误落盘，行为与既有协议一致。

### 7.5 配额与计费口径

- **输入 Token**：转换后的 Chat 请求体进入 `OpenAIRequestHeader.EstimateTokens()` 估算（Responses 的 `instructions` 已并入 system 消息、Completions 的 `prompt`/`suffix` 已并入 user 消息，天然覆盖）；后端返回 usage 时以精确值覆盖；
- **输出 Token**：非流式取 usage 或响应体估算；流式取 usage 或 `contentText` 累加估算（`fullCollectedText` 机制复用）；
- **TTFT / 延迟**：流式首 token 计时由核心代理层完成，无需适配器参与；
- **错误场景**：只记录日志、不扣减配额（`RecordErrorUsage` 路径），与既有协议一致。

### 7.6 模型参数注入与上下文裁剪

- `model_params` 注入、`context_window` 裁剪、thought_signature 缓存等均作用于转换后的 Chat 请求体，两个新协议自动获得全部既有能力；
- 转换器必须保证产物是**合法 Chat 请求**（`messages` 数组结构完整），否则 `adjustMaxTokens` / `EstimateTokens` 无法正常工作。

---

## 8. 验证与测试计划

### 8.1 单元测试矩阵

**Responses 包（`go test ./internal/gateway/responses/... -v`）：**

| 用例 | 断言 |
| :--- | :--- |
| `instructions` → system 消息 | 首条消息为 system |
| `input` 为字符串 | 归一化为单条 user 消息 |
| `input` 混合 message / function_call / function_call_output | 消息顺序与 role/tool_calls 映射正确 |
| `input_image` 内容块 | 转换为 image_url 多模态块 |
| `input_file` / `web_search_call` 条目 | 返回 400 `invalid_request_error` |
| tools 扁平结构（含 function 包裹兼容写法） | 统一转为 `function` 包裹 |
| `reasoning.effort` | 展开为 `reasoning_effort` |
| `text.format: json_schema` | 映射 `response_format` |
| `max_output_tokens` 映射 | 默认 `max_tokens`；配置后 `max_completion_tokens` |
| 非流式响应转换 | `object/status/output/output_text/usage` 结构完整 |
| 后端 tool_calls 响应 | 输出 `function_call` 条目、`fc_*` ID、call_id 保留 |
| 后端 reasoning_content 响应 | 输出 `reasoning` 条目 |

**Responses 流式（`stream_test.go`）：**

| 用例 | 断言 |
| :--- | :--- |
| 事件生命周期完整性 | created → in_progress → output_item.added → content_part.added → output_text.delta → output_text.done → content_part.done → output_item.done → completed |
| 每个事件恰好一次 | `state` 去重标记生效 |
| 推理增量 | `response.reasoning_summary_text.delta` 事件族 |
| 工具增量 | 按 index 聚合，arguments 增量经 function_call_arguments.delta |
| finish_reason=tool_calls | 工具条目 done 链 + completed |
| `[DONE]` 兜底 | 缺失的 done/completed 被补齐 |
| 流中错误 | `response.failed` + `error` 事件 |
| 粘包行 | 一行含多事件时逐个转换 |
| SDK 模式兼容 | `data:` 行均含 `type` 字段 |

**Codex 包（`go test ./internal/gateway/codex/... -v`）：**

| 用例 | 断言 |
| :--- | :--- |
| FIM 模式 A（native 后端） | `<fim_prefix>` + prompt + `<fim_suffix>` + suffix + `<fim_middle>` |
| FIM 模式 B（通用后端） | system 约束 + `<PREFIX>/<SUFFIX>` 包装 |
| `fim.mode` 覆盖 | auto/native/prompt/disabled 四分支 |
| 空 suffix / 纯空白 suffix | 退化为纯前缀补全 |
| prompt 数组 / n>1 / best_of>1 | 400 |
| 非流式响应 | `text_completion` 结构、finish_reason 映射、usage 透传 |
| 流式响应 | 首包占位、text 增量、终止 chunk usage、`[DONE]` |
| echo=true | 输出文本前置 prompt |
| 代码围栏剥离（非流式） | 首尾 ```lang / ``` 移除，正文围栏保留 |
| 代码围栏剥离（流式，跨 chunk 分片） | `fence_buf` 缓冲识别，收尾截断 |

**访问日志（`access_log_test.go` 扩展）：**

| 用例 | 断言 |
| :--- | :--- |
| `response.output_text.delta` 文本 | 日志聚合出完整文本 |
| `response.function_call_arguments.delta` | 日志记录工具参数 |
| `choices[0].text`（Completions 流） | 日志聚合出补全文本 |

### 8.2 集成测试（curl）

```bash
KEY=mg_xxxx

# Responses 非流式
curl -s http://localhost:8080/v1/responses \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"qwen3.5-coder","instructions":"You are helpful.",
       "input":[{"type":"message","role":"user","content":"1+1=?"}]}'

# Responses 流式（观察完整事件生命周期）
curl -N -s http://localhost:8080/v1/responses \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"qwen3.5-coder","input":"write hello world","stream":true}'

# Completions / FIM（带 suffix）
curl -s http://localhost:8080/v1/completions \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"qwen3.5-coder","prompt":"def add(a, b):\n    ","suffix":"\n    return a + b"}'

# 兼容路径：/v1/engines/:engine/completions
curl -s http://localhost:8080/v1/engines/qwen3.5-coder/completions \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"prompt":"select * from users where"}'
```

### 8.3 端到端联调

**Codex CLI（`~/.codex/config.toml`）：**

```toml
model = "qwen3.5-coder"
model_provider = "modelgate"

[model_providers.modelgate]
name = "ModelGate"
base_url = "http://localhost:8080/v1"
env_key = "MODELGATE_API_KEY"
wire_api = "responses"
```

**OpenCode（provider 配置，`wire_api = "responses"` 模式）：**

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "modelgate": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "ModelGate",
      "options": {
        "baseURL": "http://localhost:8080/v1",
        "apiKey": "{env:MODELGATE_API_KEY}",
        "wireAPI": "responses"
      },
      "models": {
        "qwen3.5-coder": {"name": "Qwen 3.5 Coder"}
      }
    }
  }
}
```

> 说明：以上为示意配置，实际字段名以所用 OpenCode 版本的 provider 配置文档为准。

**VS Code Continue（Autocomplete 走 `/v1/completions`）：**

```json
{
  "name": "ModelGate",
  "apiBase": "http://localhost:8080/v1",
  "apiKey": "mg_xxxx",
  "useLegacyCompletionsEndpoint": true
}
```

**联调验证清单：**

- [ ] Codex CLI 能完成多轮 Agent 循环（含 `function_call` / `function_call_output` 往返）；
- [ ] 流式下事件生命周期完整，OpenAI SDK 客户端不报 `Missing response.created` 类错误；
- [ ] 推理模型（DeepSeek 等）思考内容不混入 `output_text`；
- [ ] IDE 补全结果无 Markdown 围栏残留；
- [ ] 看板/访问日志中 Token 统计与配额扣减准确；
- [ ] `raw_dumps=full` 下四阶段文件完整可回放。

---

## 9. 实施计划与里程碑

| 里程碑 | 内容 | 验收标准 |
| :--- | :--- | :--- |
| **M1：Completions 基础** | `codex` 包：端点、非流式/流式转换、usage、错误格式 | 单测 + curl 通过 |
| **M2：FIM 与代码过滤器** | FIM 双模式、`fim` 配置、流式围栏过滤 | `filter_test.go` 全绿 |
| **M3：Responses 基础** | `responses` 包：请求/响应转换、非流式 | 单测 + curl 通过 |
| **M4：Responses 流式生命周期** | SSE 状态机、推理/工具事件、SDK 兼容 | `stream_test.go` 全绿 |
| **M5：系统集成** | 路由、访问日志解析扩展、UA 识别、配置样例 | 集成测试通过 |
| **M6：端到端与文档** | Codex CLI / OpenCode / Continue 联调，更新 README 与接入文档 | 联调清单全勾选 |

---

## 10. 开放问题

1. **`previous_response_id` 是否值得做服务端状态缓存**：本期明确不做；若后续 Codex CLI 大批量依赖，可在网关增加 LRU 会话缓存（存转换后的消息上下文）。
2. **`reasoning` 事件族选择**：默认映射 `reasoning_summary_text`；若联调发现部分 SDK 期望 `reasoning_text`，通过 `responses.reasoning_event` 配置切换。
3. **结构化输出**：`text.format: json_schema` 依赖后端 `response_format` 支持度，是否需要网关侧做 JSON 校验与修复（fallback 重试）待评估。
4. **FIM 占位符覆盖**：私有化 tokenizer 变体多，`fim.mode: auto` 的判定关键字是否足够，需收集真实模型清单后扩充。
5. **流式心跳**：`response.created` 前发送 SSE 注释是否影响个别严格解析的客户端，联调中确认。
6. **多模态输入**：`input_image` 已支持；`input_file`（文本文件内容注入）是否有真实客户诉求，决定下期是否纳入。

---

## 附录 A：Responses 流式事件速查

| 事件 | 关键 data 字段 | 网关合成时机 |
| :--- | :--- | :--- |
| `response.created` | `response` 对象（status=in_progress） | 首个后端 chunk |
| `response.in_progress` | `response` 对象 | 紧随 created |
| `response.output_item.added` | `item`（message/function_call/reasoning） | 文本/工具/推理条目开始 |
| `response.content_part.added` | `part`（output_text） | 文本块开始 |
| `response.output_text.delta` | `delta`、`item_id`、`output_index`、`content_index` | 每个文本增量 |
| `response.output_text.done` | `text`（全文）、`item_id` | 文本结束 |
| `response.content_part.done` | `part`（全文） | 文本块结束 |
| `response.output_item.done` | `item`（完整条目） | 条目结束 |
| `response.reasoning_summary_text.delta` | `delta`、`item_id` | 推理增量 |
| `response.reasoning_summary_text.done` | `summary`、`item_id` | 推理条目结束 |
| `response.function_call_arguments.delta` | `delta`、`item_id` | 工具参数增量 |
| `response.function_call_arguments.done` | `arguments`（完整）、`item_id` | 工具参数结束 |
| `response.completed` | `response`（status=completed、usage） | 流正常结束 |
| `response.failed` / `error` | `response`（status=failed、error）/ `error` | 流中错误 |

## 附录 B：参考资料

- OpenAI Responses API Reference（`POST /v1/responses`）
- OpenAI Responses API Streaming Guide（SSE 生命周期）
- Codex CLI 文档（`model_provider` / `wire_api = "responses"`）
- OpenCode Providers 文档（Responses 兼容模式）
- VS Code Continue Autocomplete 文档（`useLegacyCompletionsEndpoint`）
- 项目内既有实现：`internal/gateway/anthropic`（协议适配范式）、`internal/gateway/proxy`（核心工作流）
