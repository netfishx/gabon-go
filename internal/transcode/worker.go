// Package transcode 转码管线（ADR-0003）：DB 任务表即队列，进程内固定 worker 池，
// FOR UPDATE SKIP LOCKED 认领，失败带重试计数，重启扫表恢复。
package transcode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
)

// Result 转码产物：对象存储路径与探测出的媒体信息。
type Result struct {
	HLSPath       string
	ThumbnailPath string
	Duration      int32
	Width         int32
	Height        int32
}

// Func 执行一次转码。可注入：状态机测试用 fake，生产用 ffmpeg 实现。
type Func func(ctx context.Context, video db.Video) (Result, error)

// Options worker 池配置。
type Options struct {
	Transcode   Func
	Concurrency int
	MaxAttempts int
	JobTimeout  time.Duration
}

const pollInterval = time.Second

// Worker 转码 worker 池。
type Worker struct {
	pool *pgxpool.Pool
	q    *db.Queries
	opts Options

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewWorker 构造 worker 池。
func NewWorker(pool *pgxpool.Pool, opts Options) *Worker {
	return &Worker{pool: pool, q: db.New(pool), opts: opts}
}

// Start 启动：先恢复超时任务，再拉起 N 个轮询 goroutine。
func (w *Worker) Start(ctx context.Context) error {
	if n, err := w.RecoverStale(ctx); err != nil {
		return err
	} else if n > 0 {
		slog.Info("transcode: requeued stale jobs", "count", n)
	}

	ctx, w.cancel = context.WithCancel(ctx)
	for range w.opts.Concurrency {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			for {
				processed := w.ProcessOne(ctx)
				if ctx.Err() != nil {
					return
				}
				if !processed {
					select {
					case <-ctx.Done():
						return
					case <-time.After(pollInterval):
					}
				}
			}
		}()
	}
	return nil
}

// Stop 停止轮询并等待在途任务结束（在途 ffmpeg 因 ctx 取消而中断，任务会按失败重试路径回队）。
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

// RecoverStale 把超时的 running 任务重置回 queued（进程重启恢复）。
func (w *Worker) RecoverStale(ctx context.Context) (int64, error) {
	staleSeconds := int32((2 * w.opts.JobTimeout).Seconds())
	n, err := w.q.RequeueStaleTranscodeJobs(ctx, staleSeconds)
	if err != nil {
		return 0, fmt.Errorf("transcode: requeue stale: %w", err)
	}
	return n, nil
}

// claim 认领一个 queued 任务；无任务返回 ok=false。
func (w *Worker) claim(ctx context.Context) (db.TranscodeJob, bool, error) {
	job, err := w.q.ClaimTranscodeJob(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.TranscodeJob{}, false, nil
	}
	if err != nil {
		return db.TranscodeJob{}, false, fmt.Errorf("transcode: claim: %w", err)
	}
	return job, true, nil
}

// ProcessOne 认领并处理一个任务，返回是否处理了任务（无任务或认领失败返回 false）。
func (w *Worker) ProcessOne(ctx context.Context) bool {
	job, ok, err := w.claim(ctx)
	if err != nil || !ok {
		if err != nil {
			slog.ErrorContext(ctx, "transcode: claim failed", "error", err)
		}
		return false
	}

	video, err := w.q.GetVideoForTranscode(ctx, job.VideoID)
	if err != nil {
		slog.ErrorContext(ctx, "transcode: load video failed", "job_id", job.ID, "error", err)
		w.finishFailure(ctx, job, fmt.Sprintf("load video: %v", err))
		return true
	}
	if err := w.q.SetVideoTranscoding(ctx, video.ID); err != nil {
		slog.ErrorContext(ctx, "transcode: mark transcoding failed", "job_id", job.ID, "error", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, w.opts.JobTimeout)
	result, err := w.opts.Transcode(runCtx, video)
	cancel()
	if err != nil {
		slog.WarnContext(ctx, "transcode: attempt failed",
			"job_id", job.ID, "video_id", video.ID, "attempt", job.Attempts, "error", err)
		w.finishFailure(ctx, job, err.Error())
		return true
	}

	err = pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		q := w.q.WithTx(tx)
		if err := q.SucceedTranscodeJob(ctx, job.ID); err != nil {
			return err
		}
		return q.SetVideoTranscoded(ctx, db.SetVideoTranscodedParams{
			ID:            video.ID,
			HlsPath:       &result.HLSPath,
			ThumbnailPath: &result.ThumbnailPath,
			Duration:      &result.Duration,
			Width:         &result.Width,
			Height:        &result.Height,
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "transcode: finalize failed", "job_id", job.ID, "error", err)
	}
	return true
}

// finishFailure 失败路径：未达上限回队重试，达上限任务失败 + 视频转码失败终态。
func (w *Worker) finishFailure(ctx context.Context, job db.TranscodeJob, reason string) {
	terminal := int(job.Attempts) >= w.opts.MaxAttempts
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		q := w.q.WithTx(tx)
		if terminal {
			if err := q.FailTranscodeJob(ctx, db.FailTranscodeJobParams{ID: job.ID, LastError: &reason}); err != nil {
				return err
			}
			return q.SetVideoTranscodeFailed(ctx, job.VideoID)
		}
		if err := q.RequeueTranscodeJob(ctx, db.RequeueTranscodeJobParams{ID: job.ID, LastError: &reason}); err != nil {
			return err
		}
		return q.SetVideoPendingTranscode(ctx, job.VideoID)
	})
	if err != nil {
		slog.ErrorContext(ctx, "transcode: record failure failed", "job_id", job.ID, "error", err)
	}
}
