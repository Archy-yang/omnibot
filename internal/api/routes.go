package api

// routes.go — 路由注册(依赖装配见 wire.go buildAppDeps)。

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"omnibot/frontend"
	"omnibot/internal/api/web"
	channelfactory "omnibot/internal/channel"
	channelfeishu "omnibot/internal/channel/feishu"
	channelweb "omnibot/internal/channel/web"
	channelwechat "omnibot/internal/channel/wechat"
	"omnibot/internal/middleware"
	"omnibot/internal/pkg/backtask"
	userRepo "omnibot/internal/repository/user"
	agentpkg "omnibot/internal/service/agent"
	butlerpkg "omnibot/internal/service/butler"
	chatService "omnibot/internal/service/chat"
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

	// 全量依赖装配(wire.go)
	d := buildAppDeps(cfg)

	// 微信回调路由(v1.9:注入 wechat channel 负责 XML 序列化,handler 业务路径只产纯文本)
	wechatGroup := r.Group("/wechat")
	{
		wechatGroup.GET("/callback", d.wechatHandler.Verify)
		wechatGroup.POST("/callback", d.wechatHandler.HandleMessage)
	}

	// 管理API路由
	apiGroup := r.Group("/api/v1")
	{
		// 健康检查
		apiGroup.GET("/health", d.adminHandler.HealthCheck)

		// 系统指标
		apiGroup.GET("/metrics", d.adminHandler.Metrics)

		// 配置管理
		apiGroup.GET("/config", d.adminHandler.GetConfig)
		apiGroup.PUT("/config", d.adminHandler.UpdateConfig)
	}

	// 认证接口(不挂 AuthRequired,注册/登录本身不能要求已登录)
	authAPIGroup := r.Group("/api/v1/auth")
	{
		authAPIGroup.POST("/register", d.authHandler.HandleRegister)
		authAPIGroup.POST("/login", d.authHandler.HandleLogin)
	}

	// WebSocket 实时推送(08 §4.8):鉴权走首条消息(浏览器 WS 无法带 header,
	// 宪章 6.2 禁止 token 进 URL),故不挂 AuthRequired
	r.GET("/api/v1/ws", d.realtimeHub.Handler(d.jwtSvc))

	// v2.1: 业务接口按路由组挂 JWT 鉴权;handler 从 c.GetInt64('user_id') 取身份
	chatAPIGroup := r.Group("/api/v1/chat")
	chatAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		chatAPIGroup.GET("/messages", d.webHandler.HandleGetHistory)
		chatAPIGroup.POST("/messages", d.webHandler.HandleSendMessage)
		chatAPIGroup.POST("/messages/stream", d.webHandler.HandleSendMessageStream)
		chatAPIGroup.POST("/messages/agent", d.webHandler.HandleSendMessageAgent)
		chatAPIGroup.POST("/messages/agent/stream", d.webHandler.HandleSendMessageAgentStream)
	}

	// 后台 Agent 任务接口(08 §4.7):WS 实时推送主路径 + 轮询兜底 + 触发汇报
	agentTaskGroup := r.Group("/api/v1/agent")
	agentTaskGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		agentTaskGroup.GET("/tasks", d.agentTaskHandler.HandleListTasks)
		agentTaskGroup.GET("/tasks/:id", d.agentTaskHandler.HandleGetTaskDetail)
		agentTaskGroup.GET("/tasks/:id/steps", d.agentTaskHandler.HandleListTaskSteps)
		agentTaskGroup.POST("/tasks/:id/report", d.agentTaskHandler.HandleReportTask)
	}

	// 持久任务接口(25 号):列表 + 控制;创建走对话内工具
	ptGroup := r.Group("/api/v1/persistent-tasks")
	ptGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		ptGroup.GET("", d.webHandler.HandleListPersistentTasks)
		ptGroup.POST("/:id/:action", d.webHandler.HandleControlPersistentTask)
		ptGroup.DELETE("/:id", d.webHandler.HandleDeletePersistentTask)
	}

	// 主动管家(26 号):设置读写 + 最近主动记录
	proGroup := r.Group("/api/v1/proactive-settings")
	proGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		proGroup.GET("", d.webHandler.HandleGetProactiveSettings)
		proGroup.PUT("", d.webHandler.HandleUpdateProactiveSettings)
	}
	proNotesGroup := r.Group("/api/v1/proactive-notes")
	proNotesGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		proNotesGroup.GET("", d.webHandler.HandleListProactiveNotes)
		proNotesGroup.PUT("/:id/feedback", d.webHandler.HandleSetNoteFeedback) // 29号 N3 送达反馈
	}

	// 持久任务调度循环(25 号):启动恢复(不追帧) + 30s tick。
	// 放在路由装配尾部,与飞书长连接同款 goroutine 纪律(独立 background ctx)。
	backtask.RunDetached("persistent task scheduler", func() {
		d.persistentScheduler.Start(context.Background())
	})

	// 主动管家调度循环(26 号):30min tick × hour 对位 × 当日已跑判定。
	// 克制闸门(日限/冷却/夜间窗)在 ScanService 代码层,调度器只管"何时喊人"。
	if d.butlerScheduler != nil {
		backtask.RunDetached("butler scheduler", func() {
			d.butlerScheduler.Start(context.Background())
		})
	}

	// v1.6: 飞书机器人接入(长连接)。enabled=false 时跳过,不影响 Web/微信启动。
	// channel 复用现有 msgSvc/agentSvc/llmConfigSvc--同步 Run 路径,所有
	// 跨入口能力(Agent、长期记忆、自定义 LLM 配置、agent_steps 复盘记录)自动继承。
	// v2.2/v2.3: 身份解析改为 BindingService(绑定码 + 已绑解析 + 未绑引导),不再自动建号。
	startFeishuChannel(cfg, d.bindingSvc, d.msgSvc, d.agentSvc, d.llmConfigSvc, d.subAgentSvc, d.butlerScan, d.butlerSettings, d.userChannelRepo)

	// 长期记忆路由
	memoryAPIGroup := r.Group("/api/v1/memories")
	memoryAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		memoryAPIGroup.GET("", d.webHandler.HandleGetMemories)
		memoryAPIGroup.POST("", d.webHandler.HandleCreateMemory)
		memoryAPIGroup.DELETE("", d.webHandler.HandleClearMemories)
		memoryAPIGroup.DELETE("/:id", d.webHandler.HandleDeleteMemory)
		memoryAPIGroup.PUT("/:id", d.webHandler.HandleUpdateMemory)
		memoryAPIGroup.PUT("/:id/pin", d.webHandler.HandlePinMemory)             // M8.3:置顶(常驻 core)
		memoryAPIGroup.PUT("/:id/loop-status", d.webHandler.HandleSetLoopStatus) // E3:loop 管理面关闭/重开(§14.2.2 逃生口)
		memoryAPIGroup.GET("/journal", d.webHandler.HandleGetJournal)            // 21号:个人经历时间轴

		// 记忆规则(22号):沉淀黑名单管理
		memoryRulesGroup := r.Group("/api/v1/memory-rules")
		memoryRulesGroup.Use(middleware.AuthRequired(d.jwtSvc))
		{
			memoryRulesGroup.GET("", d.webHandler.HandleListMemoryRules)
			memoryRulesGroup.POST("", d.webHandler.HandleAddMemoryRule)
			memoryRulesGroup.DELETE("/:id", d.webHandler.HandleDeleteMemoryRule)
		}

		// 风格与认知画像(24号)
		styleProfileGroup := r.Group("/api/v1/style-profile")
		styleProfileGroup.Use(middleware.AuthRequired(d.jwtSvc))
		{
			styleProfileGroup.GET("", d.webHandler.HandleGetStyleProfile)
			styleProfileGroup.PUT("/injection", d.webHandler.HandleUpdateStyleInjection) // M2 注入开关
			styleProfileGroup.DELETE("/observation/:id", d.webHandler.HandleDeleteStyleObservation)
			styleProfileGroup.DELETE("/change/:id", d.webHandler.HandleDeleteCognitionChange)
		}

		// 个人档案(21号 Archive):主题档案列表/详情/用户纠正口
		mattersAPIGroup := r.Group("/api/v1/matters")
		mattersAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
		{
			mattersAPIGroup.GET("", d.webHandler.HandleListMatters)
			mattersAPIGroup.GET("/:id", d.webHandler.HandleGetMatter)
			mattersAPIGroup.PUT("/:id", d.webHandler.HandleUpdateMatter)
			mattersAPIGroup.POST("/:id/sync-doc", d.webHandler.HandleSyncMatterDoc) // 23号:同步档案到飞书
		}
	}

	// 订阅源管理路由(14-订阅源管理:对话入口之外的页面管理入口)
	subscriptionAPIGroup := r.Group("/api/v1/subscriptions")
	subscriptionAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		subscriptionAPIGroup.GET("", d.subscriptionHandler.HandleListSubscriptions)
		subscriptionAPIGroup.POST("", d.subscriptionHandler.HandleAddSubscription)
		subscriptionAPIGroup.DELETE("/:id", d.subscriptionHandler.HandleDeleteSubscription)
		subscriptionAPIGroup.PUT("/:id/status", d.subscriptionHandler.HandleUpdateSubscriptionStatus)
	}

	// 技能管理路由(13-插件系统):清单 + 启停
	toolAPIGroup := r.Group("/api/v1/tools")
	toolAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		toolAPIGroup.GET("", d.webHandler.HandleListTools)
		toolAPIGroup.PUT("/:name", d.webHandler.HandleUpdateTool)
	}

	// 用户级飞书授权路由(19 号):发起/轮询/状态/断开(按 JWT 用户)
	feishuAuthAPIGroup := r.Group("/api/v1/feishu-auth")
	feishuAuthAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		feishuAuthAPIGroup.GET("", d.webHandler.HandleGetFeishuAuth)
		feishuAuthAPIGroup.POST("", d.webHandler.HandleStartFeishuAuth)
		feishuAuthAPIGroup.GET("/poll", d.webHandler.HandlePollFeishuAuth)
		feishuAuthAPIGroup.DELETE("", d.webHandler.HandleDisconnectFeishuAuth)
	}

	// Skill 技能管理路由(18-Skill 技能系统):CRUD(启用技能进主 Agent 技能目录)
	skillAPIGroup := r.Group("/api/v1/skills")
	skillAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		skillAPIGroup.GET("", d.webHandler.HandleListSkills)
		skillAPIGroup.GET("/:id", d.webHandler.HandleGetSkill)
		skillAPIGroup.POST("", d.webHandler.HandleCreateSkill)
		skillAPIGroup.PUT("/:id", d.webHandler.HandleUpdateSkill)
		skillAPIGroup.DELETE("/:id", d.webHandler.HandleDeleteSkill)
	}

	// MCP server 在线管理路由(M3):增删改查 + 手动同步 + OAuth 授权(M4)
	mcpAPIGroup := r.Group("/api/v1/mcp")
	mcpAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		mcpAPIGroup.GET("/servers", d.webHandler.HandleListMCPServers)
		mcpAPIGroup.POST("/servers", d.webHandler.HandleCreateMCPServer)
		mcpAPIGroup.PUT("/servers/:id", d.webHandler.HandleUpdateMCPServer)
		mcpAPIGroup.DELETE("/servers/:id", d.webHandler.HandleDeleteMCPServer)
		mcpAPIGroup.POST("/servers/:id/sync", d.webHandler.HandleSyncMCPServer)
		mcpAPIGroup.POST("/servers/:id/authorize", d.webHandler.HandleAuthorizeMCPServer)
	}

	// OAuth 回调(M4):浏览器由服务商重定向直达,不挂 JWT——安全性由一次性 state 保障
	r.GET("/api/v1/mcp/oauth/callback", d.webHandler.HandleOAuthCallback)

	// 用户 LLM 配置路由
	userAPIGroup := r.Group("/api/v1/user")
	userAPIGroup.Use(middleware.AuthRequired(d.jwtSvc))
	{
		userAPIGroup.GET("/llm-providers", d.webHandler.HandleGetLLMProviders)
		userAPIGroup.GET("/llm-config", d.webHandler.HandleGetLLMConfig)
		userAPIGroup.PUT("/llm-config", d.webHandler.HandleUpdateLLMConfig)
		userAPIGroup.DELETE("/llm-config", d.webHandler.HandleDeleteLLMConfig)

		// v2.3: 渠道绑定(状态查询 + 出码,通用码服务飞书+微信)
		channelBindHandler := web.NewChannelBindHandler(d.bindingSvc)
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
	butlerScan *butlerpkg.ScanService,
	butlerSettings *butlerpkg.SettingsService,
	userChannelRepo userRepo.UserChannelRepository,
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
	// 29号 N4:送达反馈文本通道(「有用/不用再提」);nil 安全(handler 不拦)
	if butlerSettings != nil {
		handler.SetNoteFeedbackService(butlerSettings)
	}
	// 飞书主动推送(方案A):子 Agent 完成时把结果推回飞书 open_id。
	// sender 在此才创建,故 notifier 在飞书启动时注入(而非 subAgentSvc 创建时)。
	subAgentSvc.SetNotifier(channelfeishu.NewFeishuTaskNotifier(sender, agentSvc, msgSvc, llmConfigSvc))
	// 26 号:主动管家飞书送达器(sender 在此才创建;未绑定渠道由 notifier 内静默降级为仅落库)
	if butlerScan != nil && userChannelRepo != nil {
		butlerScan.SetNotifier(channelfeishu.NewFeishuButlerNotifier(sender, userChannelRepo))
	}
	channel := channelfeishu.NewChannel(feishuCfg, handler, sender)

	channelfactory.Register(channel)

	// 用独立 background context;主服务退出由进程结束统一回收
	backtask.RunDetached("feishu long connection", func() {
		if err := channel.Start(context.Background()); err != nil {
			logger.ErrorWithFields("feishu: long connection ended with error", zap.Error(err))
		}
	})
}

func mustReadAll(r io.Reader) []byte {
	data, err := io.ReadAll(r)
	if err != nil {
		return []byte("<!doctype html><title>OmniBot</title>")
	}
	return data
}
