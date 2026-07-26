package api

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"omnibot/frontend"
	"omnibot/internal/api/admin"
	"omnibot/internal/api/web"
	"omnibot/internal/api/wechat"
	channelfactory "omnibot/internal/channel"
	channelfeishu "omnibot/internal/channel/feishu"
	channelweb "omnibot/internal/channel/web"
	channelwechat "omnibot/internal/channel/wechat"
	"omnibot/internal/client/llm"
	"omnibot/internal/db"
	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/middleware"
	"omnibot/internal/pkg/auth"
	agentRepo "omnibot/internal/repository/agent"
	chatRepo "omnibot/internal/repository/chat"
	memoryRepo "omnibot/internal/repository/memory"
	userRepo "omnibot/internal/repository/user"
	agentpkg "omnibot/internal/service/agent"
	chatService "omnibot/internal/service/chat"
	memoryService "omnibot/internal/service/memory"
	userService "omnibot/internal/service/user"
	"omnibot/pkg/config"
	"omnibot/pkg/logger"

	"github.com/gin-gonic/gin"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	"go.uber.org/zap"
)

// researcherSystemPrompt 研究员子 Agent 的 system prompt。
//
// 设计目标:在 ReAct「每轮决定是否再次调用工具」这个决策点上,显式约束收敛,
// 避免 web_fetcher 对 JS 渲染页面拿不到正文时,模型反复换 URL 重试直至跑满 MaxSteps
// (见 task 15:15 轮 web_fetcher,最后吐"已达到最大步数限制",检索全白做)。
//
// 三条收敛规则:
//  1. 每轮工具调用前先自问:这一步对回答目标真的必要吗?已收集的信息够不够产出报告?
//     够了就立即产出报告,不要再调工具。
//  2. 同一工具连续失败(尤其换 URL 仍拿不到有效正文)说明这条路走不通,不要继续重试--
//     改换思路或基于已收集信息汇总。绝不反复重试同类失败。
//  3. 信息不足也能产出报告:基于已有来源如实汇总,明确标注哪些部分未能查证,
//     绝不空转到最后一句"已达到最大步数限制"。
var researcherSystemPrompt = `你是一名研究员。目标:{goal}。

工作方式:用可用工具检索信息,多步推理,最后产出一份结构化报告(要点 + 来源)。

== 收敛规则(每轮决策是否再次调工具时必须遵守)==
1. 调用工具前先自问:这一步对回答目标真的必要吗?已收集的信息够不够产出报告?
   信息够就立即产出报告,不要再调工具。宁可早出报告,不要多检索。
2. 同一工具连续失败(尤其换 URL 仍拿不到有效正文)说明这条路走不通,
   立即停止重试该工具,改换思路或基于已有信息汇总。绝不反复重试同类失败。
3. 信息不足也必须产出报告:基于已有来源如实汇总,对未能查证的部分明确标注"未能查证",
   绝不空转到步数耗尽。一份基于部分来源的报告,远好过跑满步数却啥也没产出。

== web_fetcher 注意事项==
web_fetcher 对 JS 渲染页面(SPA,如首页/活动页)常只能抓到导航菜单而非正文。
若一次抓取结果是导航菜单、空正文、或乱码,视为该页面无效,不要继续换该站点的
其他 URL 重试--改用 search_memories/search_history 查历史,或基于已有信息汇总。`

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
	memorySvc := memoryService.NewMemoryService(memoryRepository)
	msgRepo := chatRepo.NewMessageRepository(dbConn.GetGormDB())
	stepRepo := chatRepo.NewAgentStepRepository(dbConn.GetGormDB())
	msgSvc := chatService.NewMessageService(msgRepo, memorySvc, stepRepo)

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
	// globalToolRegistry:全量工具池(含抓取类),供子 Agent runner 按 card.Tools 选
	globalToolRegistry := agentpkg.NewToolRegistry()
	globalToolRegistry.Register(agentpkg.CreateGetCurrentTimeTool())
	globalToolRegistry.Register(agentpkg.CreateCalculatorTool())
	globalToolRegistry.Register(agentpkg.CreateSearchMemoriesTool(memorySvc))
	globalToolRegistry.Register(agentpkg.CreateSearchHistoryTool())
	globalToolRegistry.Register(agentpkg.CreateRSSReaderTool())
	globalToolRegistry.Register(agentpkg.CreateWebFetcherTool())
	globalToolRegistry.Register(agentpkg.CreateWebReaderTool())

	// agentToolRegistry:主 Agent 工具集。方向B--移除抓取类(rss/web_fetcher/web_reader),
	// 主 Agent 是管家不该亲自抓网页,联网需求必须走 delegate 派给子 Agent。抓取工具仍在
	// globalToolRegistry 供子 Agent 选。
	agentToolRegistry := agentpkg.NewToolRegistry()
	agentToolRegistry.Register(agentpkg.CreateGetCurrentTimeTool())
	agentToolRegistry.Register(agentpkg.CreateCalculatorTool())
	agentToolRegistry.Register(agentpkg.CreateSearchMemoriesTool(memorySvc))
	agentToolRegistry.Register(agentpkg.CreateSearchHistoryTool())

	defaultProviderCfg := cfg.LLM.Providers[cfg.LLM.Routing.Default]
	agentTimeout, err := time.ParseDuration(defaultProviderCfg.Timeout)
	if err != nil {
		agentTimeout = 30 * time.Second
	}
	agentLLMClient := agentpkg.NewOpenAILLMClient(defaultProviderCfg.APIKey, defaultProviderCfg.BaseURL, defaultProviderCfg.Model, agentTimeout)

	// 后台 Agent 框架装配(08 §4.6):任务表 + 子 Agent 注册中心 + 生产 runner + 服务
	// 先于 agentSvc 装配,因 delegate 工具 + 主 Agent system prompt 依赖子 Agent 框架。
	agentTaskRepo := agentRepo.NewAgentTaskRepository(dbConn.GetGormDB())
	subAgentRegistry := agentpkg.NewSubAgentRegistry()
	subAgentRegistry.Register(domainagent.SubAgentCard{
		Type:           "researcher",
		Name:           "研究员",
		Description:    "用于需要查阅资料、阅读 RSS、检索历史信息的耗时研究任务。派给它一个研究目标,它会多步检索并汇总成报告。",
		PromptTemplate: researcherSystemPrompt,
		Tools:          []string{"rss_reader", "web_fetcher", "web_reader", "search_memories", "search_history"},
		MaxSteps:       15,
		Timeout:        180 * time.Second,
	})
	// 适配 user.LLMConfigService -> agent.SubAgentLLMConfigProvider(方案3:子 Agent 优先用户配置)
	subAgentLLMProvider := &subAgentLLMConfigAdapter{svc: llmConfigSvc}
	subAgentRunner := agentpkg.NewSubAgentRunner(agentLLMClient, agentLLMClient, globalToolRegistry, subAgentLLMProvider)
	subAgentSvc := agentpkg.NewSubAgentService(agentTaskRepo, subAgentRegistry, subAgentRunner, stepRepo)
	// delegate 工具加入主 Agent 工具集(主 Agent 据此派活)
	agentToolRegistry.Register(agentpkg.CreateDelegateTool(subAgentRegistry, subAgentSvc))

	// 主 Agent 服务:system prompt 含 delegate 派活 + 汇报引导(08 §4.4),toolRegistry 含 delegate
	agentSvc := agentpkg.NewAgentService(agentpkg.AgentServiceConfig{
		LLMClient:          agentLLMClient,
		StreamingLLMClient: agentLLMClient, // OpenAILLMClient 同时实现 LLMClient 和 StreamingLLMClient
		ToolRegistry:       agentToolRegistry,
		SystemPrompt:       agentpkg.MainAgentSystemPrompt(true),
	})

	webHandler := web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc, agentSvc)
	webHandler.SetSubAgentSupport(subAgentSvc, subAgentRegistry)

	// 后台 Agent 任务接口(08 §4.7):轮询 + report
	agentTaskHandler := web.NewAgentTaskHandler(subAgentSvc, agentSvc, subAgentRegistry, llmConfigSvc, msgSvc)

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
	r.GET("/chat/*filepath", gin.WrapH(staticHandler))

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
