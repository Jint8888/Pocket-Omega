package agent

import (
	"sync"
	"testing"
)

func TestReadCache_PutAndGet(t *testing.T) {
	c := NewReadCache()
	key := CacheKey("file_read", `{"path":"a.go"}`)
	c.Put(key, ReadCacheEntry{StepNumber: 1, Output: "content-a"})

	entry, ok := c.Get(key)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if entry.StepNumber != 1 || entry.Output != "content-a" {
		t.Errorf("unexpected entry: %+v", entry)
	}
}

func TestReadCache_Miss(t *testing.T) {
	c := NewReadCache()
	_, ok := c.Get(CacheKey("file_read", `{"path":"nonexistent.go"}`))
	if ok {
		t.Error("expected cache miss")
	}
}

func TestReadCache_Invalidate(t *testing.T) {
	c := NewReadCache()
	key := FileReadCacheKey("a.go")
	c.Put(key, ReadCacheEntry{StepNumber: 1, Output: "content-a"})
	c.Invalidate(key)

	_, ok := c.Get(key)
	if ok {
		t.Error("expected cache miss after invalidation")
	}
}

func TestReadCache_InvalidateAfterWrite(t *testing.T) {
	c := NewReadCache()
	key := FileReadCacheKey("a.go")
	c.Put(key, ReadCacheEntry{StepNumber: 1, Output: "content-a"})

	// Simulate file_write invalidation
	if isWriteTool("file_write") {
		c.Invalidate(FileReadCacheKey("a.go"))
	}

	_, ok := c.Get(key)
	if ok {
		t.Error("expected cache miss after file_write invalidation")
	}
}

func TestReadCache_DifferentPathNotAffected(t *testing.T) {
	c := NewReadCache()
	keyA := FileReadCacheKey("a.go")
	keyB := FileReadCacheKey("b.go")
	c.Put(keyA, ReadCacheEntry{StepNumber: 1, Output: "content-a"})
	c.Put(keyB, ReadCacheEntry{StepNumber: 2, Output: "content-b"})

	c.Invalidate(keyA)

	_, okA := c.Get(keyA)
	if okA {
		t.Error("a.go should be invalidated")
	}

	entry, okB := c.Get(keyB)
	if !okB {
		t.Fatal("b.go should still be cached")
	}
	if entry.Output != "content-b" {
		t.Errorf("unexpected b.go entry: %+v", entry)
	}
}

func TestReadCache_ConcurrentAccess(t *testing.T) {
	c := NewReadCache()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Put("file.go", ReadCacheEntry{StepNumber: i, Output: "content"})
		}(i)
	}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Get("file.go")
		}()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Invalidate("file.go")
		}()
	}
	wg.Wait()
}

func TestCacheKey_FileRead(t *testing.T) {
	key := CacheKey("file_read", `{"path":"src/main.go"}`)
	if key != "file_read:src/main.go" {
		t.Errorf("unexpected file_read key: %s", key)
	}
}

func TestCacheKey_IdempotentTool(t *testing.T) {
	key1 := CacheKey("file_list", `{}`)
	key2 := CacheKey("file_list", `{}`)
	if key1 != key2 {
		t.Error("same tool+args should produce same cache key")
	}
	key3 := CacheKey("file_list", `{"filter":"x"}`)
	if key1 == key3 {
		t.Error("different args should produce different cache key")
	}
}

func TestCacheKey_DifferentToolsSameArgs(t *testing.T) {
	key1 := CacheKey("shell_exec", `{}`)
	key2 := CacheKey("file_list", `{}`)
	if key1 == key2 {
		t.Error("different tools with same args should produce different keys")
	}
}

func TestIsCacheable(t *testing.T) {
	tests := []struct {
		tool string
		want bool
	}{
		{"file_read", true},
		{"mcp_server_list", false},
		{"file_list", true},
		{"file_write", false},
		{"shell_exec", false},
		{"update_plan", false},
	}
	for _, tt := range tests {
		if got := isCacheable(tt.tool); got != tt.want {
			t.Errorf("isCacheable(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

func TestFileReadCacheKey(t *testing.T) {
	key := FileReadCacheKey("src/main.go")
	if key != "file_read:src/main.go" {
		t.Errorf("unexpected key: %s", key)
	}
}