package core

import (
	"fmt"
	"sort"
)

type Adapter struct {
	Key              string
	Name             string
	Prompt           string
	RequiredMetadata []string
	ExpectedTables   []TableSpec
}

type TableSpec struct {
	Name           string
	Headers        []string
	AllowedHeaders [][]string
	MinColumns     int
}

var adapters = map[string]Adapter{}

func RegisterAdapter(adapter Adapter) {
	adapters[adapter.Key] = adapter
}

func GetAdapter(key string) (Adapter, bool) {
	adapter, ok := adapters[key]
	return adapter, ok
}

func ListAdapters() []Adapter {
	list := make([]Adapter, 0, len(adapters))
	for _, adapter := range adapters {
		list = append(list, adapter)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Key < list[j].Key
	})
	return list
}

func MustGetAdapter(key string) (Adapter, error) {
	if key == "" {
		return Adapter{}, fmt.Errorf("missing bill type: pass --type <adapter_key>; run list-types to see supported types")
	}
	adapter, ok := GetAdapter(key)
	if !ok {
		return Adapter{}, fmt.Errorf("unsupported bill type %q: no prompt/profile is registered", key)
	}
	return adapter, nil
}

func init() {
	for _, adapter := range builtinAdapters() {
		RegisterAdapter(adapter)
	}
}
