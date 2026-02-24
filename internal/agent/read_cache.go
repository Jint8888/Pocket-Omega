package agent

import (
	"crypto/md5"
	"fmt"
	"sync"
)

// ReadCacheEntry stores a cached tool result with its step number.
type ReadCacheEntry struct {
	StepNumber int
	Output     string
}

// ReadCache is a session-level cache for idempotent tool results.
// Caches file_read (by path) and other read-only/idempotent tools (by tool+args hash).
// file_write/file_patch/file_delete/file_move invalidate the file_read cache for that path.
type ReadCache struct {
	mu    sync.RWMutex
	cache map[string]ReadCacheEntry // cacheKey → entry
}

// NewReadCache creates a new empty ReadCache.
func NewReadCache() *ReadCache {
	return &ReadCache{cache: make(map[string]ReadCacheEntry)}
}

// Get returns the cached entry for the given key, if any.
func (c *ReadCache) Get(key string) (ReadCacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.cache[key]
	return e, ok
}

// Put stores a tool result in the cache.
func (c *ReadCache) Put(key string, entry ReadCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = entry
}

// Invalidate removes the cached entry for the given key.
func (c *ReadCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, key)
}

// cacheableTools defines tools whose results can be cached.
// file_read: cached by path, invalidated by write tools.
// Others: cached by tool+args hash, no automatic invalidation (session-scoped).
//
// NOTE: mcp_server_list is NOT cacheable — its results change after
// mcp_server_add/mcp_server_remove/mcp_reload, and we don't track those invalidations.
var cacheableTools = map[string]bool{
	"file_read": true,
	"file_list": true,
}

// isCacheable returns true if the tool's results can be cached.
func isCacheable(toolName string) bool {
	return cacheableTools[toolName]
}

// CacheKey builds the cache key for a tool invocation.
// file_read: uses "file_read:<path>" for precise write-invalidation.
// Others: uses "tool:<name>:<md5(args)>" for general dedup.
func CacheKey(toolName, argsJSON string) string {
	if toolName == "file_read" {
		path := extractParam(argsJSON, "path")
		if path != "" {
			return "file_read:" + path
		}
	}
	// #nosec G401 -- MD5 used only for deduplication, not security
	h := md5.Sum([]byte(argsJSON))
	return fmt.Sprintf("tool:%s:%x", toolName, h)
}

// FileReadCacheKey returns the cache key for a file_read of the given path.
// Used by write-tool invalidation in Post.
func FileReadCacheKey(path string) string {
	return "file_read:" + path
}

// isWriteTool returns true for tools that modify files (cache invalidation triggers).
func isWriteTool(toolName string) bool {
	switch toolName {
	case "file_write", "file_patch", "file_delete", "file_move":
		return true
	}
	return false
}
