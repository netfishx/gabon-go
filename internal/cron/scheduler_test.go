package cron

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestStartRunsEachJobOnce(t *testing.T) {
	// 启动即跑一次（幂等 catch-up）：注册的 job 在 Start 时立即执行，无需等到 spec 触发。
	s := New(quietLogger())
	var runs atomic.Int64
	if err := s.Register(Job{
		Name: "tick", Spec: "0 0 * * *", // 每天午夜——测试期内不会自然触发
		Run: func(context.Context) error { runs.Add(1); return nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	s.Start(context.Background())
	defer s.Stop()

	if got := runs.Load(); got != 1 {
		t.Errorf("startup runs = %d, want 1", got)
	}
}

func TestJobFailureDoesNotPanic(t *testing.T) {
	// 单 job 失败仅记日志，不影响调度器启动与停止。
	s := New(quietLogger())
	if err := s.Register(Job{
		Name: "boom", Spec: "0 0 * * *",
		Run: func(context.Context) error { return errors.New("kaboom") },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	s.Start(context.Background()) // 启动 run 报错被吞
	s.Stop()                      // 干净停止
}

func TestRegisterRejectsBadSpec(t *testing.T) {
	s := New(quietLogger())
	if err := s.Register(Job{Name: "bad", Spec: "not a cron", Run: func(context.Context) error { return nil }}); err == nil {
		t.Error("expected error for malformed cron spec")
	}
}
