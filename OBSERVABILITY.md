# Prometheus 可观测平台集成指南

## 已集成的指标

### HTTP 请求指标
- `http_requests_total` - HTTP 请求总数（按 method, path, status 分组）
- `http_request_duration_seconds` - HTTP 请求延迟分布（P50/P95/P99）

### 缓存指标
- `cache_hits_total` - 缓存命中次数（L1/L2 分层）
- `cache_misses_total` - 缓存未命中次数
- `cache_operation_duration_seconds` - 缓存操作延迟

### 数据库指标
- `db_query_total` - 数据库查询总数（按 table, operation, status 分组）
- `db_query_duration_seconds` - 数据库查询延迟

### MQ 指标
- `mq_publish_total` - MQ 消息发布总数（按 queue, status 分组）
- `mq_consume_total` - MQ 消息消费总数
- `mq_process_duration_seconds` - MQ 消息处理延迟

### 业务指标
- `video_publish_total` - 视频发布总数
- `video_delete_total` - 视频删除总数
- `like_action_total` - 点赞操作总数（like/unlike）
- `comment_action_total` - 评论操作总数（publish/delete）
- `follow_action_total` - 关注操作总数（follow/unfollow）
- `feed_request_total` - Feed 流请求总数（按 feed_type 分组）

### 推拉混合架构指标
- `inbox_fanout_total` - 收件箱写扩散总数（success/failed）
- `inbox_fanout_size` - 单次写扩散粉丝数量分布
- `big_v_skip_count` - 大 V 跳过写扩散次数

### 热榜指标
- `hot_rank_cache_hit` - 热榜缓存命中次数
- `hot_rank_cache_miss` - 热榜缓存未命中次数
- `popularity_update_total` - 热度更新总数（按 source 分组）

## 快速启动

### 1. 启动所有服务（包含 Prometheus + Grafana）

```bash
docker compose up -d
```

### 2. 访问可观测平台

- **Prometheus**: http://localhost:9090
- **Grafana**: http://localhost:3000
  - 默认账号：`admin`
  - 默认密码：`admin123`

### 3. 配置 Grafana 数据源

1. 登录 Grafana
2. 进入 Configuration → Data Sources
3. 添加 Prometheus 数据源
   - URL: `http://prometheus:9090`
   - Access: `Server (default)`
4. 点击 "Save & Test"

### 4. 导入仪表盘

#### 方式一：手动创建面板

在 Grafana 中创建新仪表盘，添加以下常用查询：

**QPS（每秒请求数）**
```promql
sum(rate(http_requests_total[1m])) by (path)
```

**P99 延迟**
```promql
histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket[1m])) by (le, path))
```

**缓存命中率**
```promql
sum(rate(cache_hits_total[1m])) / (sum(rate(cache_hits_total[1m])) + sum(rate(cache_misses_total[1m])))
```

**MQ 消费速率**
```promql
sum(rate(mq_consume_total[1m])) by (queue, status)
```

**写扩散粉丝数分布**
```promql
histogram_quantile(0.95, sum(rate(inbox_fanout_size_bucket[5m])) by (le))
```

**热榜缓存命中率**
```promql
rate(hot_rank_cache_hit[1m]) / (rate(hot_rank_cache_hit[1m]) + rate(hot_rank_cache_miss[1m]))
```

#### 方式二：使用预设查询

常用 PromQL 查询示例：

```promql
# Feed 接口 QPS
sum(rate(http_requests_total{path="/feed/list"}[1m]))

# Feed 接口 P99 延迟
histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{path="/feed/list"}[1m])) by (le))

# 视频发布速率
rate(video_publish_total[5m])

# 大 V 跳过写扩散比例
rate(big_v_skip_count[5m]) / rate(inbox_fanout_total[5m])

# MQ 消息积压（需要配合 RabbitMQ Exporter）
rabbitmq_queue_messages{queue="video.box.events"}
```

## 告警规则示例

在 `prometheus.yml` 中添加告警规则：

```yaml
rule_files:
  - 'alerts.yml'
```

创建 `alerts.yml`：

```yaml
groups:
  - name: feedsystem
    interval: 30s
    rules:
      - alert: HighErrorRate
        expr: sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) > 0.05
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "高错误率告警"
          description: "5xx 错误率超过 5%"

      - alert: HighLatency
        expr: histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket[5m])) by (le, path)) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "高延迟告警"
          description: "P99 延迟超过 1 秒"

      - alert: LowCacheHitRate
        expr: sum(rate(cache_hits_total[5m])) / (sum(rate(cache_hits_total[5m])) + sum(rate(cache_misses_total[5m]))) < 0.7
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "缓存命中率过低"
          description: "缓存命中率低于 70%"
```

## 性能优化建议

根据指标数据进行优化：

1. **缓存命中率低** → 调整 TTL 或增加缓存容量
2. **写扩散粉丝数过大** → 降低大 V 阈值
3. **MQ 消息积压** → 增加 Worker 实例或提高 QoS
4. **热榜缓存未命中** → 检查 Redis 容量和过期策略
5. **P99 延迟高** → 分析慢查询，优化数据库索引

## 本地开发

如果只启动依赖服务（不启动 Prometheus/Grafana）：

```bash
docker compose up -d mysql redis rabbitmq
```

本地运行后端和 Worker，指标端点：
- Backend API: http://localhost:8080/metrics
- Worker: http://localhost:8081/metrics

可以直接用浏览器访问查看原始指标数据。
