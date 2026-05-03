# bill-file-converter

使用本地或 BYOK VLM API 将银行账单 PDF 转换为 CSV 的工具。

这是 Go 重写版本。旧的纯前端实现已经移动到 `legacy/`。

## 当前范围

- v1 只支持 PDF 输入。邮件账单请先在邮箱客户端或浏览器中打印/导出为 PDF。
- 解析使用 PDF 页面渲染图片，不依赖 PDF text layer。
- 模型必须先输出 JSON，CSV 由程序根据 JSON 生成。
- CSV 只负责还原已注册 adapter 的原始账单表格布局，不做跨银行交易归一化。
- v1 暂不实现 Web UI。`web/` 只是未来本地 Web UI 的占位目录。

## CLI 配置

构建或运行 CLI：

```bash
go run ./cmd/bill-file-converter list-types
go build ./cmd/bill-file-converter
```

安装 PDF 渲染工具。当前 CLI 支持：

- `pdftoppm`，来自 poppler
- `mutool`，来自 MuPDF

macOS 可使用：

```bash
brew install poppler
```

生成配置文件：

```bash
go run ./cmd/bill-file-converter config init -out config.json
```

默认配置面向 LM Studio 的 OpenAI-compatible endpoint：

```json
{
  "provider": {
    "provider": "openai-compatible",
    "base_url": "http://localhost:1234/v1",
    "api_key_env": "LLM_API_KEY",
    "model": "qwen3-vl-32b-instruct",
    "temperature": 0,
    "thinking_enabled": false
  },
  "renderer": {
    "command": "pdftoppm",
    "dpi": 200
  },
  "conversion": {
    "max_concurrency": 4
  }
}
```

如果使用 LM Studio，请先启动本地 OpenAI-compatible server。若服务端不需要真实 key，`api_key_env` 可以指向任意环境变量（包括未设置的变量）。若使用托管服务，请按需设置 `provider`、`base_url`、`model` 和 `api_key_env`。

为避免 API key 被错误地写入到配置文件并提交到版本控制，配置文件中**不允许**直接出现 `api_key` 字段；必须通过 `api_key_env` 指向一个环境变量，由运行时从该环境变量读取 key。如果在 `config.json` 中出现 `api_key`，加载时会直接报错。

支持的 provider 值：

- `openai-compatible`：LM Studio、Ollama/OpenAI-compatible、OpenRouter、LiteLLM、Vercel AI Gateway 等兼容或网关服务。
- `anthropic`
- `gemini`

Thinking/reasoning 默认关闭。需要开启时配置：

```json
{
  "provider": {
    "thinking_enabled": true,
    "thinking_budget_tokens": 2048
  }
}
```

Provider 支持是 best-effort：

- OpenAI-compatible：发送 `reasoning_effort: "medium"`；设置 budget 时也发送 `thinking` 对象。
- Anthropic：发送原生 `thinking`。
- Gemini：发送 `generationConfig.thinkingConfig`。

转换策略：

- 每个 adapter 可以配置 `SeedPages`。
- `SeedPages = 0`：关闭 seed 解析，每页独立并发发送给 VLM 后按页码合并。适合每页都有完整表头的账单。
- `SeedPages = 1/2/...`：前 N 页先作为 seed 解析，用来提取 `metadata`、确认表格名称和表头结构，并抽取 seed 页数据。
- seed 页之后的页面按页并发解析，每页 prompt 会带入 seed 阶段已经确认的表头结构，只抽取当前页数据行。
- 合并阶段按页码排序追加数据，去掉重复表头行，不插入空行。
- `conversion.max_concurrency` 控制 seed 页之后的页面并发数；默认 4。使用本地模型时可以按机器性能调低。

## CLI 使用

列出支持的账单类型：

```bash
go run ./cmd/bill-file-converter list-types
```

转换 PDF：

```bash
go run ./cmd/bill-file-converter convert ./statement.pdf \
  --type cmb_debit \
  --config config.json \
  --out output
```

也可以一次传入多个 PDF，适合“一页一个 PDF 文件”的账单。多个 PDF 会按命令行参数顺序渲染并合并为同一个转换任务：

```bash
go run ./cmd/bill-file-converter convert ./page-1.pdf ./page-2.pdf ./page-3.pdf \
  --type cmb_debit \
  --config config.json \
  --out output
```

每次转换都会创建一个定长 task id，并将所有产物写入 `--out <task_id>/`，避免多次运行互相覆盖。task id 格式为日期 + 时间 + crypto 随机后缀，例如 `20260501-153045-a1b2c3d4`。

输出文件：

- `output/<task_id>/result.json`：源文件信息、本地时区生成时间、metadata、表格、校验报告和 artifact 路径。
- `output/<task_id>/result.csv`：严格表格 CSV，只包含表头和数据行；标题和文档级信息属于 `metadata`。
- `output/<task_id>/pages/*.png`：PDF 渲染出的页面图片。
- `output/<task_id>/bill_file_converter.log`：固定日志文件，包含带时间戳、task id 和日志级别的阶段事件，也包含 provider raw request、raw response 和失败信息。

只检查流程、不生成 CSV：

