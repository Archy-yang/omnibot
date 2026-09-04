package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"omnibot/frontend"
	agentprompt "omnibot/internal/agentprompt"
	"omnibot/internal/api/admin"
	"omnibot/internal/api/web"
	"omnibot/internal/api/wechat"
	channelfactory "omnibot/internal/channel"
	channelfeishu "omnibot/internal/channel/feishu"
	channelweb "omnibot/internal/channel/web"
	channelwechat "omnibot/internal/channel/wechat"
	"omnibot/internal/client/llm"
	"omnibot/internal/db"
	"omnibot/internal/middleware"
	"omnibot/internal/pkg/auth"
	agentRepo "omnibot/internal/repository/agent"
	chatRepo "omnibot/internal/repository/chat"
	memoryRepo "omnibot/internal/repository/memory"
	skillRepo "omnibot/internal/repository/skill"
	userRepo "omnibot/internal/repository/user"
	agentpkg "omnibot/internal/service/agent"
	chatService "omnibot/internal/service/chat"
	memoryService "omnibot/internal/service/memory"
	skillService "omnibot/internal/service/skill"
	userService "omnibot/internal/service/user"
	"omnibot/pkg/config"
	"omnibot/pkg/logger"

	"github.com/gin-gonic/gin"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	"go.uber.org/zap"
)

func init() {
	channelfactory.Register(channelweb.NewChannel())
	channelfactory.Register(channelwechat.NewChannel())
}

