//go:build postgres_integration
// +build postgres_integration

package db

import (
	"context"
	"testing"

	"omnibot/pkg/config"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// NewPostgresTestDB 创建 PostgreSQL 测试容器
// 使用方式: go test -tags=postgres_integration ./...
func NewPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	cfg := newPostgresTestConfig(t)
	db, err := InitDB(cfg)
	if err != nil {
		t.Fatalf("Failed to init test DB: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db.GetGormDB()
}

func NewPostgresRawTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	cfg := newPostgresTestConfig(t)
	db, err := gorm.Open(postgresDriver.Open(cfg.DSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open raw postgres test DB: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func newPostgresTestConfig(t *testing.T) *config.DatabaseConfig {
	t.Helper()

	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"pgvector/pgvector:0.8.0-pg16",
		postgres.WithDatabase("omnibot_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test123"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("Failed to start postgres container: %v", err)
	}

	t.Cleanup(func() {
		if err := postgresContainer.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate postgres container: %v", err)
		}
	})

	dsn, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get postgres connection string: %v", err)
	}

	return &config.DatabaseConfig{
		Driver:   "postgres",
		DSN:      dsn,
		MaxConns: 5,
	}
}
