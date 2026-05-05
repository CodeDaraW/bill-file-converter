package adapters

func builtinAdapters() []Adapter {
	return []Adapter{
		cmbDebitAdapter(),
		cmbCreditAdapter(),
		abcDebitAdapter(),
	}
}

func cmbDebitAdapter() Adapter {
	headersWithoutCustomerSummary := []string{"记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"}
	headersWithCustomerSummary := []string{"记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息", "客户摘要"}
	englishHeadersWithoutCustomerSummary := []string{"Date", "Currency", "Transaction Amount", "Balance", "Transaction Type", "Counter Party"}
	englishHeadersWithCustomerSummary := []string{"Date", "Currency", "Transaction Amount", "Balance", "Transaction Type", "Counter Party", "Customer Summary"}
	return Adapter{
		Key:  "cmb_debit",
		Name: "招商银行借记卡",
		// CMB debit statements are text-based PDFs, but their watermark is a
		// raster image overlay that can degrade OCR/VLM table extraction.
		RemoveImages: true,
		RowGuards: []RowGuard{
			{Column: 0, Format: "YYYY-MM-DD"},
		},
		ExpectedTables: []TableSpec{
			{
				AllowedHeaders: [][]string{headersWithoutCustomerSummary, headersWithCustomerSummary},
				HeaderAliases:  [][]string{englishHeadersWithoutCustomerSummary, englishHeadersWithCustomerSummary},
				HeaderStarts:   []string{"记账日期", "Date"},
				MinColumns:     len(headersWithoutCustomerSummary),
			},
		},
	}
}

func cmbCreditAdapter() Adapter {
	headers := []string{"交易日", "记账日", "交易摘要", "人民币金额", "卡号末四位", "交易地金额"}
	bilingualHeaders := []string{
		"交易日 SOLD",
		"记账日 POSTED",
		"交易摘要 DESCRIPTION",
		"人民币金额 RMB AMOUNT",
		"卡号末四位 CARD NO(Last 4digits)",
		"交易地金额 Original Tran Amount",
	}
	return Adapter{
		Key:  "cmb_credit",
		Name: "招商银行信用卡",
		RowGuards: []RowGuard{
			{Column: 1, Format: "MM/DD"},
		},
		ExpectedTables: []TableSpec{
			{
				AllowedHeaders: [][]string{headers},
				HeaderAliases:  [][]string{bilingualHeaders},
				HeaderStarts:   []string{"交易日", "SOLD"},
				MinColumns:     len(headers),
			},
		},
	}
}

func abcDebitAdapter() Adapter {
	headers := []string{"交易日期", "交易时间", "交易摘要", "交易金额", "本次余额", "对手信息", "日志号", "交易渠道", "交易附言"}
	return Adapter{
		Key:  "abc_debit",
		Name: "中国农业银行借记卡",
		// ABC debit statements can contain image overlays/stamps that interfere
		// with table extraction, so strip raster images before parsing.
		RemoveImages: true,
		RowGuards: []RowGuard{
			{Column: 0, Format: "YYYY-MM-DD"},
		},
		ExpectedTables: []TableSpec{
			{
				AllowedHeaders: [][]string{headers},
				MinColumns:     len(headers),
			},
		},
	}
}
