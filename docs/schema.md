# Postgres Schema 设计基线

来源：2026-07-03 骨架 + schema 设计 grilling（16 问）。旧版 22 张表按 [CONTEXT.md](../CONTEXT.md) 词汇表重建模为 **28 张**。本文是设计基线，列清单与索引以最终 goose 迁移为准；标注（**核对**）的点实现该域时须回旧仓库核实。

## 全局约定

| 约定 | 取值 |
|------|------|
| 主键 | `bigint GENERATED ALWAYS AS IDENTITY` 一统（迁移友好：旧库 bigint id 可原值迁入，见 ADR-0002 修正） |
| 对外标识 | `customers`/`videos` 加 `public_id`：12 位 base58 随机短码，唯一索引；API 对外只露短码，内部 join 全走 bigint |
| 时间 | 一律 `timestamptz`；`created_at`/`updated_at` NOT NULL DEFAULT `now()` |
| 软删 | `deleted_at timestamptz NULL`（NULL=存活） |
| 金额 | 钻石 = `bigint` 整数钻（1 元 = 100 钻）；法币 = `bigint` 分单位；倍率 = 整数万分比 |
| 状态机/枚举 | PG 原生 ENUM（sqlc 生成 Go 具名类型 + 常量，编译期保护）；演化只做 `ADD VALUE` |
| 命名 | 表名复数蛇形；ENUM 类型名单数 |
| 计数列 | 反规范化计数（`video_count`、`like_count`…）用原子 UPDATE 维护（ADR-0004）；`COUNT(*)` 现算留作对账校准 |

### 防刷分唯一约束（likes / comments 共用模式）

热度只增不减、计分只发生在**首次 INSERT**。因此唯一约束**不带** `WHERE deleted_at IS NULL`：

- 点赞取消 = 软删，再赞 = 复用行翻转（每人每视频终身一行，不再计分）
- 评论当日删除后**不能再评**（否则删评再评 = 无限 +5 刷分）

### 流水幂等：一类型一来源表

`transactions` 上 `(type, ref_id)` 部分唯一索引是发奖幂等的最后防线，前提是**每个类型的 ref_id 只指向一张来源表**。因此旧版 8 个流水类型拆细为 10 个（任务奖励按周期/限时拆开，签到与里程碑拆开——否则两张来源表的 id 会在同一类型下撞出假幂等冲突）。

## ENUM 清单

```
customer_status          active / banned
admin_role               admin / normal
admin_status             active / disabled
video_status             pending_transcode / transcoding / pending_review / published / rejected / transcode_failed
transcode_job_status     queued / running / succeeded / failed
transaction_type         recharge / withdrawal / watch_reward / periodic_task_reward / claim_task_reward /
                         sign_in_reward / milestone_reward / invite_valid_reward / content_reward / vip_purchase
recharge_order_status    pending_payment / succeeded / failed / cancelled
withdrawal_order_status  pending_review / rejected / paying / succeeded / failed
payment_event_direction  request / response / callback / query
ranking_period           weekly / monthly
task_period              daily / weekly / monthly
task_category            watch_video / upload_video / share_video / comment / like / login /
                         invite_friend / watch_ad
claim_status             claimed / submitted / approved / rewarded / rejected / expired   ← 六态，验收基准
activity_reward_kind     daily / milestone / invite_valid
ad_status                active / offline（"在投" = 广告 active ∧ 广告商 active ∧ 库存>0）
```

## 账号与裂变

```
customers   id, public_id, username UNIQUE, password_hash(argon2id), password_changed_at,
            name, phone, email, avatar_path, signature,
            invite_code char(8) UNIQUE, inviter_id bigint NULL, ancestors bigint[] + GIN,
            valid_at timestamptz NULL,
            vip_level int DEFAULT 0, status customer_status,
            withdrawal_password_hash,
            video_count, invite_count, follower_count, following_count,
            last_login_at, deleted_at, created_at, updated_at

admins      id, username UNIQUE, password_hash, password_changed_at,
            role admin_role, status admin_status, last_login_at,
            deleted_at, created_at, updated_at
```

