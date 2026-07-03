# 用 ffmpeg 自建转码，替代 AWS MediaConvert

旧版转码走 AWS MediaConvert（提交任务 + 30 秒轮询状态）。本版改为自建：上传完成后写入 `transcode_jobs` 表（DB 即队列），单二进制内固定大小的 worker 池认领任务、`exec` ffmpeg 子进程产出 HLS（m3u8 + 分片）与首帧图，上传回对象存储后更新状态；进程重启扫表恢复，失败带重试计数。选择自建是为了摆脱对单一云服务的绑定、让转码链路可本地闭环测试；代价是转码质量调优、并发度与资源隔离由自己负责。

## Considered Options

- 保留 MediaConvert：省事且质量有保障，但核心管线无法本地验证，且深度绑定 AWS
- 独立转码 worker 二进制 / Redis 队列 (asynq)：与"单二进制、零额外基建"的形态决策相悖；将来需要隔离时把 worker 拆出去即可，退出成本低
