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

## Profile 设计原则

- CSV 应尽量还原已注册 profile 对应的原始账单表格布局，包括原始列集合和列顺序；不要为了兼容 legacy 输出、下游账务系统或跨银行统一格式而删列、补列或改列。
- 同一家银行、同一种卡，如果正常发送、补发、导出选项或文件来源导致表格结构明显不同，应优先拆成独立 profile，而不是在同一个 adapter 中堆叠大量条件分支。例如交通银行信用卡正常账单和补发账单分别使用 `bocom_credit_regular` 与 `bocom_credit_reissue`。
- `Headers` 表示最终导出的规范表头；`HeaderAliases` 只用于接受 MinerU/VLM 对同一张原始表头的不同识别形态，例如中英混排、空格差异或表头拆行后的英文行。
- `RowGuards` 只做结构性过滤，例如日期列、序号列；不要用它承载业务清洗、交易分类或金额推断。
- 增加新 profile 前，应优先查看真实 `logger/content_list.json`，确认 MinerU 输出的表格块、表头形态、重复表头、页眉页脚和分组行，而不是只根据截图或 legacy 代码推断。

## 与 legacy 实现的有意差异

- legacy 可能在 CSV 中输出标题行；当前结果 CSV 只输出交易表头和数据行，标题、账单周期、卡号等非表格信息保留在 `result.json.metadata.raw_text` 中。
- `boc_debit` 不再额外追加 `借记卡号` 列，因为借记卡号来自页眉文本，不属于交易明细表原始列。
- `boc_credit_regular` / `boc_credit_reissue` 不再额外追加 `币种` 列，因为币种来自“人民币账户交易明细”“美元账户交易明细”等章节状态，不属于交易明细表原始列。后续如需记录币种，应通过状态机或 metadata 设计统一处理。
- `boc_credit_reissue` 保留原始 `MM/DD` 日期，不自动补全年份。后续如需补全年份，应通过 CLI 开关统一控制日期补全，而不是在 profile 中默认改写原始日期。

## 已知限制

- 当前方案依赖 MinerU/VLM 对 PDF 表格的识别结果。若原账单中存在连续多行完全相同或高度相似的交易，VLM 可能把这些行识别成错误数量，例如漏行或膨胀出过多重复行。
- 不应使用简单去重修复上述问题，因为银行账单中连续重复交易可能是真实发生的交易，去重会误删真实数据。
- 对这类样本，应使用参考 CSV 或人工核对结果中的行数、金额合计和连续重复交易段；开发新 fixture 时也应避免把无法判断真伪的 VLM 膨胀样本作为唯一验收标准。

## 后续 TODO

- 中国银行信用卡账单的币种信息来自“人民币账户交易明细”“美元账户交易明细”等章节，而不一定存在于交易表列中。后续如果要记录币种，可能需要在清洗阶段引入按内容块顺序推进的状态机，将章节状态附加到对应交易表或 metadata，而不是在 adapter 中硬编码补列。
- 部分信用卡补制账单只在交易行中提供 `MM/DD` 日期，年份来自标题或账单周期。当前 profile 优先还原原始表格，不自动补全年份；后续如果要对齐 legacy 的年份补全逻辑，应考虑增加 CLI 开关，统一控制是否做日期补全，而不是在单个 adapter 中默认改写原始日期。

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
12. 新增 profile 应至少补一组 fake MinerU e2e fixture；有真实参考 CSV 时，应记录真实 PDF 转换与参考 CSV 的行数、表头和字段差异。

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
