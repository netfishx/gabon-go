# 项目骨架与横切约定

来源：2026-07-03 骨架 + schema 设计 grilling（16 问）。数据层设计见 [schema.md](./schema.md)。

## 目录结构（双轴包组织）

handler 按**面**切（客户面 / 后台面），业务按**功能域**切，sqlc 统一生成单包。

```
gabon-go/
├── cmd/gabon/main.go        # 装配：config → pgx pool → goose 迁移 → cron → 转码 worker → HTTP server
├── internal/
│   ├── api/                 # /api/v1 handler（Customer 面），标准库签名
│   ├── admin/               # /admin/v1 handler（Admin 面）
│   ├── apierr/              # 错误类型 + 字符串错误码常量 + HTTP status 映射 + WriteJSON
│   ├── auth/                # JWT 签发/校验 + 鉴权中间件（customer/admin 双主体一套实现）
│   ├── config/              # 环境变量 → 单 Config struct，启动校验 fail fast
│   ├── db/                  # sqlc 生成代码（单包）
│   │   ├── migrations/      # goose 纯 SQL 迁移，embed 进二进制启动自动执行
│   │   └── queries/         # 按域拆分的 .sql 查询文件（wallet.sql、video.sql…）
│   ├── customer/            # 客户域：注册/资料/邀请裂变/有效用户判定
│   ├── wallet/              # 钱包域（核心被依赖域）：余额 + 流水
│   ├── payment/             # 现金订单 + Provider 接口/注册表 + pay126/stpay/hotcl/mock
│   ├── video/               # 视频域：上传 confirm/状态机/互动/Feed
│   ├── transcode/           # 转码任务认领 + ffmpeg worker 池（ADR-0003）
│   ├── ranking/             # 热度周/月榜结算 cron
│   ├── task/                # 周期任务 + 限时领取任务
│   ├── signin/              # 签到 + 里程碑
│   ├── vip/                 # VIP 等级购买
│   ├── follow/              # 关注
│   ├── ad/                  # 广告 + 广告商
│   ├── report/              # 后台报表（DAU/播放/广告/日报）
│   └── storage/             # S3 预签名 + 对象操作（endpoint 可配）
├── Makefile
├── sqlc.yaml
└── go.mod
```

依赖方向（单向，禁止反向与环）：

```
api / admin  →  各功能域  →  internal/db
各功能域     →  wallet（发奖/扣款）、task（进度推进）      ← 仅此二者可被其他域依赖
所有层       →  apierr / auth / config / storage（横切工具包）
```

## 数据访问

- 域服务**直接持有** `*db.Queries` + `*pgxpool.Pool`，不垫 repository 接口
- 跨域事务（如任务发奖 = 进度翻转 + 入账 + 流水）用 `Queries.WithTx(tx)` 单事务贯穿
- 关键约束（`WHERE balance >= amount` 等）由 testcontainers 真库集成测覆盖，不 mock 数据层

## HTTP 面

- 路由：chi，`/api/v1/*` 与 `/admin/v1/*` 两个子路由，各挂各的鉴权中间件
- 中间件栈：RequestID / Recoverer / slog 请求日志 / 鉴权（不用 `middleware.RealIP`——chi 5.3 起因 IP 伪造漏洞废弃，客户端 IP 将来按可信代理链单独处理）
- 响应形状 **status-first**：
  - 成功：2xx + data 直出（无 envelope 剥壳）
  - 失败：4xx/5xx + `{"code": "WALLET_INSUFFICIENT_BALANCE", "message": "..."}`
  - HTTP 状态承载大类，业务码承载细类；错误码为**大写蛇形字符串**（`域_语义`），集中定义于 `internal/apierr`，预计 20–30 个

## 认证

- 无状态单 JWT（约 7 天），`/refresh` 到期前换新；**无 token/session 表**
- claim：`sub`=主体 id，`aud`=`customer`|`admin`，`iat`/`exp`
- 鉴权中间件解析后**点查主体状态**：封禁（`status=banned`）即时生效
- 改密踢下线：`iat < password_changed_at` 则拒绝
- 登出 = 客户端删 token，无服务端状态

## config / 日志 / 生命周期

- 配置：环境变量 → 单 Config struct，启动校验缺失即 fail fast，密钥不入文件
- 日志：slog 结构化 JSON
- main 装配顺序：config → pool → 迁移 → cron 注册 → 转码 worker → HTTP；graceful shutdown 逆序关停
- cron（robfig/cron，Asia/Shanghai 锚定）：周榜（周一 00:01）、月榜（每月 1 日 00:02）、限时任务过期（每 5 分钟）、充值超时取消。**旧版"周期任务每日重置"cron 不再需要**（见 schema.md 任务域 period_key 设计）
- 结算类 cron 一律**幂等 catch-up**：触发时（含启动时）补齐所有缺失周期

## lint 与测试

| 决策点 | 选型 |
|--------|------|
| 格式化 | gofumpt（gofmt 严格超集） |
| Lint | golangci-lint v2，精选启用：govet / staticcheck / errcheck / revive / gosec / sqlclosecheck / bodyclose（不开全量；版本与配置格式实现时查最新文档，v2 与 v1 配置不兼容） |
| 断言 | 标准库 `testing` + `google/go-cmp`（不引 testify，拒绝断言 DSL） |
| 单测 | table-driven，纯逻辑不碰库 |
| 集成测 | testcontainers-go 起 Postgres 18（与生产同版本锚定） |
| E2E | 标准库 `httptest` 挂完整 chi router；ffmpeg 用小样本真转码冒烟 |
| 任务入口 | Makefile：`make lint / test / build / migrate` |

## 落地顺序（M1–M7）

每个里程碑以 feature-checklist 对应项的 E2E 通过为完成标准：

| 里程碑 | 内容 | 定位 |
|--------|------|------|
| M1 | 骨架 + 全量 goose 迁移（一次落 28 表，按域拆文件）+ auth + customers 注册/登录 | 一切的根 |
| M2 | wallet + transactions | 核心被依赖域，资金不变量最先钉死 |
| M3 | 视频管线：预签名上传 → confirm → 转码 worker → 审核 → Feed | 基建最重（S3/MinIO、ffmpeg、DB 队列），风险前置 |
| M4 | 邀请裂变 + 有效用户判定 | 依赖 M2（发奖）+ M3（有作品） |
| M5 | 奖励族：任务 + 签到 + VIP + 广告 | "事件→进度→发奖"同构，流水线铺开 |
| M6 | 充值提现 + Provider 四渠道 + payment_events | 资金外联最敏感，放在钱包语义锤打成熟之后 |
| M7 | 关注 + 榜单结算 + 报表 + admin 收尾 | 低耦合尾部 |

## 工程流程

- **里程碑分支 + PR**：每个 M 一条 `feat/mN-*` 分支，完成后 PR 合入 main；main 保持"每个合并点全量测试通过"
- **CI**：GitHub Actions 最小配置——`make lint` + `make test` 两个 job（testcontainers 真库、runner 装 ffmpeg 冒烟）；不搞构建矩阵与部署流水线。CI 属开发基建，不在 ADR-0005 运行时云服务禁区内

## 平台版本锚点

- PostgreSQL **18**（testcontainers 与生产同版本）
- 依赖库具体版本与 API 一律实现时查最新文档，不依赖预训练记忆
