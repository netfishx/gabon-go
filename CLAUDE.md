# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目定位

本项目是 `/Users/ethanwang/projects/gabon`（Java 17 / Spring Boot 3.2 / MyBatis-Plus / MySQL / Redis）的 **Go 全量重写**，2026-07-03 启动。定位是**平行实验品**：不接管旧系统现网流量、不迁移数据，API/schema/密码算法全部重新设计（见 ADR-0002）。

产品一句话：短视频 + 邀请裂变 + 钻石钱包——用户看视频/签到/做任务/邀请赚钻石，钻石买 VIP，充值买钻、提现变现。

## 必读文档（按序）

1. `CONTEXT.md` — 领域词汇表（38 术语）。所有讨论与命名以此为准
2. `docs/adr/0001`–`0005` — 五个关键决策：Go 换栈、平行实验品、ffmpeg 自建转码、单 Postgres 数据层、云服务约束（仅 S3 + 阿里云 CDN 豁免）
3. `docs/feature-checklist.md` — **验收基准**：A–M 全量功能清单 + 10 条行为差异（明确不复刻的旧 bug）+ 技术决策附录

## 技术栈

Go · chi · sqlc + pgx · PostgreSQL（唯一数据层，无 Redis）· goose 迁移 · robfig/cron 进程内调度 · ffmpeg 转码（DB 任务表 + worker 池）· S3 协议存储（endpoint 可配，本地 MinIO）· 阿里云 CDN 分发 · slog · 环境变量配置（fail fast）

形态：**单 module、单二进制、单服务**（/api 客户端 + /admin 后台同进程）；测试三层（table-driven 单测 / testcontainers 真库集成 / httptest E2E）。

## 实现时的规矩

- 实现每个功能域之前，回旧仓库 `/Users/ethanwang/projects/gabon` 核对 Java 实现细节（feature-checklist 保留了旧类名线索）；但行为差异清单里列明的旧 bug **不复刻**
- 框架/库的具体 API 一律查最新文档，不依赖预训练记忆
- 设计讨论用 grilling + domain-modeling（/grill-with-docs）：一次一问、附推荐答案；术语敲定即更新 CONTEXT.md，重大取舍写 ADR
- 时区锚点 Asia/Shanghai（周期任务重置、榜单结算依赖它）

## 当前进度

设计决策全部完成（18 轮 grilling），代码零行。**下一步：项目骨架 + Postgres schema 设计**（把旧版 22 张表按新词汇表重建模）。
