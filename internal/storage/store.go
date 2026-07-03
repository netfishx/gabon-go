// Package storage S3 协议对象存储：预签名直传、对象探测、worker 上传下载。
// endpoint 可配是硬约束（ADR-0005）：生产任意 S3 兼容实现，本地与测试用 MinIO。
package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config 对象存储连接配置，全部来自环境变量。
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

// Store 单桶对象存储客户端。
type Store struct {
	client *minio.Client
	bucket string
}

// New 构造存储客户端。
func New(cfg Config) (*Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: new client: %w", err)
	}
	return &Store{client: client, bucket: cfg.Bucket}, nil
}

// EnsureBucket 确保桶存在（本地开发与测试基建用；生产桶应预建）。
func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("storage: bucket exists: %w", err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("storage: make bucket: %w", err)
	}
	return nil
}

// PresignPut 生成对象的预签名 PUT 地址。
func (s *Store) PresignPut(ctx context.Context, objectPath string, expiry time.Duration) (string, error) {
	u, err := s.client.PresignedPutObject(ctx, s.bucket, objectPath, expiry)
	if err != nil {
		return "", fmt.Errorf("storage: presign put: %w", err)
	}
	return u.String(), nil
}

// Exists 探测对象是否存在。
func (s *Store) Exists(ctx context.Context, objectPath string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, objectPath, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	var resp minio.ErrorResponse
	if errors.As(err, &resp) && resp.Code == minio.NoSuchKey {
		return false, nil
	}
	return false, fmt.Errorf("storage: stat object: %w", err)
}

// Download 拉取对象到本地文件（转码 worker 用）。
func (s *Store) Download(ctx context.Context, objectPath, localPath string) error {
	if err := s.client.FGetObject(ctx, s.bucket, objectPath, localPath, minio.GetObjectOptions{}); err != nil {
		return fmt.Errorf("storage: download %s: %w", objectPath, err)
	}
	return nil
}

// UploadFile 上传本地文件到对象路径（转码产物回传用）。
func (s *Store) UploadFile(ctx context.Context, localPath, objectPath, contentType string) error {
	if _, err := s.client.FPutObject(ctx, s.bucket, objectPath, localPath, minio.PutObjectOptions{
		ContentType: contentType,
	}); err != nil {
		return fmt.Errorf("storage: upload %s: %w", objectPath, err)
	}
	return nil
}
