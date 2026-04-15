package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP 请求指标
	HttpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP 请求总数",
		},
		[]string{"method", "path", "status"},
	)

	HttpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP 请求延迟分布",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path"},
	)

	// 缓存指标
	CacheHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_hits_total",
			Help: "缓存命中次数",
		},
		[]string{"cache_type", "key_prefix"},
	)

	CacheMisses = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_misses_total",
			Help: "缓存未命中次数",
		},
		[]string{"cache_type", "key_prefix"},
	)

	CacheOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cache_operation_duration_seconds",
			Help:    "缓存操作延迟",
			Buckets: []float64{.0001, .0005, .001, .005, .01, .05, .1},
		},
		[]string{"cache_type", "operation"},
	)

	// 数据库指标
	DbQueryTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_query_total",
			Help: "数据库查询总数",
		},
		[]string{"table", "operation", "status"},
	)

	DbQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "数据库查询延迟",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
		},
		[]string{"table", "operation"},
	)

	// MQ 指标
	MqPublishTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mq_publish_total",
			Help: "MQ 消息发布总数",
		},
		[]string{"queue", "status"},
	)

	MqConsumeTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mq_consume_total",
			Help: "MQ 消息消费总数",
		},
		[]string{"queue", "status"},
	)

	MqProcessDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mq_process_duration_seconds",
			Help:    "MQ 消息处理延迟",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"queue", "action"},
	)

	// 业务指标
	VideoPublishTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "video_publish_total",
			Help: "视频发布总数",
		},
	)

	VideoDeleteTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "video_delete_total",
			Help: "视频删除总数",
		},
	)

	LikeActionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "like_action_total",
			Help: "点赞操作总数",
		},
		[]string{"action"}, // like, unlike
	)

	CommentActionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "comment_action_total",
			Help: "评论操作总数",
		},
		[]string{"action"}, // publish, delete
	)

	FollowActionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "follow_action_total",
			Help: "关注操作总数",
		},
		[]string{"action"}, // follow, unfollow
	)

	FeedRequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "feed_request_total",
			Help: "Feed 流请求总数",
		},
		[]string{"feed_type"}, // latest, followings, likes, popularity
	)

	// 推拉混合架构指标
	InboxFanoutTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "inbox_fanout_total",
			Help: "收件箱写扩散总数",
		},
		[]string{"status"}, // success, failed
	)

	InboxFanoutSize = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "inbox_fanout_size",
			Help:    "单次写扩散粉丝数量分布",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000},
		},
	)

	BigVSkipCount = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "big_v_skip_count",
			Help: "大 V 跳过写扩散次数",
		},
	)

	// 热榜指标
	HotRankCacheHit = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "hot_rank_cache_hit",
			Help: "热榜缓存命中次数",
		},
	)

	HotRankCacheMiss = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "hot_rank_cache_miss",
			Help: "热榜缓存未命中次数",
		},
	)

	PopularityUpdateTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "popularity_update_total",
			Help: "热度更新总数",
		},
		[]string{"source"}, // like, comment, view
	)
)

// RecordHttpRequest 记录 HTTP 请求指标
func RecordHttpRequest(method, path string, status int, duration time.Duration) {
	statusClass := fmt.Sprintf("%dxx", status/100)
	HttpRequestsTotal.WithLabelValues(method, path, statusClass).Inc()
	HttpRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
}

// RecordCacheOperation 记录缓存操作
func RecordCacheOperation(cacheType, keyPrefix string, hit bool, duration time.Duration) {
	if hit {
		CacheHits.WithLabelValues(cacheType, keyPrefix).Inc()
	} else {
		CacheMisses.WithLabelValues(cacheType, keyPrefix).Inc()
	}
	CacheOperationDuration.WithLabelValues(cacheType, "get").Observe(duration.Seconds())
}

// RecordDbQuery 记录数据库查询
func RecordDbQuery(table, operation, status string, duration time.Duration) {
	DbQueryTotal.WithLabelValues(table, operation, status).Inc()
	DbQueryDuration.WithLabelValues(table, operation).Observe(duration.Seconds())
}

// RecordMqPublish 记录 MQ 发布
func RecordMqPublish(queue string, success bool) {
	status := "success"
	if !success {
		status = "failed"
	}
	MqPublishTotal.WithLabelValues(queue, status).Inc()
}

// RecordMqConsume 记录 MQ 消费
func RecordMqConsume(queue, action string, success bool, duration time.Duration) {
	status := "success"
	if !success {
		status = "failed"
	}
	MqConsumeTotal.WithLabelValues(queue, status).Inc()
	MqProcessDuration.WithLabelValues(queue, action).Observe(duration.Seconds())
}
