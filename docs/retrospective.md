# 复盘文档 — Feed 流微服务化

> 骨架：`[待填]` 处等压测 + ongrid 验证后补上实测数据，即可当作项目复盘/答辩素材。

## 1. 背景与目标
把单体 feed 流拆成微服务、容器化部署到已有 kind 集群，接入 ongrid 形成 AIOps 闭环：
`故障注入 → ongrid 告警 → AI 根因分析 → 修复 → 复盘`。

## 2. 拆分方案与关键决策（含理由）
| 服务 | 职责 | 关键决策 |
|---|---|---|
| feed-api | feed 只读主链路 | 复用 internal/feed；直连单库读 like/social/video 表（不搞 RPC） |
| post-service | 写路径 + 4 worker | 单进程（HTTP + worker 同跑）；拥有 AutoMigrate |
| rank-worker | 热度榜 | 只依赖 Redis + RabbitMQ，不连 MySQL |
| agent-worker | LLM 内容编排 | **暂缓**（AI 服务不可用） |

- MQ 沿用 **RabbitMQ**（代码现状），未引入 NSQ/Kafka。
- **单 module 多 main**，复用 internal 包，未拆多 module/共享库。
- 中间件 StatefulSet 持久化；配置 ConfigMap/Secret 注入，env 覆盖 YAML。

## 3. 拆分时发现「AGENTS.md 与代码不匹配」的地方
1. AGENTS.md 写 **NSQ**，代码实为 **RabbitMQ**。
2. rank-worker 是**事件驱动**（消费 popularity 事件写 ZSET），不是 AGENTS.md 说的「定时任务」。
3. LLM 用 **Anthropic Claude**，不是 AGENTS.md 说的 OpenAI（`OPENAI_*`）。
4. 拆分表里缺失 **account/auth**，实际归入 post-service。

## 4. 故障注入与可观测验证
| 故障 | 注入方式 | 观察到的现象 | ongrid 是否发现 |
|---|---|---|---|
| 慢 SQL | feed-api `/debug/fault/slow-sql`（SELECT SLEEP） | P99 上升（实测 3s+） | [待填] |
| 缓存击穿 | feed-api `/debug/fault/cache-breakdown`（SCAN 删 key） | DB 负载上升 | [待填] |
| Pod OOM | 调小 memory limit | 容器重启 / K8s Event | [待填] |
| 节点故障 | `kubectl drain <node>` | 节点 NotReady / Pod 重调度 | [待填] |

## 5. 压测对比（feed 主链路，k6 集群内直连 feed-api）

> 800 VU 那组暴露了瓶颈，故补跑一组 300 VU 健康基线。单体本地基线待补。

| 负载 | 指标 | 单体(本地) | 微服务(K8s) |
|---|---|---|---|
| 300 VU | 错误率 | [待填] | **0.00%** |
| 300 VU | 吞吐(RPS) | [待填] | **942/s** |
| 300 VU | 中位数延迟 | [待填] | **99ms** |
| 300 VU | P95 延迟 | [待填] | **1.09s** |
| 800 VU | 错误率 | [待填] | **35.44%**（超时） |
| 800 VU | 吞吐(RPS) | [待填] | **1074/s** |

**结论（微服务上 K8s）**：
- 300 VU 是可持续容量：0 错误、942 RPS、中位 99ms、P95 1.09s。
- 800 VU 撞上硬瓶颈：35% 请求超时。根因是统一入口 `/feed/list?query_type=latest` **无缓存直连 MySQL**，
  打爆单实例 MySQL 的默认 `max_connections=151`（已临时调到 1000 验证）；走 Redis 的 popularity 场景（200 VU）失败率仅 13%，佐证瓶颈在 DB。
- 优化方向（后续）：latest lister 补 Redis 缓存（复用旧接口 `ListLatest` 的 cache-aside + 锁）、MySQL 分库/读写分离。

## 6. AI 根因分析（1 次带证据）
[待填：描述一次故障 → ongrid 告警 → AI 助理定位根因的过程和结论]

## 7. 踩坑与教训（血泪）
- 终端粘贴丢换行 → 所有命令写成单行（`;`/`&&` 连接）。
- kind 节点看不到本地镜像 → 每次 build 后 `kind load docker-image`，且 `imagePullPolicy: IfNotPresent`。
- 港机改 `.env` 后 nginx 静态 upstream 不重建 → 遥测全 502，必须补 `docker compose restart nginx`。
- Docker 镜像加速器 `1ms.run` 返回 EOF → 换源/直连。
- go-redis v9 的 `Scan` 返回**单页 ScanCmd**（需游标循环），不是迭代器 → 首版编译报错。
