# AGENTS.md — Feed 流微服务化项目

> 交接文档：本文件由上一个会话（ongrid 环境搭建）产出。
> 目的：把已有环境、目标、约束一次性交代清楚，避免新会话重复踩坑或推翻既有决策。

## 一、这个项目要做什么

把本仓库（原单体 feed 流项目）**拆成微服务**，容器化后部署到一个**已经存在的 K8s 集群**，
并接入 ongrid 可观测性平台，形成完整闭环：

```
故障注入 → ongrid 告警 → AI 根因分析 → 修复 → 复盘
```

**最终交付**：
1. 4 个可独立部署的服务（Dockerfile + K8s manifests）
2. 每个服务带可触发的故障注入开关
3. 在 ongrid UI 里能看到服务拓扑、日志、指标
4. 一份压测对比数据（单体本地 vs 微服务上 K8s）
5. 简历可用的项目条目草稿

**这不是一个"学习 K8s"的项目** —— 环境早就搭好了，目标是**产出能演示、能写进简历的成果**。

## 二、已有环境（不要重建，不要改）

### 云端 Manager（香港服务器）
- 地址：`https://162.211.183.156`（自签证书，浏览器需点"继续"）
- ongrid v0.17.4，docker compose 部署，安装目录 `/opt/ongrid`
- 端口：443（UI/API）、80（跳转）、40012（Edge 隧道）
- 配置：`/opt/ongrid/.env`（**注意：不是安装包目录里的 .env**）
- 服务器：雨云香港，Debian 12，4C8G，100GB SSD，**只剩约 6GB 内存，不要再往上堆业务负载**
- 里面的 MySQL/Prometheus/Loki/Tempo/Grafana 是 ongrid 自用，**不要复用、不要连**

### 本地 K8s 集群
- 位置：Windows + WSL2（Ubuntu 24.04，用户 `hu`）+ Docker Desktop
- 集群名：`ongrid-local`，**3 节点**（1 control-plane + 2 worker），K8s `v1.31.0`
- kind 配置：`/mnt/c/Users/32256/Desktop/ongrid/deploy/kubernetes/kind-ongrid-local.yaml`
- 已装：kind v0.24.0、kubectl、helm v3.16.3、metrics-server（带 `--kubelet-insecure-tls`）
- NodePort 30080 已通过 extraPortMapping 暴露到 `localhost:30080`

### ongrid Edge（已接入，正常）
- 命名空间 `ongrid-system`，8 个 Pod 全 `1/1 Running`：
  `controller` / `node`×3 / `telemetry-gateway`×2 / `metrics-scraper` / `kube-state-metrics`
- 集群在 ongrid UI 里状态 online，3 个 `K8s Node` 设备已注册
- 参考仓库（ongrid 源码）：`C:\Users\32256\Desktop\ongrid`

## 三、服务拆分方案

| 服务 | 技术 | 职责 | 为什么这么拆 |
|---|---|---|---|
| `feed-api` | Go + Gin | Feed 拉取（高 QPS 主链路） | 压测与故障注入主战场 |
| `post-service` | Go | 发帖、写路径 | 与 Feed 形成读写分离 |
| `rank-worker` | Go + Redis ZSET | 榜单计算，定时任务 | 可制造慢任务/长时间占用 |
| `agent-worker` | Go + LLM | 消费 MQ 调 LLM 做 Agentic 发布 | 有外部依赖，天然有超时风险 |

**中间件**：MySQL、Redis、NSQ，全部用 StatefulSet 跑在集群内（kind 自带 local-path provisioner）。

**原则**：
- **复用现有业务代码，不要重写业务逻辑**（项目里已有的 Feed/榜单/Agent 代码就是资产）
- 不引入新的重型中间件（不上 Kafka/Istio/Service Mesh，保持 3 节点能跑）
- 每个服务独立 Dockerfile，多阶段构建，非 root 运行

## 四、故障注入设计（这是项目的核心价值）

每个服务暴露一个受控开关（HTTP endpoint 或 ConfigMap 驱动），能主动制造：