// SetupRouter 设置路由
func SetupRouter(cfg *config.Config) *gin.Engine {
	// 创建Gin引擎
	r := gin.Default()

	// 注册中间件
	r.Use(middleware.CORS())
	r.Use(middleware.Logger())
	r.Use(middleware.Recovery())

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
	memorySvc := memoryService.NewMemoryService(memoryRepository, digestRepository)
	if aware, ok := memorySvc.(memoryService.EmbeddingAware); ok {
		aware.SetEmbeddingProvider(memoryEmbedding)
	}
	// 用户级向量配置解析(用户级覆盖系统默认,§5.3)
	if aware, ok := memorySvc.(memoryService.ResolverAware); ok {
		aware.SetEmbeddingResolver(&userEmbeddingResolver{svc: llmConfigSvc, cache: make(map[int64]struct {
			fingerprint string
			provider    memoryService.EmbeddingProvider
		})})
	}
	msgRepo := chatRepo.NewMessageRepository(dbConn.GetGormDB())
	stepRepo := chatRepo.NewAgentStepRepository(dbConn.GetGormDB())
	// 沉淀管线(M2,§7):轮次结束异步生成纪要+提取记忆,LLM/embedding 均系统默认(系统能力)。
	// extraction.enabled=false 或 LLM 未装配时管线整体不注入(对话零开销)。
	agentLLMClient := newAgentLLMClient(cfg)
	var digestPipeline *memoryService.DigestPipeline
	if cfg.Memory.Extraction.Enabled && agentLLMClient != nil {
		digestThreshold := cfg.Memory.Extraction.BatchSize
		if digestThreshold <= 0 {
			digestThreshold = 20 // 攒批越大摊销越低,更贴近"按对话段落"(§7 修订)
		}
		digestPipeline = memoryService.NewDigestPipeline(
			memoryRepo.NewWatermarkRepository(dbConn.GetGormDB()),
			digestRepository,
			memoryRepository,
			msgRepo, // chat 仓储实现 ConversationSource
			pipelineLLMAdapter{inner: agentLLMClient},
			memoryEmbedding,
			digestThreshold,
		)
		logger.Info("memory: 沉淀管线已启用",
			zap.Int("threshold", digestThreshold),
			zap.Bool("embedding", memoryEmbedding != nil))
	}
	msgSvcOpts := []interface{}{memorySvc, stepRepo}
	if digestPipeline != nil {
		msgSvcOpts = append(msgSvcOpts, digestPipeline) // chat.TurnSink
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
	wechatGroup := r.Group("/wechat")
	{
		wechatGroup.GET("/callback", wechatHandler.Verify)
		wechatGroup.POST("/callback", wechatHandler.HandleMessage)
	}

	// 管理API路由
	adminHandler := admin.NewHandler(cfg)
	apiGroup := r.Group("/api/v1")
	{
		// 健康检查
		apiGroup.GET("/health", adminHandler.HealthCheck)

		// 系统指标
		apiGroup.GET("/metrics", adminHandler.Metrics)

		// 配置管理
		apiGroup.GET("/config", adminHandler.GetConfig)
		apiGroup.PUT("/config", adminHandler.UpdateConfig)
	}

	// Web 聊天 API 路由
	// 创建 Agent 服务
	// globalToolRegistry:全量工具池(含抓取类),供子 Agent runner 按能力白名单选。
	// 13-插件系统:能力工具由 SkillService 统一供给(定义落库可启停),框架工具另行注册。
	globalToolRegistry := agentpkg.NewToolRegistry()
	// agentToolRegistry:主 Agent 工具集。方向B--抓取类(rss/web_read)对主 Agent 不可见,
	// 主 Agent 是管家不该亲自抓网页,联网需求必须走 delegate 派给子 Agent。抓取技能
	// RegisterBuiltinSubOnly:只进 globalToolRegistry 供子 Agent 选。
	agentToolRegistry := agentpkg.NewToolRegistry()
	skillRepoImpl := skillRepo.NewSkillRepository(dbConn.GetGormDB())
	skillSvc := skillService.NewSkillService(skillRepoImpl)
	skillSvc.RegisterBuiltin(agentpkg.CreateGetCurrentTimeTool)
	skillSvc.RegisterBuiltin(agentpkg.CreateCalculatorTool)
	skillSvc.RegisterBuiltin(func() agentpkg.Tool { return agentpkg.CreateSearchMemoriesTool(memorySvc) })
	skillSvc.RegisterBuiltin(func() agentpkg.Tool { return agentpkg.CreateSearchHistoryTool(memorySvc) })
	skillSvc.RegisterBuiltinSubOnly(agentpkg.CreateRSSReaderTool)
	skillSvc.RegisterBuiltinSubOnly(agentpkg.CreateWebReadTool)
	if err := skillSvc.SeedBuiltins(); err != nil {
		logger.Error("技能定义 seed 失败: " + err.Error())
	}
	if err := skillSvc.BindRegistries(agentToolRegistry, globalToolRegistry); err != nil {
		logger.Error("技能 registry 绑定失败: " + err.Error())
	}
	if err := skillSvc.ApplyTo(agentToolRegistry, globalToolRegistry); err != nil {
		logger.Error("技能应用到工具池失败: " + err.Error())
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

	webHandler := web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc, agentSvc)
	webHandler.SetSubAgentSupport(subAgentSvc)
	webHandler.SetSkillService(skillSvc)

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

	// 认证接口(不挂 AuthRequired,注册/登录本身不能要求已登录)
	authAPIGroup := r.Group("/api/v1/auth")
	{
		authAPIGroup.POST("/register", authHandler.HandleRegister)
		authAPIGroup.POST("/login", authHandler.HandleLogin)
	}

	// v2.1: 业务接口按路由组挂 JWT 鉴权;handler 从 c.GetInt64('user_id') 取身份
	chatAPIGroup := r.Group("/api/v1/chat")
	chatAPIGroup.Use(middleware.AuthRequired(jwtSvc))
	{
		chatAPIGroup.GET("/messages", webHandler.HandleGetHistory)
		chatAPIGroup.POST("/messages", webHandler.HandleSendMessage)
		chatAPIGroup.POST("/messages/stream", webHandler.HandleSendMessageStream)
		chatAPIGroup.POST("/messages/agent", webHandler.HandleSendMessageAgent)
		chatAPIGroup.POST("/messages/agent/stream", webHandler.HandleSendMessageAgentStream)
	}

	// 后台 Agent 任务接口(08 §4.7):前端轮询 + 触发汇报
	agentTaskGroup := r.Group("/api/v1/agent")
	agentTaskGroup.Use(middleware.AuthRequired(jwtSvc))
	{
		agentTaskGroup.GET("/tasks", agentTaskHandler.HandleListTasks)
		agentTaskGroup.GET("/tasks/:id/steps", agentTaskHandler.HandleListTaskSteps)
		agentTaskGroup.POST("/tasks/:id/report", agentTaskHandler.HandleReportTask)
	}

	// v1.6: 飞书机器人接入(长连接)。enabled=false 时跳过,不影响 Web/微信启动。
	// channel 复用现有 msgSvc/agentSvc/llmConfigSvc--同步 Run 路径,所有
	// 跨入口能力(Agent、长期记忆、自定义 LLM 配置、agent_steps 复盘记录)自动继承。
	// v2.2/v2.3: 身份解析改为 BindingService(绑定码 + 已绑解析 + 未绑引导),不再自动建号。
	startFeishuChannel(cfg, bindingSvc, msgSvc, agentSvc, llmConfigSvc, subAgentSvc)

	// 长期记忆路由
	memoryAPIGroup := r.Group("/api/v1/memories")
	memoryAPIGroup.Use(middleware.AuthRequired(jwtSvc))
	{
		memoryAPIGroup.GET("", webHandler.HandleGetMemories)
		memoryAPIGroup.POST("", webHandler.HandleCreateMemory)
		memoryAPIGroup.DELETE("", webHandler.HandleClearMemories)
		memoryAPIGroup.DELETE("/:id", webHandler.HandleDeleteMemory)
		memoryAPIGroup.PUT("/:id", webHandler.HandleUpdateMemory)
	}

	// 技能管理路由(13-插件系统):清单 + 启停
	skillAPIGroup := r.Group("/api/v1/skills")
	skillAPIGroup.Use(middleware.AuthRequired(jwtSvc))
	{
		skillAPIGroup.GET("", webHandler.HandleListSkills)
		skillAPIGroup.PUT("/:name", webHandler.HandleUpdateSkill)
	}

	// 用户 LLM 配置路由
	userAPIGroup := r.Group("/api/v1/user")
	userAPIGroup.Use(middleware.AuthRequired(jwtSvc))
	{
		userAPIGroup.GET("/llm-providers", webHandler.HandleGetLLMProviders)
		userAPIGroup.GET("/llm-config", webHandler.HandleGetLLMConfig)
		userAPIGroup.PUT("/llm-config", webHandler.HandleUpdateLLMConfig)
		userAPIGroup.DELETE("/llm-config", webHandler.HandleDeleteLLMConfig)

		// v2.3: 渠道绑定(状态查询 + 出码,通用码服务飞书+微信)
		channelBindHandler := web.NewChannelBindHandler(bindingSvc)
		userAPIGroup.GET("/channel-binding", channelBindHandler.HandleGetBindingStatus)
		userAPIGroup.POST("/channel-binding/bind-code", channelBindHandler.HandleGenerateBindCode)
	}

	// 前端静态资源路由 - 嵌入到二进制中
	// 使用 SubFS 获取 dist 子目录作为根
	distFS, err := fs.Sub(frontend.FS, "dist")
	if err != nil {
		logger.Fatal("Failed to create dist sub filesystem", zap.Error(err))
	}
	webFS := http.FS(distFS)
	staticHandler := http.StripPrefix("/chat/", http.FileServer(webFS))
	// SPA 服务(路由 base '/chat/' 修复 + 缓存策略):
	//   - 文件不存在时回退 index.html(前端路由深链 /chat/login 等由前端路由接管)
	//   - index.html 必须 no-cache——资产文件名带内容哈希,每次发版哈希变化,
	//     浏览器缓存旧 index.html 会去请求已不存在的旧资产 → 404 白屏
	//   - 带哈希的资产可长缓存(内容不变则哈希不变)
	r.GET("/chat/*filepath", func(c *gin.Context) {
		path := c.Param("filepath")
		if path == "/" || path == "/index.html" {
			c.Header("Cache-Control", "no-cache, must-revalidate")
		} else if strings.Contains(path, "/assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		// SPA 回退:请求的文件不存在(非静态资源) → index.html 交前端路由
		if path != "/" && path != "/index.html" {
			if _, err := fs.Stat(distFS, strings.TrimPrefix(path, "/")); err != nil {
				c.Header("Cache-Control", "no-cache, must-revalidate")
				indexFile, indexErr := distFS.Open("index.html")
				if indexErr == nil {
					defer indexFile.Close()
					c.Data(http.StatusOK, "text/html; charset=utf-8", mustReadAll(indexFile))
					return
				}
			}
		}
		staticHandler.ServeHTTP(c.Writer, c.Request)
	})

	// 根路径重定向到 /chat
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/chat/")
	})

	// Ping路由，用于服务探活
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"message": "pong",
		})
	})

	return r
}

