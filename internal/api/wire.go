package api

// wire.go — 依赖装配(路由注册见 routes.go)。
// SetupRouter 原来是 759 行的「构造+注册」混排;拆分后本文件只做一件事:
// buildAppDeps 把全部服务/handler 一次构造完成,routes.go 只做路由注册。
// 适配器(digestAuditAdapter/userPipelineLLM/userEmbeddingResolver/
// subAgentLLMConfigAdapter)与系统默认装配辅助(buildEmbeddingProvider/
// newAgentLLMClient)也随之收拢到本文件。

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"

	agentprompt "omnibot/internal/agentprompt"
	"omnibot/internal/api/admin"
	"omnibot/internal/api/web"
	"omnibot/internal/api/wechat"
	channelwechat "omnibot/internal/channel/wechat"
	"omnibot/internal/client/llm"
	"omnibot/internal/db"
	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	"omnibot/internal/pkg/auth"
	"omnibot/internal/realtime"
	agentRepo "omnibot/internal/repository/agent"
	chatRepo "omnibot/internal/repository/chat"
	memoryRepo "omnibot/internal/repository/memory"
	mcpRepo "omnibot/internal/repository/mcp"
	toolRepo "omnibot/internal/repository/tool"
	subscriptionRepo "omnibot/internal/repository/subscription"
	userRepo "omnibot/internal/repository/user"
	agentpkg "omnibot/internal/service/agent"
	agenttools "omnibot/internal/service/agent/tools"
	chatService "omnibot/internal/service/chat"
	mcpService "omnibot/internal/service/mcp"
	memoryService "omnibot/internal/service/memory"
	subscriptionService "omnibot/internal/service/subscription"
	toolService "omnibot/internal/service/tool"
	userService "omnibot/internal/service/user"
	"omnibot/pkg/config"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// appDeps 路由注册所需的全部装配产物。
type appDeps struct {
	cfg *config.Config

	// handlers
	wechatHandler       *wechat.Handler
	adminHandler        *admin.Handler
	webHandler          *web.Handler
	agentTaskHandler    *web.AgentTaskHandler
	subscriptionHandler *web.SubscriptionHandler
	authHandler         *web.AuthHandler

	// 通信
	jwtSvc      *auth.JWTService
	realtimeHub *realtime.Hub

	// 飞书渠道启动所需
	bindingSvc   *userService.BindingService
	msgSvc       chatService.MessageService
	agentSvc     *agentpkg.AgentService
	llmConfigSvc userService.LLMConfigService
	subAgentSvc  *agentpkg.SubAgentService

	// 沉淀管线(可空:extraction.enabled=false 未启用)。
	// 暴露给手动触发工具(digest_manual_test.go,env 门控)复用同一装配。
	digestPipeline *memoryService.DigestPipeline

	// Phase 3:召回块构建器(手动回填工具用)。
	chunkEmbedder *chatService.ChunkEmbedder
}

