// Package cron 进程内周期任务调度（robfig/cron v3），Asia/Shanghai 锚定。
// 结算/清理类 job 一律幂等（无状态依赖）：装配后立即跑一次补齐，随后按 spec 周期触发。
package cron

import (
	"context"
	"log/slog"

	"github.com/robfig/cron/v3"

	"github.com/netfishx/gabon-go/internal/tz"
)

// Job 一个周期任务：spec 为标准 5 字段 cron 表达式，run 幂等且自管错误。
type Job struct {
	Name string
	Spec string
	Run  func(ctx context.Context) error
}

// Scheduler 进程内调度器。
type Scheduler struct {
	c      *cron.Cron
	logger *slog.Logger
	jobs   []Job
}

// New 构造调度器（时区锚定 Asia/Shanghai，修旧版无 zone 缺陷）。
func New(logger *slog.Logger) *Scheduler {
	return &Scheduler{
		c:      cron.New(cron.WithLocation(tz.Shanghai)),
		logger: logger,
	}
}

// Register 注册一个 job（须在 Start 前调用）。
func (s *Scheduler) Register(j Job) error {
	if _, err := s.c.AddFunc(j.Spec, s.wrap(j)); err != nil {
		return err
	}
	s.jobs = append(s.jobs, j)
	return nil
}

// wrap 统一日志与 panic 隔离：单个 job 失败/panic 不影响调度器与其他 job。
func (s *Scheduler) wrap(j Job) func() {
	return func() { s.safeRun(context.Background(), j) }
}

// safeRun 执行 job 并隔离 panic（robfig/cron v3 默认不 recover，任一 job panic 会拖垮进程）。
func (s *Scheduler) safeRun(ctx context.Context, j Job) {
	defer func() {
		if p := recover(); p != nil {
			s.logger.Error("cron job panicked", "job", j.Name, "panic", p)
		}
	}()
	if err := j.Run(ctx); err != nil {
		s.logger.Error("cron job failed", "job", j.Name, "error", err)
	}
}

// Start 启动调度并对每个 job 立即跑一次（幂等 catch-up：补齐宕机期间缺失的执行）。
// 启动 run 同样经 safeRun 隔离 panic/错误，一个 job 挂了不阻断其余 job 与调度启动。
func (s *Scheduler) Start(ctx context.Context) {
	for _, j := range s.jobs {
		s.safeRun(ctx, j)
	}
	s.c.Start()
}

// Stop 停止调度并等待在途 job 完成。
func (s *Scheduler) Stop() {
	<-s.c.Stop().Done()
}
