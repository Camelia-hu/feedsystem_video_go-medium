/**
 * feed-api 专用压测脚本（微服务拆分后的只读主链路）。
 *
 * 拆分后 /feed/* 归 feed-api，/video/getDetail 归 post-service，
 * 因此压测 feed 主链路请用本脚本（单 BASE_URL 只打 feed-api）。
 *
 * 运行：
 *   k6 run -e BASE_URL=http://127.0.0.1:8080 k6/bench-feed.js
 *   k6 run -e BASE_URL=http://127.0.0.1:8080 -e VIDEO_MAX_ID=1000 k6/bench-feed.js
 */

import http from 'k6/http';
import { check } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const BASE_URL  = __ENV.BASE_URL || 'http://127.0.0.1:8080';

const errorRate     = new Rate('errors');
const latestLatency = new Trend('feed_latest_duration', true);
const hotLatency    = new Trend('feed_popularity_duration', true);

export const options = {
  scenarios: {
    feed_latest: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '15s', target: 250 }, // 15s 爬坡到 250 VU
        { duration: '45s', target: 250 }, // 保持 45s
      ],
      exec: 'latest',
    },
    feed_popularity: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '15s', target: 50 },
        { duration: '45s', target: 50 },
      ],
      exec: 'popularity',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.05'],
  },
};

const HEADERS = { 'Content-Type': 'application/json' };

export function latest() {
  const res = http.post(
    `${BASE_URL}/feed/list`,
    JSON.stringify({ query_type: 'latest', limit: 10 }),
    { headers: HEADERS },
  );
  const ok = check(res, { 'latest 200': (r) => r.status === 200 });
  errorRate.add(!ok);
  latestLatency.add(res.timings.duration);
}

export function popularity() {
  const res = http.post(
    `${BASE_URL}/feed/list`,
    JSON.stringify({ query_type: 'order_by_popularity', limit: 10 }),
    { headers: HEADERS },
  );
  const ok = check(res, { 'popularity 200': (r) => r.status === 200 });
  errorRate.add(!ok);
  hotLatency.add(res.timings.duration);
}
