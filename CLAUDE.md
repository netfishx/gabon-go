# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目定位

本项目是 `/Users/ethanwang/projects/gabon`（Java 17 / Spring Boot 3.2 / MyBatis-Plus / MySQL / Redis）的 **Go 全量重写**，2026-07-03 启动。定位是**候选替代品**：验证可行即替换旧版接管现网（见 ADR-0002 及其 2026-07-03 修正）。无 API/schema 兼容义务（调用方配合修改），但质量按生产标准，schema 不关闭数据迁移之门。

产品一句话：短视频 + 邀请裂变 + 钻石钱包——用户看视频/签到/做任务/邀请赚钻石，钻石买 VIP，充值买钻、提现变现。

## 必读文档（按序）

1. `CONTEXT.md` — 领域词汇表。所有讨论与命名以此为准
2. `docs/adr/0001`–`0006` — 六个关键决策：Go 换栈、候选替代品定位、ffmpeg 自建转码、单 Postgres 数据层、云服务约束（仅 S3 + 阿里云 CDN 豁免）、流水纯账本
3. `docs/feature-checklist.md` — **验收基准**：A–M 全量功能清单 + 行为差异清单（明确不复刻的旧 bug）+ 技术决策附录
4. `docs/skeleton.md` + `docs/schema.md` — 骨架与 schema 设计基线（2026-07-03 敲定）

## 技术栈

Go · chi · sqlc + pgx · PostgreSQL（唯一数据层，无 Redis）· goose 迁移 · robfig/cron 进程内调度 · ffmpeg 转码（DB 任务表 + worker 池）· S3 协议存储（endpoint 可配，本地 MinIO）· 阿里云 CDN 分发 · slog · 环境变量配置（fail fast）

形态：**单 module、单二进制、单服务**（/api 客户端 + /admin 后台同进程）；测试三层（table-driven 单测 / testcontainers 真库集成 / httptest E2E）。

## 实现时的规矩

- 实现每个功能域之前，回旧仓库 `/Users/ethanwang/projects/gabon` 核对 Java 实现细节（feature-checklist 保留了旧类名线索）；但行为差异清单里列明的旧 bug **不复刻**
- 框架/库的具体 API 一律查最新文档，不依赖预训练记忆
- 设计讨论用 grilling + domain-modeling（/grill-with-docs）：一次一问、附推荐答案；术语敲定即更新 CONTEXT.md，重大取舍写 ADR
- 时区锚点 Asia/Shanghai（周期任务重置、榜单结算依赖它）

## 当前进度

**M1–M5 已合并**；里程碑与 PR 映射见 README 路线图，未完成工作见 issue tracker。当前推进 **M6——充值提现 + Provider 四渠道 + payment_events**（PRD #63、竖切片 #64–#69 已就绪）。

**关键接入约定**（新域接入复用）：①资金操作只走 `wallet.Service` 的 `XxxTx` 原语同事务注入，撞幂等约束以 `errors.Is(ErrAlreadyGranted)` 识别（事务须整体回滚）；②`video_count` 语义 = 已发布且未删除的作品数（审核 +1 / 删除 −1 对称维护）；③有效用户判定原语 `customer.MarkValidIfQualifiedTx`（翻转 CAS + 邀请人行锁 cap 检查 + 同事务发奖）；④任务进度推进 `task.Service.Advance`（独立事务自管，主事件提交后调用、失败仅记日志；幂等三层）；⑤cron 基建 `internal/cron`（robfig/cron + Asia/Shanghai + 启动幂等 catch-up + graceful stop），M7 周/月榜结算复用；⑥奖励/扣款发放涉资金一律钱包事务原语 + 各自幂等约束（签到唯一键 / VIP CAS / 广告库存条件 UPDATE）。
