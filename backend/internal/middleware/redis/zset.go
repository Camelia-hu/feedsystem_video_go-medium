package redis

import (
	"context"
	"time"

	redis "github.com/redis/go-redis/v9"
)

func (c *Client) ZincrBy(ctx context.Context, key string, member string, score float64) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZIncrBy(ctx, key, score, member).Err()
}

func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Expire(ctx, key, ttl).Err()
}

func (c *Client) ZUnionStore(ctx context.Context, dst string, keys []string, aggregate string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZUnionStore(ctx, dst, &redis.ZStore{
		Keys:      keys,
		Aggregate: aggregate,
	}).Err()
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, nil
	}
	n, err := c.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

func (c *Client) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	if c == nil || c.rdb == nil {
		return nil, nil
	}
	return c.rdb.ZRevRange(ctx, key, start, stop).Result()
}

func (c *Client) ZRevRangeByScore(ctx context.Context, key string, max, min string, offset, count int64) ([]string, error) {
	if c == nil || c.rdb == nil {
		return nil, nil
	}
	return c.rdb.ZRevRangeByScore(ctx, key, &redis.ZRangeBy{
		Max:    max,
		Min:    min,
		Offset: offset,
		Count:  count,
	}).Result()
}

// ZAddBatchInbox 使用 Pipeline 批量向多个收件箱 ZSET 写入同一条记录
// 每个 key 执行：ZADD + ZREMRANGEBYRANK（裁剪到 maxLen）+ EXPIRE（刷新 TTL）
// 所有命令在一次网络往返中发送，比循环调用 ZAdd 效率高得多
// keys 由调用方构造（如 "follow:inbox:123"），redis 层保持纯粹不感知业务
func (c *Client) ZAddBatchInbox(ctx context.Context, keys []string, score float64, member string, maxLen int64, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	if len(keys) == 0 {
		return nil
	}
	pipe := c.rdb.Pipeline()
	for _, key := range keys {
		pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: member})
		pipe.ZRemRangeByRank(ctx, key, 0, -(maxLen+1))
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// ZAdd 向有序集合写入一个成员，score 用 float64 表示时间戳或权重
func (c *Client) ZAdd(ctx context.Context, key string, score float64, member string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZAdd(ctx, key, redis.Z{Score: score, Member: member}).Err()
}

// ZRemRangeByRank 按排名范围删除成员（排名从 0 开始，负数从末尾倒数）
// 常用姿势：ZRemRangeByRank(ctx, key, 0, -(maxLen+1)) 保留最新 maxLen 条
func (c *Client) ZRemRangeByRank(ctx context.Context, key string, start, stop int64) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZRemRangeByRank(ctx, key, start, stop).Err()
}
