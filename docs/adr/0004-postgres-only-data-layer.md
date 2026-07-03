# 数据层收敛为单 PostgreSQL（弃 MySQL、砍 Redis）

旧版用 MySQL 8 + Redis（ZSET 周月榜、INCR 计数、Set 去重、HyperLogLog 算 DAU、setIfAbsent 做锁）。本版数据层只有一个 PostgreSQL：热度榜 = score 列 + 索引由 cron 结算；计数 = 原子条件 UPDATE；去重 = 唯一约束；UV = 明细表 COUNT(DISTINCT)；单实例下不存在分布式锁需求。选 Postgres 而非沿用 MySQL，是因为 sqlc + pgx 对 Postgres 的支持是一等公民，且个人技术生态已向 Postgres 收敛；平行实验品（见 ADR-0002）无数据迁移成本，换库的主要代价恰好不存在。

## Consequences

- 点赞、播放记录等旧版只写 Redis 的数据在本版全部持久化落库，语义从"7 天 TTL 会过期"变为永久（这是修复缺陷，见 feature-checklist 行为差异）
- 写放大（每次播放/点赞都写 Postgres）在实验规模下可接受；真出现瓶颈时，榜单与计数器是最容易增量迁回 Redis 的部分