// startFeishuChannel 装配并启动飞书 channel(v1.6)。
//
//   - cfg.Feishu.Enabled=false: 跳过,不日志(避免 dev 启动噪音)
//   - cfg.Feishu.Enabled=true 但凭证空: 仅警告日志,不阻断主服务
//   - 否则: 构造 sender + handler + channel,go-routine 启动长连接,带 recover
//
// 长连接由飞书 SDK 内部循环 + 自动重连维护;Start() 阻塞,故必须放 goroutine。
// 程序退出时由进程结束统一回收(SDK ws client 没有显式 Stop 接口)。
func startFeishuChannel(
	cfg *config.Config,
	bindingSvc *userService.BindingService,
	msgSvc chatService.MessageService,
	agentSvc *agentpkg.AgentService,
	llmConfigSvc userService.LLMConfigService,
	subAgentSvc *agentpkg.SubAgentService,
) {
	feishuCfg := channelfeishu.Config{
		AppID:     cfg.Feishu.AppID,
		AppSecret: cfg.Feishu.AppSecret,
		Enabled:   cfg.Feishu.Enabled,
	}
	if !feishuCfg.Enabled {
		return
	}
	if feishuCfg.AppID == "" || feishuCfg.AppSecret == "" {
		logger.Warn("feishu enabled but credentials missing, skipping")
		return
	}

	// SDK lark client(发消息用)
	larkClient := lark.NewClient(feishuCfg.AppID, feishuCfg.AppSecret)
	sender := channelfeishu.NewLarkSender(larkClient)

	handler := channelfeishu.NewMessageHandler(bindingSvc, msgSvc, agentSvc, llmConfigSvc, sender)
	handler.SetSubAgentReporter(subAgentSvc)
	// 飞书主动推送(方案A):子 Agent 完成时把结果推回飞书 open_id。
	// sender 在此才创建,故 notifier 在飞书启动时注入(而非 subAgentSvc 创建时)。
	subAgentSvc.SetNotifier(channelfeishu.NewFeishuTaskNotifier(sender, agentSvc, msgSvc, llmConfigSvc))
	channel := channelfeishu.NewChannel(feishuCfg, handler, sender)

	channelfactory.Register(channel)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorWithFields("feishu: long connection goroutine panic", zap.Any("recover", r))
			}
		}()
		// 用独立 background context;主服务退出由进程结束统一回收
		if err := channel.Start(context.Background()); err != nil {
			logger.ErrorWithFields("feishu: long connection ended with error", zap.Error(err))
		}
	}()
}

