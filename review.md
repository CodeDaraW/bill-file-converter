# 代码 Review

针对当前仓库（`cmd/`、`cli/`、`core/`、`core/adapters/`、`core/logger/`）做的一次结构性 review，重点是：模块抽象是否合理、模块和接口、代码是否有冗余设计。

## 整体结构印象

```
cmd/bill-file-converter/main.go        # 入口
cli/{cli.go, config.go}                # 子命令 + 配置加载
core/{convert.go, types.go, json.go,
     mineru_client.go, table_html.go,
     csv.go, validation.go}            # 转换主流程 + 工具
core/adapters/{adapter.go, *_*.go}     # 银行账单 profile
core/logger/{logger.go, events.go}     # 任务日志
```

包划分基本合理：`cli` 只负责命令行，`core` 负责领域逻辑，`adapters` 抽出 profile，`logger` 抽出日志副作用。问题主要集中在 **`core` 内部的文件粒度** 和 **几个重复/冗余的设计**。

---

## A. 高优先级 —— 结构性问题

### A1. `Input` 的「单文件/多文件」双形态是核心冗余

`core/types.go` 的 `Input` 同时持有顶层的 `Path/Reader/FileName/MIMEType` 和 `Files []InputFile`：

```11:24:core/types.go
type Input struct {
	Path     string
	Reader   io.Reader
	FileName string
	MIMEType string
	Files    []InputFile
}

type InputFile struct {
	Path     string
	Reader   io.Reader
	FileName string
	MIMEType string
}
```

这一双形态在整个 codebase 下扩散出大量冗余转换代码：

- `cli.go` 在打包 Input 时要做特例：

```84:88:cli/cli.go
if len(input.Files) == 1 {
    input.Path = input.Files[0].Path
    input.FileName = input.Files[0].FileName
    input.Files = nil
}
```

- `core/convert.go` 的 `inputFiles()` 反过来再把它「展平」回 `[]InputFile`：

```503:513:core/convert.go
func inputFiles(input Input) []InputFile {
	if len(input.Files) > 0 {
		return input.Files
	}
	return []InputFile{{
		Path:     input.Path,
		Reader:   input.Reader,
		FileName: input.FileName,
		MIMEType: input.MIMEType,
	}}
}
```

- `removeInputPDFImages` 处理完后还要再把 1 个文件「折叠回」单形态：

```536:539:core/convert.go
if len(processed) == 1 {
    return Input{Path: processed[0].Path, FileName: processed[0].FileName, MIMEType: processed[0].MIMEType}, nil
}
return Input{Files: processed}, nil
```

- `sourceInfo` 也为单文件 vs 多文件返回不同形态的 `SourceInfo`（顶层 path/file_name 或 `files`）。

**建议**：去掉单文件特例，`Input` 只保留 `Files []InputFile`，`SourceInfo` 也只保留 `Files []SourceFileInfo`（输出 schema 也跟着简化）。这一改动可以删掉至少 5 处来回转换代码、消掉 `inputFileName/sourceFileInfo` 中重复的 fallback 逻辑，整体代码更简单。

### A2. `MinerUClient.Parse` 的多文件支持有「两套真相」

`mineru_client.go` 的 `multipartBody` 已经支持把多个文件作为单次 multipart 请求一起发送（`for _, file := range inputFiles(input) { writer.CreateFormFile("files", ...) }`），并且测试 `TestMinerUHTTPClientPostsMultipartToFileParse` 也验证了这种行为。

但是 `convert.go` 的 `parseMinerUInInputOrder` 在多文件时**完全不走 client 的多文件路径**，而是把每个文件拆成独立请求逐个调用 `Parse`：

```151:200:core/convert.go
func parseMinerUInInputOrder(ctx context.Context, client MinerUClient, input Input) (MinerUParseResult, error) {
	files := inputFiles(input)
	if len(files) <= 1 {
		return client.Parse(ctx, input)
	}
	// ... loop: client.Parse(ctx, Input{Path: file.Path, ...})
}
```

也就是说 client 内部的多文件 multipart 逻辑在 `Convert` 的真实链路里**永远不会被触发**，只活在直接调用 `Parse` 的单元测试里 —— 这是典型的死代码 + 双重真相。

