package user

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"modelgate/internal/config"
	"modelgate/internal/domain/usage"
	"modelgate/internal/infra/auth"
	"modelgate/internal/infra/middleware"
	"modelgate/internal/repository"
	"modelgate/internal/version"
)

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

type RefinedServerConfigJSON struct {
	ReadTimeout  string `json:"read_timeout" binding:"required"`
	WriteTimeout string `json:"write_timeout" binding:"required"`
	IdleTimeout  string `json:"idle_timeout" binding:"required"`
}

type RefinedSystemConfigJSON struct {
	Server       RefinedServerConfigJSON    `json:"server" binding:"required"`
	Frontend     config.FrontendConfig      `json:"frontend" binding:"required"`
	ClientFilter config.ClientFilterConfig  `json:"client_filter"`
}

type QuotaService interface {
	GetQuotaStats(userID uuid.UUID, policyName string) (map[string]interface{}, error)
}

type QuotaStore interface {
	GetRecentUsageRecords(userID uuid.UUID, days int) ([]map[string]interface{}, error)
	GetDailyUsageList(userID uuid.UUID, startDate, endDate time.Time) ([]*entity.QuotaUsageDaily, error)
	ValidatePoliciesNoConflict(policyNames []string) error
}

type UsageService interface {
	GetRecentAccess(userID uuid.UUID, limit int) []usage.AccessLog
	GetAllRecentAccess(limit int) []usage.AccessLog
}

type Cache interface {
	DeleteUser(userID string)
	DeleteAPIKeysByUser(userID uuid.UUID)
}

type Handler struct {
	store        *entity.UserStore
	jwtManager   *auth.JWTManager
	quotaService QuotaService
	quotaStore   QuotaStore
	usageService UsageService
	cache        Cache
	cm           *config.ConfigManager
}

type NewHandlerParams struct {
	Store         *entity.UserStore
	JWTManager    *auth.JWTManager
	QuotaService  QuotaService
	QuotaStore    QuotaStore
	UsageService  UsageService
	Cache         Cache
	ConfigManager *config.ConfigManager
}

func NewHandler(p NewHandlerParams) *Handler {
	return &Handler{
		store:        p.Store,
		jwtManager:   p.JWTManager,
		quotaService: p.QuotaService,
		quotaStore:   p.QuotaStore,
		usageService: p.UsageService,
		cache:        p.Cache,
		cm:           p.ConfigManager,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// 公开接口
	r.POST("/auth/login", h.Login)
	r.POST("/auth/register", h.Register)
	r.GET("/config/frontend", h.GetFrontendConfig)

	// SSO 接口（如果启用）
	ssoConfig := h.cm.GetConfig().SSO
	if ssoConfig.Enabled {
		r.GET("/auth/sso/config", h.GetSSOConfig)
		r.GET("/auth/sso/login", h.SSOLogin)
		r.GET("/auth/sso/callback", h.SSOCallback)
	}

	// 需要认证的接口
	auth := r.Group("")
	auth.Use(middleware.AuthMiddlewareWithUserValidation(h.jwtManager, h.store))
	{
		auth.GET("/user/profile", h.Profile)
		auth.GET("/user/quota", h.GetQuota)
		auth.GET("/user/usage", h.GetUsage)
		auth.GET("/user/access-logs", h.GetAccessLogs)
		auth.PUT("/user/password", h.ChangePassword)
	}

	// 管理员接口
	admin := r.Group("/admin")
	admin.Use(middleware.AuthMiddlewareWithUserValidation(h.jwtManager, h.store))
	admin.Use(middleware.AdminRequired())
	{
		// /admin/users
		users := admin.Group("/users")
		users.GET("", h.List)
		users.POST("", h.Create)
		users.PUT("/:id", h.Update)
		users.DELETE("/:id", h.Delete)
		// /admin/config
		config := admin.Group("/config")
		config.GET("/system", h.GetSystemConfig)
		config.PUT("/system", h.UpdateSystemConfig)

		// /admin/access-logs
		admin.GET("/access-logs", h.GetAllAccessLogs)
		admin.GET("/access-logs/export", h.ExportAccessLogsByToken)
	}
}

func (h *Handler) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := h.store.GetByEmailAll(req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if user == nil || !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !user.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "account disabled"})
		return
	}

	// 更新最后登录时间
	_ = h.store.UpdateLastLogin(user.ID)

	token, err := h.jwtManager.Generate(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"token": token,
			"user":  user.ToResponse(),
		},
	})
}

