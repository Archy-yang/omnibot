package db

import (
	"wechat-intelligent-bot/internal/domain/user"
	"wechat-intelligent-bot/pkg/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// InitDB 初始化数据库连接
func InitDB(cfg *config.DatabaseConfig) (*gorm.DB, error) {
	var db *gorm.DB
	var err error

	switch cfg.Driver {
	case "sqlite", "":
		db, err = gorm.Open(sqlite.Open(cfg.DSN), &gorm.Config{})
	default:
		db, err = gorm.Open(sqlite.Open(cfg.DSN), &gorm.Config{})
	}

	if err != nil {
		return nil, err
	}

	// 自动迁移表结构
	err = autoMigrate(db)
	if err != nil {
		return nil, err
	}

	return db, nil
}

// autoMigrate 自动迁移数据库表
func autoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&user.User{},
		&user.WechatAccount{},
	)
}