- `inviter_id NULL` = 自然注册（旧版 `-1` 魔数消灭）
- `ancestors`：自根到父的物化祖先路径，注册时写死终身不变；团队 3 级查询 = `ancestors && ARRAY[me]` 走 GIN 再按数组尾部位置过滤；`inviter_id` 是规范来源，数组可随时用递归 CTE 重建
- `valid_at` 取代旧版 `valid_user 0/1/2`：置位即有效、永不回退；僵尸态 `2`（从未被写入）不复刻，"作品奖励是否已发"由流水幂等索引承载
- 有效用户翻转（有作品 且 有成功邀请 且 有联系方式）用条件 UPDATE `WHERE valid_at IS NULL` 原子完成
- 邀请奖励上限：邀请人有效邀请数**超过**其 VIP 档 `invite_reward_cap` 时跳过（实际至多发 cap 笔）；金额读 `activity_reward_configs`（该表为通用活动奖励配置，非签到专属）

## 钱包（纯账本，见 ADR-0006）

```
wallets       customer_id PK → customers, available bigint CHECK(>=0),
              frozen bigint CHECK(>=0), updated_at

transactions  id, customer_id, type transaction_type,
              amount bigint CHECK(<>0),          -- 带符号：+入 −出
              balance_after bigint,               -- 变动后总额(available+frozen)快照
              ref_id bigint NULL,                 -- 关联单据，语义由 type 决定
              created_at
              UNIQUE(type, ref_id) WHERE ref_id IS NOT NULL
              INDEX(customer_id, id DESC)         -- 钱包明细分页
```

- insert-only、无状态列；扣减一律原子条件 UPDATE（`WHERE available >= amount`）+ `RETURNING` 取 `balance_after`，同事务落流水
- 审计：`SUM(amount) = available + frozen` 一条 SQL 对账
- `ref_id` 语义表：recharge/withdrawal→现金订单 id；watch_reward→plays.id；periodic_task_reward→periodic_task_progress.id；claim_task_reward→task_claims.id；sign_in_reward→sign_ins.id；milestone_reward→milestone_awards.id；invite_valid_reward→**被邀请人 customer_id**（"一个被邀请人只触发一次"由约束保证）；vip_purchase→vip_purchases.id；content_reward→NULL（类型保留以承接迁移数据；发放链路旧版即不存在，不建）

## 现金订单（充值/提现分表）

```
recharge_orders    id, order_no UNIQUE,            -- 'R'+确定性派生自 id
                   customer_id, amount bigint,      -- 钻石
                   fiat_amount bigint, currency,    -- 分单位
                   payment_method, provider, provider_order_no, provider_status,
                   status recharge_order_status,
                   failure_code, failure_reason, expires_at, completed_at,
                   created_at, updated_at
                   UNIQUE(provider, provider_order_no)

withdrawal_orders  id, order_no UNIQUE,             -- 'W'+确定性派生自 id
                   customer_id, amount bigint,
                   fiat_amount bigint, currency,
                   bank_card_id,
                   payee_account, payee_name, payee_bank, payee_bank_code,
                   payee_province, payee_city,      -- 打款瞬间快照（仅此一份，不再双份）
                   provider, provider_order_no, provider_status,
                   status withdrawal_order_status,
                   reviewed_by → admins, reviewed_at, reject_reason,
                   failure_code, failure_reason, completed_at,
                   created_at, updated_at
                   UNIQUE(provider, provider_order_no)

payment_events     id, order_no,                    -- R/W 前缀全局唯一，单列引用 + 索引
                   provider, direction payment_event_direction,
                   payload jsonb,                   -- 原始报文，资金纠纷排查依据
                   created_at                       -- append-only

bank_cards         id, customer_id, card_no, holder_name, bank_name, bank_code,
                   province, city, deleted_at, created_at, updated_at
```

