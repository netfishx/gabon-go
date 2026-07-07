# 旧版充值超时机制核对（M6 · checklist G）

> 旧仓库：`/Users/ethanwang/projects/gabon`（Java）。2026-07-07 核对，服务于 gabon-go M6 充值提现设计。
> 这是 CLAUDE.md 自项目启动标记的**唯一遗留核对项**（"实现 M6 前须回旧仓库核对充值超时时长"），至此清除。
> 一句话：**超时 10 分钟（全局单值、配置驱动，非每渠道）；扫描 fixedDelay 5 分钟；先查单再取消；PROCESSING→CANCELLED；无 expire 字段落库，create_time + 时长现算。**

## 关键参数（写入新版 config/schema）

| 参数 | 旧版值 | 来源 |
|------|--------|------|
| 充值超时阈值 | **10 分钟**（全局单一，非渠道差异） | `application.properties:59-63` `recharge.timeout.minutes=10` |
| 超时扫描节拍 | **5 分钟 fixedDelay**（非 cron、非 fixedRate） | `RechargeTimeoutScheduler.java:43` `@Scheduled(fixedDelayString="${...:300000}")` |
| 单轮批量上限 | 200 | `recharge.timeout.batch-size=200` |
| 取消前查单 | 默认 true（先向渠道查账，仍 PROCESSING 才取消） | `recharge.timeout.verify-before-cancel=true` |

配置类 `RechargeTimeoutProperties.java:35` 默认 `minutes=10`；注释要求 minutes ≥ 支付链接有效期（链接失效后用户无法再付）。无 prod/env/DB 覆盖，无按渠道区分。

## 逐题结论

1. **超时时长**：10 分钟，`recharge.timeout.minutes` 配置项（`application.properties:59`），非硬编码非每渠道。
2. **扫描调度**：`RechargeTimeoutScheduler.java:43`，`fixedDelay=300000ms`（5 分钟，上轮结束到下轮开始）。扫描条件（`:50-57`）：`order_type=RECHARGE(2) AND status=PROCESSING(3) AND deleted_flag IS NULL AND create_time < now-10min ORDER BY create_time LIMIT 200`；cutoff 扫描时现算 `Instant.now().minus(minutes, MINUTES)`。
3. **超时流程（先查后取消）**：`verify-before-cancel=true` 时先 `paymentService.queryAndReconcile`（`PaymentServiceImpl.java:208`）调 `PaymentProvider.query(cashOrder)` 查账，解析出终态则 `confirmPaymentResult` 推进+发钻；查账后 re-select 仍 PROCESSING 才 `cancelTimedOutRecharge`。查账失败（渠道不可用）只累加 retryCount、照常走取消。false 则跳过查账直接取消。
4. **状态机**：`CashOrderStatusEnum` PROCESSING(3)/SUCCESS(4)/FAILED(5)/CANCELLED(7)（1=待审核/2=已拒绝仅提现审核用）。充值创建即 PROCESSING；超时 PROCESSING→CANCELLED（`PaymentCallbackServiceImpl.java:71-79`），取消不动余额（钻石只在 SUCCESS 回调发）。**已成功不误杀**：`settle()` 内 `isFinalized` 短路 + `requireProcessing` 护栏（`:99-103,146-150`）。
5. **无 expire 字段落库**：订单表只有 `create_time`（+ 索引 `idx_cash_order_create_time`），V1.8 加的时间戳仅 `notified_time`/`queried_time`。超时靠 create_time + 固定时长现算，不快照。→ **新版若要每渠道差异化超时，需新增 `expires_at`（创建时快照 = created_at + 渠道时长）**。
6. **回调 vs 超时竞态**：共用 `settle()` + `@Transactional` + 读-判-写护栏；超时侧先查账再重读缩小窗口；取消后回调进 settle 见 CANCELLED 即静默不重复发钻。**残留风险（新版须修）**：`BaseDO` 无 `@Version`、无 `SELECT FOR UPDATE`、`finalizeCashOrder` 用 `updateById` 盲更新（WHERE 不含 status=PROCESSING），并发下极端窗口可能"钻石已发但订单被改 CANCELLED"。→ **新版改条件 UPDATE（`WHERE status='pending_payment'`）或乐观锁**。
7. **其他时长**：无独立查单轮询 scheduler（queryAndReconcile 仅超时清扫 verify 分支与按需触发）；三渠道 Provider 下单请求不下发链接/二维码有效期（用渠道默认）。

## 对 M6 设计的直接影响

- **超时时长 = 10 分钟、扫描 5 分钟**：复刻。新版 cron 基建（internal/cron，M5 已建）加"充值超时取消"job，5 分钟一跑、幂等 catch-up。
- **schema 基线已优于旧版**：`recharge_orders` 已设 `expires_at`（创建时快照），无需现算——比旧版更规范，支持将来每渠道差异化。回调幂等三重保障（order_no 唯一 + (provider, provider_order_no) 唯一 + 已终态短路）也强于旧版盲更新。
- **超时取消前先查账**：复刻"先调 Provider.query 查单，仍 pending_payment 才取消"，避免误杀在途到账。
- **状态命名**：新版用 `pending_payment/succeeded/failed/cancelled`（schema 基线 recharge_order_status），语义对齐旧版 PROCESSING/SUCCESS/FAILED/CANCELLED。
- **竞态**：新版一律条件 UPDATE（`WHERE status='pending_payment'`）翻转，堵旧版盲更新的窗口——延续 M4/M5 的锁行/条件 UPDATE 范式。
