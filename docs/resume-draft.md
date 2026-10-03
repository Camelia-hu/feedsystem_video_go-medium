# 简历条目草稿 — 短视频 Feed 流微服务化 + AIOps 实践

> 用法：直接取下面任意一段放进简历项目经历；`[待填]` 处填入你实测的压测数字。
> 诚实边界（务必遵守）：ongrid 是开源项目，只能写「基于开源 ongrid 搭建」，**不要**写「自研 ongrid」。

## 项目名
短视频 Feed 流系统：单体拆微服务 + 容器化部署 + 可观测性/故障注入闭环

## 一句话简介
把一个 Go 单体短视频 Feed 流（推拉混合、多级缓存、热度榜）拆成 4 个可独立部署的微服务，
部署到 3 节点 kind(K8s v1.31) 集群，基于开源 ongrid 搭建可观测闭环，跑通「故障注入 → 告警 → AI 根因分析 → 复盘」。

## 技术栈
Go / Gin / GORM / MySQL / Redis / RabbitMQ / Docker / Kubernetes(kind) / Prometheus / k6 / ongrid(开源)

## 项目亮点（可写进简历的 bullets）
- 按读写路径把单体拆为 feed-api（高 QPS 读链路）/ post-service（写路径 + 4 个异步 worker）/ rank-worker（热度榜），
  **复用现有业务代码、不重写业务逻辑**；agent-worker（LLM 内容编排）独立拆分。
- 中间件 MySQL / Redis / RabbitMQ 以 StatefulSet 运行并持久化（local-path PVC），
  服务配置通过 ConfigMap/Secret 注入，镜像多阶段构建、非 root 运行，本地镜像 `kind load` 分发到 3 节点。
- 设计并实现**故障注入开关**（受控 HTTP 端点 + 内存态开关）：慢 SQL（`SELECT SLEEP`）拉高 P99、
  缓存击穿（SCAN 删 key）制造 DB 负载，用于验证可观测平台能否捕获异常。
- 用 k6 对 feed 主链路压测（800 VU 爬坡），对比单体本地 vs 微服务上 K8s 的 P99/错误率/吞吐：`[待填]`。
- 基于开源 ongrid 搭建可观测闭环（拓扑/日志/指标），用 AI 助理完成至少 1 次带证据的根因定位。

## 面试可讲的难点
- 推拉混合 Feed：大 V 读扩散（outbox）+ 普通作者写扩散（inbox fanout），按粉丝数阈值分流。
- 热度榜：Redis ZSET + 「预乘时间权重」实现指数衰减，读时无需重算，O(log N) 取 TopK。
- 拆分时识别出「账号鉴权」「静态文件服务」等隐性跨服务依赖，并给出归属与共享库决策。

## 诚实边界（别写错）
- ❌ 不写「自研 ongrid」→ ✅ 写「基于开源 ongrid 搭建的实践」。
- 数据要真实：压测数字、故障注入现象都填你实际测出来的。
