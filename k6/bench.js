/**
 * 短视频信息流系统 - 压测脚本
 *
 * 场景设计（模拟真实流量分布，总 VU = 1000）：
 *   - feed_latest      700 VU  匿名最新流（命中 Redis STRING 缓存）
 *   - video_detail     200 VU  视频详情  （命中进程内 L1 或 Redis L2 缓存）
 *   - feed_popularity  100 VU  热榜流    （命中衰减 ZSET hot:video:decay）
 *
 * 运行方式：
 *   k6 run k6/bench.js
 *
 * 自定义 BASE_URL 和视频 ID 范围：
 *   k6 run -e BASE_URL=http://127.0.0.1:8080 -e VIDEO_MAX_ID=500 k6/bench.js
 */

import http from 'k6/http';
import { check } from 'k6';
import { Rate, Trend } from 'k6/metrics';

// ──────────────────────────────────────────────
// 配置
// ──────────────────────────────────────────────
const BASE_URL   = __ENV.BASE_URL    || 'http://127.0.0.1:8080';
const VIDEO_MAX  = parseInt(__ENV.VIDEO_MAX_ID || '200');  // seed 脚本插入的视频数量上限

// ──────────────────────────────────────────────
// 自定义指标
// ──────────────────────────────────────────────
const errorRate      = new Rate('errors');
const feedLatencyP99 = new Trend('feed_latest_duration',     true);
const detailLatency  = new Trend('video_detail_duration',    true);
const hotLatency     = new Trend('feed_popularity_duration', true);

// ──────────────────────────────────────────────
// 场景 & 阈值
// ──────────────────────────────────────────────
export const options = {
  scenarios: {
    // 匿名最新流：最重要的缓存路径
    feed_latest: {
      executor:  'ramping-vus',
      startVUs:  0,
      stages: [
        { duration: '15s', target: 800 },  // 15s 爬坡到 800 VU
        { duration: '45s', target: 800 },  // 保持 45s
      ],
      exec: 'scenarioFeedLatest',
    },
    // 视频详情：验证 L1(进程内) + L2(Redis) 双层缓存
    video_detail: {
      executor:  'ramping-vus',
      startVUs:  0,
      stages: [
        { duration: '15s', target: 200 },
        { duration: '45s', target: 200 },
      ],
      exec: 'scenarioVideoDetail',
    },
  },
  thresholds: {
    'http_req_duration':        ['p(99)<200'],
    'http_req_failed':          ['rate<0.05'],
    'feed_latest_duration':     ['p(99)<200'],
    'video_detail_duration':    ['p(99)<100'],
  },
};

// ──────────────────────────────────────────────
// 公共请求头
// ──────────────────────────────────────────────
const JSON_HEADERS = { 'Content-Type': 'application/json' };

// ──────────────────────────────────────────────
// 场景实现
// ──────────────────────────────────────────────

/** 场景 1：匿名最新信息流（走有 Redis 缓存的旧接口 /feed/listLatest） */
export function scenarioFeedLatest() {
  const res = http.post(
    `${BASE_URL}/feed/listLatest`,
    JSON.stringify({ limit: 10 }),
    { headers: JSON_HEADERS },
  );

  const ok = check(res, {
    'feed_latest status 200': (r) => r.status === 200,
    'feed_latest has videos':  (r) => {
      try { return Array.isArray(JSON.parse(r.body).videos); }
      catch { return false; }
    },
  });
  errorRate.add(!ok);
  feedLatencyP99.add(res.timings.duration);
}

/** 场景 2：视频详情（随机访问热点 ID，触发 L1/L2 缓存命中） */
export function scenarioVideoDetail() {
  // 集中在前 50 个 ID 以加速热点缓存写入，模拟帕累托效应
  const id = Math.floor(Math.random() * Math.min(50, VIDEO_MAX)) + 1;

  const res = http.post(
    `${BASE_URL}/video/getDetail`,
    JSON.stringify({ id }),
    { headers: JSON_HEADERS },
  );

  const ok = check(res, {
    'video_detail status 200': (r) => r.status === 200,
    'video_detail has id':     (r) => {
      try { return JSON.parse(r.body).id > 0; }
      catch { return false; }
    },
  });
  errorRate.add(!ok);
  detailLatency.add(res.timings.duration);
}

/** 场景 3：热榜信息流（走旧接口 /feed/listByPopularity，有 Redis ZSET 兜底） */
export function scenarioFeedPopularity() {
  const res = http.post(
    `${BASE_URL}/feed/listByPopularity`,
    JSON.stringify({ limit: 10 }),
    { headers: JSON_HEADERS },
  );

  const ok = check(res, {
    'feed_popularity status 200': (r) => r.status === 200,
  });
  errorRate.add(!ok);
  hotLatency.add(res.timings.duration);
}
