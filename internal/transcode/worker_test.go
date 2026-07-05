package transcode

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/testdb"
)

// 转码状态机的服务缝测试（issue #14）：认领互斥、成功翻转、重试与终态、超时恢复。
// 转码执行注入 fake——真 ffmpeg 只在 app 包的冒烟 E2E 里跑一条。

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		log.Printf("transcode setup: %v", err)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

var fixtureSeq atomic.Int64

// newVideoJob 直插测试夹具：客户 + 待转码视频 + queued 任务（被测缝是 worker，不是 confirm）。
func newVideoJob(t *testing.T) (videoID, jobID int64) {
	t.Helper()
	ctx := context.Background()
	n := fixtureSeq.Add(1)
	var customerID int64
	if err := testPool.QueryRow(
		ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ($1, $2, 'x', $3) RETURNING id`,
		fmt.Sprintf("pub_tc_%d", n), fmt.Sprintf("tc_user_%d", n), fmt.Sprintf("TC%06d", n),
	).Scan(&customerID); err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	if err := testPool.QueryRow(
		ctx,
		`INSERT INTO videos (public_id, customer_id, title, storage_path)
		 VALUES ($1, $2, 't', $3) RETURNING id`,
		fmt.Sprintf("vid_tc_%d", n), customerID, fmt.Sprintf("videos/%d/raw.mp4", customerID),
	).Scan(&videoID); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := testPool.QueryRow(
		ctx,
		`INSERT INTO transcode_jobs (video_id) VALUES ($1) RETURNING id`, videoID,
	).Scan(&jobID); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	return videoID, jobID
}

func jobRow(t *testing.T, jobID int64) (status string, attempts int) {
	t.Helper()
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT status, attempts FROM transcode_jobs WHERE id = $1`, jobID,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("query job: %v", err)
	}
	return status, attempts
}

func videoRow(t *testing.T, videoID int64) (status string, hlsPath, thumbPath *string, duration *int32) {
	t.Helper()
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT status, hls_path, thumbnail_path, duration FROM videos WHERE id = $1`, videoID,
	).Scan(&status, &hlsPath, &thumbPath, &duration); err != nil {
		t.Fatalf("query video: %v", err)
	}
	return status, hlsPath, thumbPath, duration
}

func testWorker(fn Func, maxAttempts int) *Worker {
	return NewWorker(testPool, Options{
		Transcode:   fn,
		Concurrency: 2,
		MaxAttempts: maxAttempts,
		JobTimeout:  30 * time.Second,
	})
}

func okResult() Result {
	return Result{HLSPath: "hls/x/index.m3u8", ThumbnailPath: "thumbs/x.jpg", Duration: 1, Width: 640, Height: 360}
}

func TestClaimMutualExclusion(t *testing.T) {
	const jobs = 6
	ids := make(map[int64]bool)
	for range jobs {
		_, jobID := newVideoJob(t)
		ids[jobID] = true
	}

	var mu sync.Mutex
	claimed := map[int64]int{}
	var wg sync.WaitGroup
	w := testWorker(nil, 3)
	for range 12 { // 多于任务数的并发认领
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, ok, err := w.claim(context.Background())
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if ok && ids[job.ID] {
				mu.Lock()
				claimed[job.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claimed) != jobs {
		t.Errorf("claimed %d distinct jobs, want %d", len(claimed), jobs)
	}
	for id, n := range claimed {
		if n != 1 {
			t.Errorf("job %d claimed %d times, want exactly once", id, n)
		}
	}
}

func TestProcessSuccessFlipsVideo(t *testing.T) {
	videoID, jobID := newVideoJob(t)
	w := testWorker(func(_ context.Context, _ db.Video) (Result, error) {
		return okResult(), nil
	}, 3)

	if processed := w.ProcessOne(context.Background()); !processed {
		t.Fatalf("ProcessOne found no job")
	}

	status, attempts := jobRow(t, jobID)
	if status != "succeeded" || attempts != 1 {
		t.Errorf("job = (%s, %d), want (succeeded, 1)", status, attempts)
	}
	vStatus, hls, thumb, dur := videoRow(t, videoID)
	if vStatus != "pending_review" {
		t.Errorf("video status = %s, want pending_review", vStatus)
	}
	if hls == nil || thumb == nil || dur == nil || *dur <= 0 {
		t.Errorf("video media fields not set: hls=%v thumb=%v dur=%v", hls, thumb, dur)
	}
}

func TestRetryThenTerminalFailure(t *testing.T) {
	videoID, jobID := newVideoJob(t)
	w := testWorker(func(_ context.Context, _ db.Video) (Result, error) {
		return Result{}, errors.New("boom")
	}, 3)

	for i := 1; i <= 3; i++ {
		if processed := w.ProcessOne(context.Background()); !processed {
			t.Fatalf("round %d: no job claimed", i)
		}
		status, attempts := jobRow(t, jobID)
		if attempts != i {
			t.Fatalf("round %d: attempts = %d", i, attempts)
		}
		if i < 3 && status != "queued" {
			t.Fatalf("round %d: status = %s, want queued (retry)", i, status)
		}
	}

	status, _ := jobRow(t, jobID)
	if status != "failed" {
		t.Errorf("job status = %s, want failed after max attempts", status)
	}
	vStatus, _, _, _ := videoRow(t, videoID)
	if vStatus != "transcode_failed" {
		t.Errorf("video status = %s, want transcode_failed", vStatus)
	}
}

func TestFailOnceThenSucceed(t *testing.T) {
	videoID, jobID := newVideoJob(t)
	var calls atomic.Int64
	w := testWorker(func(_ context.Context, _ db.Video) (Result, error) {
		if calls.Add(1) == 1 {
			return Result{}, errors.New("transient")
		}
		return okResult(), nil
	}, 3)

	w.ProcessOne(context.Background())
	w.ProcessOne(context.Background())

	status, attempts := jobRow(t, jobID)
	if status != "succeeded" || attempts != 2 {
		t.Errorf("job = (%s, %d), want (succeeded, 2)", status, attempts)
	}
	if vStatus, _, _, _ := videoRow(t, videoID); vStatus != "pending_review" {
		t.Errorf("video status = %s, want pending_review", vStatus)
	}
}

func TestRecoverStaleRequeues(t *testing.T) {
	_, jobID := newVideoJob(t)
	// 制造超时的 running 任务
	if _, err := testPool.Exec(context.Background(),
		`UPDATE transcode_jobs SET status = 'running', started_at = now() - interval '1 hour' WHERE id = $1`,
		jobID); err != nil {
		t.Fatalf("stage stale job: %v", err)
	}

	w := testWorker(nil, 3)
	n, err := w.RecoverStale(context.Background())
	if err != nil {
		t.Fatalf("recover stale: %v", err)
	}
	if n < 1 {
		t.Errorf("recovered %d, want >= 1", n)
	}
	if status, _ := jobRow(t, jobID); status != "queued" {
		t.Errorf("job status = %s, want queued", status)
	}
}
