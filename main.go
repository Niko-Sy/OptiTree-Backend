package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/config"
	"optitree-backend/internal/handler"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/service"
	"optitree-backend/pkg/jwt"
	applogger "optitree-backend/pkg/logger"
)

func main() {
	// 日志初始化（控制台）
	consoleWriter := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	log.Logger = zerolog.New(consoleWriter).With().Timestamp().Logger()

	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("加载配置失败")
	}

	// 日志重新初始化：按天写文件 + 控制台双输出
	if cfg.Log.Enabled && cfg.Log.Dir != "" {
		fileWriter := applogger.NewDailyFileWriter(cfg.Log.Dir)
		multi := io.MultiWriter(consoleWriter, fileWriter)
		log.Logger = zerolog.New(multi).With().Timestamp().Logger()
		log.Info().Str("dir", cfg.Log.Dir).Msg("文件日志已启用")
	}

	// 设置 Gin 模式
	gin.SetMode(cfg.Server.Mode)

	// ---- 数据库 ----
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=Asia/Shanghai",
		cfg.Database.Host, cfg.Database.Port,
		cfg.Database.User, cfg.Database.Password,
		cfg.Database.DBName, cfg.Database.SSLMode,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		log.Fatal().Err(err).Msg("连接数据库失败")
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	// ---- Redis ----
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		MaxRetries:   cfg.Redis.MaxRetries,
		PoolSize:     cfg.Redis.PoolSize,
		MinIdleConns: cfg.Redis.MinIdleConns,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatal().Err(err).Msg("连接 Redis 失败")
	}

	// ---- JWT ----
	jwtManager := jwt.NewManager(
		cfg.JWT.Secret,
		cfg.JWT.AccessExpire,
		cfg.JWT.RefreshExpire,
		cfg.JWT.RefreshExpireLong,
	)

	// ---- 仓储层 ----
	userRepo := repository.NewUserRepository(db)
	projectRepo := repository.NewProjectRepository(db)
	graphRepo := repository.NewGraphRepository(db)
	versionRepo := repository.NewVersionRepository(db)
	memberRepo := repository.NewMemberRepository(db)
	authRepo := repository.NewAuthRepository(db)
	aiTaskRepo := repository.NewAITaskRepository(db)
	docRepo := repository.NewDocumentRepository(db)
	aiConversationRepo := repository.NewAIConversationRepository(db)
	aiChatMessageRepo := repository.NewAIChatMessageRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)
	auditRepo := repository.NewAuditLogRepository(db)

	// ---- 服务层 ----
	storageSvc := service.NewStorageService(
		cfg.Storage.LocalPath,
		cfg.Storage.BaseURL,
		cfg.Storage.MaxFileSize,
		cfg.Storage.AllowedImageTypes,
		cfg.Storage.AllowedDocTypes,
	)
	aiClient := ai.NewClient(cfg.AI.Endpoint, cfg.AI.APIKey, cfg.AI.DefaultModel, cfg.AI.ChatModel, cfg.AI.Timeout)
	taskProgressHub := service.NewTaskProgressHub()
	authSvc := service.NewAuthService(userRepo, authRepo, jwtManager, rdb)
	userSvc := service.NewUserService(userRepo, storageSvc)
	projectSvc := service.NewProjectService(db, projectRepo, memberRepo, graphRepo, versionRepo, docRepo)
	ftSvc := service.NewFaultTreeService(db, projectRepo, graphRepo, rdb)
	kgSvc := service.NewKnowledgeGraphService(db, projectRepo, graphRepo, rdb)
	versionSvc := service.NewVersionService(versionRepo, projectRepo, graphRepo, ftSvc, kgSvc)
	aiTaskSvc := service.NewAITaskService(
		aiTaskRepo,
		docRepo,
		projectRepo,
		memberRepo,
		projectSvc,
		ftSvc,
		kgSvc,
		taskProgressHub,
		rdb,
		cfg.AITask.Stream,
		cfg.AITask.StreamMaxLen,
		cfg.AITask.CallbackHeader,
		cfg.AITask.CallbackToken,
		cfg.AITask.ProducerStream,
		cfg.AITask.ProducerGroup,
		cfg.AITask.ProducerReadCount,
		cfg.AITask.ProducerBlockMs,
		cfg.AITask.DispatcherWorkers,
		cfg.AITask.ProducerDelayedZSet,
		cfg.AITask.ProducerRetryDelay,
		cfg.AITask.ProjectLockTTL,
		cfg.AITask.CallbackDedupeTTL,
		cfg.AITask.SnapshotTTL,
	)
	assistantSvc := service.NewAssistantService(
		aiConversationRepo,
		aiChatMessageRepo,
		projectRepo,
		memberRepo,
		aiClient,
		ftSvc,
		kgSvc,
	)
	if err := aiTaskSvc.StartFaultTreeDispatcher(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("启动 AI 故障树调度器失败")
	}
	docSvc := service.NewDocumentService(docRepo, storageSvc)
	memberSvc := service.NewMemberService(db, memberRepo, projectRepo, userRepo, notificationRepo, auditRepo)
	notificationSvc := service.NewNotificationService(notificationRepo)
	teamSvc := service.NewTeamService(db)

	// ---- 处理器 ----
	authH := handler.NewAuthHandler(authSvc)
	userH := handler.NewUserHandler(userSvc)
	projectH := handler.NewProjectHandler(projectSvc)
	dashboardH := handler.NewDashboardHandler(projectSvc)
	ftH := handler.NewFaultTreeHandler(ftSvc)
	kgH := handler.NewKnowledgeGraphHandler(kgSvc)
	versionH := handler.NewVersionHandler(versionSvc)
	docH := handler.NewDocumentHandler(docSvc)
	aiTaskH := handler.NewAITaskHandler(aiTaskSvc)
	assistantH := handler.NewAssistantHandler(assistantSvc)
	aiTaskWSH := handler.NewAITaskWSHandler(taskProgressHub, jwtManager, rdb, memberRepo)
	memberH := handler.NewMemberHandler(memberSvc)
	notificationH := handler.NewNotificationHandler(notificationSvc, memberSvc)
	teamH := handler.NewTeamHandler(teamSvc)

	// ---- 路由 ----
	router := gin.New()
	router.Use(
		middleware.RequestID(),
		middleware.Logger(),
		middleware.Recovery(),
		middleware.CORSLocalhost(),
	)

	// 静态文件（本地存储）
	router.Static("/static", cfg.Storage.LocalPath)

	// 健康检查
	router.GET("/healthz", func(c *gin.Context) {
		sqlDB, _ := db.DB()
		if err := sqlDB.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db_error", "error": err.Error()})
			return
		}
		if err := rdb.Ping(c.Request.Context()).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "redis_error", "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// AI 任务实时进度推送（WebSocket）
	router.GET("/api/v1/ws/tasks/:projectId", aiTaskWSH.StreamTaskProgress)
	router.POST("/internal/ai/tasks/callback", aiTaskH.TaskCallback)

	v1 := router.Group("/api/v1")

	// ---- 公开接口 ----
	auth := v1.Group("/auth")
	{
		auth.POST("/register", authH.Register)
		auth.POST("/login", authH.Login)
		auth.POST("/refresh", authH.Refresh)
		auth.POST("/forgot-password", authH.ForgotPassword)
		auth.POST("/reset-password", authH.ResetPassword)
	}

	// ---- 需要认证的接口 ----
	authed := v1.Group("")
	authed.Use(middleware.JWTAuth(jwtManager, rdb))
	{
		// 认证相关（需登录）
		authed.POST("/auth/logout", authH.Logout)
		authed.POST("/auth/change-password", authH.ChangePassword)

		// 用户
		users := authed.Group("/users")
		{
			users.GET("/me", userH.GetMe)
			users.POST("/me/update", userH.UpdateProfile)
			users.POST("/me/avatar", userH.UploadAvatar)
			users.GET("/me/login-logs", userH.GetLoginLogs)
			users.GET("/me/invitations", memberH.ListMyInvitations)
		}

		// 仪表盘
		authed.GET("/dashboard/summary", dashboardH.GetSummary)

		// 项目
		projects := authed.Group("/projects")
		{
			// Backward-compatible summary endpoint for clients still calling /projects/summary.
			projects.GET("/summary", dashboardH.GetSummary)
			projects.GET("", projectH.List)
			projects.POST("", projectH.Create)
			projects.GET("/:projectId", projectH.GetByID)
			projects.POST("/:projectId/update", projectH.Update)
			projects.POST("/:projectId/rename", withEditorRole(memberRepo, projectH.Rename))
			projects.POST("/:projectId/delete", withAdminRole(memberRepo, projectH.Delete))

			// 版本
			projects.GET("/:projectId/versions", versionH.List)
			projects.POST("/:projectId/versions", versionH.Create)
			projects.GET("/:projectId/versions/:versionId", versionH.GetDetail)
			projects.POST("/:projectId/versions/:versionId/rollback", withEditorRole(memberRepo, versionH.Rollback))
			projects.POST("/:projectId/versions/:versionId/delete", withAdminRole(memberRepo, versionH.Delete))

			// 成员管理
			projects.GET("/:projectId/members", memberH.ListMembers)
			projects.POST("/:projectId/members/invite", withAdminRole(memberRepo, memberH.InviteMember))
			projects.GET("/:projectId/members/invite-candidate", withAdminRole(memberRepo, memberH.QueryInviteCandidate))
			projects.GET("/:projectId/invitations", withAdminRole(memberRepo, memberH.ListInvitations))
			projects.POST("/:projectId/invitations/:invitationId/accept", memberH.AcceptInvitation)
			projects.POST("/:projectId/invitations/:invitationId/reject", memberH.RejectInvitation)
			projects.POST("/:projectId/invitations/:invitationId/revoke", withAdminRole(memberRepo, memberH.RevokeInvitation))
			projects.POST("/:projectId/members/:memberId/update", withAdminRole(memberRepo, memberH.UpdateRole))
			projects.POST("/:projectId/members/:memberId/remove", withAdminRole(memberRepo, memberH.RemoveMember))
		}

		notifications := authed.Group("/notifications")
		{
			notifications.GET("", notificationH.List)
			notifications.GET("/unread-count", notificationH.UnreadCount)
			notifications.POST("/:notificationId/read", notificationH.MarkRead)
			notifications.POST("/read-all", notificationH.MarkAllRead)
			notifications.POST("/:notificationId/action", notificationH.Action)
		}

		// 故障树
		ft := authed.Group("/fault-trees")
		{
			ft.GET("/:projectId/graph", ftH.GetGraph)
			ft.POST("/:projectId/graph/save", withEditorRole(memberRepo, ftH.SaveGraph))
			ft.POST("/:projectId/validate", ftH.Validate)
			ft.GET("/:projectId/export", ftH.ExportGraph)
			ft.POST("/import", ftH.ImportGraph)
		}

		// 知识图谱
		kg := authed.Group("/knowledge-graphs")
		{
			kg.GET("/:projectId/graph", kgH.GetGraph)
			kg.POST("/:projectId/graph/save", withEditorRole(memberRepo, kgH.SaveGraph))
			kg.POST("/:projectId/validate", kgH.Validate)
			kg.GET("/:projectId/export", kgH.ExportGraph)
			kg.POST("/import", kgH.ImportGraph)
		}

		// 文档
		docs := authed.Group("/documents")
		{
			docs.POST("/upload", docH.Upload)
			docs.GET("/:docId", docH.GetByID)
		}

		// AI 任务
		ai := authed.Group("/ai")
		{
			ai.GET("/tasks/:taskId", aiTaskH.GetTask)
			ai.GET("/projects/:projectId/tasks/latest", aiTaskH.GetLatestFaultTreeTaskByProject)
			ai.POST("/fault-trees/generate", aiTaskH.GenerateFaultTree)
			ai.POST("/knowledge-graphs/generate", aiTaskH.GenerateKnowledgeGraph)
		}

		// AI 助手（会话化）
		assistant := authed.Group("/assistant")
		{
			assistant.GET("/models", assistantH.GetModels)
			assistant.POST("/conversations", assistantH.CreateConversation)
			assistant.GET("/conversations", assistantH.ListConversations)
			assistant.GET("/conversations/:conversationId/messages", assistantH.GetMessageHistory)
			assistant.POST("/conversations/:conversationId/messages", assistantH.SendMessage)
			assistant.POST("/conversations/:conversationId/messages/stream", assistantH.SendMessageStream)
			assistant.DELETE("/conversations/:conversationId", assistantH.DeleteConversation)
		}

		// 团队总览
		team := authed.Group("/team")
		{
			team.GET("/overview", teamH.GetOverview)
			team.GET("/projects", teamH.ListProjects)
			team.GET("/activities", teamH.ListActivities)
		}
	}

	// ---- 启动服务 ----
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		log.Info().Msgf("服务启动，监听端口 %s:%d", cfg.Server.Host, cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("服务启动失败")
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("正在关闭服务...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("服务关闭超时")
	}
	aiTaskSvc.StopFaultTreeDispatcher()

	_ = rdb.Close()
	log.Info().Msg("服务已关闭")
}

// withEditorRole 要求项目 editor 及以上权限
func withEditorRole(memberRepo *repository.MemberRepository, handler gin.HandlerFunc) gin.HandlerFunc {
	return gin.HandlersChain{
		middleware.RequireProjectRole("editor", memberRepo),
		handler,
	}.Last()
}

// withAdminRole 要求项目 admin 权限
func withAdminRole(memberRepo *repository.MemberRepository, handler gin.HandlerFunc) gin.HandlerFunc {
	return gin.HandlersChain{
		middleware.RequireProjectRole("admin", memberRepo),
		handler,
	}.Last()
}
