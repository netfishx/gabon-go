package transcode

import (
	"context"
	"testing"
	"time"

	"github.com/netfishx/gabon-go/internal/db"
)

// P2 回归：停机时在途任务必须被记录回队，不得因 worker ctx 已取消而卡死在 running。
// fake 转码阻塞至 ctx 取消——模拟 ffmpeg 被 shutdown 中断。
func TestStopRequeuesInFlightJob(t *testing.T) {
	_, jobID := newVideoJob(t)

	started := make(chan struct{})
	w := NewWorker(testPool, Options{
		Transcode: func(ctx context.Context, _ db.Video) (Result, error) {
			close(started)
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
		Concurrency: 1,
		MaxAttempts: 3,
		JobTimeout:  30 * time.Second,
	})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("worker never claimed the job")
	}
	w.Stop()

	status, attempts := jobRow(t, jobID)
	if status != "queued" {
		t.Fatalf("job status = %s, want queued (in-flight job must be requeued on shutdown)", status)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}
