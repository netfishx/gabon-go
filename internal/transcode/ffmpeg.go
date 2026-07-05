package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/storage"
)

// NewFFmpeg 生产转码实现：下载原片 → ffprobe 探测 → ffmpeg 产出单码率 HLS（封顶 720p）
// 与首帧图 → 产物回传对象存储。产物路径约定：hls/{videoID}/index.m3u8、thumbs/{videoID}.jpg。
func NewFFmpeg(store *storage.Store) Func {
	return func(ctx context.Context, video db.Video) (Result, error) {
		tmp, err := os.MkdirTemp("", "gabon-transcode-*")
		if err != nil {
			return Result{}, fmt.Errorf("mkdtemp: %w", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()

		raw := filepath.Join(tmp, "raw")
		if err := store.Download(ctx, video.StoragePath, raw); err != nil {
			return Result{}, err
		}

		duration, width, height, err := probe(ctx, raw)
		if err != nil {
			return Result{}, err
		}

		outDir := filepath.Join(tmp, "out")
		if err := os.Mkdir(outDir, 0o750); err != nil {
			return Result{}, fmt.Errorf("mkdir out: %w", err)
		}
		// 单码率 HLS：分辨率封顶 720（短边），保持宽高比且为偶数
		// #nosec G204 -- 可变参数仅为进程自建的临时目录路径，非用户输入
		hlsCmd := exec.CommandContext(
			ctx, "ffmpeg", "-y", "-i", raw,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-vf", "scale='if(gt(iw,ih),-2,min(720,iw))':'if(gt(iw,ih),min(720,ih),-2)'",
			"-c:a", "aac",
			"-hls_time", "4", "-hls_playlist_type", "vod",
			"-hls_segment_filename", filepath.Join(outDir, "seg_%03d.ts"),
			filepath.Join(outDir, "index.m3u8"),
		)
		if out, err := hlsCmd.CombinedOutput(); err != nil {
			return Result{}, fmt.Errorf("ffmpeg hls: %w: %s", err, tail(out))
		}
		thumb := filepath.Join(tmp, "thumb.jpg")
		// #nosec G204 -- 同上，受控临时路径
		thumbCmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", raw,
			"-vf", "select=eq(n\\,0)", "-vframes", "1", thumb)
		if out, err := thumbCmd.CombinedOutput(); err != nil {
			return Result{}, fmt.Errorf("ffmpeg thumbnail: %w: %s", err, tail(out))
		}

		hlsPrefix := fmt.Sprintf("hls/%d", video.ID)
		entries, err := os.ReadDir(outDir)
		if err != nil {
			return Result{}, fmt.Errorf("read out dir: %w", err)
		}
		for _, e := range entries {
			contentType := "video/mp2t"
			if filepath.Ext(e.Name()) == ".m3u8" {
				contentType = "application/vnd.apple.mpegurl"
			}
			if err := store.UploadFile(ctx,
				filepath.Join(outDir, e.Name()),
				hlsPrefix+"/"+e.Name(), contentType); err != nil {
				return Result{}, err
			}
		}
		thumbPath := fmt.Sprintf("thumbs/%d.jpg", video.ID)
		if err := store.UploadFile(ctx, thumb, thumbPath, "image/jpeg"); err != nil {
			return Result{}, err
		}

		return Result{
			HLSPath:       hlsPrefix + "/index.m3u8",
			ThumbnailPath: thumbPath,
			Duration:      duration,
			Width:         width,
			Height:        height,
		}, nil
	}
}

// probe 用 ffprobe 探测时长与第一条视频流的宽高。
func probe(ctx context.Context, path string) (duration, width, height int32, err error) {
	// #nosec G204 -- path 为进程自建的临时文件路径
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-print_format", "json", "-show_streams", "-show_format", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("ffprobe: %w", err)
	}
	var parsed struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int32  `json:"width"`
			Height    int32  `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0, 0, 0, fmt.Errorf("parse ffprobe output: %w", err)
	}
	for _, s := range parsed.Streams {
		if s.CodecType == "video" {
			width, height = s.Width, s.Height
			break
		}
	}
	if width == 0 || height == 0 {
		return 0, 0, 0, fmt.Errorf("no video stream found")
	}
	seconds, err := strconv.ParseFloat(parsed.Format.Duration, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse duration %q: %w", parsed.Format.Duration, err)
	}
	return int32(math.Round(seconds)), width, height, nil
}

func tail(out []byte) []byte {
	const n = 400
	if len(out) > n {
		return out[len(out)-n:]
	}
	return out
}
