# gabon-go

旧版 gabon（Java 17 / Spring Boot 3.2 / MySQL / Redis）的 **Go 全量重写**。定位是**候选替代品**：验证可行即替换旧版接管现网——无 API/schema 兼容义务（调用方配合修改），但质量按生产标准，schema 不关闭数据迁移之门（ADR-0002 及其修正）。

## 产品一句话

短视频 + 邀请裂变 + 钻石钱包：用户看视频、签到、做任务、邀请他人赚取钻石，钻石可购买 VIP；法币充值买钻、钻石提现变现。

## 技术栈

| 层 | 选型 |
|----|------|
| 语言 / HTTP | Go · chi（handler 全程标准库签名） |
| 数据 | PostgreSQL 18（唯一数据层，无 Redis）· sqlc + pgx · goose 迁移 |
| 调度 / 转码 | robfig/cron 进程内调度 · ffmpeg 自建转码（DB 任务表 + worker 池） |
| 存储 / 分发 | S3 协议对象存储（本地 MinIO）· 阿里云 CDN 回源 |
| 认证 / 日志 | 无状态 JWT 双主体 · slog 结构化 JSON |
| 工程 | gofumpt · golangci-lint v2 · testcontainers-go · GitHub Actions |

形态：**单 module、单二进制、单服务**（`/api/v1` 客户端 + `/admin/v1` 后台同进程）。云服务仅豁免 S3 与阿里云 CDN，其余自托管/进程内（ADR-0005）。

## 文档地图（按必读顺序）

1. [CONTEXT.md](./CONTEXT.md) — 领域词汇表，所有讨论与命名以此为准
2. [docs/adr/](./docs/adr/) — 六个关键决策：Go 换栈、候选替代品定位、ffmpeg 自建转码、单 Postgres 数据层、云服务约束、流水纯账本
3. [docs/feature-checklist.md](./docs/feature-checklist.md) — **验收基准**：A–M 全量功能清单 + 明确不复刻的旧行为差异
4. [docs/skeleton.md](./docs/skeleton.md) — 项目骨架与横切约定 + 里程碑序列与工程流程
5. [docs/schema.md](./docs/schema.md) — 28 表 schema 设计基线

## 当前状态与路线图

设计与旧版核对**全部完成**，代码零行。按里程碑落地，每个里程碑走 `feat/mN-*` 分支 + PR，以对应功能项的 E2E 通过为完成标准：

| 里程碑 | 内容 | 状态 |
|--------|------|------|
| M1 | 骨架 + 全量迁移 + 双主体认证 + 注册登录 | ✅ [PR #2](https://github.com/netfishx/gabon-go/pull/2) |
| M2 | 钱包 + 流水（纯账本） | ✅ [PR #9](https://github.com/netfishx/gabon-go/pull/9) |
| M3 | 视频管线：上传 → 转码 → 审核 → Feed | 待启动 |
| M4 | 邀请裂变 + 有效用户判定 | 待启动 |
| M5 | 奖励族：任务 + 签到 + VIP + 广告 | 待启动 |
| M6 | 充值提现 + 支付渠道 | 待启动 |
| M7 | 关注 + 榜单 + 报表 + 后台收尾 | 待启动 |

## 开发命令

```bash
make lint      # gofumpt + golangci-lint
make test      # 全量测试（testcontainers 起真 Postgres 18，需 Docker）
make build     # 编译单二进制
make migrate   # 手动执行迁移（服务启动时也会自动执行）
```

时区锚点 **Asia/Shanghai**（周期任务重置、榜单结算依赖它）；配置全部走环境变量，启动校验 fail fast。