func (h *Handler) createUserInternal(req entity.UserCreateRequest, enabled bool) (*entity.User, error) {
	if req.Role == "" {
		req.Role = entity.RoleUser
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	user := &entity.User{
		Email:         req.Email,
		PasswordHash:  passwordHash,
		Name:          req.Name,
		Role:          req.Role,
		Department:    req.Department,
		QuotaPolicy:   req.QuotaPolicy,
		QuotaPolicies: entity.StringArray(req.QuotaPolicies),
		Enabled:       enabled,
	}

	if user.QuotaPolicy == "" {
		user.QuotaPolicy = "default"
	}

	if err := h.store.Create(user); err != nil {
		return nil, err
	}

	return user, nil
}

func (h *Handler) Register(c *gin.Context) {
	var req entity.UserCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 检查邮箱是否已存在（包括待审核的用户）
	existing, err := h.store.GetByEmailAll(req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "email already exists"})
		return
	}

	_, err = h.createUserInternal(req, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "注册成功，请等待管理员审核",
	})
}

func (h *Handler) Profile(c *gin.Context) {
	user := middleware.GetCurrentFullUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": user.ToResponse()})
}

func (h *Handler) List(c *gin.Context) {
	// 分页参数
	page := 1
	pageSize := 20
	if p, err := strconv.Atoi(c.DefaultQuery("page", "1")); err == nil && p > 0 {
		page = p
	}
	if ps, err := strconv.Atoi(c.DefaultQuery("page_size", "20")); err == nil && ps > 0 {
		if ps > 1000 {
			pageSize = 1000
		} else {
			pageSize = ps
		}
	}

	// 排序参数
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")

	offset := (page - 1) * pageSize

	// 查询总数
	total, err := h.store.Count()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 查询分页数据
	users, err := h.store.ListPaginated(pageSize, offset, sortBy, sortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var responses []entity.UserResponse
	for _, u := range users {
		responses = append(responses, u.ToResponse())
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      responses,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *Handler) Create(c *gin.Context) {
	var req entity.UserCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 检查邮箱是否已存在
	existing, err := h.store.GetByEmail(req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "email already exists"})
		return
	}

	if len(req.QuotaPolicies) > 1 {
		if err := h.quotaStore.ValidatePoliciesNoConflict(req.QuotaPolicies); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	user, err := h.createUserInternal(req, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": user.ToResponse()})
}

func (h *Handler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	user, err := h.store.GetByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var req entity.UserUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != "" {
		user.Name = req.Name
	}
	if req.Role != "" {
		user.Role = req.Role
	}
	if req.Department != "" {
		user.Department = req.Department
	}
	if req.QuotaPolicy != "" {
		user.QuotaPolicy = req.QuotaPolicy
	}
	if req.QuotaPolicies != nil {
		if len(req.QuotaPolicies) > 1 {
			if err := h.quotaStore.ValidatePoliciesNoConflict(req.QuotaPolicies); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		user.QuotaPolicies = entity.StringArray(req.QuotaPolicies)
	}
	if req.Enabled != nil {
		user.Enabled = *req.Enabled
	}

	if err := h.store.Update(user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 如果用户被禁用，清除缓存
	if req.Enabled != nil && !*req.Enabled {
		h.cache.DeleteUser(id.String())
		h.cache.DeleteAPIKeysByUser(id)
	}

	c.JSON(http.StatusOK, gin.H{"data": user.ToResponse()})
}

func (h *Handler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	// 清除用户缓存和 API Key 缓存
	h.cache.DeleteUser(id.String())
	h.cache.DeleteAPIKeysByUser(id)

	if err := h.store.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}

func (h *Handler) GetQuota(c *gin.Context) {
	user := middleware.GetCurrentFullUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	stats, err := h.quotaService.GetQuotaStats(user.ID, user.QuotaPolicy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": stats})
}

func (h *Handler) GetUsage(c *gin.Context) {
	user := middleware.GetCurrentFullUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// 支持查询参数 days，默认 7 天
	days := 7
	// 获取最近 N 天的使用统计
	records, err := h.quotaStore.GetRecentUsageRecords(user.ID, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": records})
}

func (h *Handler) GetAccessLogs(c *gin.Context) {
	user := middleware.GetCurrentFullUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// 支持查询参数 ?detailed=true 返回完整信息
	detailed := c.Query("detailed") == "true"

	// 获取最近20条访问记录
	logs := h.usageService.GetRecentAccess(user.ID, 20)

	if detailed {
		// 返回完整信息（包含请求/响应体和头信息）
		// 在返回前也对 UserAgent 进行脱敏/美化处理
		for i := range logs {
			logs[i].Beautify()
		}
		c.JSON(http.StatusOK, gin.H{"data": logs})
		return
	}

	// 默认返回简化版本（不包含请求/响应体和头信息，保持兼容性）
	simpleLogs := make([]usage.SimpleAccessLog, 0, len(logs))
	for _, log := range logs {
		simpleLogs = append(simpleLogs, log.ToSimple())
	}

	c.JSON(http.StatusOK, gin.H{"data": simpleLogs})
}

func (h *Handler) GetAllAccessLogs(c *gin.Context) {
	// 支持查询参数 ?detailed=true 返回完整信息
	detailed := c.Query("detailed") == "true"

	// 从查询参数获取 limit
	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}

	// 获取最近访问记录
	logs := h.usageService.GetAllRecentAccess(limit)

	if detailed {
		// 返回完整信息（包含请求/响应体和头信息）
		// 在返回前也对 UserAgent 进行脱敏/美化处理
		for i := range logs {
			logs[i].Beautify()
		}
		c.JSON(http.StatusOK, gin.H{"data": logs})
		return
	}

	// 默认返回简化版本
	simpleLogs := make([]usage.SimpleAccessLog, 0, len(logs))
	for _, log := range logs {
		simpleLogs = append(simpleLogs, log.ToSimple())
	}

	c.JSON(http.StatusOK, gin.H{"data": simpleLogs})
}

func (h *Handler) GetFrontendConfig(c *gin.Context) {
	cfg := h.cm.GetConfig()
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"feedback_url":         cfg.Frontend.FeedbackURL,
			"dev_manual_url":       cfg.Frontend.DevManualURL,
			"sso_enabled":          cfg.SSO.Enabled,
			"registration_enabled": cfg.Frontend.RegistrationEnabled,
			"version":              version.Version,
			"build_time":           version.BuildTime,
			"commit":               version.Commit,
		},
	})
}



// ========== SSO 相关接口 ==========

// GetSSOConfig 获取 SSO 配置（供前端使用）
func (h *Handler) GetSSOConfig(c *gin.Context) {
	ssoConfig := h.cm.GetConfig().SSO
	if !ssoConfig.Enabled {
		c.JSON(http.StatusOK, gin.H{"data": nil})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"enabled":   ssoConfig.Enabled,
			"client_id": ssoConfig.ClientID,
			"auth_url":  ssoConfig.GetAuthorizeURL(),
			"provider":  ssoConfig.Provider,
		},
	})
}

