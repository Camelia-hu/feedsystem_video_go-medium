package redis

import (
	"context"
	"time"
)

func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	return c.rdb.Get(ctx, key).Bytes()
}

func (c *Client) SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

func (c *Client) Del(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

// DelByPattern 使用 SCAN 删除匹配 pattern 的 key，返回删除数量（故障注入/缓存击穿演示用）
func (c *Client) DelByPattern(ctx context.Context, pattern string) (int, error) {
	if c == nil || c.rdb == nil {
		return 0, nil
	}
	var deleted int
	var cursor uint64
	for {
		keys, nextCursor, err := c.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return deleted, err
		}
		for _, key := range keys {
			if n, err := c.rdb.Del(ctx, key).Result(); err == nil {
				deleted += int(n)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return deleted, nil
}

// Incr 对 key 做原子自增，返回自增后的值
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	if c == nil || c.rdb == nil {
		return 0, nil
	}
	return c.rdb.Incr(ctx, key).Result()
}
