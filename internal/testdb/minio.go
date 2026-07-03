package testdb

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

// MinIO 测试容器信息。
type MinIO struct {
	Endpoint  string // host:port，不含 scheme
	AccessKey string
	SecretKey string
}

// StartMinIO 启动一次性 MinIO 容器（与 Postgres 同样的创建重试策略）。
func StartMinIO(ctx context.Context) (*MinIO, func(), error) {
	container, err := RunWithRetry(ctx, func(ctx context.Context) (*tcminio.MinioContainer, error) {
		return tcminio.Run(ctx, "minio/minio:RELEASE.2024-01-16T16-07-38Z")
	})
	if err != nil {
		return nil, nil, fmt.Errorf("start minio container: %w", err)
	}
	terminate := func() { _ = testcontainers.TerminateContainer(container) }

	endpoint, err := container.ConnectionString(ctx)
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("minio connection string: %w", err)
	}
	return &MinIO{
		Endpoint:  endpoint,
		AccessKey: container.Username,
		SecretKey: container.Password,
	}, terminate, nil
}
