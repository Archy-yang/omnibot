package user

import (
	"testing"

	"omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
	repo "omnibot/internal/repository/user"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 用户级向量配置测试(12-记忆系统技术方案 §5.3 / TDD#12):
//   - UpdateFullConfig 持久化 embedding 字段,API Key 加密落库
//   - GetEmbeddingConfigForUser 解密返回完整配置;未配置返回 false
//   - 嵌入配置不完整 → 校验拒绝

func embeddingTestSetup(t *testing.T) LLMConfigService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&user.LLMConfig{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return NewLLMConfigService(repo.NewLLMConfigRepository(db))
}

func validBaseReq() UpdateConfigRequest {
	return UpdateConfigRequest{
		Provider:    "custom_openai_compatible",
		APIKey:      "sk-chat-key-123456",
		BaseURL:     "https://llm.example.com/v1",
		Model:       "chat-model",
		Temperature: 0.7,
		MaxTokens:   2048,
	}
}

func withEmbedding(req UpdateConfigRequest) UpdateConfigRequest {
	req.EmbeddingProvider = "openai_compatible"
	req.EmbeddingBaseURL = "https://qianfan.baidubce.com/v2"
	req.EmbeddingAPIKey = "sk-embed-key-654321"
	req.EmbeddingModel = "bge-large-zh"
	req.EmbeddingDims = 1024
	return req
}

// TestUpdateFullConfig_EmbeddingPersisted 加密落库 + 解密读回。
func TestUpdateFullConfig_EmbeddingPersisted(t *testing.T) {
	svc := embeddingTestSetup(t)
	if err := svc.UpdateFullConfig(42, withEmbedding(validBaseReq())); err != nil {
		t.Fatalf("UpdateFullConfig: %v", err)
	}

	cfg, ok, err := svc.GetEmbeddingConfigForUser(42)
	if err != nil || !ok {
		t.Fatalf("GetEmbeddingConfigForUser: ok=%v err=%v, want true/nil", ok, err)
	}
	if cfg.Provider != "openai_compatible" || cfg.Model != "bge-large-zh" || cfg.Dims != 1024 {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.BaseURL != "https://qianfan.baidubce.com/v2" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.APIKey != "sk-embed-key-654321" {
		t.Errorf("APIKey 应解密为原文, got %q", cfg.APIKey)
	}
}

// TestEmbeddingAPIKeyEncryptedAtRest 落库的是密文,非原文(安全红线)。
func TestEmbeddingAPIKeyEncryptedAtRest(t *testing.T) {
	svc := embeddingTestSetup(t)
	if err := svc.UpdateFullConfig(42, withEmbedding(validBaseReq())); err != nil {
		t.Fatalf("UpdateFullConfig: %v", err)
	}
	// 直接查库验证密文
	repoSvc := svc.(*GormLLMConfigService)
	raw, err := repoSvc.repo.GetByUserID(42)
	if err != nil {
		t.Fatalf("repo get: %v", err)
	}
	if raw.EmbeddingAPIKey == "" || raw.EmbeddingAPIKey == "sk-embed-key-654321" {
		t.Errorf("EmbeddingAPIKey 应为密文, got %q", raw.EmbeddingAPIKey)
	}
	if _, err := crypto.Decrypt(raw.EmbeddingAPIKey); err != nil {
		t.Errorf("密文应可被 crypto.Decrypt 解密: %v", err)
	}
}

// TestUpdateFullConfig_EmbeddingIncomplete 嵌入配置部分填写 → 拒绝(避免半配置导致的静默混乱)。
func TestUpdateFullConfig_EmbeddingIncomplete(t *testing.T) {
	svc := embeddingTestSetup(t)
	req := validBaseReq()
	req.EmbeddingProvider = "openai_compatible"
	req.EmbeddingModel = "bge-large-zh"
	// 缺 base_url/api_key/dims
	err := svc.UpdateFullConfig(42, req)
	if err == nil {
		t.Fatal("不完整的 embedding 配置应被拒绝")
	}

	// 全部为空 → 合法(用户级关闭,用系统默认)
	req2 := validBaseReq()
	if err := svc.UpdateFullConfig(42, req2); err != nil {
		t.Fatalf("全空 embedding 配置应合法: %v", err)
	}
	if _, ok, _ := svc.GetEmbeddingConfigForUser(42); ok {
		t.Error("未配置 embedding 时应返回 false")
	}
}

// TestUpdateFullConfig_EmbeddingUnknownProvider 未知嵌入 provider 拒绝。
func TestUpdateFullConfig_EmbeddingUnknownProvider(t *testing.T) {
	svc := embeddingTestSetup(t)
	req := withEmbedding(validBaseReq())
	req.EmbeddingProvider = "magic_provider"
	if err := svc.UpdateFullConfig(42, req); err == nil {
		t.Fatal("未知 embedding provider 应被拒绝")
	}
}

// TestUpdateFullConfig_EmbeddingDimsInvalid dims 非正数拒绝。
func TestUpdateFullConfig_EmbeddingDimsInvalid(t *testing.T) {
	svc := embeddingTestSetup(t)
	req := withEmbedding(validBaseReq())
	req.EmbeddingDims = 0
	if err := svc.UpdateFullConfig(42, req); err == nil {
		t.Fatal("dims=0 应被拒绝")
	}
}

// TestGetEmbeddingConfig_NoUser 无配置用户返回 false 不报错。
func TestGetEmbeddingConfig_NoUser(t *testing.T) {
	svc := embeddingTestSetup(t)
	_, ok, err := svc.GetEmbeddingConfigForUser(999)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ok {
		t.Error("无配置应返回 false")
	}
}
