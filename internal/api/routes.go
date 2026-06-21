package api

import (
	"io/fs"
	"net/http"
	"time"

	"omnibot/frontend"
	"omnibot/internal/api/admin"
	"omnibot/internal/api/web"
	"omnibot/internal/api/wechat"
	channelfactory "omnibot/internal/channel"
	channelweb "omnibot/internal/channel/web"
	"omnibot/internal/client/llm"
	"omnibot/internal/db"
	"omnibot/internal/middleware"
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
	"go.uber.org/zap"
)

func init() {
	channelfactory.Register(channelweb.NewChannel())
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
	wechatAccountRepository := userRepo.NewWechatAccountRepository(dbConn.GetGormDB())
	llmConfigRepository := userRepo.NewLLMConfigRepository(dbConn.GetGormDB())
	userChannelRepository := userRepo.NewUserChannelRepository(dbConn.GetGormDB())
	memoryRepository := memoryRepo.NewMemoryRepository(dbConn.GetGormDB())

	// 初始化用户服务
	userSvc := userService.NewUserService(userRepository, wechatAccountRepository, userChannelRepository)
	llmConfigSvc := userService.NewLLMConfigService(llmConfigRepository)

	// 初始化消息服务
	memorySvc := memoryService.NewMemoryService(memoryRepository)
	msgRepo := chatRepo.NewMessageRepository(dbConn.GetGormDB())
	msgSvc := chatService.NewMessageService(msgRepo, memorySvc)

	// 微信回调路由
	wechatHandler := wechat.NewHandler(wechat.Config{
		AppID:          cfg.Wechat.AppID,
		AppSecret:      cfg.Wechat.AppSecret,
		Token:          cfg.Wechat.Token,
		EncodingAESKey: cfg.Wechat.EncodingAESKey,
		CallbackURL:    cfg.Wechat.CallbackURL,
	}, llmClient, userSvc, llmConfigSvc, msgSvc, memorySvc)
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
	agentToolRegistry := agentpkg.NewToolRegistry()
	agentToolRegistry.Register(agentpkg.CreateGetCurrentTimeTool())
	agentToolRegistry.Register(agentpkg.CreateCalculatorTool())
	agentToolRegistry.Register(agentpkg.CreateSearchMemoriesTool(memorySvc))
	agentToolRegistry.Register(agentpkg.CreateSearchHistoryTool())
	agentToolRegistry.Register(agentpkg.CreateRSSReaderTool())

	defaultProviderCfg := cfg.LLM.Providers[cfg.LLM.Routing.Default]
	agentTimeout, err := time.ParseDuration(defaultProviderCfg.Timeout)
	if err != nil {
		agentTimeout = 30 * time.Second
	}
	agentLLMClient := agentpkg.NewOpenAILLMClient(defaultProviderCfg.APIKey, defaultProviderCfg.BaseURL, defaultProviderCfg.Model, agentTimeout)
	agentSvc := agentpkg.NewAgentService(agentpkg.AgentServiceConfig{
		LLMClient:          agentLLMClient,
		StreamingLLMClient: agentLLMClient, // OpenAILLMClient 同时实现 LLMClient 和 StreamingLLMClient
		ToolRegistry:       agentToolRegistry,
	})

	webHandler := web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc, agentSvc)
	chatAPIGroup := r.Group("/api/v1/chat")
	{
		chatAPIGroup.GET("/messages", webHandler.HandleGetHistory)
		chatAPIGroup.POST("/messages", webHandler.HandleSendMessage)
		chatAPIGroup.POST("/messages/stream", webHandler.HandleSendMessageStream)
		chatAPIGroup.POST("/messages/agent", webHandler.HandleSendMessageAgent)
		chatAPIGroup.POST("/messages/agent/stream", webHandler.HandleSendMessageAgentStream)
	}

	// 长期记忆路由
	memoryAPIGroup := r.Group("/api/v1/memories")
	{
		memoryAPIGroup.GET("", webHandler.HandleGetMemories)
		memoryAPIGroup.POST("", webHandler.HandleCreateMemory)
		memoryAPIGroup.DELETE("", webHandler.HandleClearMemories)
		memoryAPIGroup.DELETE("/:id", webHandler.HandleDeleteMemory)
		memoryAPIGroup.PUT("/:id", webHandler.HandleUpdateMemory)
	}

	// 用户 LLM 配置路由
	userAPIGroup := r.Group("/api/v1/user")
	{
		userAPIGroup.GET("/llm-providers", webHandler.HandleGetLLMProviders)
		userAPIGroup.GET("/llm-config", webHandler.HandleGetLLMConfig)
		userAPIGroup.PUT("/llm-config", webHandler.HandleUpdateLLMConfig)
		userAPIGroup.DELETE("/llm-config", webHandler.HandleDeleteLLMConfig)
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