| 故障 | 注入方式 | ongrid 应该发现什么 |
|---|---|---|
| 慢 SQL | `feed-api` 加 `SELECT SLEEP(n)` | 接口 P99 上升 + MySQL 指标异常 |
| 缓存击穿 | 随机删除 Redis key | Redis QPS 尖刺 + DB 负载上升 |
| MQ 堆积 | 停止 `agent-worker` 消费 | 队列深度 + 消费延迟 |
| 下游超时 | LLM 调用注入超时 | 链路 trace 断点 + 错误率 |
| Pod OOM | 调小 memory limit | K8s Event + 容器重启 |
| 节点故障 | `kubectl drain <node>` | 节点 NotReady + Pod 重调度 |

**"能被搞坏"的系统才是好的 AIOps 实验场。** 一个永远健康的系统讲不出根因分析的故事。

## 五、必须遵守的约束（血泪教训）

1. **终端粘贴会丢换行**（用户环境实测）。给用户的每条命令都写成**单行**（用 `;` / `&&` 连接），不要给多行块让他粘贴。
2. **kind 节点看不到本地构建的镜像**。每次 build 后必须：
   `kind load docker-image <image>:<tag> --name ongrid-local`
   且 Deployment 里 `imagePullPolicy: IfNotPresent`（或 `Never`）。
3. **港机改 `.env` 的坑**：改 `/opt/ongrid/.env` 后 `docker compose up -d` 会重建
   `ongrid`/`prometheus`/`loki`/`tempo`，但 **nginx 用的是静态 upstream，不会重建** →
   它的上游 IP 立刻过期 → **遥测链路全部 502**。所以必须补一条：
   `cd /opt/ongrid && sudo docker compose up -d && sudo docker compose restart nginx`
4. **雨云 IP 会变**（重装/换机）。Manager 地址被写死在 Edge 的 Helm values 里，
   IP 一变就要在所有集群重新 `helm upgrade`。**这也是应该尽快上域名的原因。**
5. **LLM 相关环境变量在港机 `.env` 里没有 `ONGRID_` 前缀**：
   是 `OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL`。
6. **绝对不要把凭证写进任何文档、代码、commit**：ongrid admin 密码、Edge 的
   `controllerBootstrapToken` / `nodeBootstrapToken`、LLM API key，都不行。
7. **港机内存已紧张**（Manager 占约 1.5–4GB / 7.8GB），业务负载一律跑在本地 kind 上。

## 六、验收标准

- [ ] 4 个服务在 `ongrid-local` 集群里全部 `Running`
- [ ] 中间件（MySQL/Redis/NSQ）以 StatefulSet 运行且数据可持久（集群内）
- [ ] ongrid UI 的**集群页**能看到 workload/pod 列表和指标
- [ ] ongrid UI 的**日志页**能查到服务的容器日志
- [ ] 至少 3 种故障能被注入，且 ongrid 能发现异常
- [ ] 至少 1 次用 AI 助理完成根因定位（带证据的结论，不是泛泛而谈）
- [ ] 一份 k6 压测对比（单体 vs 微服务上 K8s）

## 七、工作方式（用户偏好）

- **一次只做一件事**，不要一次性生成大量代码；每步给可验证的结果
- 涉及 K8s 的每步都给出**验证命令 + 预期输出**
- 用户是 2027 届校招生，有 3 段后端实习（Go/Java、云数据库控制面、AI 应用），
  技术底子好，**可以直接讲技术决策和权衡**，不需要科普基础概念
- **中文输出**
- 先问清楚再动手：仓库现在的结构、哪些代码可复用，先确认拆分方案可行再写代码

## 八、不要做的事

- ❌ 不要重建 K8s 集群、不要重装 ongrid Manager（环境已就绪）
- ❌ 不要动港机上的 ongrid 部署（除非明确是修 bug）
- ❌ 不要为了"技术先进"引入 Service Mesh / Kafka / 完整 GitOps 流水线
- ❌ 不要把 ongrid 的数据库当成自己业务的数据库
- ❌ 不要在简历/文档里把开源项目 ongrid 说成"自己开发的"——
  如实写"基于开源 ongrid 搭建的实践"，或等有了真实 PR 再写"开源贡献"
