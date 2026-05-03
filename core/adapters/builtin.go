package adapters

const commonPrompt = `你正在从 PDF 页面渲染图片中提取银行账单表格。

通用规则：
- 只使用图片中的视觉证据。
- 只使用同一视觉行范围内的文字。
- 不要把上一行或下一行的文字填入当前行。
- 空单元格必须输出 null。null 是合法结果，不需要补齐。
- 没有明确视觉证据时不要推断。
- 金额字段保留原始符号、小数位、分隔符和币种文字。
- metadata 的 value 必须来自原文，不要归一化、改写或推断。
- metadata 的 key 使用英文 snake_case，例如 account_no、verification_code。不要使用中文 key。
- 页面标题、英文标题、日期范围、户名、Name、账号、Account No、账户类型、Account Type、开户行、Sub Branch、申请时间、Date、验证码、Verification Code 等都属于 metadata，不要放到 CSV。
- 不要输出 CSV。
- 只输出 JSON，结构必须匹配：
{
  "metadata": {"snake_case_key": "原始值"},
  "title": "可选标题；同时也应放入 metadata",
  "tables": [
    {
      "name": "表格名称",
      "headers": ["表头"],
      "rows": [["单元格文字或 null"]],
      "source_pages": [],
      "warnings": ["可选警告"]
    }
  ]
}
`

func builtinAdapters() []Adapter {
	return []Adapter{
		cmbDebitAdapter(),
		abcDebitAdapter(),
	}
}

func cmbDebitAdapter() Adapter {
	headersWithoutCustomerSummary := []string{"记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"}
	headersWithCustomerSummary := []string{"记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息", "客户摘要"}
	return Adapter{
		Key:       "cmb_debit",
		Name:      "招商银行借记卡",
		SeedPages: 0,
		Prompt: commonPrompt + `

账单类型：招商银行借记卡交易流水 PDF。

请提取 metadata：
- 尽量完整提取首页顶部和左右两侧的元信息。
- metadata key 必须使用英文 snake_case；如果原文有英文定义，优先基于英文定义命名。
- 示例 key：
  - title
  - english_title
  - statement_period
  - name
  - account_no
  - account_type
  - sub_branch
  - application_time
  - verification_code
- 示例 metadata：
{
  "title": "招商银行交易流水",
  "english_title": "Transaction Statement of China Merchants Bank",
  "statement_period": "2025-01-01 -- 2025-12-31",
  "name": "张三",
  "account_no": "6200********0000",
  "account_type": "ALL/全币种",
  "sub_branch": "北京示例支行",
  "application_time": "2025-01-01 12:00",
  "verification_code": "X*****"
}

请提取交易明细表。招商银行借记卡交易流水可能有两种合法表头：
1. 记账日期, 货币, 交易金额, 联机余额, 交易摘要, 对手信息
2. 记账日期, 货币, 交易金额, 联机余额, 交易摘要, 对手信息, 客户摘要

该 adapter 的表格还原规则：
- 必须按 PDF 中实际出现的表头输出；如果原始表格没有「客户摘要」列，就输出 6 列，不要额外补一列。
- 如果原始表格有「客户摘要」列，就输出 7 列；某一行客户摘要为空时，该单元格输出 null。
- 每一行的单元格数量必须和 headers 数量一致。
- 保持交易行的视觉顺序。
- 同一单元格内的多行文字用一个空格连接。
- 不要新增归一化列，不要改写列名。
- 如果上述两种表头都不可见，返回空 tables，并在 warnings 中说明没有找到预期交易明细表。
`,
		RequiredMetadata: []string{"title", "english_title", "statement_period", "name", "account_no", "account_type", "sub_branch", "application_time", "verification_code"},
		ExpectedTables: []TableSpec{
			{
				AllowedHeaders: [][]string{headersWithoutCustomerSummary, headersWithCustomerSummary},
				MinColumns:     len(headersWithoutCustomerSummary),
			},
		},
	}
}

func abcDebitAdapter() Adapter {
	headers := []string{"交易日期", "交易时间", "交易摘要", "交易金额", "本次余额", "对手信息", "日志号", "交易渠道", "交易附言"}
	return Adapter{
		Key:       "abc_debit",
		Name:      "中国农业银行借记卡",
		SeedPages: 0,
		Prompt: commonPrompt + `

账单类型：中国农业银行账户活期交易明细清单 PDF。

请提取 metadata：
- 尽量完整提取首页顶部和左右两侧的元信息。
- metadata key 必须使用英文 snake_case。
- 示例 key：
  - title
  - name
  - account_no
  - currency
  - statement_period
  - remittance_flag
  - electronic_serial_no
- 示例 metadata：
{
  "title": "中国农业银行账户活期交易明细清单",
  "name": "张三",
  "account_no": "62********0000",
  "currency": "人民币",
  "statement_period": "20250101-20251231",
  "remittance_flag": "本币",
  "electronic_serial_no": "0000000000"
}
- 起止日期对应 statement_period，原文是 "20250101-20251231" 这种紧凑格式时直接保留，不要改写为带分隔符的形式。
- 汇钞标识对应 remittance_flag，电子流水号对应 electronic_serial_no。
- 账户号通常带有红色印章遮挡或星号，按可见原文输出，不要补全。

请提取交易明细表。中国农业银行借记卡活期交易明细的合法表头为：
交易日期, 交易时间, 交易摘要, 交易金额, 本次余额, 对手信息, 日志号, 交易渠道, 交易附言

该 adapter 的表格还原规则：
- 必须严格按以上 9 列输出，列顺序与原文一致。
- 每一行的单元格数量必须等于 9。
- 视觉上空白的单元格输出 null，不要用空字符串、占位符或推断值替换。
- 保持交易行的视觉顺序，不要重排。
- 同一单元格内的多行文字用一个空格连接。
- 交易金额保留原始正负号、小数位和千分位分隔符；不要把 +21000.00 改写成 21000、21,000 或 21000.00。
- 对手信息常被红色印章或马赛克遮挡，按可见字符原样输出，缺失部分不要推断；如果完全不可读则输出 null。
- 对手信息列如果是占位符（例如两个 ASCII 半角短横 "--"、单个短横 "-"、单个全角破折号 "—"），按图像中可见的字符原样输出，不要把 "--" 改写成 "—"、"–"、"-" 或反过来；不要把多个短横合并成破折号。
- 日志号通常是 10 位（少数早期记录可能是 9 位）的纯数字，或以 1 位英文字母开头后接 9 位数字。提取时按图像中可见字符逐位输出，不要在数字间擅自插入或删除字符；如果识别结果不是上述长度，请重新检查图像。
- 不要把表格上方的标题、户名、账户、币种、起止日期、汇钞标识、电子流水号等信息当成数据行；它们属于 metadata。
- 如果上述表头不可见，返回空 tables，并在 warnings 中说明没有找到预期交易明细表。
`,
		RequiredMetadata: []string{"title", "name", "account_no", "currency", "statement_period", "remittance_flag", "electronic_serial_no"},
		ExpectedTables: []TableSpec{
			{
				AllowedHeaders: [][]string{headers},
				MinColumns:     len(headers),
			},
		},
	}
}