// generateState 生成随机 state
func generateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// SSOLogin 跳转至 SSO 登录页
func (h *Handler) SSOLogin(c *gin.Context) {
	ssoConfig := h.cm.GetConfig().SSO
	if !ssoConfig.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSO not enabled"})
		return
	}

	state := generateState()
	// 将 state 存入 cookie（简化版，生产环境应使用 session/cache）
	c.SetCookie("sso_state", state, 600, "/", "", false, true)

	// 构建回调 URL
	callbackURL := fmt.Sprintf("%s/api/v1/auth/sso/callback", c.Request.Host)
	if c.Request.TLS == nil {
		callbackURL = "http://" + callbackURL
	} else {
		callbackURL = "https://" + callbackURL
	}

	// 构建授权 URL
	authURL := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=openid email profile&state=%s",
		ssoConfig.GetAuthorizeURL(),
		url.QueryEscape(ssoConfig.ClientID),
		url.QueryEscape(callbackURL),
		state,
	)

	c.Redirect(http.StatusFound, authURL)
}

// SSOCallback SSO 回调处理
func (h *Handler) SSOCallback(c *gin.Context) {
	ssoConfig := h.cm.GetConfig().SSO
	if !ssoConfig.Enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "SSO is disabled"})
		return
	}

	// 1. 获取 Authorization Code
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing authorization code"})
		return
	}

	// 2. 交换 Token
	// 构建回调 URL
	callbackURL := fmt.Sprintf("%s/api/v1/auth/sso/callback", c.Request.Host)
	if c.Request.TLS == nil {
		callbackURL = "http://" + callbackURL
	} else {
		callbackURL = "https://" + callbackURL
	}

	// 用 code 换取 token
	tokenResp, err := h.exchangeCodeForToken(code, callbackURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "exchange token failed: " + err.Error()})
		return
	}

	// 解析 id_token 获取用户信息
	email, err := h.parseIDToken(tokenResp.IDToken)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse id_token failed: " + err.Error()})
		return
	}

	// 查找本地用户
	user, err := h.store.GetByEmail(email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	if user == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "user not provisioned, please contact admin"})
		return
	}

	// 更新最后登录时间
	h.store.UpdateLastLogin(user.ID)

	// 签发本地 JWT
	token, err := h.jwtManager.Generate(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "generate token failed"})
		return
	}

	// 返回 token（前端存储到 localStorage）
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"token": token,
			"user":  user.ToResponse(),
		},
	})
}

