# 流水表为纯账本：只记总额变动，冻结不记账

旧版 `customer_transactions` 一表三职：钻石账本 + 提现审核状态机（7 态）+ 银行卡快照（仅提现用，其余类型大片 NULL）。本版把流水收敛为**纯账本**：insert-only、无状态列，只在客户资产总额（available+frozen）真正增减时落一笔——充值在回调成功时、提现在打款成功时。带符号金额承载方向，`balance_after` 记录变动后总额快照（原子条件 `UPDATE ... RETURNING` 顺手取得），`SUM(amount) = available + frozen` 一条 SQL 完成全账对账。提现的冻结/解冻是 available↔frozen 的内部转移，总额未变，**不写流水**；其完整生命周期（申请、审核、打款、终态）由 `withdrawal_orders` 记载，客户端"钱包明细"= 流水 ∪ 进行中的现金订单。发奖幂等由 `(type, ref_id)` 部分唯一索引兜底，要求每个流水类型的 ref 只指向一张来源表（因此类型从旧版 8 个拆为 10 个）。

## Considered Options

- 复刻旧版混合表：流水带状态机与提现快照——账本不可变性丧失，非提现行大片 NULL，已被识别为旧版病灶
- 双维度记账：冻结/解冻也写流水（withdraw_freeze / unfreeze / settle 类型）——账目"完备"但引入非资产变动条目，`SUM(amount)` 对账失效，且与用户视角（冻结≠钱少了）相悖

## Consequences

- 流水表永不 UPDATE，审计与对账极简；`balance_after` 让任意一笔的前后余额可回放
- 提现进行中在流水里不可见，钱包明细接口须联合展示进行中的提现单
- 余额变更与流水写入必须同事务且经由 wallet 域统一入口，禁止绕过