// buildAppDeps 构造全部依赖(原 SetupRouter 前半段,行为零变化)。
func buildAppDeps(cfg *config.Config) *appDeps {
	// 创建 LLM 客户端
	llmClient, err := llm.NewClient(cfg.LLM)
	if err != nil {
		logger.Fatal("Failed to create LLM client", zap.Error(err))
	}

	// 初始化数据库连接
	dbConn, err := db.InitDB(&cfg.Database)
	if err != nil {
		logger.Fatal("Failed to initialize database", zap.Error(err))
	}

	// 初始化仓储层
	userRepository := userRepo.NewUserRepository(dbConn.GetGormDB())
	llmConfigRepository := userRepo.NewLLMConfigRepository(dbConn.GetGormDB())
	userChannelRepository := userRepo.NewUserChannelRepository(dbConn.GetGormDB())
	memoryRepository := memoryRepo.NewMemoryRepository(dbConn.GetGormDB())

	// 初始化用户服务
	// v1.8:WechatAccount 双轨已删除,身份解析统一走 user_channels。
	userSvc := userService.NewUserService(userRepository, userChannelRepository)
	llmConfigSvc := userService.NewLLMConfigService(llmConfigRepository)
	// v2.3: 账号绑定服务(渠道通用,飞书+微信共用;绑定码走 bind_codes 表)
	bindCodeRepo := userRepo.NewBindCodeRepository(dbConn.GetGormDB())
	bindingSvc := userService.NewBindingService(userChannelRepository, bindCodeRepo, 5*time.Minute)

	// 初始化消息服务
	// 12-记忆系统技术方案 §5.3:向量化 provider 按配置装配,未配置=子串降级(记忆照常存取)。
	memoryEmbedding := buildEmbeddingProvider(cfg)
	digestRepository := memoryRepo.NewDigestRepository(dbConn.GetGormDB())
	msgRepo := chatRepo.NewMessageRepository(dbConn.GetGormDB())
	// 14-订阅源管理:RSS 信息源登记簿(查询时按需取,无定时抓取)
	subscriptionSvc := subscriptionService.NewSubscriptionService(
		subscriptionRepo.NewSubscriptionRepository(dbConn.GetGormDB()),
		subscriptionService.NewDiscoverer(),
	)
	// M7 中期记忆(§10.6):消息向量 + 原文回表注入检索;任一缺省则中期区静默缺失
	memorySvc := memoryService.NewMemoryService(
		memoryRepository,
		memoryRepo.NewMatterRepository(dbConn.GetGormDB()),
		memoryRepo.NewMessageEmbeddingRepository(dbConn.GetGormDB()),
		msgRepo, // RecentMessageSource(GetByIDs 回表)
	)
	if aware, ok := memorySvc.(memoryService.EmbeddingAware); ok {
		aware.SetEmbeddingProvider(memoryEmbedding)
	}
	// 用户级向量配置解析(用户级覆盖系统默认,§5.3;M5.1 起沉淀管线共用同一缓存)
	embeddingResolver := &userEmbeddingResolver{svc: llmConfigSvc, cache: make(map[int64]struct {
		fingerprint string
		provider    memoryService.EmbeddingProvider
	})}
	if aware, ok := memorySvc.(memoryService.ResolverAware); ok {
		aware.SetEmbeddingResolver(embeddingResolver)
	}
	stepRepo := chatRepo.NewAgentStepRepository(dbConn.GetGormDB())
	// 沉淀管线(M2,§7;M5.1 修订):轮次结束异步生成纪要+提取记忆。
	// LLM/embedding 均按用户解析:用户 Web 端配置优先,系统默认兜底,皆无则静默跳过。
	// extraction.enabled=false 时管线整体不注入(对话零开销)。
	agentLLMClient := newAgentLLMClient(cfg)
	var digestPipeline *memoryService.DigestPipeline
	if cfg.Memory.Extraction.Enabled {
		digestThreshold := cfg.Memory.Extraction.BatchSize
		if digestThreshold <= 0 {
			digestThreshold = 20 // 攒批越大摊销越低,更贴近"按对话段落"(§7 修订)
		}
		digestPipeline = memoryService.NewDigestPipeline(
			memoryRepo.NewWatermarkRepository(dbConn.GetGormDB()),
			digestRepository,
			memoryRepository,
			memoryRepo.NewMatterRepository(dbConn.GetGormDB()), // M6:事项层
			msgRepo, // chat 仓储实现 ConversationSource
			&userPipelineLLM{svc: llmConfigSvc, system: agentLLMClient, cache: make(map[int64]struct {
				fingerprint string
				client      agentpkg.LLMClient
			})},
			memoryEmbedding,
			digestThreshold,
		)
		digestPipeline.SetEmbeddingResolver(embeddingResolver.ResolveEmbeddingProvider)
		// 留痕(M5.3):task+step 可观测,静默不进回执;留痕失败管线自动降级为仅日志
		digestPipeline.SetAudit(&digestAuditAdapter{
			taskRepo: agentRepo.NewAgentTaskRepository(dbConn.GetGormDB()),
			stepRepo: stepRepo,
		})
		logger.Info("memory: 沉淀管线已启用",
			zap.Int("threshold", digestThreshold),
			zap.Bool("embedding", memoryEmbedding != nil))
	}
	msgSvcOpts := []interface{}{memorySvc, stepRepo}
	if digestPipeline != nil {
		msgSvcOpts = append(msgSvcOpts, digestPipeline) // chat.TurnSink
	}
	// Phase 2(16-架构迭代路线图 §7):Context Manager 三件套——Turn 模型状态仓储 +
	// Compact 水位仓储 + LLM 压缩器。压缩失败在服务内降级(水位不推进),不阻塞对话。
	// 压缩器用独立 LLM 客户端:超时放宽到 180s——压缩输入可达数万 token,
	// 默认 30s 必超时(HTTP client 超时在建客户端时固定,无法按调用放宽)。
	compactLLMCfg := cfg.LLM
	for name, p := range compactLLMCfg.Providers {
		p.Timeout = "180s"
		compactLLMCfg.Providers[name] = p
	}
	compactLLM, err := llm.NewClient(compactLLMCfg)
	if err != nil {
		logger.Fatal("Failed to create compact LLM client", zap.Error(err))
	}
	// 压缩客户端按用户解析(与对话同源,用户自定义配置生效);无配置回落系统默认。
	compactResolver := func(ctx context.Context, userID int64) (chatService.CompactLLMClient, error) {
		userConfig, has, err := llmConfigSvc.GetFullConfigForUser(userID)
		if err != nil || !has {
			return nil, err
		}
		return llm.NewUserConfigClientWithTimeout(llm.UserConfig{
			Provider: userConfig.Provider,
			APIKey:   userConfig.APIKey,
			BaseURL:  userConfig.BaseURL,
			Model:    userConfig.Model,
		}, 180*time.Second)
	}
	msgSvcOpts = append(msgSvcOpts,
		chatRepo.NewConversationRepository(dbConn.GetGormDB()),
		chatRepo.NewContextStateRepository(dbConn.GetGormDB()),
		chatService.NewLLMContextCompactor(compactResolver, compactLLM),
	)
	// Phase 3(§8/§9):Conversation Recall——turn 粒度 chunk 索引(TurnSink 增量构建)
	// + 向量召回(邻居展开/阈值),注入 Recent Raw 之后。embedding 未配置时静默缺失。
	chunkEmbedder := chatService.NewChunkEmbedder(
		chatRepo.NewChunkWatermarkRepository(dbConn.GetGormDB()),
		chatRepo.NewConversationChunkRepository(dbConn.GetGormDB()),
		msgRepo,
		memoryEmbedding,
	)
	chunkEmbedder.SetEmbeddingResolver(func(userID int64) memoryService.EmbeddingProvider {
		return embeddingResolver.ResolveEmbeddingProvider(userID)
	})
	msgSvcOpts = append(msgSvcOpts, chunkEmbedder) // chat.TurnSink
	chunkRecall := chatService.NewChunkRecallService(
		chatRepo.NewConversationChunkRepository(dbConn.GetGormDB()),
		memoryEmbedding,
	)
	chunkRecall.SetEmbeddingResolver(func(userID int64) memoryService.EmbeddingProvider {
		return embeddingResolver.ResolveEmbeddingProvider(userID)
	})
	msgSvcOpts = append(msgSvcOpts, chunkRecall)
	// M7 中期记忆(§10.5):消息级向量增量嵌入,同一 TurnSink 链路、独立水位独立降级。
	// 复用沉淀的 ConversationSource(msgRepo)与用户级向量解析;存量回填=水位 0 首轮自然全量。
	if cfg.Memory.Extraction.Enabled {
		msgEmbedder := memoryService.NewMessageEmbedder(
			memoryRepo.NewEmbeddingWatermarkRepository(dbConn.GetGormDB()),
			memoryRepo.NewMessageEmbeddingRepository(dbConn.GetGormDB()),
			msgRepo,
			memoryEmbedding,
		)
		msgEmbedder.SetEmbeddingResolver(embeddingResolver.ResolveEmbeddingProvider)
		msgSvcOpts = append(msgSvcOpts, msgEmbedder) // chat.TurnSink
		logger.Info("memory: 消息嵌入器已启用(中期记忆)",
			zap.Bool("embedding", memoryEmbedding != nil))
	}
	msgSvc := chatService.NewMessageService(msgRepo, msgSvcOpts...)

	// 微信回调路由(v1.9:注入 wechat channel 负责 XML 序列化,handler 业务路径只产纯文本)
	// v2.3: 身份解析改为 BindingService(绑定码 + 已绑解析 + 未绑引导),不再自动建号。
	wechatChan := channelwechat.NewChannel()
	wechatHandler := wechat.NewHandler(wechat.Config{
		AppID:          cfg.Wechat.AppID,
		AppSecret:      cfg.Wechat.AppSecret,
		Token:          cfg.Wechat.Token,
		EncodingAESKey: cfg.Wechat.EncodingAESKey,
		CallbackURL:    cfg.Wechat.CallbackURL,
	}, llmClient, bindingSvc, llmConfigSvc, msgSvc, memorySvc, wechatChan)
	// 管理API路由 handler(路由注册见 routes.go)
	adminHandler := admin.NewHandler(cfg)

	// Web 聊天 API 路由
	// 创建 Agent 服务
	// globalToolRegistry:全量工具池(含抓取类),供子 Agent runner 按能力白名单选。
	// 13-插件系统:能力工具由 ToolService 统一供给(定义落库可启停),框架工具另行注册。
	globalToolRegistry := agentpkg.NewToolRegistry()
	// agentToolRegistry:主 Agent 工具集。方向B--抓取类(rss/web_read)对主 Agent 不可见,
	// 主 Agent 是管家不该亲自抓网页,联网需求必须走 delegate 派给子 Agent。抓取工具
	// RegisterBuiltinSubOnly:只进 globalToolRegistry 供子 Agent 选。
	agentToolRegistry := agentpkg.NewToolRegistry()
	toolRepoImpl := toolRepo.NewToolRepository(dbConn.GetGormDB())
	mcpServerRepo := mcpRepo.NewMCPServerRepository(dbConn.GetGormDB())
	toolSvc := toolService.NewToolService(toolRepoImpl)
	toolSvc.RegisterBuiltin(agenttools.CreateGetCurrentTimeTool)
	toolSvc.RegisterBuiltin(agenttools.CreateCalculatorTool)
	toolSvc.RegisterBuiltin(func() agentpkg.Tool { return agenttools.CreateSearchMemoriesTool(memorySvc) })
	// 订阅源管理(14 §6.1):主 Agent 管理订阅;子 Agent list 取清单选源(能力打标 research/memory)
	toolSvc.RegisterBuiltin(func() agentpkg.Tool { return agenttools.CreateManageSubscriptionsTool(subscriptionSvc) })
	toolSvc.RegisterBuiltinSubOnly(agenttools.CreateRSSReaderTool)
	toolSvc.RegisterBuiltinSubOnly(agenttools.CreateWebReadTool)
	// 飞书 CLI 桥接(M5):受控执行 lark-cli,以用户身份操作飞书全业务域
	toolSvc.RegisterBuiltin(func() agentpkg.Tool {
		return agenttools.CreateFeishuTool(agenttools.FeishuCLIConfig{
			BinPath: cfg.Feishu.CLI.BinPath,
			Timeout: time.Duration(cfg.Feishu.CLI.TimeoutSeconds) * time.Second,
		})
	})
	if err := toolSvc.SeedBuiltins(); err != nil {
		logger.Error("工具定义 seed 失败: " + err.Error())
	}
	if err := toolSvc.BindRegistries(agentToolRegistry, globalToolRegistry); err != nil {
		logger.Error("工具 registry 绑定失败: " + err.Error())
	}
	if err := toolSvc.ApplyTo(agentToolRegistry, globalToolRegistry); err != nil {
		logger.Error("工具应用到工具池失败: " + err.Error())
	}
	// MCP 连接器(13-插件系统,B2):server 配置 + 内存工具目录 + mcp_call/mcp_search 元工具。
	// 元工具恒定注入主 Agent tools 参数;具体 MCP 工具走语义匹配晚置注入,不进 registry。
	mcpSvc := mcpService.NewMCPService()
	agentToolRegistry.Register(mcpSvc.CreateMCPCallTool())
	agentToolRegistry.Register(mcpSvc.CreateMCPSearchTool())

	// M3:MCP server 在线配置(DB 为单一事实源,config.yaml 仅首次启动 seed)。
	mcpSvc.SetMCPClientFactory(mcpService.NewStreamableHTTPMCPClient)
	mcpSvc.SetMCPServerRepository(mcpServerRepo)
	// B2:工具描述向量化 provider(用户级解析,与沉淀/召回同模式)
	mcpSvc.SetEmbeddingProvider(memoryEmbedding)
	mcpSvc.SetEmbeddingResolver(func(userID int64) memoryService.EmbeddingProvider {
		return embeddingResolver.ResolveEmbeddingProvider(userID)
	})
	// M4:OAuth 回调基址(redirect_uri 须与服务商登记一致)
	oauthRedirectBase := cfg.App.ExternalURL
	if oauthRedirectBase == "" {
		oauthRedirectBase = fmt.Sprintf("http://localhost:%d", cfg.App.Port)
	}
	mcpSvc.SetOAuthRedirectBase(oauthRedirectBase)
	if len(cfg.MCP.Servers) > 0 {
		specs := make([]mcpService.MCPServerSpec, 0, len(cfg.MCP.Servers))
		for _, s := range cfg.MCP.Servers {
			specs = append(specs, mcpService.MCPServerSpec{
				Name: s.Name, BaseURL: s.BaseURL, APIKey: s.APIKey, Enabled: s.Enabled,
			})
		}
		if n, err := mcpSvc.SeedServersFromConfig(specs); err != nil {
			logger.Error("MCP 配置 seed 失败: " + err.Error())
		} else if n > 0 {
			logger.Info(fmt.Sprintf("已从 config.yaml 导入 %d 个 MCP server(此后以数据库配置为准)", n))
		}
	}
	if err := mcpSvc.SyncAllServers(context.Background()); err != nil {
		logger.Error("MCP server 启动同步失败: " + err.Error())
	}

	// agentLLMClient 已在上方 newAgentLLMClient 创建(沉淀管线与主/子 Agent 共用系统默认模型)

	// 后台 Agent 框架装配(08 §4.6):任务表 + 子 Agent 注册中心 + 生产 runner + 服务
	// 先于 agentSvc 装配,因 delegate 工具 + 主 Agent system prompt 依赖子 Agent 框架。
	agentTaskRepo := agentRepo.NewAgentTaskRepository(dbConn.GetGormDB())
	// 适配 user.LLMConfigService -> agent.SubAgentLLMConfigProvider(方案3:子 Agent 优先用户配置)
	subAgentLLMProvider := &subAgentLLMConfigAdapter{svc: llmConfigSvc}
	// 子 Agent 可见工具由「工具能力标签 ∩ config 白名单」算出(仿 DSH ToolProviderResult,去角色后无子 Agent 卡)。
	// 子 Agent 执行超时:config agent.sub_agent.timeout,缺省回落默认 180s(过短会误杀耗时研究任务)。
	subAgentTimeout := agentpkg.DefaultSubAgentTimeout
	if cfg.Agent.SubAgent.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Agent.SubAgent.Timeout); err == nil && d > 0 {
			subAgentTimeout = d
		} else {
			logger.Warn("agent.sub_agent.timeout 无效,回落默认 180s: " + cfg.Agent.SubAgent.Timeout)
		}
	}
	subAgentRunner := agentpkg.NewSubAgentRunner(agentLLMClient, agentLLMClient, globalToolRegistry,
		cfg.Agent.SubAgent.AllowedCapabilities, // 空回落默认 ["research","interactive"]
		subAgentTimeout,
		subAgentLLMProvider, agentTaskRepo)
	artifactRepo := agentRepo.NewArtifactRepository(dbConn.GetGormDB())
	eventRepo := agentRepo.NewTaskEventRepository(dbConn.GetGormDB())
	subAgentSvc := agentpkg.NewSubAgentService(agentTaskRepo, subAgentRunner, stepRepo, artifactRepo, eventRepo, nil) // notifier 飞书启动后注入(见 startFeishuChannel)
	// 08 §4.8:web 实时推送 Hub——任务完成事件经 WS 直推前端,轮询降级为兜底
	realtimeHub := realtime.NewHub()
	subAgentSvc.SetCompletionPublisher(realtimeHub)
	// request_input 工具加入子 Agent 工具集(子 Agent 主动要输入,#19)
	globalToolRegistry.Register(agentpkg.CreateRequestInputTool(subAgentSvc))
	// delegate 工具加入主 Agent 工具集(主 Agent 派活给通用执行器,去角色后无 registry)
	agentToolRegistry.Register(agentpkg.CreateDelegateTool(subAgentSvc))
	// 任务管理工具:主 Agent 对派出去的任务可查(query)/补充(update)/取消(cancel)。
	agentToolRegistry.Register(agentpkg.CreateQueryTaskTool(subAgentSvc))
	agentToolRegistry.Register(agentpkg.CreateUpdateTaskTool(subAgentSvc))
	agentToolRegistry.Register(agentpkg.CreateCancelTaskTool(subAgentSvc))

	// 主 Agent 服务:system prompt 由 PromptRegistry 组装(11-Prompt管理),toolRegistry 含 delegate
	// 派活只走循环内的 delegate 工具一条抽象框架路径:任务在循环内创建,task_id 由框架解析。
	// 静态 section 组装不可能失败,故忽略 error。
	agentMainPrompt, _ := agentprompt.BuildMainAgentSystemPrompt(true)
	agentSvc := agentpkg.NewAgentService(agentpkg.AgentServiceConfig{
		LLMClient:          agentLLMClient,
		StreamingLLMClient: agentLLMClient, // OpenAILLMClient 同时实现 LLMClient 和 StreamingLLMClient
		ToolRegistry:       agentToolRegistry,
		SystemPrompt:       agentMainPrompt,
		// 主 Agent 同样装配执行链:熔断(工具连失败抑制)+ 强制汇总(MaxSteps 兜底出报告,不吐废话)。
		Hooks: []agentpkg.RoundHook{
			agentpkg.NewCircuitBreakerHook(agentpkg.ToolFailureThreshold),
			agentpkg.NewForceSummaryHook(agentLLMClient),
		},
	})
	// B2:每轮按问题语义匹配 MCP 工具,以 system 消息晚置注入(Recent Raw 后/当前问题前)。
	agentSvc.SetMCPContextProvider(mcpSvc.BuildMCPContextBlock)

	webHandler := web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc, agentSvc)
	webHandler.SetSubAgentSupport(subAgentSvc)
	webHandler.SetToolService(toolSvc)
	webHandler.SetMCPManager(mcpSvc)

	// 后台 Agent 任务接口(08 §4.7):轮询 + report
	agentTaskHandler := web.NewAgentTaskHandler(subAgentSvc, agentSvc, llmConfigSvc, msgSvc)

	// v2.1: 邮箱密码认证装配
	// AuthService 内部直接用 *gorm.DB 跑事务(users + user_channels + user_credentials),
	// credentialRepo 目前留给未来「改密码」等场景,不通过 repo 注入。
	tokenTTL, err := time.ParseDuration(cfg.Auth.TokenTTL)
	if err != nil || tokenTTL <= 0 {
		tokenTTL = 720 * time.Hour // 30 天,PRD 5.3
	}
	jwtSvc := auth.NewJWTService(cfg.Auth.JWTSecret, tokenTTL)
	authSvc := userService.NewAuthService(dbConn.GetGormDB(), jwtSvc)
	authHandler := web.NewAuthHandler(authSvc)

	// 订阅源管理接口(14-订阅源管理:页面管理入口)
	subscriptionHandler := web.NewSubscriptionHandler(subscriptionSvc)

	return &appDeps{
		cfg:                 cfg,
		wechatHandler:       wechatHandler,
		adminHandler:        adminHandler,
		webHandler:          webHandler,
		agentTaskHandler:    agentTaskHandler,
		subscriptionHandler: subscriptionHandler,
		authHandler:         authHandler,
		jwtSvc:              jwtSvc,
		realtimeHub:         realtimeHub,
		bindingSvc:          bindingSvc,
		msgSvc:              msgSvc,
		agentSvc:            agentSvc,
		llmConfigSvc:        llmConfigSvc,
		subAgentSvc:         subAgentSvc,
		digestPipeline:      digestPipeline,
		chunkEmbedder:       chunkEmbedder,
	}
}