// TokenResponse OAuth token 响应
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token"`
}

// exchangeCodeForToken 用 code 换取 token
func (h *Handler) exchangeCodeForToken(code, redirectURI string) (*TokenResponse, error) {
	ssoConfig := h.cm.GetConfig().SSO
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", ssoConfig.ClientID)
	data.Set("client_secret", ssoConfig.ClientSecret)
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)

	resp, err := http.PostForm(ssoConfig.GetTokenURL(), data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	return &tokenResp, nil
}

// parseIDToken 解析 id_token 获取 email
// 简化版：只解析 payload，不验证签名（企业内部信任 IdP）
func (h *Handler) parseIDToken(idToken string) (string, error) {
	// JWT 格式: header.payload.signature
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid id_token format")
	}

	// Base64 解码 payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}

	ssoConfig := h.cm.GetConfig().SSO
	emailClaim := ssoConfig.EmailClaim
	if emailClaim == "" {
		emailClaim = "email"
	}

	email, ok := claims[emailClaim].(string)
	if !ok || email == "" {
		return "", fmt.Errorf("email not found in id_token")
	}

	return email, nil
}

func (h *Handler) ChangePassword(c *gin.Context) {
	user := middleware.GetCurrentFullUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 验证旧密码
	if !auth.CheckPassword(req.OldPassword, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "old password is incorrect"})
		return
	}

	// 新密码不能和旧密码相同
	if auth.CheckPassword(req.NewPassword, user.PasswordHash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new password cannot be the same as old password"})
		return
	}

	// 生成新密码哈希
	newPasswordHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 更新密码
	if err := h.store.UpdatePassword(user.ID, newPasswordHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "password changed successfully"})
}