// buildEmbeddingProvider 从 config 构造向量化 provider(12-记忆系统技术方案 §5.2/§5.3)。
// 未配置返回 nil(检索降级子串);配置非法 fail-fast 启动失败(防静默错配,§6.3)。
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

// mustReadAll 读满整个 reader(SPA 回退用;index.html 小,一次性读入)。
func mustReadAll(r io.Reader) []byte {
	data, err := io.ReadAll(r)
	if err != nil {
		return []byte("<!doctype html><title>OmniBot</title>")
	}
	return data
}

// newAgentLLMClient 创建系统默认对话模型客户端(主 Agent/子 Agent/沉淀管线共用)。
// 返回具体类型(同时实现 LLMClient 与 StreamingLLMClient)。
func newAgentLLMClient(cfg *config.Config) *agentpkg.OpenAILLMClient {
	defaultProviderCfg := cfg.LLM.Providers[cfg.LLM.Routing.Default]
	agentTimeout, err := time.ParseDuration(defaultProviderCfg.Timeout)
	if err != nil {
		agentTimeout = 30 * time.Second
	}
	return agentpkg.NewOpenAILLMClient(defaultProviderCfg.APIKey, defaultProviderCfg.BaseURL, defaultProviderCfg.Model, agentTimeout)
}

// pipelineLLMAdapter 适配 agentpkg.LLMClient → memory.PipelineLLM(§7.2 沉淀管线用系统默认模型)。
type pipelineLLMAdapter struct {
	inner agentpkg.LLMClient
}

func (a pipelineLLMAdapter) Complete(ctx context.Context, system, user string) (string, error) {
	messages := []map[string]interface{}{
		{"role": "system", "content": system},
		{"role": "user", "content": user},
	}
	content, _, err := a.inner.ChatCompletion(ctx, messages, nil)
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