可能的原因是希望显式控制 `page_idx` 偏移，但现状的代价是：
- client 中的多文件 multipart 分支变成"看起来支持但实际不用"；
- 调用者必须做一次自己组合 raw request/response 的工作（`marshalRawMessages` 把多个 raw 拼成 JSON 数组），这一逻辑跟"用户原本只发了一个 multipart 请求"的语义对不上。

**建议二选一**：

- (A) 让 client 始终单文件，删除 `multipartBody` 中的 `for _, file := range inputFiles(input)` 循环，签名改成 `Parse(ctx, file InputFile)`；当前 convert 流程就是这种用法。
- (B) 反过来：让 client 一次性提交所有文件，删除 `parseMinerUInInputOrder`，让服务端返回的 page_idx 直接生效；前提是确认 MinerU 同步接口对多 file 的 page_idx 行为。

倾向 (A)：简单、和现状对齐；只是 `MinerUClient` 接口现在接 `Input`，改成 `InputFile` 之后语义最清晰。

### A3. `core/convert.go` 文件过大、职责过多

这一个文件 ~700 行，承载了至少 7 个不同的关注点：

| 关注点 | 函数 |
| --- | --- |
| 主流程编排 | `Convert` |
| MinerU 调用编排 | `parseMinerUInInputOrder`, `marshalRawMessages` |
| 内容清洗 | `DocumentFromMinerUContent`, `tableFromRows`, `dedupeRawText` |
| 表头匹配 | `matchingHeaderRow`, `rowMatchesHeaderAlias`, `rowStartsWithHeaderAlias`, `rowMatchesHeaderAliasPrefix` |
| RowGuard 校验 | `rowMatchesGuards`, `valueMatchesGuardFormat` |
| PDF 预处理 | `removeInputPDFImages`, `removePDFImages`, `materializePDFInput` |
| Artifact / TaskID / Source / Input 校验 | `writeArtifacts`, `newTaskID`, `validatePDFInput`, `sourceInfo`, ... |

**建议**至少按下面拆开（不需要新增子包）：

```
core/convert.go         # 仅保留 Convert 主流程
core/cleaning.go        # DocumentFromMinerUContent + tableFromRows + 表头/RowGuard
core/preprocess.go      # removeInputPDFImages 等 Ghostscript 调用
core/artifacts.go       # writeArtifacts、newTaskID、SaveFailure 配合
core/input.go           # Input/InputFile/sourceInfo/validatePDFInput 等
core/orchestrator.go    # parseMinerUInInputOrder（如保留）
```

同时 `core/preprocess.go` 中显式 `exec.LookPath("gs")` 的依赖应当在 README 增加一段「运行依赖：Ghostscript（仅 `cmb_debit`/`abc_debit` 等开启 `RemoveImages` 的 profile 需要）」的说明，目前文档完全没提。

### A4. 一些字段实际上是死代码，建议删除

- `Artifacts.CSVBytes` / `Artifacts.JSONBytes`（`json:"-"`）：在 `Convert` 写完磁盘之后留在内存里，但 CLI 和测试都从磁盘读，没有任何调用方使用这两个字段。

```83:86:core/types.go
FailurePath           string `json:"failure_path,omitempty"`
CSVBytes              []byte `json:"-"`
JSONBytes             []byte `json:"-"`
```

- `Options.taskID`（unexported）：只是 `Convert` 内部从 `newTaskID` 写到自己用，把它从 `Options` 字段降级为普通局部变量即可：

```26:34:core/types.go
type Options struct {
	MinerU          MinerUClient
	...
	taskID          string   // ← 这个不应该挂在 Options 上
}
```

- `Pinger` interface：在 `core/types.go` 里定义但 CLI 里直接调 `*MinerUHTTPClient.Ping`，没有通过 `Pinger` 抽象耦合解耦。要么 `runMinerU` 改成接收 `core.Pinger`，要么把这个接口删掉。

```139:141:core/types.go
type Pinger interface {
	Ping(ctx context.Context) error
}
```

---

## B. 中优先级 —— 接口与重复

