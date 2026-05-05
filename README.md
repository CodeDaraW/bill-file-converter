# bill-file-converter

使用 MinerU 本地兼容 API 将银行账单 PDF 转换为 CSV 的工具。

这是 Go 重写版本。旧的纯前端实现位于 `legacy/`。

## 当前范围

- v1 只支持 PDF 输入。邮件账单请先在邮箱客户端或浏览器中打印/导出为 PDF。
- 解析依赖 MinerU local-compatible 同步接口：`POST {base_url}/file_parse`。
- 程序只消费 MinerU `content_list` 中的表格块，并根据内置账单类型 profile 做清洗和校验。
- CSV 只负责还原已注册 profile 的原始账单表格布局，不做跨银行交易归一化。
- metadata 仅保留去重后的原始非表格文本，写入 `result.metadata.raw_text`，方便调试。

## CLI 配置

构建或运行 CLI：

```bash
go run ./cmd/bill-file-converter list-types
go build ./cmd/bill-file-converter
```

生成 YAML 配置文件：

```bash
go run ./cmd/bill-file-converter config init -output config.yaml
```

默认配置会写出一个需要编辑的 MinerU endpoint 示例：

```yaml
mineru:
  base_url: "http://127.0.0.1:<port>"
  lang_list:
    - ch
  backend: hybrid-auto-engine
  parse_method: auto
  timeout: 10m
  max_retries: 1
  headers: {}
```

`mineru.base_url` 必须改成真实地址，例如动态本地端口、内网 IP 或三方兼容服务地址。客户端固定调用 `{base_url}/file_parse`，不提供 path 配置。解析请求会固定发送 `return_md=false`、`formula_enable=false`、`table_enable=true`、`return_content_list=true`，这些不是配置项。

如果配置中省略 `lang_list`、`backend`、`parse_method` 或 `timeout`，CLI 会使用上面的默认值；显式配置为空值也按默认值处理。

检查 MinerU 服务：

```bash
go run ./cmd/bill-file-converter mineru test --config config.yaml
```

## CLI 使用

列出支持的账单类型：

```bash
go run ./cmd/bill-file-converter list-types
```

转换单个 PDF：

```bash
go run ./cmd/bill-file-converter convert ./statement.pdf \
  --type cmb_debit \
  --config config.yaml \
  --output output
```

同一期账单可以传入多个 PDF，文件按命令行参数顺序解析并合并：

```bash
go run ./cmd/bill-file-converter convert ./page-1.pdf ./page-2.pdf ./page-3.pdf \
  --type cmb_debit \
  --config config.yaml \
  --output output
```

也可以传入目录。目录只展开第一层 `.pdf` 文件，不递归；目录内 PDF 按文件名自然序排序，例如 `1.pdf`、`2.pdf`、`10.pdf`：

```bash
go run ./cmd/bill-file-converter convert ./statement-pages \
  --type cmb_debit \
  --config config.yaml \
  --output output
```

`--output` 默认是当前执行 CLI 目录下的 `output`。每次转换都会创建一个定长 task id，并将产物写入 `output/<task_id>/`，避免多次运行互相覆盖。

只检查流程、不生成 CSV：

```bash
go run ./cmd/bill-file-converter inspect ./statement.pdf \
  --type cmb_debit \
  --config config.yaml \
  --output output-inspect
```

## 输出目录

```
output/<task_id>/
├── result/
│   ├── result.json
│   └── result.csv
└── logger/
    ├── bill_file_converter.log
    ├── content_list.json
    ├── mineru_request.json
    ├── mineru_response.json
    └── failure.json   # 仅在失败时
```

- `result/result.json`：源文件信息、本地时区生成时间、metadata、表格、校验报告和 artifact 路径。
- `result/result.csv`：严格表格 CSV，只包含表头和数据行。
- `logger/bill_file_converter.log`：带时间戳、task id 和日志级别的阶段事件。
- `logger/content_list.json`：MinerU 内容列表，是清洗和排查问题的主要输入。
- `logger/mineru_request.json` / `mineru_response.json`：MinerU 原始请求摘要和响应体。长 JSON 单独存放，避免写入行日志后导致编辑器卡顿。
- `logger/failure.json`：失败时写入，包含 source、adapter、task id、时间戳和错误信息。

## 运行依赖

- MinerU local-compatible API 服务。
- Ghostscript 可执行文件 `gs`：仅在账单 profile 启用 `RemoveImages` 时需要，例如当前部分借记卡 profile 会先移除 PDF 中影响 VLM 识别的水印图片。未启用 `RemoveImages` 的 profile 不需要安装 Ghostscript。

## 清洗规则

- 只处理 `type=table` 且存在 `table_body` 的块。
- 使用 HTML parser 解析 `table_body`，支持 `rowspan` 和 `colspan` 展开。
- 单元格会做 HTML entity 解码、连续空白压缩、首尾空白去除；空字符串在 JSON 中输出为 `null`，在 CSV 中输出为空字段。
- 每张表会寻找与当前 `--type` profile 匹配的表头；不匹配的表丢弃。
- 表头之后的数据行进入结果；重复表头行和空行丢弃。
- 相同表头的多张表按 MinerU 顺序合并，`source_pages` 使用 MinerU `page_idx + 1`。
- 非表格文本归一化并去重后写入 `metadata.raw_text`，保留首次出现顺序。

## 验收清单

1. `go test ./...` 通过。
2. `go run ./cmd/bill-file-converter list-types` 能看到目标 profile key。
3. `go run ./cmd/bill-file-converter mineru test --config config.yaml` 成功。
4. `convert` 生成 `result/result.json` 和 `result/result.csv`。
5. 日志包含 task id，所有产物写入 `output/<task_id>/`。
6. `result.json.task_id` 与输出目录名一致。
7. `result.json.source.files` 记录源 PDF 路径/文件名，单个或多个 PDF 都使用同一结构。
8. `result.json.metadata.raw_text` 包含去重后的原始非表格文本。
9. `result.json.tables[].headers` 精确匹配 profile 允许的表头。
10. CSV 列顺序和行顺序与 MinerU 表格顺序一致，不包含 metadata 或 title 行。
11. 预期表格或表头不存在时，转换必须校验失败，不能生成猜测格式的 CSV。

## Profiles

内置 profile 实现在 `core/adapters` 下。CLI 使用 `adapters.BuiltinRegistry()`，其中只包含公开内置 profile。私有账单类型应放在内置 registry 之外，并通过 `core.Options.AdapterRegistry` 传给 `core.Convert`。

私有 profile 最小示例：

```go
registry := adapters.NewRegistry(adapters.Adapter{
    Key:     "private_payroll",
    Name:    "Private Payroll",
    Headers: []string{"日期", "项目", "金额"},
    RowGuards: []adapters.RowGuard{
        {Column: 0, Format: adapters.RowGuardFormatYYYYDashMMDashDD},
    },
})
```
