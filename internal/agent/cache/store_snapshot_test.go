package cache

import (
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
)

// TestCacheSnapshot_IncludesIndexes：索引类缓存必须出现在快照里（修 LimiterIndex 漏显示 bug）。
func TestCacheSnapshot_IncludesIndexes(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	snap := s.CacheSnapshot()
	for _, name := range []string{"route_index", "limiter_index"} {
		if _, ok := snap[name]; !ok {
			t.Fatalf("snapshot missing %q", name)
		}
	}
	if snap["limiter_index"].Kind != "index" {
		t.Fatalf("limiter_index kind=%s", snap["limiter_index"].Kind)
	}
}

// TestCacheSnapshot_Complete 防复发：所有实现 NamedCacheStat 的 Store 字段都必须在快照里。
func TestCacheSnapshot_Complete(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	snap := s.CacheSnapshot()
	v := reflect.ValueOf(s).Elem()
	ncsType := reflect.TypeOf((*NamedCacheStat)(nil)).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanInterface() {
			continue
		}
		if f.Kind() == reflect.Ptr && f.IsNil() {
			continue
		}
		if !f.Type().Implements(ncsType) {
			continue
		}
		name := f.Interface().(NamedCacheStat).CacheName()
		if _, ok := snap[name]; !ok {
			t.Fatalf("Store field %s (NamedCacheStat %q) absent from CacheSnapshot — register it",
				v.Type().Field(i).Name, name)
		}
	}
}