### B1. 表头匹配的三个函数语义重叠，且有 magic number

`tableFromRows` 中过滤伪表头的代码：

```267:269:core/convert.go
if stringRowIsEmpty(row) || stringRowMatchesHeaders(row, headers) || rowMatchesHeaderAlias(row, adapter) || rowStartsWithHeaderAlias(row, adapter) {
    continue
}
```

它依赖了 4 个相关函数，分布在 50 行里，其中 `rowMatchesHeaderAliasPrefix` 还藏着一个不解释的 magic number：

```374:376:core/convert.go
return matches >= 3
```

为什么是 3？没有注释。如果某个未来 adapter 的表头只有 4 列，这个阈值就立即失效。

**建议**：

- 把表头识别合并成一个函数 `rowLooksLikeHeader(row []string, adapter adapters.Adapter) bool`，按"严格相等 / 归一化相等 / 前缀对齐"三种策略统一处理 `adapter.Headers` 和 `adapter.HeaderAliases`；
- 把 `>= 3` 改成基于 `len(headers)` 的比例（例如 `>= len(headers)*2/3`）或者作为 `Adapter` 上一个显式可配置的字段，并加注释解释 *why*。

### B2. 两个 `equal*` helper，所属文件不一致

- `validation.go` 有 `equalStrings(a, b []string) bool`（严格比较）；
- `convert.go` 有 `equalStringSlices(a, b []string) bool`（每个元素都先 `normalizeText`）；
- `csv.go` 又复用了 `equalStrings`（验证文件中的私有函数）。

跨文件复用一个未导出 helper 没问题，但是命名让两者的差别看不出来。

**建议**：

- 把 `equalStringSlices` → `equalNormalizedStrings`、`equalHeaderSlices` → `equalCollapsedHeaders` 之类，名字直接表达策略；
- 把"helper 类"集中放一个 `core/strings.go` 或 `core/textutil.go`，避免散落在 `convert.go`/`validation.go`/`csv.go`。

### B3. `inputFileName` 这种 fallback 在 4 处重复

```go
name := file.FileName
if name == "" && file.Path != "" {
    name = filepath.Base(file.Path)
}
```

这种"FileName 空就退到 Path 的 base"在 `inputFileName`、`addMultipartFile`、`sourceFileInfo`、`rawMinerURequest` 都各写了一次。应该在 `InputFile` 上加一个方法：

```go
func (f InputFile) Name() string {
    if f.FileName != "" {
        return f.FileName
    }
    if f.Path != "" {
        return filepath.Base(f.Path)
    }
    return "input.pdf" // 仅 multipart 用，可参数化或调用方自行兜底
}
```

### B4. 配置默认值有"双层兜底"

`cli/config.go`：

- `LoadConfig` 先用 `DefaultConfig()` 填默认，再 yaml unmarshal 覆盖；
- `MinerUHTTPConfig()` 又对 `Timeout/LangList/Backend/ParseMethod` 再兜底一次。

```62:73:cli/config.go
langList := c.MinerU.LangList
if len(langList) == 0 {
    langList = []string{"ch"}
}
backend := c.MinerU.Backend
if backend == "" {
    backend = "hybrid-auto-engine"
}
```

第二层兜底是为了防止用户写 `lang_list: []` 这种"显式空"。但目前实际行为是：用户写 `lang_list: []` 跟没写一样 —— 这个语义到底想要什么应该是一个产品决策。要么：

- 显式空表示"用默认"（现状）→ 文档里说明，第二层兜底保留；
- 显式空表示"我就要空"→ 删掉第二层兜底，让用户自己负责。

不要让 yaml/Go 默认值不一致带来"看起来一样但语义不同"的暗坑。

### B5. logger 包的内部分文件并未形成模块边界

`core/logger/logger.go` 定义 `Logger`、`Level`、`formatLine`、`SaveText`；`events.go` 又给 `Logger` 加 `Verbosef/Infof/Errorf/SaveFailure/SaveRawPayloads/ColorizeLine`，并定义 `failureArtifact`。

它们属于同一个 receiver，分文件只是物理拆分。没什么大问题，但：

