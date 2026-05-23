package db

import (
	"context"
	"testing"

	"omnibot/internal/domain/user"
	"omnibot/pkg/config"
	zaplogger "omnibot/pkg/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMain(m *testing.M) {
	// 初始化 logger，避免未初始化 panic
	zaplogger.Init(config.LoggerConfig{
		Level:  "error",
		Format: "console",
		Output: "stdout",
	})
	m.Run()
}

func TestInitDB_SQLite(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Driver:   "sqlite",
		DSN:      ":memory:",
		MaxConns: 5,
	}

	db, err := InitDB(cfg)

	require.NoError(t, err)
	assert.NotNil(t, db)
	assert.Equal(t, "sqlite", db.Driver())

	// 验证表已创建成功
	hasUserTable := db.GetGormDB().Migrator().HasTable(&user.User{})
	assert.True(t, hasUserTable)
	hasWechatAccountTable := db.GetGormDB().Migrator().HasTable(&user.WechatAccount{})
	assert.True(t, hasWechatAccountTable)

	// 清理
	_ = db.Close()
}

func TestInitDB_PostgreSQL_DriverLoads(t *testing.T) {
	// 只验证驱动可以正常加载，不要求真实 PG 连接
	cfg := &config.DatabaseConfig{
		Driver:   "postgres",
		DSN:      "host=localhost port=1 invalid=1",
		MaxConns: 5,
	}

	_, err := InitDB(cfg, func(cfg *gorm.Config) {
		cfg.Logger = logger.Default.LogMode(logger.Silent)
	})

	// 应该返回连接错误，而不是驱动不支持或 panic
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), "unsupported database driver")
}

func TestHealthCheck(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, _ := InitDB(cfg)
	require.NotNil(t, db)
	defer db.Close()

	err := db.HealthCheck(context.Background())

	assert.NoError(t, err)
}

func TestTransaction_Commit(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, _ := InitDB(cfg)
	require.NotNil(t, db)
	defer db.Close()

	// 执行事务：创建测试表并插入数据
	err := db.Transaction(context.Background(), func(tx *gorm.DB) error {
		type TestTable struct {
			ID   int
			Name string
		}
		err := tx.AutoMigrate(&TestTable{})
		if err != nil {
			return err
		}
		return tx.Create(&TestTable{ID: 1, Name: "test"}).Error
	})

	assert.NoError(t, err)

	// 验证数据已提交
	var count int64
	db.GetGormDB().Table("test_tables").Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestTransaction_Rollback(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, _ := InitDB(cfg)
	require.NotNil(t, db)
	defer db.Close()

	// 先创建表
	type TestTable struct {
		ID   int
		Name string
	}
	_ = db.GetGormDB().AutoMigrate(&TestTable{})

	// 执行事务：插入数据后返回错误触发回滚
	err := db.Transaction(context.Background(), func(tx *gorm.DB) error {
		tx.Create(&TestTable{ID: 1, Name: "test"})
		return assert.AnError // 触发回滚
	})

	assert.Error(t, err)

	// 验证数据已回滚（表应该是空的）
	var count int64
	db.GetGormDB().Table("test_tables").Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestStats(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Driver:   "sqlite",
		DSN:      ":memory:",
		MaxConns: 10,
	}

	db, _ := InitDB(cfg)
	require.NotNil(t, db)
	defer db.Close()

	stats := db.Stats()

	assert.NotNil(t, stats)
	assert.Contains(t, stats, "max_open_conns")
	assert.Contains(t, stats, "open_conns")
	assert.Contains(t, stats, "idle")

	// 验证值的类型正确
	maxOpen, ok := stats["max_open_conns"].(int)
	assert.True(t, ok)
	assert.GreaterOrEqual(t, maxOpen, 0)
}

func TestClose(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, _ := InitDB(cfg)
	require.NotNil(t, db)

	err := db.Close()

	assert.NoError(t, err)
}

func TestGetGormDB(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, _ := InitDB(cfg)
	require.NotNil(t, db)
	defer db.Close()

	gormDB := db.GetGormDB()

	assert.NotNil(t, gormDB)

	// 验证返回的 DB 可以正常使用
	type TestModel struct {
		ID int
	}
	err := gormDB.AutoMigrate(&TestModel{})
	assert.NoError(t, err)
}

func TestAutoMigration_MessagesTable(t *testing.T) {
	// 使用现有测试数据库创建逻辑
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, err := InitDB(cfg)
	require.NoError(t, err)
	require.NotNil(t, db)
	defer db.Close()

	// 验证 messages 表是否存在
	var count int64
	err = db.GetGormDB().Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='messages'").Scan(&count).Error
	if err != nil {
		t.Fatalf("Failed to check messages table: %v", err)
	}
	if count == 0 {
		t.Error("messages table was not created by AutoMigration")
	}
}

func TestAutoMigration_MemoriesTable(t *testing.T) {
	cfg := &config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}

	db, err := InitDB(cfg)
	require.NoError(t, err)
	require.NotNil(t, db)
	defer db.Close()

	var count int64
	err = db.GetGormDB().Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memories'").Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestNewTestDB(t *testing.T) {
	testDB := NewTestDB(t)
	require.NotNil(t, testDB)

	// 验证可以正常使用
	type TestModel struct {
		ID int
	}
	err := testDB.AutoMigrate(&TestModel{})
	require.NoError(t, err)

	err = testDB.Create(&TestModel{ID: 1}).Error
	require.NoError(t, err)

	var count int64
	testDB.Model(&TestModel{}).Count(&count)
	assert.Equal(t, int64(1), count)
}
