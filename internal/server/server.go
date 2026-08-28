package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"modelgate/internal/config"
	"modelgate/internal/domain/admin"
	"modelgate/internal/domain/apikey"
	"modelgate/internal/domain/dashboard"
	"modelgate/internal/domain/quota"
	"modelgate/internal/domain/usage"
	"modelgate/internal/domain/user"
	"modelgate/internal/gateway/anthropic"
	"modelgate/internal/gateway/codex"
	"modelgate/internal/gateway/openai"
	"modelgate/internal/gateway/proxy"
	"modelgate/internal/gateway/responses"
	"modelgate/internal/infra/auth"
	"modelgate/internal/infra/cache"
	"modelgate/internal/infra/concurrency"
	"modelgate/internal/infra/db"
	"modelgate/internal/infra/logger"
	"modelgate/internal/infra/middleware"
	"modelgate/internal/infra/static"
	"modelgate/internal/repository"
)

type Server struct {
	cfg        *config.Config
	cfgManager *config.ConfigManager
	engine     *gin.Engine
	httpServer *http.Server

	// Infrastructure
	db         *db.DB
	userLogger *logger.UserLogger
	localCache *cache.Cache
	jwtManager *auth.JWTManager
	limiter    *concurrency.Limiter

	// Stores (Singletons)
	userStore    *entity.UserStore
	apiKeyStore  *entity.APIKeyStore
	modelStore   *entity.ModelStore
	backendStore *entity.BackendStore
	quotaStore   *entity.QuotaStore

	// Services
	apiKeyService    *apikey.Service
	dashboardService *dashboard.Service
	quotaService     *quota.Service
	usageService     *usage.Service

	// Proxy
	lb            *proxy.RoundRobinBalancer
	proxyInstance *proxy.Proxy
}

func NewServer(cfg *config.Config, cfgPath string) *Server {
	return &Server{
		cfg:        cfg,
		cfgManager: config.NewManager(cfg, cfgPath),
	}
}

func (s *Server) Start() error {
	// 1. Initialize Infrastructure
	if err := s.initInfrastructure(); err != nil {
		return err
	}

	// 2. Initialize Stores & Services
	s.initServices()

	// 3. Setup Routes
	s.setupRoutes()

	// 4. Start Background Tasks
	s.startBackgroundTasks()

	// 5. Create HTTP Server
	s.httpServer = &http.Server{
		Addr:           fmt.Sprintf(":%d", s.cfg.Server.Port),
		Handler:        s.engine,
		ReadTimeout:    s.cfg.Server.ReadTimeout,
		WriteTimeout:   s.cfg.Server.WriteTimeout,
		IdleTimeout:    s.cfg.Server.IdleTimeout,
		MaxHeaderBytes: s.cfg.Server.MaxHeaderBytes,
	}

	// 6. Graceful Shutdown Handling
	go s.handleSignals()

	log.Printf("Server starting on %s", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %v", err)
	}

	return nil
}

func (s *Server) initInfrastructure() error {
	gin.SetMode(s.cfg.Server.Mode)

	development := s.cfg.Server.Mode == "debug"
	if err := logger.InitLogger(development); err != nil {
		log.Printf("Failed to initialize logger: %v", err)
	}

	database, err := db.New(s.cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %v", err)
	}
	s.db = database

	if err := s.db.Migrate(); err != nil {
		log.Printf("Migration warning: %v", err)
	}

	s.userLogger = logger.NewUserLogger(s.cfg.Logs.Path, s.cfg.Logs.RetentionDays, s.cfg.Logs.LogPayloads)
	s.localCache = cache.New()
	s.jwtManager = auth.NewJWTManager(s.cfg.JWT.Secret, s.cfg.JWT.ExpireHours)
	s.limiter = concurrency.NewLimiter()

	return nil
}

func (s *Server) initServices() {
	// Stores
	s.userStore = entity.NewUserStore(s.db.DB)
	s.apiKeyStore = entity.NewAPIKeyStore(s.db.DB)
	s.modelStore = entity.NewModelStore(s.cfgManager)
	s.backendStore = entity.NewBackendStore(s.cfgManager)
	s.quotaStore = entity.NewQuotaStore(s.cfgManager, s.db.DB)

	// Create default admin
	s.createDefaultAdmin(s.userStore, s.cfg.Admin.DefaultEmail, s.cfg.Admin.DefaultPassword)

	// Services
	s.apiKeyService = apikey.NewService(s.apiKeyStore, s.userStore, s.localCache)
	s.dashboardService = dashboard.NewService(s.db.DB)
	s.quotaService = quota.NewService(s.quotaStore, s.modelStore, s.apiKeyStore, s.dashboardService)
	s.usageService = usage.NewServiceWithStore(s.userLogger, entity.NewAccessLogStore(s.db.DB))

	// Proxy
	s.lb = proxy.NewRoundRobinBalancer()
	s.lb.ReloadConfig(s.cfgManager.GetModels())
	s.lb.StartHealthCheck(30 * time.Second)

	s.proxyInstance = proxy.NewProxy(s.lb, s.quotaService, s.usageService, s.modelStore, s.backendStore, s.userStore)

	trafficDumper := logger.NewTrafficDumper(s.cfg.Logs.Path, s.cfg.Logs.RawDumps)
	s.proxyInstance.SetTrafficDumper(trafficDumper)

	s.dashboardService.SetConcurrencyLimiter(s.limiter)
}