// ---- 装配辅助(系统默认 provider / 适配器) ----

func buildEmbeddingProvider(cfg *config.Config) memoryService.EmbeddingProvider {
	embCfg := cfg.Memory.Embedding
	if embCfg.Provider == "" {
		return nil
	}
	var timeout time.Duration
	if embCfg.Timeout != "" {
		if d, err := time.ParseDuration(embCfg.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	provider, err := memoryService.NewEmbeddingProvider(memoryService.EmbeddingProviderConfig{
		Provider: embCfg.Provider,
		BaseURL:  embCfg.BaseURL,
		APIKey:   embCfg.APIKey,
		Model:    embCfg.Model,
		Dims:     embCfg.Dims,
		Timeout:  timeout,
	})
	if err != nil {
		logger.Fatal("memory.embedding 配置无效", zap.Error(err))
	}
	return provider
}

func newAgentLLMClient(cfg *config.Config) *agentpkg.OpenAILLMClient {
	defaultProviderCfg := cfg.LLM.Providers[cfg.LLM.Routing.Default]
	agentTimeout, err := time.ParseDuration(defaultProviderCfg.Timeout)
	if err != nil {
		agentTimeout = 30 * time.Second
	}
	return agentpkg.NewOpenAILLMClient(defaultProviderCfg.APIKey, defaultProviderCfg.BaseURL, defaultProviderCfg.Model, agentTimeout)
}

// digestAuditAdapter 适配 agent_tasks/agent_steps → memory.DigestAudit(M5.3):
// 每轮沉淀 = 一条 task(SubAgentType=digest,Reported=true 静默——不进主 Agent 回执轮询),
// 步骤锚 task_id 记执行链。留痕失败由管线侧降级为仅日志,这里只透传错误。
type digestAuditAdapter struct {
	taskRepo agentRepo.AgentTaskRepository
	stepRepo chatRepo.AgentStepRepository
}

func (a *digestAuditAdapter) BeginTask(userID, fromID, toID int64, msgCount int) (int64, error) {
	spec := domainagent.TaskSpec{
		Goal: fmt.Sprintf("记忆沉淀:消息 #%d-#%d(%d 条)", fromID, toID, msgCount),
		Type: "digest",
	}
	t := domainagent.NewAgentTask(userID, spec, "web", "")
	t.Reported = true // 静默:digest 是系统能力,完成/失败都不打扰主 Agent
	if err := a.taskRepo.Create(t); err != nil {
		return 0, err
	}
	if ok, err := a.taskRepo.TransitionStatus(t.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil); err != nil || !ok {
		return 0, fmt.Errorf("digest task %d pending→running 迁移失败: %v", t.ID, err)
	}
	return t.ID, nil
}

func (a *digestAuditAdapter) RecordStep(taskID, userID int64, seq int, kind, tool, request, response, status string, durationMs int64) error {
	step := &conversation.AgentStep{
		UserID: userID, TaskID: &taskID, Seq: seq,
		Kind: kind, Status: status, DurationMs: durationMs,
		Tool: tool, Request: request, Response: response,
		CreatedAt: time.Now(),
	}
	return a.stepRepo.CreateBatch([]*conversation.AgentStep{step})
}

func (a *digestAuditAdapter) EndTask(taskID int64, status, artifact, errMsg string) error {
	var artifactPtr, errMsgPtr *string
	if artifact != "" {
		artifactPtr = &artifact
	}
	if errMsg != "" {
		errMsgPtr = &errMsg
	}
	// Phase 5 CAS:digest 任务正常恒为 running,running→终态;false=状态被并发改动(异常),如实报错
	ok, err := a.taskRepo.TransitionStatus(taskID, domainagent.TaskStatusRunning, status, artifactPtr, errMsgPtr)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("digest task %d 状态迁移到 %s 失败(当前非 running)", taskID, status)
	}
	return nil
}

// userPipelineLLM 沉淀管线 LLM 按用户解析(M5.1 修订 §7.2):
// 用户在 Web 端配置了自定义 LLM → 用该配置构造客户端(指纹缓存,改配置免重启);
// 未配置 → 回落系统默认;两者皆无 → ErrPipelineNoLLM(管线静默跳过,水位不推进)。
type userPipelineLLM struct {
	svc    userService.LLMConfigService
	system agentpkg.LLMClient // 系统默认兜底,可 nil
	mu     sync.Mutex
	cache  map[int64]struct {
		fingerprint string
		client      agentpkg.LLMClient
	}
}

func (a *userPipelineLLM) clientFor(userID int64) (agentpkg.LLMClient, error) {
	apiKey, baseURL, model, hasCustom, err := a.svc.GetConfigForUser(userID)
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
	if a.system != nil {
		return a.system, nil
	}
	return nil, memoryService.ErrPipelineNoLLM
}

func (a *userPipelineLLM) Complete(ctx context.Context, userID int64, system, user string) (string, error) {
	client, err := a.clientFor(userID)
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

// userEmbeddingResolver 用户级向量配置解析器(12-记忆系统技术方案 §5.3):
// 用户级配置完整 → 构造 provider(带指纹缓存);未配置/异常 → nil(memory 层回落系统默认)。
type userEmbeddingResolver struct {
	svc userService.LLMConfigService
	mu  sync.Mutex
	// cache: userID → {配置指纹, provider}。指纹变了自动失效(用户改配置后无需重启)。
	cache map[int64]struct {
		fingerprint string
		provider    memoryService.EmbeddingProvider
	}
}

func (r *userEmbeddingResolver) ResolveEmbeddingProvider(userID int64) memoryService.EmbeddingProvider {
	cfg, ok, err := r.svc.GetEmbeddingConfigForUser(userID)
	if err != nil || !ok {
		return nil
	}
	// 指纹 = 关键字段拼接 + key 哈希(不含明文)
	sum := sha1.Sum([]byte(cfg.Provider + "|" + cfg.BaseURL + "|" + cfg.Model + "|" + strconv.Itoa(cfg.Dims) + "|" + cfg.APIKey))
	fingerprint := hex.EncodeToString(sum[:])

	r.mu.Lock()
	cached, hit := r.cache[userID]
	r.mu.Unlock()
	if hit && cached.fingerprint == fingerprint && cached.provider != nil {
		return cached.provider
	}

	provider, err := memoryService.NewEmbeddingProvider(memoryService.EmbeddingProviderConfig{
		Provider: cfg.Provider,
		BaseURL:  cfg.BaseURL,
		APIKey:   cfg.APIKey,
		Model:    cfg.Model,
		Dims:     cfg.Dims,
	})
	if err != nil {
		logger.Warn("用户级向量配置构造失败,回落系统默认",
			zap.Int64("user_id", userID), zap.Error(err))
		provider = nil
	}
	r.mu.Lock()
	r.cache[userID] = struct {
		fingerprint string
		provider    memoryService.EmbeddingProvider
	}{fingerprint, provider}
	r.mu.Unlock()
	return provider
}

// subAgentLLMConfigAdapter 适配 userService.LLMConfigService -> agentpkg.SubAgentLLMConfigProvider。
// 方案3:子 Agent 优先用用户自定义 LLM 配置,无配置时 runner 内部回落系统默认。
type subAgentLLMConfigAdapter struct {
	svc userService.LLMConfigService
}

func (a *subAgentLLMConfigAdapter) GetFullConfig(userID int64) (apiKey, baseURL, model string, hasConfig bool, err error) {
	cfg, has, e := a.svc.GetFullConfigForUser(userID)
	if e != nil {
		return "", "", "", false, e
	}
	if !has || cfg == nil {
		return "", "", "", false, nil
	}
	return cfg.APIKey, cfg.BaseURL, cfg.Model, true, nil
}
