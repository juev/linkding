package metadata

import (
	"context"
	"sync"
)

type cacheEntry struct {
	url   string
	value Metadata
}

// Cache retains recent website metadata so a bookmark save can reuse its preview.
type Cache struct {
	mu      sync.Mutex
	entries []cacheEntry
	limit   int
}

func NewCache(limit int) *Cache {
	return &Cache{limit: limit}
}

// Load bypasses the cache when requested without replacing the cached value.
func (c *Cache) Load(ctx context.Context, client Doer, pageURL string, ignoreCache bool) Metadata {
	if c == nil || c.limit <= 0 || ignoreCache {
		return Load(ctx, client, pageURL)
	}
	c.mu.Lock()
	for i, entry := range c.entries {
		if entry.url == pageURL {
			copy(c.entries[1:i+1], c.entries[:i])
			c.entries[0] = entry
			c.mu.Unlock()
			return entry.value
		}
	}
	c.mu.Unlock()

	value := Load(ctx, client, pageURL)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, entry := range c.entries {
		if entry.url == pageURL {
			copy(c.entries[1:i+1], c.entries[:i])
			c.entries[0] = entry
			return entry.value
		}
	}
	c.entries = append(c.entries, cacheEntry{})
	copy(c.entries[1:], c.entries[:len(c.entries)-1])
	c.entries[0] = cacheEntry{url: pageURL, value: value}
	if len(c.entries) > c.limit {
		c.entries = c.entries[:c.limit]
	}
	return value
}
