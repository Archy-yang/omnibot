package api

// wire.go — 依赖装配(路由注册见 routes.go)。
// SetupRouter 原来是 759 行的「构造+注册」混排;拆分后本文件只做一件事:
// buildAppDeps 把全部服务/handler 一次构造完成,routes.go 只做路由注册。
// 适配器(digestAuditAdapter/userPipelineLLM/userEmbeddingResolver/ ← 已迁出至各 service 包(2026-10 审计 #4);
// 此注释行留档兼容——
// subAgentLLMConfigAdapter)与系统默认装配辅助(buildEmbeddingProvider/
// newAgentLLMClient)也随之收拢到本文件。

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentprompt "omnibot/internal/agentprompt"
	"omnibot/internal/api/admin"
	"omnibot/internal/api/web"
	"omnibot/internal/api/wechat"
	channelwechat "omnibot/internal/channel/wechat"
	"omnibot/internal/client/llm"
	"omnibot/internal/db"
	"omnibot/internal/pkg/auth"
	"omnibot/internal/pkg/crypto"
	"omnibot/internal/realtime"
	agentRepo "omnibot/internal/repository/agent"
	butlerRepo "omnibot/internal/repository/butler"
	chatRepo "omnibot/internal/repository/chat"
	mcpRepo "omnibot/internal/repository/mcp"
	memoryRepo "omnibot/internal/repository/memory"
	memoryrepo "omnibot/internal/repository/memory"
	skillRepo "omnibot/internal/repository/skill"
	subscriptionRepo "omnibot/internal/repository/subscription"
	toolRepo "omnibot/internal/repository/tool"
	userRepo "omnibot/internal/repository/user"
	agentpkg "omnibot/internal/service/agent"
	agenttools "omnibot/internal/service/agent/tools"
	butlerpkg "omnibot/internal/service/butler"
	chatService "omnibot/internal/service/chat"
	feishudocSvc "omnibot/internal/service/feishudoc"
	larkauthSvc "omnibot/internal/service/larkauth"
	"omnibot/internal/service/larkhome"
	mcpService "omnibot/internal/service/mcp"
	memoryService "omnibot/internal/service/memory"
	sandboxsvc "omnibot/internal/service/sandbox"
	skillService "omnibot/internal/service/skill"
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
	bindingSvc      *userService.BindingService
	msgSvc          chatService.MessageService
	agentSvc        *agentpkg.AgentService
	llmConfigSvc    userService.LLMConfigService
	subAgentSvc     *agentpkg.SubAgentService
	userChannelRepo userRepo.UserChannelRepository // 26 号:butler 送达器查 open_id

	// 25 号:持久任务调度器(进程级后台循环,routes.go 启动)
	persistentScheduler *agentpkg.PersistentTaskScheduler

	// 26 号:主动管家(扫描服务供飞书送达器注入;调度器进程级后台循环,routes.go 启动)
	butlerScan      *butlerpkg.ScanService
	butlerScheduler *butlerpkg.ScanScheduler

	// 沉淀管线(可空:extraction.enabled=false 未启用)。
	// 暴露给手动触发工具(digest_manual_test.go,env 门控)复用同一装配。
	digestPipeline *memoryService.DigestPipeline

	// Phase 3:召回块构建器(手动回填工具用)。
	chunkEmbedder *chatService.ChunkEmbedder
}

