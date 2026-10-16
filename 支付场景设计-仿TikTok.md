# 支付场景：架构与可行性

对象：`FEED` 仓库 `test` 分支。场景仿 TikTok 短视频，不做商城。

支付已包含复式资金账、渠道订单落库、回调重试、原路退款、模拟拒付和待结算转已结算。送礼、提现、RocketMQ、真实商户号还没做。

## 做什么

法币买金币，金币给视频送礼，作者得钻石，钻石再提现。金币和钻石不能互换，避免买币立刻套现。

加热（花金币买曝光）是第二阶段，复用扣币和现有热榜。直播礼物、Shop、付费短剧不做。演示用 Mock 支付，不接真实商户。

## 架构

金钱和消息队列分开。扣款、入账以 MySQL 事务为准。队列只负责关单、回调补偿、热度、通知。

这个仓库的队列是 RabbitMQ，Compose 和 K8s 都已接好。支付异步消息用 RocketMQ 更合适：事务消息能把「本地入账」和「发出下游事件」绑在一起，延迟消息能直接做 15 分钟关单，重试和死信也更适合资金流水。RabbitMQ 要自己用 outbox 轮询和延迟插件补这两件事。

当前阶段仍用 RabbitMQ。热度事件已经在 `video.popularity.events` 上，再引入 RocketMQ 会多一套中间件，收益只在支付可靠性，不在送礼功能本身。账本正确性靠数据库事务和幂等键，不靠换队列。以后支付独立部署时，再把关单、回调补偿、对账切到 RocketMQ；热榜继续留在现有 RabbitMQ。

```text
Vue
  |  JWT，POST + JSON
post-service / 单体 API
  |  同步事务：钱包 + 账本 + 订单
MySQL
  |  同一事务写 outbox
pay-worker
  |  投递后才标 sent
RabbitMQ video.popularity.events
  |
现有 PopularityWorker -> Redis 热榜
```

新模块 `backend/internal/pay`，沿用 handler / service / repo。路由挂到 `SetRouter` 和 `SetPostRouter`。`feed-api` 只读礼物计数，不碰钱包。

三条链路不要合成一个接口：

| 动作 | 一致性 | 原因 |
|---|---|---|
| 充值 | 异步 | 等渠道回调才入账 |
| 送礼 | 同步 | 扣的是站内余额 |
| 提现 | 异步 | 等打款结果才消耗冻结钻石 |

核心表：`wallet`、`ledger_entry`、`coin_package`、`pay_order`、`gift_catalog`、`gift_order`、`video_gift_stat`、`withdraw_order`、`pay_callback_log`、支付自己的 `outbox`。金额用整数分，虚拟币用整数个。余额是账本投影，不能单独改。

送礼在一个事务里完成：按 `account_id` 升序锁双方钱包，扣金币，加钻石，写双方流水和礼物单，更新 `video_gift_stat`，插入 outbox。成功后才返回。热榜可以晚几秒。

充值入账用条件更新，只有 `RowsAffected = 1` 才加金币：

```text
UPDATE pay_order SET status='paid'
WHERE order_no=? AND status IN ('created','paying') AND amount_fen=?;
```

价格只读服务端目录。幂等键绑定 `account_id`。禁止给自己的视频送礼。

和现有热度的接点：outbox 发出 `{video_id, change}`，`change` 用礼物的 `popularity_weight`，走现有 `PopularityMQ.Update`。不要把推广视频直接加进自然热度；第二阶段用独立 ZSET `promote:active`，Feed 混排并标记 `is_promoted`。

## 可行性

能做，而且适合嵌进这个仓库。理由：

- 已有账号、视频、作者、JWT、`AdminAuth`、MySQL 事务、RabbitMQ 热度事件、Redis 热榜。送礼只是一次新的同步写，再发一条已有的热度增量。
- 前端已有视频详情、Feed 卡片、账号页，礼物盘和钱包是加页面，不改 Feed 主链路。
- 点赞那条「先发 MQ，失败再写库」不能用于支付。支付成功必须表示账已经提交。这是设计约束，不是做不到。
- 第一阶段表放在现有 `feedsystem` 库，`AutoMigrate` 注册即可。不必先拆支付库，也不必先拆新服务。`pay-worker` 单独进程，只处理 outbox、关单、对账。

做不到或先不做的部分：

- 真实支付宝、微信、IAP。缺商户号和回调公网地址。Mock 网关能把状态机和幂等跑通。
- 热门作者钱包行会串行。当前量级够用。按接收方分片入账是以后的事。
- 充值默认不自助退款。已送出的礼物不回收。

建议顺序：钱包和 Mock 充值，再送礼并推热度，再提现，最后加热。第一阶段就能证明「付一次钱只入账一次」。
