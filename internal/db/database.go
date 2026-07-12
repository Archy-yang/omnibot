package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"omnibot/internal/domain/conversation"
	"omnibot/internal/domain/memory"
	"omnibot/internal/domain/user"
	"omnibot/pkg/config"
	zaplogger "omnibot/pkg/logger"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 数据库驱动常量
const (
	DriverSQLite     = "sqlite"
	DriverPostgreSQL = "postgres"
	DriverMySQL      = "mysql"
)

// 错误定义
var (
	ErrUnsupportedDriver = errors.New("unsupported database driver")
	ErrInitFailed        = errors.New("database initialization failed")
	ErrHealthCheckFailed = errors.New("database health check failed")
)

// Database 数据库实例
type Database struct {
	*gorm.DB
	config *config.DatabaseConfig
}

// Option 数据库配置选项
type Option func(*gorm.Config)

// WithSkipDefaultTransaction 跳过默认事务
func WithSkipDefaultTransaction(skip bool) Option {
	return func(cfg *gorm.Config) {
		cfg.SkipDefaultTransaction = skip
	}
}

// InitDB 初始化数据库连接
func InitDB(cfg *config.DatabaseConfig, opts ...Option) (*Database, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInitFailed, err)
	}

	gormConfig := &gorm.Config{
		SkipDefaultTransaction: false,
		PrepareStmt:            true, // 启用预编译语句缓存
		TranslateError:         true, // 将驱动错误翻译为 GORM 通用错误(如唯一约束冲突 → ErrDuplicatedKey)
	}

	// 应用选项
	for _, opt := range opts {
		opt(gormConfig)
	}

	// 根据驱动创建连接
	var db *gorm.DB
	var err error

	switch cfg.Driver {
	case DriverSQLite, "":
		db, err = gorm.Open(sqlite.Open(cfg.DSN), gormConfig)
	case DriverPostgreSQL:
		db, err = gorm.Open(postgres.Open(cfg.DSN), gormConfig)
	case DriverMySQL:
		return nil, fmt.Errorf("%w: mysql driver not implemented yet", ErrUnsupportedDriver)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedDriver, cfg.Driver)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: failed to open database: %v", ErrInitFailed, err)
	}

	// 配置连接池
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to get sql.DB: %v", ErrInitFailed, err)
	}

	// 设置连接池参数
	maxConns := cfg.MaxConns
	if maxConns <= 0 {
		maxConns = 25 // 默认最大连接数
	}
	sqlDB.SetMaxOpenConns(maxConns)
	sqlDB.SetMaxIdleConns(maxConns / 2)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	// 验证连接
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("%w: failed to ping database: %v", ErrInitFailed, err)
	}

	if cfg.Driver == DriverPostgreSQL {
		if err := ensurePostgresExtensions(db); err != nil {
			return nil, fmt.Errorf("%w: postgres extension initialization failed: %v", ErrInitFailed, err)
		}
	}

	// 自动迁移表结构
	if err := autoMigrate(db); err != nil {
		return nil, fmt.Errorf("%w: migration failed: %v", ErrInitFailed, err)
	}

	zaplogger.InfoWithFields("Database initialized successfully",
		zap.String("driver", cfg.Driver),
		zap.Int("max_conns", maxConns),
	)

	return &Database{
		DB:     db,
		config: cfg,
	}, nil
}

// validateConfig 验证数据库配置
func validateConfig(cfg *config.DatabaseConfig) error {
	if cfg == nil {
		return errors.New("database config is nil")
	}
	if cfg.DSN == "" {
		return errors.New("database DSN is required")
	}
	return nil
}

// autoMigrate 自动迁移数据库表
func autoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&user.User{},
		&user.UserChannel{},
		&user.LLMConfig{},
		&user.UserCredential{},
		&user.FeishuBindCode{},
		&conversation.Message{},
		&conversation.AgentStep{},
		&memory.Memory{},
	)
}

func ensurePostgresExtensions(db *gorm.DB) error {
	return db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error
}

// HealthCheck 健康检查
func (db *Database) HealthCheck(ctx context.Context) error {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrHealthCheckFailed, err)
	}

	// 带超时的 ping
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrHealthCheckFailed, err)
	}

	return nil
}

// Stats 获取连接池统计信息
func (db *Database) Stats() map[string]interface{} {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	stats := sqlDB.Stats()
	return map[string]interface{}{
		"max_open_conns":      stats.MaxOpenConnections,
		"open_conns":          stats.OpenConnections,
		"in_use":              stats.InUse,
		"idle":                stats.Idle,
		"wait_count":          stats.WaitCount,
		"wait_duration":       stats.WaitDuration.String(),
		"max_idle_closed":     stats.MaxIdleClosed,
		"max_lifetime_closed": stats.MaxLifetimeClosed,
	}
}

// Close 优雅关闭数据库连接
func (db *Database) Close() error {
	sqlDB, err := db.DB.DB()
	if err != nil {
		return err
	}

	// 等待当前操作完成（最多 5 秒）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- sqlDB.Close()
	}()

	select {
	case err := <-done:
		if err == nil {
			zaplogger.Info("Database connection closed gracefully")
		}
		return err
	case <-ctx.Done():
		zaplogger.Warn("Database close timeout, force closing")
		return sqlDB.Close()
	}
}

// Transaction 执行事务（自动提交/回滚）
func (db *Database) Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	tx := db.DB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r) // 重新抛出 panic
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback().Error; rbErr != nil {
			zaplogger.ErrorWithFields("Transaction rollback failed",
				zap.Error(err),
				zap.NamedError("rollback_error", rbErr),
			)
		}
		return err
	}

	return tx.Commit().Error
}

// GetGormDB 获取原始 *gorm.DB
func (db *Database) GetGormDB() *gorm.DB {
	return db.DB
}

// Driver 获取当前驱动
func (db *Database) Driver() string {
	return db.config.Driver
}

// NewTestDB 创建测试用的内存数据库（返回 *gorm.DB）
// 仅用于测试
func NewTestDB(t interface {
	Fatalf(string, ...interface{})
	Cleanup(func())
}) *gorm.DB {
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, err := InitDB(cfg)
	if err != nil {
		t.Fatalf("Failed to init test DB: %v", err)
	}
	if db == nil {
		t.Fatalf("Test DB is nil")
	}

	// 注册清理函数
	t.Cleanup(func() {
		_ = db.Close()
	})

	return db.GetGormDB()
}
