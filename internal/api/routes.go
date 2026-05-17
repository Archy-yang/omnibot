package api

import (
	"net/http"

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
	userRepo "omnibot/internal/repository/user"
	chatService "omnibot/internal/service/chat"
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

	// 初始化用户服务
	userSvc := userService.NewUserService(userRepository, wechatAccountRepository, userChannelRepository)
	llmConfigSvc := userService.NewLLMConfigService(llmConfigRepository)

	// 初始化消息服务
	msgRepo := chatRepo.NewMessageRepository(dbConn.GetGormDB())
	msgSvc := chatService.NewMessageService(msgRepo)

	// 微信回调路由
	wechatHandler := wechat.NewHandler(wechat.Config{
		AppID:          cfg.Wechat.AppID,
		AppSecret:      cfg.Wechat.AppSecret,
		Token:          cfg.Wechat.Token,
		EncodingAESKey: cfg.Wechat.EncodingAESKey,
		CallbackURL:    cfg.Wechat.CallbackURL,
	}, llmClient, userSvc, llmConfigSvc, msgSvc)
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
	webHandler := web.NewHandler(userSvc, msgSvc, llmClient)
	chatAPIGroup := r.Group("/api/v1/chat")
	{
		chatAPIGroup.GET("/messages", webHandler.HandleGetHistory)
		chatAPIGroup.POST("/messages", webHandler.HandleSendMessage)
	}

	// 前端静态资源路由 - 嵌入到二进制中
	webFS := http.FS(frontend.FS)
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
