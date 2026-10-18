package memory

// user_pipeline_llm.go — 沉淀管线/主动管家共用的 LLM 按用户解析(2026-10 架构审计 #4:
// 从 API 装配层迁入——领域适配逻辑(指纹缓存/回落语义)应住在 service 层)。

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"sync"
	"time"

	agentpkg "omnibot/internal/service/agent"
	"omnibot/pkg/logger"

	"go.uber.org/zap"

	userService "omnibot/internal/service/user"
)

// UserPipelineLLM 实现 PipelineLLM(与 butler.ButlerLLM 同形:Complete(ctx, userID, system, user))。
// 用户在 Web 端配置了自定义 LLM → 用该配置构造客户端(指纹缓存,改配置免重启);
// 未配置 → 回落系统默认;两者皆无 → ErrPipelineNoLLM(管线静默跳过,水位不推进)。
// 注:配置读取已随 2026-10 缓存下沉(LLMConfigService)免 DB 热点,这里只缓存 client 对象。
type UserPipelineLLM struct {
	Svc    userService.LLMConfigService
	System agentpkg.LLMClient // 系统默认兜底,可 nil
	mu     sync.Mutex
	cache  map[int64]struct {
		fingerprint string
		client      agentpkg.LLMClient
	}
}

// NewUserPipelineLLM 创建按用户解析的管线 LLM。
func NewUserPipelineLLM(svc userService.LLMConfigService, system agentpkg.LLMClient) *UserPipelineLLM {
	return &UserPipelineLLM{Svc: svc, System: system, cache: make(map[int64]struct {
		fingerprint string
		client      agentpkg.LLMClient
	})}
}

// ClientFor 取该用户的 LLM 客户端(指纹缓存)。
func (a *UserPipelineLLM) ClientFor(userID int64) (agentpkg.LLMClient, error) {
	apiKey, baseURL, model, hasCustom, err := a.Svc.GetConfigForUser(userID)
	if err != nil {
		logger.Warn("memory: 读用户 LLM 配置失败,沉淀回落系统默认",
			zap.Int64("user_id", userID), zap.Error(err))
	}
	if hasCustom {
		// 指纹 = 关键字段拼接 + key 哈希(不含明文);指纹变了自动重建
		sum := sha1.Sum([]byte(baseURL + "|" + model + "|" + apiKey))
		fingerprint := hex.EncodeToString(sum[:])
		a.mu.Lock()
		cached, hit := a.cache[userID]
		a.mu.Unlock()
		if hit && cached.fingerprint == fingerprint && cached.client != nil {
			return cached.client, nil
		}
		// 非流式长输入:TTFB 放宽到 120s(全文生成完才回响应头,对话的 30s 默认不够)
		client := agentpkg.NewOpenAILLMClientWithTTFB(apiKey, baseURL, model, 120*time.Second, 120*time.Second)
		a.mu.Lock()
		a.cache[userID] = struct {
			fingerprint string
			client      agentpkg.LLMClient
		}{fingerprint, client}
		a.mu.Unlock()
		return client, nil
	}
	if a.System != nil {
		return a.System, nil
	}
	return nil, ErrPipelineNoLLM
}

// Complete 非流式补全。
func (a *UserPipelineLLM) Complete(ctx context.Context, userID int64, system, user string) (string, error) {
	client, err := a.ClientFor(userID)
	if err != nil {
		return "", err
	}
	messages := []map[string]interface{}{
		{"role": "system", "content": system},
		{"role": "user", "content": user},
	}
	content, _, err := client.ChatCompletion(ctx, messages, nil)
	return content, err
}