func (h *Handler) GetSystemConfig(c *gin.Context) {
	cfg := h.cm.GetConfig()
	c.JSON(http.StatusOK, gin.H{
		"data": RefinedSystemConfigJSON{
			Server: RefinedServerConfigJSON{
				ReadTimeout:  cfg.Server.ReadTimeout.String(),
				WriteTimeout: cfg.Server.WriteTimeout.String(),
				IdleTimeout:  cfg.Server.IdleTimeout.String(),
			},
			Frontend:     cfg.Frontend,
			ClientFilter: cfg.ClientFilter,
		},
	})
}

func (h *Handler) UpdateSystemConfig(c *gin.Context) {
	var req RefinedSystemConfigJSON
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	readTimeout, err := time.ParseDuration(req.Server.ReadTimeout)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid read_timeout: " + err.Error()})
		return
	}

	writeTimeout, err := time.ParseDuration(req.Server.WriteTimeout)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid write_timeout: " + err.Error()})
		return
	}

	idleTimeout, err := time.ParseDuration(req.Server.IdleTimeout)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid idle_timeout: " + err.Error()})
		return
	}

	if err := h.cm.UpdateSystemSettings(readTimeout, writeTimeout, idleTimeout, req.Frontend, req.ClientFilter); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save configuration: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": req})
}

// sanitizeFilename 清理文件名，移除非法字符
var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9\-_.]`)

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = unsafeChars.ReplaceAllString(name, "_")
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" {
		name = "unknown"
	}
	return name
}

// ExportAccessLogsByToken 导出日志按 API Token 分组，打包为 zip 文件
func (h *Handler) ExportAccessLogsByToken(c *gin.Context) {
	logs := h.usageService.GetAllRecentAccess(0) // 0 = no limit

	// 以 APIKey 前缀（有则用前缀，无则用 ID，否则归 unknown_token）为 key 分组
	groups := make(map[string][]usage.AccessLog)
	for _, log := range logs {
		var key string
		if log.APIKeyPrefix != "" {
			key = sanitizeFilename(log.APIKeyPrefix)
		} else if log.APIKeyName != "" {
			key = sanitizeFilename(log.APIKeyName)
		} else if log.APIKeyID != uuid.Nil {
			key = sanitizeFilename(log.APIKeyID.String())
		} else {
			key = "unknown_token"
		}
		groups[key] = append(groups[key], log)
	}

	dateStr := time.Now().Format("20060102")
	filename := fmt.Sprintf("token-logs-%s.zip", dateStr)

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	zw := zip.NewWriter(c.Writer)
	defer zw.Close()

	csvHeader := []string{
		"time", "user_id", "api_key_prefix", "api_key_name",
		"model", "method", "path", "client_ip",
		"input_tokens", "output_tokens", "total_tokens",
		"status_code", "duration_ms",
		"request_bytes", "response_bytes",
	}

	for tokenKey, tokenLogs := range groups {
		fw, err := zw.Create(tokenKey + ".csv")
		if err != nil {
			continue
		}

		var buf bytes.Buffer
		cw := csv.NewWriter(&buf)

		_ = cw.Write(csvHeader)

		for _, l := range tokenLogs {
			totalTokens := l.InputTokens + l.OutputTokens
			record := []string{
				l.Timestamp.Format(time.RFC3339),
				l.UserID.String(),
				l.APIKeyPrefix,
				l.APIKeyName,
				l.ModelName,
				l.Method,
				l.Path,
				l.ClientIP,
				strconv.Itoa(l.InputTokens),
				strconv.Itoa(l.OutputTokens),
				strconv.Itoa(totalTokens),
				strconv.Itoa(l.StatusCode),
				strconv.FormatInt(l.DurationMs, 10),
				strconv.FormatInt(l.RequestBytes, 10),
				strconv.FormatInt(l.ResponseBytes, 10),
			}
			_ = cw.Write(record)
		}
		cw.Flush()

		_, _ = fw.Write(buf.Bytes())
	}
}