- `failureArtifact` 是业务结构（task 失败摘要），并不是通用日志能力；放在通用 logger 包里有点越界，也让 logger 反过来「认识」业务概念。可以让 caller 传入要序列化的 `any`，或者把它干脆挪到 `core` 包里、logger 只暴露 `SaveJSON(name string, payload any)`。
- `ColorizeLine` 和 ANSI 终端着色相关，跟「短事件 vs 长 payload」没有本质关系，可以跟 `Level` 一起放在 `logger.go`，文件命名上暂不分开也可以接受。

---

## C. 低优先级 —— 清理项

### C1. `Table.UnmarshalJSON` 缺少注释说明用意

```9:27:core/json.go
func (t *Table) UnmarshalJSON(data []byte) error {
```

它是为了兼容 `source_pages` 历史上可能写过 int / string / array 三种形态的 `result.json`。但代码本身没说为什么需要这种容错；目前整个项目内部只 *写* 不 *读* `result.json`（`web/` 还是占位），属于「为未来场景预留」的代码。

**建议**：加一句注释说明用途和场景；如果短期不会重新加载，可以考虑直接删除自定义 Unmarshal，等到真要消费 `result.json` 时再加，避免无人验证的解析路径长期沉淀。

### C2. CLI 的 `normalizeFlagArgs` 是为了绕开标准库 `flag` 的限制

```258:296:cli/cli.go
func normalizeFlagArgs(args []string, flagsWithValue map[string]bool) []string {
```

这是为了支持 `convert ./bill.pdf --type cmb_debit` 这种 positional 在前的写法。功能正确，但每个新增带值 flag 都要去 `flagsWithValue` 里登记，长期维护成本不低。如果未来命令更复杂，建议直接换成 `spf13/cobra` 或 `spf13/pflag` —— 现在还能接受。

### C3. `MinerUParseResult` 的 RawRequest/RawResponse 是字符串

`mineru_client.go` 把请求 metadata 序列化成 JSON 字符串返回给 `Convert`，再由 `parseMinerUInInputOrder` 再次 marshal 成 JSON 数组：

```202:211:core/convert.go
func marshalRawMessages(values []json.RawMessage) string {
	if len(values) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(values, "", "  ")
```

也就是字符串 → `json.RawMessage` → `[]byte` 来回转换。如果改成 `[]byte`/`json.RawMessage` 一直流到 logger，可减少 1 次 string ↔ bytes 转换；不算性能问题，更多是类型上的"心智整洁"。

### C4. `appendUniqueInts` 总会 `sort.Ints`

```480:481:core/convert.go
sort.Ints(base)
return base
```

source_pages 数量很小，没有性能问题，但行为上"任何插入都隐式重排"和函数名 `append` 不太对得上。要么改名 `mergeSortedUniqueInts`，要么不在 helper 里 sort、由 caller 决定。

---

## 总结

按修改影响面排序，建议先做：

1. 合并 `Input` 单/多形态 → `Files []InputFile`（删 ~30 行转换代码）。
2. `MinerUClient.Parse` 接口和 `parseMinerUInInputOrder` 二选一：定一处真相（建议 client 改成单文件，删 multipart 多文件分支与对应测试）。
3. 拆分 `core/convert.go`：`cleaning / preprocess / artifacts / input` 至少分四块。
4. 删除死代码：`Artifacts.CSVBytes/JSONBytes`、`Options.taskID`、未被使用的 `Pinger`。

其次：

5. 合并 4 个表头匹配函数为 `rowLooksLikeHeader`，去掉 `>= 3` magic number。
6. 把 `equalStrings/equalStringSlices` 命名澄清并集中到 `core/strings.go`。
7. README 加 Ghostscript 依赖说明（与 `RemoveImages` 配套）。
8. 配置兜底「单层化」，避免 `LoadConfig` + `MinerUHTTPConfig` 双层默认。

这样改完之后，`core` 的边界会从「一个 700 行的 convert.go + 散落 helper」收敛成「Convert 主流程 + 几个职责单一的辅助文件」，对外接口（`Input` / `MinerUClient` / `Adapter`）也会少 50% 左右的边角 case。
