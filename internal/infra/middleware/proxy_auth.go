package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"modelgate/internal/infra/auth"
	"modelgate/internal/repository"
)

// ErrorResponseBuilder 定义了用于生成错误响应的接口
type ErrorResponseBuilder interface {
	BuildErrorResponse(errType, message string) []byte
}

const ContextKeyProtocol = "protocol"

// ProtocolInjectionMiddleware 注入当前的协议处理器到上下文
func ProtocolInjectionMiddleware(proto ErrorResponseBuilder) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ContextKeyProtocol, proto)
		c.Next()
	}
}

// APIKeyValidator 定义 API Key 验证服务接口，避免循环依赖
type APIKeyValidator interface {
	ValidateKey(plainKey string) (*entity.APIKey, *entity.User, error)
}

const (
	ContextKeyAPIKeyID     = "api_key_id"
	ContextKeyAPIKeyName   = "api_key_name"
	ContextKeyAPIKeyPrefix = "api_key_prefix"
	ContextKeyAuthType     = "auth_type"
	ContextKeyUserID       = "user_id"
	AuthTypeAPIKey         = "api_key"
	AuthTypeJWT            = "jwt"
)

// ProxyAuthMiddleware 为大模型代理路由提供混合认证（支持 API Key 和 JWT）
// 如果发生未授权错误，将调用 proto.BuildErrorResponse 生成协议专属的错误格式
func ProxyAuthMiddleware(
	keyValidator APIKeyValidator,
	jwtManager *auth.JWTManager,
	userStore *entity.UserStore,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		proto := c.MustGet(ContextKeyProtocol).(ErrorResponseBuilder)

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Data(http.StatusUnauthorized, "application/json", proto.BuildErrorResponse("authentication_error", "missing authorization header"))
			c.Abort()
			return
		}

		// 支持 Bearer Token 格式
		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.Data(http.StatusUnauthorized, "application/json", proto.BuildErrorResponse("authentication_error", "invalid authorization header format"))
			c.Abort()
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")

		// 1. 先尝试作为 API Key 验证
		key, user, err := keyValidator.ValidateKey(token)
		if err == nil {
			// API Key 验证成功
			c.Set(ContextKeyAPIKeyID, key.ID)
			c.Set(ContextKeyAPIKeyName, key.Name)
			c.Set(ContextKeyAPIKeyPrefix, key.KeyPrefix)
			c.Set(ContextKeyUserID, key.UserID)
			c.Set(ContextKeyAuthType, AuthTypeAPIKey)
			c.Next()
			return
		}

		// 2. 如果不是有效的 API Key，尝试作为 JWT Token 验证
		claims, err := jwtManager.Validate(token)
		if err != nil {
			if err == auth.ErrExpiredToken {
				c.Data(http.StatusUnauthorized, "application/json", proto.BuildErrorResponse("authentication_error", "token expired"))
				c.Abort()
				return
			}
			c.Data(http.StatusUnauthorized, "application/json", proto.BuildErrorResponse("authentication_error", "invalid authorization"))
			c.Abort()
			return
		}

		// 3. JWT 验证通过后，必须验证用户是否存在于数据库且未禁用
		user, err = userStore.GetByID(claims.UserID)
		if err != nil {
			c.Data(http.StatusInternalServerError, "application/json", proto.BuildErrorResponse("api_error", "failed to verify user"))
			c.Abort()
			return
		}
		if user == nil {
			c.Data(http.StatusUnauthorized, "application/json", proto.BuildErrorResponse("authentication_error", "user not found"))
			c.Abort()
			return
		}
		if !user.Enabled {
			c.Data(http.StatusUnauthorized, "application/json", proto.BuildErrorResponse("authentication_error", "user disabled"))
			c.Abort()
			return
		}

		// 检查是否需要刷新 Token (滑动过期)
		if jwtManager.ShouldRefresh(claims) {
			newToken, err := jwtManager.Generate(user)
			if err == nil {
				c.Header("X-Refresh-Token", newToken)
				c.Header("Access-Control-Expose-Headers", "X-Refresh-Token")
			}
		}

		// JWT 彻底验证成功
		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyAuthType, AuthTypeJWT)
		c.Next()
	}
}
