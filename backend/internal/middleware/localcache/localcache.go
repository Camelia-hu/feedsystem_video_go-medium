// Package localcache 提供进程内 LRU 缓存，作为 Redis 的 L1 层。
//
// 双层缓存架构：
//   L1（本包）：进程内 ristretto，TTL=1s，避免同一秒内重复请求打到 Redis
//   L2（Redis）：分布式缓存，TTL=5min，跨进程/重启共享
//
// 两层使用相同的 string key，业务层无需感知切换细节。
package localcache

import (
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

const (
	// DefaultMaxCost 默认最大内存 32 MB
	DefaultMaxCost = 32 << 20
	// L1TTL L1 缓存默认 TTL，1 秒内合并重复请求
	L1TTL = 1 * time.Second
)

// Cache 进程内缓存，线程安全，零值不可用，请通过 New 创建。
type Cache struct {
	c *ristretto.Cache[string, []byte]
}

// New 创建进程内缓存。maxCost 为最大内存字节数，传 0 使用 DefaultMaxCost。
func New(maxCost int64) (*Cache, error) {
	if maxCost <= 0 {
		maxCost = DefaultMaxCost
	}
	c, err := ristretto.NewCache(&ristretto.Config[string, []byte]{
		NumCounters: 1e6,     // 追踪最近 1M 个 key 的访问频率（TinyLFU 准入策略）
		MaxCost:     maxCost, // 总内存上限，cost 以 value 字节数计
		BufferItems: 64,      // 写入异步缓冲区大小
	})
	if err != nil {
		return nil, err
	}
	return &Cache{c: c}, nil
}

// Get 读取缓存，未命中返回 (nil, false)
func (lc *Cache) Get(key string) ([]byte, bool) {
	if lc == nil {
		return nil, false
	}
	return lc.c.Get(key)
}

// Set 写入缓存，cost 自动按 value 字节数计算
func (lc *Cache) Set(key string, val []byte, ttl time.Duration) {
	if lc == nil {
		return
	}
	lc.c.SetWithTTL(key, val, int64(len(val)), ttl)
}

// Del 主动淘汰某个 key
func (lc *Cache) Del(key string) {
	if lc == nil {
		return
	}
	lc.c.Del(key)
}

// Close 释放后台 goroutine
func (lc *Cache) Close() {
	if lc == nil {
		return
	}
	lc.c.Close()
}
