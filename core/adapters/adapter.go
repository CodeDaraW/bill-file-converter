package adapters

import (
	"fmt"
	"sort"
)

type Adapter struct {
	Key  string
	Name string
	// RemoveImages rewrites the input PDF with raster images removed before
	// parsing. Enable this only for profiles where image overlays such as
	// watermarks are known to degrade extraction; leave it off otherwise because
	// PDF rewriting can alter document structure.
	RemoveImages   bool
	ExpectedTables []TableSpec
}

type TableSpec struct {
	Name           string
	Headers        []string
	AllowedHeaders [][]string
	HeaderAliases  [][]string
	HeaderStarts   []string
	MinColumns     int
}

type Registry struct {
	adapters map[string]Adapter
}

func NewRegistry(items ...Adapter) *Registry {
	registry := &Registry{adapters: map[string]Adapter{}}
	for _, item := range items {
		registry.Register(item)
	}
	return registry
}

func BuiltinRegistry() *Registry {
	return NewRegistry(builtinAdapters()...)
}

func (r *Registry) Register(adapter Adapter) {
	r.adapters[adapter.Key] = adapter
}

func (r *Registry) Get(key string) (Adapter, bool) {
	adapter, ok := r.adapters[key]
	return adapter, ok
}

func (r *Registry) List() []Adapter {
	list := make([]Adapter, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		list = append(list, adapter)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Key < list[j].Key
	})
	return list
}

func (r *Registry) MustGet(key string) (Adapter, error) {
	if key == "" {
		return Adapter{}, fmt.Errorf("missing bill type: pass --type <adapter_key>; run list-types to see supported types")
	}
	adapter, ok := r.Get(key)
	if !ok {
		return Adapter{}, fmt.Errorf("unsupported bill type %q: no cleaning profile is registered", key)
	}
	return adapter, nil
}
