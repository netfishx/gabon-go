# 旧版 VIP 购买与广告投放核对（M5 · checklist H + K）

> 研究票：[#34 核对旧版 VIP 购买与广告投放细节](https://github.com/netfishx/gabon-go/issues/34)
> 旧仓库：`/Users/ethanwang/projects/gabon`（Java）· 服务于 gabon-go M5（地图 [#29]，喂 [#38 定稿：签到/VIP/广告设计]）
> 结论一句话：**VIP = 永久等级升级（无到期、只升不降、可跳级、全价、CAS 防并发）；`upload_video_limit` 旧版真执行（Go 基线缺列，需 #38 定夺）；广告"在投"= 广告↑∧广告商↑∧库存>0∧未过期，取广告=均匀随机+发放即扣库存，旧版无 `ad_watches` 表（看=取，一次调用）；广告商下架写级联（单向、不可逆）。**

## 一、核心结论（对应票面五问）

| # | 问题 | 结论 |
|---|------|------|
| 1 | VIP 购买流程（扣钻/防并发/生效/全价） | 只升级（`target ≤ current` 报错）、**可跳级不补差价**、**付目标档全价** `price`；**CAS** `UPDATE customers SET is_vip=1, vip_level=target WHERE vip_level=current`（0 行→`VIP_PURCHASE_CONFLICT`）先行，同事务再 `deductDiamondTransaction`（余额不足抛错整体回滚）；**立即生效、永久无到期**。 |
| 2 | `upload_video_limit` 有无真实执行点 | **有**。`VideoServiceImpl.confirmVideoUpload:124`：`limit = getUploadVideoLimitByVipLevel(vipLevel)`，`limit>0` 且 `video_count ≥ limit` → 抛 `VIDEO_UPLOAD_LIMIT_REACHED`；`limit≤0` = 不限制（同 inviteLimit 语义）。比对基数 = `customers.video_count`（审核通过累加，仅已发布）。 |
| 3 | 广告在投判定与随机返回 | 在投 = `SELECT a.* JOIN advertiser adv WHERE a.status=1 AND a.deleted_flag IS NULL AND a.remain_count>0 AND (a.expire_time IS NULL OR a.expire_time>NOW()) AND adv.status=1 AND adv.deleted_flag IS NULL`（**五条件**）；返回 = 应用内 `Random.nextInt(list.size())` **均匀随机取一条**（非加权）。 |
| 4 | 库存扣减与 ad_watches 时序 | **旧版无 `ad_watches` 表**。`GET /api/ads/watch` 一次调用即完成：`getRandomAd()`→原子扣库存 `UPDATE advertisement SET remain_count=remain_count-1 WHERE id=? AND remain_count>0`（0 行→返回 null，防并发超扣）→ Redis 日报计数；随后若登录 → `updateWatchAdProgress` 推"看广告"任务进度。**库存在"取广告"时即扣（= 展示即扣），无观看确认二段式，无逐次落库**。 |
| 5 | 广告商下架级联 | `toggleAdvertiserStatus(id, 0)`（@Transactional）：更新广告商 status=0 后，`UPDATE advertisement SET status=0 WHERE advertiser_id=id AND deleted_flag IS NULL` **写级联下架名下全部广告**。⚠️ **单向**：重新上架广告商（status=1）**不反向恢复**广告，需逐条重开。此外在投 JOIN 本就按 `adv.status=1` 过滤，写级联属双保险。 |

## 二、机制细节

**VIP（`VipServiceImpl.purchaseVip`）**
- 表：`vip_level_config`(vip_level 0–3, price, level_name, `multiplier`, `upload_video_limit`, `invite_limit`, status …)；`customers` 仅 `is_vip`(0/1) + `vip_level`(0–3)，**无到期列、无到期 cron** → VIP 是一次性永久升级。
- `transaction_no = "VIP_"+customerId+"_"+currentTimeMillis`（**非幂等**，无唯一去重）；并发防线唯一靠 vip_level 的 CAS。
- 扣钻走 `deductDiamondTransaction`（`deductDiamondBalance` 原子 `WHERE balance≥amount`，0 行→`ORDER_INSUFFICIENT_COINS`）→ 写 `VIP_PURCHASE` 流水。

**广告（客户端 `AdServiceImpl` + `AdController`）**
- `advertisement`(ad_name, advertiser_id, resource_url, resource_type=1, jump_url, `remain_count`, `expire_time`, `total_count`, status, remark)；新建默认 `status=0`、`remain_count=total_count=count`、`expire_time`=北京当日 23:59:59（null=永不过期）。
- 取广告非 @Transactional：扣库存 / Redis 日报 / 任务进度三步无原子性（可接受，均为幂等/统计性）。

**广告商（admin `AdServiceImpl`）**
- 新建广告商默认 `status=0`（须手动上架才可投放）；广告商名唯一（软删外）。
- 列表/CRUD 均按 `deleted_flag IS NULL` 过滤；广告列表可按广告商名模糊 → 先查广告商 id 集再 `IN`。

## 三、对 Go 的直接影响（喂 #38 定稿：签到/VIP/广告设计）

1. **`upload_video_limit` 必须定夺（清 Fog）**：旧版**真执行**（发布上限，比对 `video_count`，≤0 不限）。Go `vip_level_configs`（`docs/schema.md:236`）**未建此列**，checklist H 配置项也只列"价格/奖励倍率/邀请上限"。→ #38 拍板：**复刻**（加 `upload_video_limit` 列 + 在发布/确认上传处按 `video_count` 拦截）还是**砍掉**。旧档位 6/30/50/100 属现网运营数据（与 sign-in 同，非种子）。倾向复刻（真功能非死代码）。
2. **广告过期 `expire_time`——Go 疑似漏了**：旧 `advertisement` 有 `expire_time`（当日末，null=永不），在投判定含"未过期"。Go `ads` 表（`schema.md:249-251`）与"在投"定义（`schema.md:48`，仅 广告↑∧广告商↑∧库存>0 **三条件**）**均无过期维度**。→ #38 确认：Go 是否保留广告到期（加 `expires_at` 列 + 在投加过期过滤）还是有意砍掉。
3. **`ad_watches` 是 Go 新增（旧版无）**：旧版看=取一次调用、库存展示即扣、不落逐次明细（仅 Redis 日报 + 任务进度）。Go `schema.md:252` 已设 `ad_watches(customer_id, ad_id, created_at)` 落明细 + 报表现算。→ 属**设计增强**，但需 #38 明确落 `ad_watches` 与扣库存/推任务进度的**时序与事务边界**（旧版三步无原子性；Go 单二进制内可考虑同事务）。
4. **VIP 已高度对齐，两处措辞校准**：Go 购买同事务（wallet 扣 + `vip_level` CAS + `vip_purchases` 落记录 + 流水 ref=本表 id）与旧版一致且更规范（旧版无 `vip_purchases` 源表、靠时间戳非幂等 transaction_no）。① Go CAS 用 `WHERE vip_level < $new`（比旧版 `= current` 更稳健，防跳级并发同样成立）；② Go 定 VIP **永久无到期**与旧版一致，无需新增到期语义（除非产品要订阅制——属新增）。
5. **广告商下架级联单向性**：Go `schema.md:257` 已定"下架级联 = UPDATE 语义"。→ #38 明确是否复刻旧版**单向不可逆**（重开广告商不恢复广告），或改为可逆/不写级联只靠在投 JOIN 过滤。

## 四、遗留/移交
- 本票为 M5 研究族**最后一张**；关闭后 [#38] 解锁、研究族清零。
- 里程碑/日签/VIP 上限/广告数量等**运营数值**均属现网数据，非本核对可得 → #38 决定迁移导出或运营重定。
- 清 Fog：「VIP `upload_video_limit`」项已由本票确证旧版真执行，升格为 #38 的明确决策项，从 Fog 移除。
