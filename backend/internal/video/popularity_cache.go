package video

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
)

const (
	// HotDecayKey 全局热榜衰减分 ZSET，单一 key 存储所有视频的时间加权得分
	HotDecayKey = "hot:video:decay"

	// halfLifeSec 热度半衰期（秒），24 小时后衰减至原始得分的 50%
	halfLifeSec = float64(24 * 60 * 60)

	// decayLambda 指数衰减系数 λ = ln(2) / 半衰期
	decayLambda = math.Ln2 / halfLifeSec

	// decayEpoch 计算衰减分的时间基准（Unix 秒），使用相对秒数而非绝对时间戳
	// 避免 math.Exp(λ × absoluteUnix) 因参数 ~14000 而溢出为 +Inf
	// 选取项目上线时间附近的某个点即可，只需保证 λ×(now-epoch) < 709
	// 当前约为 e^53（距 2025-01-01 约 76 天），安全范围内
	decayEpoch = int64(1735689600) // 2025-01-01 00:00:00 UTC
)

// UpdatePopularityCache 更新视频热榜分（只写 Redis，MySQL 原始计数由 Repo 层维护）
//
// 核心技巧——预乘时间权重：
//   stored_score += change × e^(λ × t_write)
//
// 在任意时刻 T 执行 ZRevRange，实际衰减得分为：
//   stored_score × e^(−λ×T) = change × e^(−λ×(T−t_write))
//
// 由于 e^(−λ×T) 对所有成员是同一常数，ZRevRange 的相对排名
// 与"各视频经过时间衰减后的真实得分"完全一致，无需读时额外计算。
func UpdatePopularityCache(ctx context.Context, cache *rediscache.Client, id uint, change int64) {
	if cache == nil || id == 0 || change == 0 {
		return
	}

	// 失效视频详情缓存，下次读取时重新回源
	_ = cache.Del(context.Background(), fmt.Sprintf("video:detail:id=%d", id))

	weightedScore := float64(change) * math.Exp(decayLambda*float64(time.Now().Unix()-decayEpoch))
	member := strconv.FormatUint(uint64(id), 10)

	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_ = cache.ZincrBy(opCtx, HotDecayKey, member, weightedScore)
}