```bash
go run ./cmd/bill-file-converter inspect ./statement.pdf \
  --type cmb_debit \
  --config config.json \
  --out output-inspect
```

检查 provider 配置和 renderer 可用性：

```bash
go run ./cmd/bill-file-converter providers test --config config.json
```

## 可追溯性要求

过程可追溯是本项目的严格要求。每次转换都必须能仅通过 `output/<task_id>/` 下的本地文件完成审计，不需要再次调用 LLM API。

必须满足：

- 每条 CLI 日志必须包含时间戳和 task id。
- 每个 task 必须写入固定文件 `bill_file_converter.log`，记录带时间戳、task id 和日志级别的阶段事件。
- 日志级别只使用 `verbose`、`info`、`warning`、`error` 四类。
- PDF 渲染出的页面图片必须保留在 `pages/` 下。
- Provider raw request 和 raw response 必须写入 `bill_file_converter.log`，按阶段/页码使用 `seed_pages_raw_request`、`page_<n>_raw_request` 等 block 记录。
- raw request 和 raw response 必须在解析模型 JSON 前写入日志，确保 parse 失败也可调试。
- 成功转换必须写入 `result.json`；非 inspect 转换还必须写入 `result.csv`。
- 任务目录创建后发生失败时，必须在 `bill_file_converter.log` 中写入 `failure` block，包含 source、adapter、task id、时间戳和错误信息。
- CSV 只能包含表格数据；metadata、标题、source、task、校验信息都必须放在 JSON artifact 中。

## 验收清单

新环境或新 adapter 按以下清单验收：

1. `go test ./...` 通过。
2. `go run ./cmd/bill-file-converter list-types` 能看到目标 adapter key。
3. `go run ./cmd/bill-file-converter providers test --config config.json` 成功。
4. `convert` 生成 `result.json` 和 `result.csv`。
5. 日志包含 task id，产物写入 `output/<task_id>/`。
6. `result.json.task_id` 与输出目录名一致。
7. `result.json.source` 记录源 PDF 路径/文件名，`generated_at` 使用机器本地时区。
8. `result.json.metadata` 包含 adapter 必需的源文件元信息。
9. metadata key 使用英文 `snake_case`，例如 `account_no`、`verification_code`。
10. `result.json.tables[].headers` 精确匹配 adapter 允许的表头。
11. 视觉空单元格在 JSON 中为 `null`，在 CSV 中为空字段。
12. CSV 列顺序和行顺序与 PDF 表格布局一致，不包含 metadata 或 title 行。
13. 预期表格或表头不存在时，转换必须校验失败，不能生成猜测格式的 CSV。

## Providers 和 Adapters

Provider 实现在 `core/providers` 下，并通过 `providers.New(config)` 创建。`core.Convert` 只依赖 `core.VLMProvider` 接口，因此调用方可以提供自己的 provider 实现，而不需要修改 core 转换逻辑。

Adapter 实现在 `core/adapters` 下。CLI 使用 `adapters.BuiltinRegistry()`，其中只包含公开内置 adapter。私有 adapter，例如公司工资单，应放在内置 registry 之外，并通过 `core.Options.AdapterRegistry` 传给 `core.Convert`。

私有 adapter 最小示例：

```go
registry := adapters.NewRegistry(adapters.Adapter{
    Key: "private_payroll",
    Name: "Private Payroll",
    Prompt: "...",
    RequiredMetadata: []string{"employee_name", "pay_period"},
    ExpectedTables: []adapters.TableSpec{
        {AllowedHeaders: [][]string{{"日期", "项目", "金额"}}, MinColumns: 3},
    },
})

result, err := core.Convert(ctx, input, core.Options{
    Provider: provider,
    Renderer: renderer,
    AdapterKey: "private_payroll",
    AdapterRegistry: registry,
    OutputDir: "output",
})
```

## 首个 Adapter：`cmb_debit`

第一个公开内置 adapter 注册在 `core/adapters/builtin.go`，key 为 `cmb_debit`，用于招商银行借记卡交易流水。

它定义了：

- 必需 metadata：`title`、`english_title`、`statement_period`、`name`、`account_no`、`account_type`、`sub_branch`、`application_time`、`verification_code`
- 允许的表头：`记账日期`、`货币`、`交易金额`、`联机余额`、`交易摘要`、`对手信息`；或在此基础上增加 `客户摘要`
- `SeedPages: 0`，因为招行借记卡交易流水每页都有表头，默认按页独立并发解析后合并
- 专用 VLM prompt，要求模型保留原始表格布局并只输出 JSON
- CSV 只写表格内容，不写 metadata 或标题

新增公开内置银行 adapter 的流程：

1. 在 `core/adapters/builtin.go` 增加 adapter constructor，例如 `bocCreditAdapter()`。
2. 设置稳定 key、可读名称、必需 metadata key、精确表头和 adapter 专用 prompt。
3. 只有当 prompt/profile 足够明确、能安全拒绝不支持格式时，才注册进 `builtinAdapters()`。
4. 使用页面图片 fixture 和 fake VLM JSON 增加 golden tests。
5. 执行 `go test ./...`，并用脱敏 PDF 做一次真实 `convert` 后，才视为支持。

未注册的账单类型会被明确拒绝。
