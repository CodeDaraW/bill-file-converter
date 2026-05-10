package adapters

import (
	"fmt"
	"sort"
)

type Adapter struct {
	// Key is the stable CLI --type value and must be unique.
	Key string
	// Name is the human-readable bill type shown in logs and list-types.
	Name string
	// Headers is the canonical transaction table header exported to result files.
	// VLM output must match either Headers or one HeaderAliases entry.
	Headers []string
	// HeaderAliases lists accepted VLM header variants. When an alias is
	// matched, the exported header is still Headers.
	HeaderAliases [][]string
	// RowGuards identify real data rows after the header. They are intentionally
	// structural checks, usually on a date column, not business-rule cleanup.
	RowGuards []RowGuard
	// RemoveImages rewrites the input PDF with raster images removed before
	// parsing. Enable this only for profiles where image overlays such as
	// watermarks are known to degrade extraction; leave it off otherwise because
	// PDF rewriting can alter document structure.
	RemoveImages bool
}

type RowGuard struct {
	// Column is zero-based in the canonical table.
	Column int
	// Format is one of the RowGuardFormat* constants below.
	Format RowGuardFormat
}

type RowGuardFormat string

const (
	RowGuardFormatYYYYMMDD         RowGuardFormat = "YYYYMMDD"
	RowGuardFormatYYYYMMDDHHMMSS   RowGuardFormat = "YYYYMMDDHH:mm:ss"
	RowGuardFormatYYYYDashMMDashDD RowGuardFormat = "YYYY-MM-DD"
	RowGuardFormatMMSlashDD        RowGuardFormat = "MM/DD"
	RowGuardFormatPositiveInteger  RowGuardFormat = "positive_integer"
)

type registry struct {
	adapters map[string]Adapter
}

// NewRegistry creates an adapter registry for Convert callers that provide
// private profiles outside the built-in CLI profile set.
func NewRegistry(adapters ...Adapter) *registry {
	values := map[string]Adapter{}
	for _, adapter := range adapters {
		values[adapter.Key] = adapter
	}
	return &registry{adapters: values}
}

func BuiltinRegistry() *registry {
	return NewRegistry(
		abcDebitAdapter(),
		bocDebitAdapter(),
		bocomCreditRegularAdapter(),
		bocomCreditReissueAdapter(),
		bocomDebitAdapter(),
		cmbCreditAdapter(),
		cmbDebitAdapter(),
		zbankDebitAdapter(),
	)
}

func (r *registry) List() []Adapter {
	list := make([]Adapter, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		list = append(list, adapter)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Key < list[j].Key
	})
	return list
}

func (r *registry) MustGet(key string) (Adapter, error) {
	if key == "" {
		return Adapter{}, fmt.Errorf("missing bill type: pass --type <adapter_key>; run list-types to see supported types")
	}
	adapter, ok := r.adapters[key]
	if !ok {
		return Adapter{}, fmt.Errorf("unsupported bill type %q: no cleaning profile is registered", key)
	}
	return adapter, nil
}