func (s *Server) setupRoutes() {
	r := gin.New()
	r.UseRawPath = true
	r.UnescapePathValues = true
	r.Use(gin.Recovery())
	r.Use(gin.Logger())
	r.Use(static.Serve())

	api := r.Group("/api/v1")

	userHandler := user.NewHandler(user.NewHandlerParams{
		Store:         s.userStore,
		JWTManager:    s.jwtManager,
		QuotaService:  s.quotaService,
		QuotaStore:    s.quotaStore,
		UsageService:  s.usageService,
		Cache:         s.localCache,
		ConfigManager: s.cfgManager,
	})
	userHandler.RegisterRoutes(api)

	apiKeyHandler := apikey.NewHandler(s.apiKeyService, s.userStore)
	apiKeyHandler.RegisterRoutes(api, s.jwtManager)

	adminModelHandler := admin.NewModelHandler(s.modelStore, s.backendStore, s.lb, s.userStore)
	adminModelHandler.RegisterRoutes(api, s.jwtManager)

	adminPolicyHandler := admin.NewPolicyHandler(s.quotaStore, s.userStore)
	adminPolicyHandler.RegisterRoutes(api, s.jwtManager)

	dashboardHandler := dashboard.NewHandler(s.dashboardService)
	dashboardAPI := api.Group("/dashboard")
	dashboardAPI.Use(middleware.AuthMiddlewareWithUserValidation(s.jwtManager, s.userStore))
	dashboardHandler.RegisterRoutes(dashboardAPI)

	// Admin stats (matching inline handlers from main.go)
	adminAPI := api.Group("/admin")
	adminAPI.Use(middleware.AuthMiddlewareWithUserValidation(s.jwtManager, s.userStore))
	adminAPI.Use(middleware.AdminRequired())
	{
		adminAPI.GET("/concurrency/stats", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"data": s.limiter.GetStats()})
		})
		adminAPI.GET("/cache/stats", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"data": s.localCache.Stats()})
		})
	}

	// OpenAI 兼容代理接口
	openaiAuth := middleware.ProxyAuthMiddleware(s.apiKeyService, s.jwtManager, s.userStore)
	openaiHandler := openai.NewHandler(s.proxyInstance, s.usageService)
	openaiHandler.RegisterRoutes(r, openaiAuth, s.limiter, middleware.ClientFilterMiddleware(s.cfgManager))

	// Anthropic 兼容代理接口
	anthropicAuth := middleware.ProxyAuthMiddleware(s.apiKeyService, s.jwtManager, s.userStore)
	anthropicHandler := anthropic.NewHandler(s.proxyInstance, s.usageService)
	anthropicHandler.RegisterRoutes(r, anthropicAuth, s.limiter, middleware.ClientFilterMiddleware(s.cfgManager))

	// Codex Text Completions 兼容代理接口
	codexAuth := middleware.ProxyAuthMiddleware(s.apiKeyService, s.jwtManager, s.userStore)
	codexHandler := codex.NewHandler(s.proxyInstance, s.usageService, s.cfgManager)
	codexHandler.RegisterRoutes(r, codexAuth, s.limiter, middleware.ClientFilterMiddleware(s.cfgManager))

	// OpenAI Responses API 兼容代理接口 (Codex CLI / OpenCode)
	responsesAuth := middleware.ProxyAuthMiddleware(s.apiKeyService, s.jwtManager, s.userStore)
	responsesHandler := responses.NewHandler(s.proxyInstance, s.usageService, s.cfgManager)
	responsesHandler.RegisterRoutes(r, responsesAuth, s.limiter, middleware.ClientFilterMiddleware(s.cfgManager))

	s.engine = r
}

func (s *Server) startBackgroundTasks() {
	// Config Hot-Reload
	configChanges := s.cfgManager.Subscribe()
	go func() {
		for event := range configChanges {
			switch event.Type {
			case "models", "all":
				log.Println("Config reload detected: updating load balancer")
				s.lb.ReloadConfig(s.cfgManager.GetModels())
				log.Printf("Load balancer updated with %d models", len(s.cfgManager.GetModels()))
			}
		}
	}()

	// Usage record cleanup
	go s.cleanupTask()
}

func (s *Server) cleanupTask() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		log.Println("Running cleanup task...")
		if err := s.usageService.CleanupOldRecords(); err != nil {
			log.Printf("Cleanup error: %v", err)
		}
		retentionDays := s.cfgManager.GetConfig().Logs.PayloadRetentionDays
		if retentionDays <= 0 {
			retentionDays = 7
		}
		deleted, err := s.usageService.CleanupOldPayloads(retentionDays)
		if err != nil {
			log.Printf("Payload cleanup error: %v", err)
		} else if deleted > 0 {
			log.Printf("Cleaned up %d expired payload records (retention: %d days)", deleted, retentionDays)
		}
	}
}

func (s *Server) handleSignals() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	s.usageService.Flush()
	s.db.Close()
	s.userLogger.Close()
	s.localCache.Stop()

	log.Println("Server stopped")
}

func (s *Server) createDefaultAdmin(store *entity.UserStore, email, password string) {
	if email == "" {
		return
	}
	existing, err := store.GetByEmailAll(email)
	if err != nil {
		log.Printf("Failed to check existing admin: %v", err)
		return
	}
	if existing != nil {
		return
	}

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		log.Printf("Failed to hash password: %v", err)
		return
	}

	adminUser := &entity.User{
		Email:        email,
		PasswordHash: passwordHash,
		Name:         "Administrator",
		Role:         entity.RoleAdmin,
		QuotaPolicy:  "vip",
		Enabled:      true,
	}

	if err := store.Create(adminUser); err != nil {
		log.Printf("Failed to create default admin: %v", err)
		return
	}
	log.Printf("Created default admin: %s", email)
}