- 回调幂等三重保障：`order_no` 唯一 + `(provider, provider_order_no)` 唯一 + 已终态短路（代码逻辑）
- 提现流程：校验取款密码 → available→frozen 冻结 → `pending_review`；驳回解冻；审核通过 `paying` 走代付；打款成功扣 frozen + 落 withdrawal 流水；失败解冻
- 冻结/解冻**不写流水**（总额未变），生命周期由本表完整记载

## 视频内容

```
videos          id, public_id, customer_id, title, tags text[] CHECK(cardinality<=3),
                storage_path, hls_path, thumbnail_path,
                duration, width, height, file_size, mime_type,
                status video_status,
                reviewed_by → admins, reviewed_at, review_notes,
                click_count, valid_play_count, like_count, comment_count, hot_score,
                deleted_at, created_at, updated_at
                INDEX(hot_score DESC) WHERE status='published' AND deleted_at IS NULL

transcode_jobs  id, video_id, status transcode_job_status,
                attempts int, last_error, started_at, finished_at, created_at
                -- worker 用 FOR UPDATE SKIP LOCKED 认领；running 超时重置回 queued；attempts 带上限

likes           id, customer_id, video_id, deleted_at, created_at
                UNIQUE(customer_id, video_id)                     -- 含软删行，防刷分

comments        id, video_id, customer_id, content,
                comment_date date,                                 -- 应用按 Asia/Shanghai 写入
                deleted_at, created_at
                UNIQUE(customer_id, video_id, comment_date)        -- 含软删行，防刷分

plays           id, customer_id, video_id, played_at, valid_at timestamptz NULL
                INDEX(played_at), INDEX(video_id)
                -- 两段式：开播 INSERT（播放点击，不去重）返回 id；达标 UPDATE SET valid_at（有效播放）
```

- 状态机：`pending_transcode → transcoding → pending_review → published / rejected`（+`transcode_failed` 终态）；审核通过触发作者 `video_count+1`
- 旧版 `uploader_name`/`is_uploader_vip` 快照列不复刻，Feed 联表现查
- 标签为自由文本数组，无字典表（已核对：旧版无任何按标签检索/筛选，纯展示字段）

## 热度榜单

```
rankings   id, period ranking_period, period_start date,
           rank int, video_id, score bigint, created_at
           UNIQUE(period, period_start, rank)
           UNIQUE(period, period_start, video_id)
```

- 结算 = **明细聚合**：从 plays/likes/comments 按事件时间在周期内加权（点击+1/有效播放+1/赞+2/评+5）SUM，只写 Top N（N 进配置）——低频操作换热路径零额外写、历史可重算
- 软删行照算（按 created_at/valid_at 计），与"热度只增不减"自动一致
- cron 幂等 catch-up：从 rankings 最新 period_start 补齐到当前，同事务先删后插
- Feed 排序（行为差异 #3）：默认流 `hash(video_id, seed)` 伪随机（seed 随游标传递，刷内不重不漏）；精选走 hot_score 索引序

## 关注

```
follows   id, follower_id, followee_id, deleted_at, created_at
          UNIQUE(follower_id, followee_id)      -- 软删复用行
```

- 三态（未关注/已关注/互关）= 查双向；计数列在 customers，关注 +1 / 取关 −1 对称维护

## 任务系统（定义分表 + 进度去重置 cron）

```
periodic_tasks          id, name, description, icon_path, category task_category,
                        period task_period, target int, reward bigint,
                        display_order, enabled, deleted_at, created_at, updated_at

claim_tasks             id, name, description, icon_path,
                        min_vip_level, reward bigint,
                        requirement, flow, link,
                        display_order, enabled, starts_at, ends_at,
                        deleted_at, created_at, updated_at

periodic_task_progress  id, customer_id, task_id, period_key text,      -- '2026-07-03' / '2026-W27' / '2026-07'
                        progress int, target int,                       -- target 快照，中途改定义不影响本期
                        completed_at, reward_granted_at, reward_amount,
                        UNIQUE(customer_id, task_id, period_key)

task_claims             id, customer_id, task_id, status claim_status,
                        proof_text, proof_images text[],                -- 须本人上传的存储路径
                                                                        -- CHECK：上界恒 ≤9；claimed/expired 允许空，提交后各状态 ≥1
                        reward_base bigint, reward_granted bigint,      -- 倍率前/后
                        expires_at,                                     -- 领取时任务 ends_at 的快照
                        claimed_at, submitted_at,
                        reviewed_by → admins, reviewed_at, review_remark,
                        rewarded_at, created_at, updated_at
                        UNIQUE(customer_id, task_id)                    -- 一人一次（旧版 count 防重的约束化）
```

