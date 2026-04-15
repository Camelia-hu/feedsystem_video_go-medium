/**
 * 冒烟测试：跑正式压测前先用此脚本验证各接口均可正常响应
 *
 * 运行：k6 run k6/smoke.js
 */

import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const HEADERS  = { 'Content-Type': 'application/json' };

export const options = {
  vus:        5,
  duration:  '10s',
  thresholds: {
    'http_req_failed':   ['rate<0.05'],
    'http_req_duration': ['p(95)<500'],
  },
};

export default function () {
  // 1. 最新流
  const r1 = http.post(`${BASE_URL}/feed/list`,
    JSON.stringify({ query_type: 'latest', limit: 5 }), { headers: HEADERS });
  check(r1, { 'latest 200': (r) => r.status === 200 });

  // 2. 热榜
  const r2 = http.post(`${BASE_URL}/feed/list`,
    JSON.stringify({ query_type: 'order_by_popularity', limit: 5 }), { headers: HEADERS });
  check(r2, { 'popularity 200': (r) => r.status === 200 });

  // 3. 视频详情（id=1，需要 DB 里有数据）
  const r3 = http.post(`${BASE_URL}/video/getDetail`,
    JSON.stringify({ id: 1 }), { headers: HEADERS });
  check(r3, { 'detail 200': (r) => r.status === 200 });
}
