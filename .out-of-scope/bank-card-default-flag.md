# 银行卡默认卡（isDefault）

新版不提供「设为默认卡」功能，`bank_cards` 表不设 `is_default` 列。

## 为什么不做

旧版（Java）`UpsertBankCardRequest.isDefault` / `CustomerBankCardVO.isDefault` 存在此概念，但一手核对发现它在业务链路中没有实际用途：提现请求（`WithdrawRequest`）用显式 `bankCardId` 选卡，Withdraw 服务实现中检索不到任何 isDefault 的读取点——它只是客户端「预选一张卡」的 UI 便利。

新版裁决（2026-07-13，#73 triage）：无业务收益的持久状态不进 schema。客户端如需预选，用列表第一张（`ORDER BY id DESC`，即最近添加的卡）即可，语义等价且无需服务端状态。

若将来出现真实业务需求（如多卡分流打款策略），应作为新需求重新设计，而不是复刻旧版的摆设字段。

## Prior requests

- #73 — 银行卡域与旧版差异 follow-up（cross-review 产出）
