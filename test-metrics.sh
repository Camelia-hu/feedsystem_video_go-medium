#!/bin/bash

# Prometheus 指标端点测试脚本

echo "=== 测试 Prometheus 指标端点 ==="
echo ""

# 检查 Backend API metrics 端点
echo "1. 检查 Backend API metrics 端点 (http://localhost:8080/metrics)"
if curl -s http://localhost:8080/metrics | grep -q "http_requests_total"; then
    echo "✓ Backend API metrics 端点正常"
    echo "  - 发现指标: http_requests_total"
else
    echo "✗ Backend API metrics 端点异常"
fi
echo ""

# 检查 Worker metrics 端点
echo "2. 检查 Worker metrics 端点 (http://localhost:8081/metrics)"
if curl -s http://localhost:8081/metrics | grep -q "mq_consume_total"; then
    echo "✓ Worker metrics 端点正常"
    echo "  - 发现指标: mq_consume_total"
else
    echo "✗ Worker metrics 端点异常"
fi
echo ""

# 检查 Prometheus 服务
echo "3. 检查 Prometheus 服务 (http://localhost:9090)"
if curl -s http://localhost:9090/-/healthy | grep -q "Prometheus"; then
    echo "✓ Prometheus 服务正常"
else
    echo "✗ Prometheus 服务异常"
fi
echo ""

# 检查 Grafana 服务
echo "4. 检查 Grafana 服务 (http://localhost:3000)"
if curl -s http://localhost:3000/api/health | grep -q "ok"; then
    echo "✓ Grafana 服务正常"
else
    echo "✗ Grafana 服务异常"
fi
echo ""

echo "=== 常用指标查询示例 ==="
echo ""
echo "在 Prometheus (http://localhost:9090) 中尝试以下查询："
echo ""
echo "# QPS（每秒请求数）"
echo "sum(rate(http_requests_total[1m])) by (path)"
echo ""
echo "# P99 延迟"
echo "histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket[1m])) by (le, path))"
echo ""
echo "# 缓存命中率"
echo "sum(rate(cache_hits_total[1m])) / (sum(rate(cache_hits_total[1m])) + sum(rate(cache_misses_total[1m])))"
echo ""
echo "# MQ 消费速率"
echo "sum(rate(mq_consume_total[1m])) by (queue, status)"
echo ""
echo "# 大 V 跳过写扩散比例"
echo "rate(big_v_skip_count[5m]) / rate(inbox_fanout_total[5m])"
echo ""
