# 旧版观看/互动奖励机制核对（M5 · watch_reward + 互动奖励列）

> 研究票：[#30 核对旧版观看奖励机制](https://github.com/netfishx/gabon-go/issues/30)
> 旧仓库：`/Users/ethanwang/projects/gabon`（Java 17 / Spring Boot / MyBatis-Plus / MySQL + Redis）
> 结论一句话：**旧版没有"按次直接发钻"的观看/点赞/评论奖励。全部经由任务系统发放（`TASK_REWARD`），四列 `diamond_per_*` 与流水类型 `WATCH_REWARD(2)` 均为死代码。**

## 一、核心结论（对应票面五问）

| # | 问题 | 结论 |
|---|------|------|
| 1 | 谁收钱？ | **观看者/互动者本人**（登录用户）。但不是直接收，而是喂任务进度，任务达标时才发。游客（`customerId == null`）不推进度、不发钻。 |
| 2 | 金额来源？是否来自 `diamond_per_view` / `diamond_per_valid_view`，按谁的 VIP 档？ | **不是**。这两列（连同 `diamond_per_like` / `diamond_per_comment`）在实时链路中**从不被读取用于发放**，只在 `VipLevelConfigServiceImpl.toResponse()` 回显给前端 config 接口。实际金额 = 任务定义 `reward_diamonds` × **执行者本人** VIP `multiplier`（`floor`）。 |
| 3 | 按次发放还是有每日上限？ | **按任务上限**，非每次播放发钻。任务有 `target_count` + 周期 `period_key`（日/周期）。"上限"= 任务目标次数 × 周期，天然封顶。 |
| 4 | 与有效播放判定的关系？ | 只有 **有效播放** `recordValidPlay` 推进 watch-video 任务进度；普通 **点击** `recordPlayClick` 不推进奖励，只计 `total_clicks`/热度/DAU/日报。有效播放去重 = 同 IP 对同视频 24h 一次（Redis `setIfAbsent` TTL 24h）。 |
| 5 | `diamond_per_like` / `diamond_per_comment` 有无真实发放链路？ | **无**。不存在 `LIKE_REWARD`/`COMMENT_REWARD` 流水类型；点赞/评论仅推进对应任务进度，发的仍是 `TASK_REWARD`。确认"引而未用"（dead columns）。 |

## 二、实际发放链路（唯一真链路：经由任务系统）

```
有效播放  recordValidPlay(videoId, customerId, ip)
   └─ 24h/IP 去重 → valid_clicks+1、热度+1、日报计数
   └─ customerId != null → taskProgressService.updateWatchVideoProgress
点赞      VideoLikeServiceImpl → taskProgressService.updateLikeProgress
评论      VideoCommentServiceImpl → taskProgressService.updateCommentProgress
                       │
                       ▼  updateTaskProgressByCategory(customerId, taskType, periodKey, category)
                          category: 1=watch_video 3=like 4=comment
                       │  currentCount+1；达 targetCount → tryAutoClaimReward
                       ▼
   autoAwardDiamonds → addDiamondTransaction(customerId, TASK_REWARD, floor(reward × VIP multiplier), ...)
```

关键源文件（旧仓库）：
- `service/impl/VideoPlayRecordServiceImpl.java` — `recordPlayClick`（点击，不发钻）/ `recordValidPlay`（有效播放，推 watch 任务）
- `service/impl/TaskProgressServiceImpl.java` — `updateWatchVideoProgress`(75) / `updateLikeProgress`(107) / `updateCommentProgress`(124)；`calcActualReward`(132) VIP 倍率；`autoAwardDiamonds`(342) 发 `TASK_REWARD`
- `service/impl/VideoLikeServiceImpl.java:66`、`service/impl/VideoCommentServiceImpl.java:87` — 互动 → 任务进度
- `enums/TransactionTypeEnum.java` — `WATCH_REWARD(2)`、`TASK_REWARD(3)`
- `entity/VipLevelConfig.java` — 四死列 `diamondPerView/ValidView/Like/Comment`（38–47 行）+ 实际生效的 `multiplier`

## 三、死代码证据

**`diamond_per_view` / `diamond_per_valid_view` / `diamond_per_like` / `diamond_per_comment`**
- 全仓 grep：四列 getter 唯一调用点是 `VipLevelConfigServiceImpl.toResponse()`（entity→response DTO 映射），无任何发放/结算调用。
- 结论：**引而未用**，随 VIP config 一起暴露给前端展示，但发放逻辑从不消费。

**`WATCH_REWARD(2, "观看奖励")` 流水类型**
- 全仓 grep：仅出现在两处 —— ①枚举定义本身；②`CustomerTransactionServiceImpl.EARNING_TYPES`（今日/近 7 天收益统计的过滤白名单）。
- `addDiamondTransaction(...)` 的全部真实调用点（SignIn / ClaimTask / TaskReward / TaskProgress）**无一以 `WATCH_REWARD` 入账**。
- git 历史 `-S WATCH_REWARD` 仅命中枚举注释/改动提交（"fix comment"、"change the enum"），无发放点被删痕迹。
- 结论：`WATCH_REWARD` 是**发放层死类型**，仅用于兜底解析历史行 + 收益统计归类。实时新流水从不产生该类型。

## 四、对 Go 重写的直接影响（喂给 #36 定稿：观看奖励设计）

1. **watch_reward 不是独立奖励，它就是 watch-video 任务。** Go 骨架预留的"观看奖励事件 ref = `plays.id`"应理解为**喂任务进度的事件**，而非"每有效播放直接发一笔 watch_reward 流水"。这与旧版实时行为一致。#36 需拍板：是延续"有效播放→喂 watch 任务→发 TASK_REWARD"，还是引入独立的 per-play 直发（旧版无此行为，属新增）。
2. **基线流水 10 类是否保留 `watch_reward` 独立类型？** 旧版该类型发放层已死。若 Go 也走"喂任务"路线，则 `watch_reward` 流水类型无产生源 —— 要么删除该类型（推荐，符合 feature-checklist"旧 bug/死代码不复刻"精神），要么明确保留为 M5 新增的 per-play 直发能力（需产品决策，属行为差异）。
3. **`diamond_per_like` / `diamond_per_comment` 两列**：确认可从 Go schema 基线**删除**（旧版死列，无发放链路）。清除对应 Fog 项。
4. **VIP 倍率作用点**：旧版对**任务基础奖励**乘 `multiplier`（`floor(base × mult)`，取执行者本人 VIP 档，缺省 ×1）。Go 已定 `floor(base × bp / 10000)` 整数万分比等价 —— 语义一致，作用对象是任务奖励。
5. **有效播放去重口径**：旧版 = 同 IP×同视频 24h（Redis）。Go 无 Redis，须用 Postgres 表达等价去重（`plays` 唯一约束 / 时间窗判定）—— 归入任务域/视频域实现细节，非本票范围。

## 五、遗留/移交

- 本票**不涉及**任务进度推进的通用接口形态（归 #37）、签到里程碑 VIP 倍率细节（归 #33/#34/#38）。
- 已澄清 Fog：`diamond_per_like`/`diamond_per_comment` 去留（→删）、watch_reward 是否真发（→旧版不直发，经任务）。剩余待 #36 拍板：Go 是否保留独立 `watch_reward` 流水类型 / 是否新增 per-play 直发。
