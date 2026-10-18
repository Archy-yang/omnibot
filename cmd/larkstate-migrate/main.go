// larkstate-migrate — lark-cli 授权状态迁移到 DB(27 号 A2-3,一次性工具)。
//
// 把既有 HOME 目录下的两个 CLI 状态路径(.lark-cli + .local/share/lark-cli)
// 打包加密写入 lark_cli_states,作为指定用户的首版 blob。此后 Executor 的
// hydrate 以 DB 为准;原目录/volume 保留作回退保险。
//
// 用法(迁移容器内状态:先 docker cp 出来再本机执行):
//
//	docker cp omnibot-app:/root/.lark-cli /tmp/lark-mig/.lark-cli
//	docker cp omnibot-app:/root/.local/share/lark-cli /tmp/lark-mig/.local/share/lark-cli
//	go run ./cmd/larkstate-migrate -config configs/config.yaml -user 1 -home /tmp/lark-mig
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"omnibot/internal/db"
	"omnibot/internal/pkg/crypto"
	domain "omnibot/internal/domain/user"
	"omnibot/internal/service/larkhome"
	userRepo "omnibot/internal/repository/user"
	"omnibot/pkg/config"
)

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "❌ "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	configPath := flag.String("config", "configs/config.yaml", "服务配置路径")
	home := flag.String("home", "", "含 .lark-cli 与 .local/share/lark-cli 的 HOME 根目录(必填)")
	userID := flag.Int64("user", 1, "归属用户 ID")
	flag.Parse()

	if *home == "" {
		fatal("缺少 -home(包含 .lark-cli 与 .local/share/lark-cli 的目录)")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal("加载配置失败: %v", err)
	}
	encryptMaterial := cfg.Security.EncryptKey
	if encryptMaterial == "" {
		encryptMaterial = os.Getenv("LLM_CONFIG_ENCRYPT_KEY")
	}
	if encryptMaterial != "" {
		crypto.Init(encryptMaterial)
	} else {
		crypto.InitWithKey(crypto.LegacyDefaultKey)
	}
	dbConn, err := db.InitDB(&cfg.Database)
	if err != nil {
		fatal("连 DB 失败: %v", err)
	}

	// 前置检查:两个状态路径至少存在一个
	_, err1 := os.Stat(filepath.Join(*home, ".lark-cli"))
	_, err2 := os.Stat(filepath.Join(*home, ".local/share/lark-cli"))
	if err1 != nil && err2 != nil {
		fatal("%s 下既无 .lark-cli 也无 .local/share/lark-cli", *home)
	}

	mgr := larkhome.NewManager(userRepo.NewLarkCLIStateRepository(dbConn.GetGormDB()))
	// 借用 PackHome:把迁移目录伪装成 mgr 的 home 布局
	blob, err := larkhome.PackHome(*home)
	if err != nil {
		fatal("打包失败: %v", err)
	}
	repo := userRepo.NewLarkCLIStateRepository(dbConn.GetGormDB())
	if err := repo.Save(&domain.LarkCLIState{UserID: *userID, StateBlob: blob}); err != nil {
		fatal("写 DB 失败: %v", err)
	}
	got, err := repo.GetByUserID(*userID)
	if err != nil {
		fatal("回读失败: %v", err)
	}
	// 验证:解包到临时目录无错
	verifyDir := filepath.Join(os.TempDir(), fmt.Sprintf("lark-migrate-verify-%d", *userID))
	if err := larkhome.UnpackHome(got.StateBlob, verifyDir); err != nil {
		fatal("往返验证失败: %v", err)
	}
	_ = mgr // Manager 仅用于文档关联;迁移走 PackHome/Save 直连
	fmt.Printf("✅ 用户 %d 状态已迁移:blob %d 字节(version %d),往返验证通过(%s)\n", *userID, len(got.StateBlob), got.Version, verifyDir)
}
