# 旧版签到与里程碑奖励配置核对（M5 · checklist J）

> 研究票：[#33 核对旧版签到与里程碑奖励配置](https://github.com/netfishx/gabon-go/issues/33)
> 旧仓库：`/Users/ethanwang/projects/gabon`（Java）· 服务于 gabon-go M5 签到域设计（地图 [#29]，喂 [#38 定稿：签到/VIP/广告设计]）
> 结论一句话：**日签 + 当月累计里程碑，金额全走 `activity_reward_config`（无种子，纯运营配置，日签缺省 1）；VIP 倍率对"日签+里程碑合并基数"一次性放大、`RoundingMode.HALF_DOWN`（≠ 任务链路的 floor）；无补签；里程碑按自然月累计次数（非连续），月初归零。**

## 一、核心结论（对应票面五问）

| # | 问题 | 结论 |
|---|------|------|
| 1 | 日签基础金额与配置来源 | `activity_reward_config` 行 `config_type='DAILY_SIGN_IN'` + `config_key='daily'` → `reward_diamonds`。**无committed种子**，查不到行时**代码缺省 = 1**（`getDailySignInReward`）。即纯运营配置数据。 |
| 2 | 里程碑档位/阈值种子值 | `config_type='SIGN_IN_MILESTONE'`，`config_key`=**天数字符串**（`Integer.parseInt`），`reward_diamonds`=该档奖励，`display_order` 排序。**仓库无任何种子行**（DDL dump 无 data，V1.14 只 seed `task_definitions`，admin 迁移也无）——真实档位是**现网 DB 运营数据**，代码不含。 |
| 3 | VIP 倍率放大精确口径 | `base = daily + milestone`（**合并**）；`total = base × multiplier`，**`setScale(0, HALF_DOWN)` + `intValueExact()`** —— **四舍五入(半舍)，不是 floor**。倍率取**签到当刻**客户 `vip_level` 的 `multiplier`（缺省 ×1）。⚠️ 与任务链路 `calcActualReward` 的 `RoundingMode.DOWN`(floor) **口径不一致**。 |
| 4 | 有无补签 | **无**。`doSignIn` 仅签 `LocalDate.now(Asia/Shanghai)`，先查"今日已签"再插；无补签端点/日期参数。 |
| 5 | `activity_reward_config` 的 config_key 结构 | 日签=字面量 `"daily"`；里程碑=天数字符串（如 `"7"`/`"15"`/`"30"`）；邀请=`"invite"`（`INVITE_VALID_REWARD`，M4 已用，非签到）。唯一约束 `uk_type_key (config_type, config_key, deleted_flag)`。 |

## 二、签到发放全链路（`SignInServiceImpl.doSignIn`，单 `@Transactional`）

```
1. 校验客户存在
2. periodKey = "YYYY-MM"（Asia/Shanghai，自然月）
3. 今日已签？→ 抛 SIGN_IN_ALREADY_TODAY（幂等：每客户每日一行，(customer_id, sign_in_date)）
4. daily = config(DAILY_SIGN_IN/daily).reward_diamonds ?? 1
5. previousDays = COUNT(customer, periodKey)   ← 当月已签天数(不校验连续)
   newDayCount = previousDays + 1
6. 里程碑：遍历 SIGN_IN_MILESTONE(按 display_order)，首个 config_key(天数)==newDayCount 命中 → milestoneReward，break
7. base = daily + milestone
   total = (base × multiplier).setScale(0, HALF_DOWN).intValueExact()   ← 合并后一次舍入
8. INSERT customer_sign_in_records(diamonds_awarded=total, period_key)
9. addDiamondTransaction(SIGN_IN_REWARD, total, "SIGNIN-"+recordId)     ← 单笔合并流水
```

关键源文件：`gabon-service/.../service/impl/SignInServiceImpl.java`（`doSignIn:60-143`、`getDailySignInReward:210-219`、`getSignInMilestones:224-232`）；`entity/ActivityRewardConfig.java`、`entity/CustomerSignInRecord.java`；DDL `resources/sql/last_main_tables.sql`（`activity_reward_config`）。

## 三、值得注意的行为细节

1. **"连续"是误称。** 里程碑判定基于 `COUNT(当月签到记录)+1`，**不校验连续性**——本月内跳签不清零，只按累计次数。`remark` 文案写 `"连续%d天额外奖励"` 是**文案与逻辑不符**（实为"当月累计第 N 次"）。按自然月归零（`period_key=YYYY-MM`）。→ Go checklist J 已正确表述为"**当月累计**里程碑…按自然月计数"，设计已对齐，无需再纠。
2. **日签+里程碑合并成一笔 `SIGN_IN_REWARD` 流水**（金额 = 合并后放大值），旧版不区分日签/里程碑两笔。
3. **舍入不一致**：签到 `HALF_DOWN`，任务 `DOWN`(floor)。旧版自身两套口径。
4. **无 login 任务推进**：签到端点不碰任务系统（印证 #31：login 为死任务类别）。
5. **admin 无签到配置管理 UI**：`ActivityRewardConfigMapper` 在 admin 侧唯一消费者是 `VideoServiceImpl`（读 `INVITE_VALID_REWARD/invite`，属邀请/内容奖励，非签到）。日签/里程碑档位在旧版是**裸 DB 数据**，无后台 CRUD。

## 四、对 Go 的直接影响（喂 #38 定稿：签到/VIP/广告设计）

1. **类型拆分已定，需认领拆分后的舍入后果。** Go 基线（`docs/schema.md`）已把 `sign_in_reward`(ref=`sign_ins.id`) 与 `milestone_reward`(ref=`milestone_awards.id`, `UNIQUE(customer_id, month, threshold)`) **拆成两类两源表**（行为差异 #6）。→ 意味着日签与里程碑会各自入账、各自乘倍率：`floor(daily×m) + floor(milestone×m)`，与旧版 `round((daily+milestone)×m)` 单次舍入**结果可能差 1 钻**。#38 须明确接受这一拆分后果（本就是 #6 的自然推论）。
2. **舍入统一为 floor（新增微行为差异）。** 旧签到用 `HALF_DOWN`；地图接入约定已定"VIP 倍率 = `floor(base×bp/10000)`"统一 floor。→ 这是一条**尚未进 checklist"行为差异"清单**的偏离，建议 #38 定稿时把"签到舍入由 HALF_DOWN 改 floor（全域统一）"补进清单（现为 12 条）。
3. **档位种子值缺失，须运营/迁移补齐。** 仓库无日签/里程碑种子；Go `activity_reward_configs`（kind=`daily`/`milestone`/`invite_valid`）应：①日签缺省沿用旧代码兜底语义（可设种子行而非硬编码 1）；②里程碑档位从**现网 DB 导出**或由运营重定（属数据迁移/运营任务，非代码决策）。#38 需拍板"是否建 admin 签到配置 CRUD"——旧版无此 UI，地图 admin 范围决策②也未列，倾向 M7 或直接 DB 配置。
4. **无补签**：Go 无需补签能力（旧版即无，无兼容义务）。
5. **幂等**：日签靠 `(customer_id, sign_date)` 唯一；里程碑靠 `UNIQUE(customer_id, month, threshold)`（Go 基线已建）——比旧版"COUNT 计数"更硬，天然防同月同档重发。

## 五、遗留/移交

- 本票**不涉及** VIP 购买/广告（#34）、任务域接口（#37）、限时任务（#32）。
- 里程碑**具体档位数值**属现网数据，非本核对可得——移交 #38 决定"迁移导出 or 运营重定"。
- 已确认可清理的 Fog：无（本票为 #38 的研究前置，#38 仍阻塞于 #33+#34，#34 未完不解锁）。