- 周期进度 **period_key 行模式**：新周期首个事件 UPSERT 新行，无预生成、无重置 cron（旧版每日 00:00:05 重置 cron 消失——实现简化，对外行为等价）；"过期/已完成"由 `period_key`/`progress>=target` 推导，不设状态列
- 达标发奖与进度事件同事务；手动补领 = `WHERE reward_granted_at IS NULL` 条件 UPDATE（行为差异 #8）
- 限时任务过期 cron（每 5 分钟）保留：超时的 `claimed`/`rejected` claim 翻 `expired`；`submitted`（待审核）与 `rewarded` 豁免（M5 #37 定稿：用户已尽义务不作废，积压属运营问题）

## 签到活动

```
sign_ins                 id, customer_id, sign_date date, reward_amount, created_at
                         UNIQUE(customer_id, sign_date)        -- 防重签靠约束
milestone_awards         id, customer_id, month date, threshold int, reward_amount, created_at
                         UNIQUE(customer_id, month, threshold) -- 里程碑幂等 + 流水 ref
activity_reward_configs  id, kind activity_reward_kind, threshold int, reward bigint,
                         enabled, created_at, updated_at
```

- 按自然月累计 = `COUNT WHERE month`；奖励按 VIP 倍率放大（`floor(base × multiplier)`，整数 bp 除法与旧版同口径）

## VIP

```
vip_level_configs  level int PK (0–3), name, price bigint,
                   reward_multiplier_bp int,        -- 10000 = 1 倍
                   invite_reward_cap bigint,
                   upload_video_limit int,          -- 发布作品上限，≤0 不限（M5 复刻旧版，00015 迁移加列）
                   updated_at
vip_purchases      id, customer_id, from_level, to_level, price bigint, created_at
```

- 购买同一事务：wallets 扣减 + `customers.vip_level` CAS（`WHERE vip_level < $new`，防并发重复购买）+ 本表落记录 + 流水（ref=本表 id）
- 只升不降、可跳级；四档配置为 seed 数据

## 广告

```
advertisers  id, name, contact, status, deleted_at, created_at, updated_at
ads          id, advertiser_id, title, media_path, link,
             stock_total int, stock_remaining int CHECK(>=0),
             status ad_status, deleted_at, created_at, updated_at
ad_watches   id, customer_id, ad_id, created_at
             INDEX(created_at)                     -- 广告展示报表
```

- 看广告：随机取在投 + 原子扣 `stock_remaining WHERE > 0` + ad_watches 落明细 + 推进"看广告"任务进度
- 下架级联 = UPDATE 语义（广告商下架 → 其广告全下架），不用 FK CASCADE

## 报表

```
daily_actives  customer_id, active_date date
               PRIMARY KEY(customer_id, active_date)
```

- 鉴权中间件每请求 `INSERT ... ON CONFLICT DO NOTHING`（进程内"今日已记"缓存消掉空转）；DAU = `COUNT(*) GROUP BY active_date`
- 播放/广告展示/日视频报表**现算**：对 plays/ad_watches/videos 按日聚合；变慢再加每日物化 cron（纯追加式改造）

## 通用上传（无表）

预签名 path 按 `{kind}/{customer_id}/{random}.{ext}` 约定生成；归属校验 = 解析 path 前缀比对当前客户 + confirm 时查对象存在。不建 uploads 表。