// buildAppDeps 构造全部依赖(原 SetupRouter 前半段,行为零变化)。
func buildAppDeps(cfg *config.Config) *appDeps {
	// §17:密钥安全校验——production 缺加密密钥/JWT secret 拒绝启动(2026-10)。
	if err := validateSecurityForProduction(cfg); err != nil {
		logger.Fatal("security validation failed", zap.Error(err))
	}

	// §17:初始化全局加密密钥(替代旧"env 直读+静默回退默认密钥",2026-10)。
	// 未配置时:production 已在上面拒绝;development 回落 legacy 默认密钥并显眼告警。
	encryptMaterial := resolveEncryptKeyMaterial(cfg)
	if encryptMaterial != "" {
		crypto.Init(encryptMaterial)
	} else {
		crypto.InitWithKey(crypto.LegacyDefaultKey)
		logger.Warn("⚠️  未配置 security.encrypt_key,正在使用 legacy 默认加密密钥——" +
			"请在 config.yaml 配置 security.encrypt_key(openssl rand -hex 32 生成),§17")
	}

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

	// §17:存量密文迁移(启动自愈,幂等)——legacy 默认密钥密文重加密为当前密钥。
	// 未配置新密钥时(development legacy 兜底)新旧行为同密钥,天然 no-op。
	if n, err := db.MigrateLegacyCiphertext(dbConn.GetGormDB(), crypto.DeriveKey(encryptMaterial)); err != nil {
		logger.Error("legacy ciphertext migration failed", zap.Error(err))
	} else if n > 0 {
		logger.Info("legacy ciphertext migrated to configured key",
			zap.Int("fields", n))
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
	embeddingResolver := memoryService.NewUserEmbeddingResolver(llmConfigSvc)
	if aware, ok := memorySvc.(memoryService.ResolverAware); ok {
		aware.SetEmbeddingResolver(embeddingResolver)
	}
	// 24 号 M2:风格注入开关存取(user 表;开关默认关,显式开启才注入)
	if si, ok := memorySvc.(interface {
		SetStyleInjectionStore(memoryService.StyleInjectionStore)
	}); ok {
		si.SetStyleInjectionStore(userRepository)
	}
	stepRepo := chatRepo.NewAgentStepRepository(dbConn.GetGormDB())
	// 沉淀管线(M2,§7;M5.1 修订):轮次结束异步生成纪要+提取记忆。
	// LLM/embedding 均按用户解析:用户 Web 端配置优先,系统默认兜底,皆无则静默跳过。
	// extraction.enabled=false 时管线整体不注入(对话零开销)。
	agentLLMClient := newAgentLLMClient(cfg)
	// 管线 LLM 按用户解析:沉淀管线与 26 号主动管家共用同一实例(用户配置优先、系统默认兜底)。
	// 提在 extraction 开关之外:butler 不依赖沉淀开关。
	pipelineLLM := memoryService.NewUserPipelineLLM(llmConfigSvc, agentLLMClient)
	var digestPipeline *memoryService.DigestPipeline
	if cfg.Memory.Extraction.Enabled {
		digestThreshold := cfg.Memory.Extraction.BatchSize
		if digestThreshold <= 0 {
			digestThreshold = 20 // 攒批越大摊销越低,更贴近"按对话段落"(§7 修订)
		}
		digestPipeline = memoryService.NewDigestPipeline(
			memoryRepo.NewWatermarkRepository(dbConn.GetGormDB()),
			memoryRepository,
			memoryRepo.NewMatterRepository(dbConn.GetGormDB()), // M6:事项层
			msgRepo, // chat 仓储实现 ConversationSource
			pipelineLLM,
			memoryEmbedding,
			digestThreshold,
			cfg.Memory.Extraction.SilenceGap, // M8.1:段落边界静默阈值(0 → 管线默认 10m)
		)
		digestPipeline.SetEmbeddingResolver(embeddingResolver.ResolveEmbeddingProvider)
		// 留痕(M5.3):task+step 可观测,静默不进回执;留痕失败管线自动降级为仅日志
		digestPipeline.SetAudit(&memoryService.TaskAuditAdapter{
			TaskRepo: agentRepo.NewAgentTaskRepository(dbConn.GetGormDB()),
			StepRepo: stepRepo,
		})
		logger.Info("memory: 沉淀管线已启用",
			zap.Int("threshold", digestThreshold),
			zap.Bool("embedding", memoryEmbedding != nil))
	}
	// 显式依赖集(DeepSeek 审查 §6.1(a) 整改):取代 ...interface{} type-switch 注入
	msgSvcDeps := chatService.MessageServiceDeps{
		Memory:    memorySvc,
		Steps:     stepRepo,
		TurnSinks: []chatService.TurnSink{},
	}
	// 24 号 M2:风格注入段(开关关/无观察 → 空串不注入,零回归)
	if sp, ok := memorySvc.(chatService.StyleSegmentProvider); ok {
		msgSvcDeps.Style = sp
	}
	if digestPipeline != nil {
		msgSvcDeps.TurnSinks = append(msgSvcDeps.TurnSinks, digestPipeline) // chat.TurnSink
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
	msgSvcDeps.Conversation = chatRepo.NewConversationRepository(dbConn.GetGormDB())
	msgSvcDeps.ContextState = chatRepo.NewContextStateRepository(dbConn.GetGormDB())
	msgSvcDeps.Compactor = chatService.NewLLMContextCompactor(compactResolver, compactLLM)
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
	msgSvcDeps.TurnSinks = append(msgSvcDeps.TurnSinks, chunkEmbedder) // chat.TurnSink
	chunkRecall := chatService.NewChunkRecallService(
		chatRepo.NewConversationChunkRepository(dbConn.GetGormDB()),
		memoryEmbedding,
	)
	chunkRecall.SetEmbeddingResolver(func(userID int64) memoryService.EmbeddingProvider {
		return embeddingResolver.ResolveEmbeddingProvider(userID)
	})
	msgSvcDeps.Recall = chunkRecall
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
		msgSvcDeps.TurnSinks = append(msgSvcDeps.TurnSinks, msgEmbedder) // chat.TurnSink
		logger.Info("memory: 消息嵌入器已启用(中期记忆)",
			zap.Bool("embedding", memoryEmbedding != nil))
	}
	msgSvc := chatService.NewMessageService(msgRepo, msgSvcDeps)

	// 微信回调路由(v1.9:注入 wechat channel 负责 XML 序列化,handler 业务路径只产纯文本)
	// v2.3: 身份解析改为 BindingService(绑定码 + 已绑解析 + 未绑引导),不再自动建号。
	wechatChan := channelwechat.NewChannel()
	wechatHandler := wechat.NewHandler(wechat.Config{
		AppID:          cfg.Wechat.AppID,
		AppSecret:      cfg.Wechat.AppSecret,
		Token:          cfg.Wechat.Token,
		EncodingAESKey: cfg.Wechat.EncodingAESKey,
		CallbackURL:    cfg.Wechat.CallbackURL,
	}, llmClient, bindingSvc, wechat.HandlerDeps{
		LLMConfig:     llmConfigSvc,
		Messages:      msgSvc,
		Memory:        memorySvc,
		WechatChannel: wechatChan,
	})
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
	// Skill 技能服务(18 号 §4):DB 为事实源,内存目录缓存 CRUD 即失效(load_skill/目录注入即时生效)
	skillSvc := skillService.NewSkillService(skillRepo.NewSkillRepository(dbConn.GetGormDB()))
	// 27 号:lark-cli 状态 DB 物化——统一执行入口(hydrate → HOME 物化 → exec → 回写)。
	// 三个调用点(larkauth/feishu_tool/feishudoc)全走它,漏一处即 token 分叉。
	larkHomeMgr := larkhome.NewManager(userRepo.NewLarkCLIStateRepository(dbConn.GetGormDB()))
	larkExecutor := larkhome.NewExecutor(larkHomeMgr, cfg.Feishu.CLI.BinPath)
	// 用户级飞书授权服务(19 号 M1):device flow 编排 + feishu_tool 的 profile 解析
	larkauthCLI := larkauthSvc.NewRealCLI(larkauthSvc.CLIConfig{BinPath: cfg.Feishu.CLI.BinPath})
	if sr, ok := larkauthCLI.(interface{ SetRunner(larkauthSvc.Runner) }); ok {
		sr.SetRunner(larkExecutor)
	}
	larkauthService := larkauthSvc.NewService(
		userRepo.NewLarkCLIAuthRepo(dbConn.GetGormDB()),
		larkauthCLI,
	)
	// 23号:个人文档同步(按用户 profile 执行 lark-cli docs)。前移到工具注册点之前:
	// F2 对话内 sync_matter_doc 工具与 web handler 同源共用这一个 service 实例。
	docSyncRunner := feishudocSvc.NewRealRunner(feishudocSvc.CLIConfig{BinPath: cfg.Feishu.CLI.BinPath})
	if sr, ok := docSyncRunner.(interface{ SetRunner(feishudocSvc.Runner) }); ok {
		sr.SetRunner(larkExecutor)
	}
	docSyncSvc := feishudocSvc.NewService(
		memoryRepo.NewMatterRepository(dbConn.GetGormDB()),
		memoryRepo.NewMemoryRepository(dbConn.GetGormDB()),
		docSyncRunner,
		larkauthService,
	)
	toolSvc.RegisterBuiltin(agenttools.CreateGetCurrentTimeTool)
	toolSvc.RegisterBuiltin(agenttools.CreateCalculatorTool)
	toolSvc.RegisterBuiltin(func() agentpkg.Tool { return agenttools.CreateSearchMemoriesTool(memorySvc) })
	toolSvc.RegisterBuiltin(func() agentpkg.Tool { return agenttools.CreateMemoryRulesTool(memorySvc) }) // 22号:沉淀黑名单
	// 订阅源管理(14 §6.1):主 Agent 管理订阅;子 Agent list 取清单选源(能力打标 research/memory)
	toolSvc.RegisterBuiltin(func() agentpkg.Tool { return agenttools.CreateManageSubscriptionsTool(subscriptionSvc) })
	// 天气(内置,NMC 中央气象台 REST,零 key):主 Agent 直调即答("今天穿什么"),子 Agent(白名单
	// research)行程规划派活时可用。抓取类不同,天气是单点结构化查询,不属"管家不亲自抓网页"的边界
	toolSvc.RegisterBuiltin(agenttools.CreateWeatherTool)
	toolSvc.RegisterBuiltinSubOnly(agenttools.CreateRSSReaderTool)
	toolSvc.RegisterBuiltinSubOnly(agenttools.CreateWebReadTool)
	// 飞书 CLI 桥接(M5):受控执行 lark-cli,以用户身份操作飞书全业务域
	toolSvc.RegisterBuiltin(func() agentpkg.Tool {
		return agenttools.CreateFeishuTool(agenttools.FeishuCLIConfig{
			BinPath:  cfg.Feishu.CLI.BinPath,
			Timeout:  time.Duration(cfg.Feishu.CLI.TimeoutSeconds) * time.Second,
			Resolver: larkauthService, // 19 号 M2:按 ctx 用户动态 --profile;nil 时行为同现状
			Runner:   larkExecutor,    // 27 号:按用户物化执行(HOME+回写)
		})
	})
	// 档案同步到飞书文档(23 号 F2):对话内承接"把XX档案同步到飞书"。窄接口适配:
	// feishudoc.SyncResult → agenttools.MatterDocSyncResult;sentinel 错误译成模型可读文案。
	toolSvc.RegisterBuiltin(func() agentpkg.Tool {
		return agenttools.CreateMatterDocSyncTool(matterDocSyncAdapter{svc: docSyncSvc})
	})
	// 档案/待办收口与建档(28号 N1/N2 + 29号 N1):对话内承接"这事结了/收掉吧/我要在X前完成Y/
	// 帮我建个档案"。红线:仅用户对话确认触发,系统侧(管线/调度)严禁调用;memoryService 实现
	// 经窄接口断言注入。
	if ms, ok := memorySvc.(interface {
		SettleMatter(ctx context.Context, userID, matterID int64, action string) (memoryService.MatterSettleResult, error)
		CloseLoopByQuery(ctx context.Context, userID int64, query string) ([]string, bool, error)
		SetTargetMatter(ctx context.Context, userID, matterID int64, target *time.Time) (string, error)
		CreateMatterByTitle(ctx context.Context, userID int64, title, stateDesc string, target *time.Time) (int64, bool, error)
	}); ok {
		toolSvc.RegisterBuiltin(func() agentpkg.Tool {
			return agenttools.CreateManageMatterTool(matterSettleAdapter{svc: ms})
		})
		toolSvc.RegisterBuiltin(func() agentpkg.Tool {
			return agenttools.CreateCloseLoopTool(matterSettleAdapter{svc: ms})
		})
		toolSvc.RegisterBuiltin(func() agentpkg.Tool {
			return agenttools.CreateCreateMatterTool(matterSettleAdapter{svc: ms})
		})
	}
	// 脚本沙箱(17-脚本沙箱技术方案):一次性容器执行 Python/Shell,子 Agent 专属。
	// Docker 客户端初始化失败(如 DOCKER_HOST 非法)仅告警跳过注册——工具隐藏,不影响启动
	if sandboxAPI, sbErr := sandboxsvc.NewDefaultAPIClient(); sbErr != nil {
		logger.Error("沙箱 docker 客户端初始化失败,工具不注册: " + sbErr.Error())
	} else {
		sandboxExec := sandboxsvc.NewDockerExecutor(sandboxAPI, sandboxsvc.DockerConfig{
			Image:          cfg.Sandbox.Image,
			Timeout:        time.Duration(cfg.Sandbox.TimeoutSeconds) * time.Second,
			MemoryMB:       cfg.Sandbox.MemoryMB,
			CPUCores:       cfg.Sandbox.CPUCores,
			MaxOutputBytes: cfg.Sandbox.MaxOutputBytes,
			PidsLimit:      cfg.Sandbox.PidsLimit,
		})
		toolSvc.RegisterBuiltinSubOnly(func() agentpkg.Tool {
			return agenttools.CreateSandboxRunTool(sandboxExec)
		})
	}
	// load_skill(18 号 §5.2):技能正文按需加载,主 Agent 可见(技能是管家的"方法库")
	toolSvc.RegisterBuiltin(func() agentpkg.Tool {
		return agenttools.CreateLoadSkillTool(skillSvc)
	})
	// save_skill(18 号 §5.3,S1.5):对话中把流程沉淀为技能,默认停用待审
	toolSvc.RegisterBuiltin(func() agentpkg.Tool {
		return agenttools.CreateSaveSkillTool(skillSvc)
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
	subAgentLLMProvider := &agentpkg.SubAgentLLMConfigAdapter{Svc: llmConfigSvc}
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
	// 25 号:持久任务(周期调度)。宿主只管"何时派活",到点 StartTask 派实例,
	// 执行/汇报链路与 delegate 同构;终态观察者维护 fail_streak(≥3 自动停用)。
	persistentTaskRepo := agentRepo.NewPersistentTaskRepository(dbConn.GetGormDB())
	persistentTaskSvc := agentpkg.NewPersistentTaskService(persistentTaskRepo)
	persistentScheduler := agentpkg.NewPersistentTaskScheduler(persistentTaskRepo, persistentTaskSvc)
	persistentScheduler.WireStarter(subAgentSvc)
	subAgentSvc.SetFinishedObserver(persistentScheduler.ObserveFinished)
	agentToolRegistry.Register(agentpkg.CreateManagePersistentTaskTool(persistentTaskSvc))

	// 26 号:主动管家(每日主动关心扫描)。克制闸门在 ScanService 代码层,
	// 调度器只管"何时喊人";送达器在飞书启动后注入(startFeishuChannel)。
	butlerScanSvc := butlerpkg.NewScanService(
		butlerRepo.NewProactiveNoteRepository(dbConn.GetGormDB()),
		memoryRepo.NewMatterRepository(dbConn.GetGormDB()),
		memoryRepo.NewMemoryRepository(dbConn.GetGormDB()),
		userRepo.NewUserRepository(dbConn.GetGormDB()),
		pipelineLLM, // 与沉淀管线同款按用户解析通路
	)
	if sink, ok := msgSvc.(butlerpkg.MessageSink); ok {
		butlerScanSvc.SetMessageSink(sink) // Kind=proactive 消息还原
	}
	butlerScheduler := butlerpkg.NewScanScheduler(butlerScanSvc,
		userRepo.NewUserRepository(dbConn.GetGormDB()))

	// 主 Agent 服务:system prompt 由 PromptRegistry 组装(11-Prompt管理),toolRegistry 含 delegate
	// 派活只走循环内的 delegate 工具一条抽象框架路径:任务在循环内创建,task_id 由框架解析。
	// 静态 section 组装不可能失败,故忽略 error。
	// skill_catalog(18 号 §5.1,order 95):首个 Provider 动态段——目录内容来自 SkillService
	// (prompt 常量零清单,weather 事故同源教训);技能 CRUD 经 OnChange 回调重建整份 prompt。
	skillCatalogSection := agentprompt.DynamicSection("skill_catalog", agentprompt.ScopeMain, 95,
		func(agentprompt.PromptCtx) string { return skillSvc.CatalogPrompt() })
	agentMainPrompt, _ := agentprompt.BuildMainAgentSystemPromptWithExtras(true,
		[]agentprompt.PromptSection{skillCatalogSection})
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
	// 技能 CRUD → 重建主 Agent system prompt(技能目录即时生效,免重启)
	skillSvc.SetOnChange(func() {
		if p, err := agentprompt.BuildMainAgentSystemPromptWithExtras(true,
			[]agentprompt.PromptSection{skillCatalogSection}); err == nil {
			agentSvc.SetSystemPrompt(p)
		}
	})

	webHandler := web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc, agentSvc)
	if js, ok := memorySvc.(web.JournalService); ok { // 21号:memoryService 兼具个人经历层
		webHandler.SetJournalService(js)
	}
	if rs, ok := memorySvc.(web.MemoryRuleService); ok { // 22号:沉淀黑名单管理
		webHandler.SetMemoryRuleService(rs)
	}
	if sp, ok := memorySvc.(web.StyleProfileService); ok { // 24号:风格与认知画像
		webHandler.SetStyleProfileService(sp)
	}
	if ba, ok := memorySvc.(memoryService.BlacklistAware); ok { // 22号:黑名单仓储注入
		ba.SetBlacklistRepo(memoryrepo.NewBlacklistRepository(dbConn.GetGormDB()))
	}
	webHandler.SetDocSyncService(docSyncSvc) // 23号:service 实例在工具注册点前创建(F2 共用)
	webHandler.SetSubAgentSupport(subAgentSvc)
	webHandler.SetToolService(toolSvc)
	webHandler.SetSkillService(skillSvc)
	// 用户级飞书授权(19 号 M1):device flow 编排 + 状态管理
	webHandler.SetLarkAuthService(larkauthService)
	webHandler.SetMCPManager(mcpSvc)
	// 25 号:持久任务 API(列表+控制;创建走对话内工具)
	webHandler.SetPersistentTaskService(persistentTaskSvc)
	// 26 号:主动管家设置 API(开关/时刻读写 + 最近记录列表)
	webHandler.SetProactiveService(butlerpkg.NewSettingsService(
		userRepo.NewUserRepository(dbConn.GetGormDB()),
		butlerRepo.NewProactiveNoteRepository(dbConn.GetGormDB()),
	))

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
	authSvc := userService.NewAuthService(userRepo.NewAuthRepository(dbConn.GetGormDB()), jwtSvc)
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
		userChannelRepo:     userChannelRepository,
		persistentScheduler: persistentScheduler,
		butlerScan:          butlerScanSvc,
		butlerScheduler:     butlerScheduler,
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

// matterDocSyncAdapter feishudoc.Service → agenttools.MatterDocSyncer 窄接口适配
// (23 号 F2:agent 包不 import service 实现,sentinel 错误在此翻译成模型可读文案)。
type matterDocSyncAdapter struct {
	svc *feishudocSvc.Service
}

func (a matterDocSyncAdapter) SyncMatter(ctx context.Context, userID, matterID int64) (agenttools.MatterDocSyncResult, error) {
	r, err := a.svc.SyncMatter(ctx, userID, matterID)
	switch {
	case errors.Is(err, feishudocSvc.ErrMatterNotFound):
		return agenttools.MatterDocSyncResult{}, agenttools.ErrMatterDocNotFound
	case errors.Is(err, feishudocSvc.ErrNotAuthorized):
		return agenttools.MatterDocSyncResult{}, agenttools.ErrMatterDocUnauthorized
	}
	return agenttools.MatterDocSyncResult{DocURL: r.DocURL, Created: r.Created, SyncedAt: r.SyncedAt}, err
}

// matterSettleAdapter memoryService → agenttools.MatterLifecycleManager/MatterCreator 窄接口适配
// (28号 N1/N2 + 29号 N1):sentinel 错误翻译成模型可读文案;收口/建档动作仅经工具由用户对话确认触发。
type matterSettleAdapter struct {
	svc interface {
		SettleMatter(ctx context.Context, userID, matterID int64, action string) (memoryService.MatterSettleResult, error)
		CloseLoopByQuery(ctx context.Context, userID int64, query string) ([]string, bool, error)
		SetTargetMatter(ctx context.Context, userID, matterID int64, target *time.Time) (string, error)
		CreateMatterByTitle(ctx context.Context, userID int64, title, stateDesc string, target *time.Time) (int64, bool, error)
	}
}

func (a matterSettleAdapter) SettleMatter(ctx context.Context, userID, matterID int64, action string) (agenttools.MatterSettleResult, error) {
	r, err := a.svc.SettleMatter(ctx, userID, matterID, action)
	if errors.Is(err, memoryService.ErrMatterNotFound) {
		return agenttools.MatterSettleResult{}, agenttools.ErrMatterLifecycleNotFound
	}
	return agenttools.MatterSettleResult{Title: r.Title, Status: r.Status, CascadedLoops: r.CascadedLoops}, err
}

func (a matterSettleAdapter) CloseLoopByQuery(ctx context.Context, userID int64, query string) ([]string, bool, error) {
	return a.svc.CloseLoopByQuery(ctx, userID, query)
}

func (a matterSettleAdapter) SetTargetMatter(ctx context.Context, userID, matterID int64, target *time.Time) (string, error) {
	title, err := a.svc.SetTargetMatter(ctx, userID, matterID, target)
	if errors.Is(err, memoryService.ErrMatterNotFound) {
		return "", agenttools.ErrMatterLifecycleNotFound
	}
	return title, err
}

// CreateMatterByTitle 29号 N1:建档幂等由 service 层保证,adapter 纯透传。
func (a matterSettleAdapter) CreateMatterByTitle(ctx context.Context, userID int64, title, stateDesc string, target *time.Time) (int64, bool, error) {
	return a.svc.CreateMatterByTitle(ctx, userID, title, stateDesc, target)
}
