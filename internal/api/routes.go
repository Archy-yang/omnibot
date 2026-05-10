package api

import (
	"wechat-intelligent-bot/internal/api/admin"
	"wechat-intelligent-bot/internal/api/wechat"
	"wechat-intelligent-bot/internal/client/llm"
	"wechat-intelligent-bot/internal/db"
	"wechat-intelligent-bot/internal/middleware"
	userRepo "wechat-intelligent-bot/internal/repository/user"
	userService "wechat-intelligent-bot/internal/service/user"
	"wechat-intelligent-bot/pkg/config"
	"wechat-intelligent-bot/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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

	// 初始化用户服务
	userSvc := userService.NewUserService(userRepository, wechatAccountRepository)
	llmConfigSvc := userService.NewLLMConfigService(llmConfigRepository)

	// 微信回调路由
	wechatHandler := wechat.NewHandler(wechat.Config{
		AppID:          cfg.Wechat.AppID,
		AppSecret:      cfg.Wechat.AppSecret,
		Token:          cfg.Wechat.Token,
		EncodingAESKey: cfg.Wechat.EncodingAESKey,
		CallbackURL:    cfg.Wechat.CallbackURL,
	}, llmClient, userSvc, llmConfigSvc)
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

	// Ping路由，用于服务探活
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"message": "pong",
		})
	})

	return r
}
