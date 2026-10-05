package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/aomtest/komari-slim-server/database/accounts"
	"github.com/aomtest/komari-slim-server/database/clients"
	"github.com/aomtest/komari-slim-server/internal/config"
	"github.com/aomtest/komari-slim-server/pkg/rpc"
	"gorm.io/gorm"
)

const (
	RoleAdmin  = "admin"
	RoleClient = "client"
	RoleGuest  = "guest"
)

// maxTokenScanBytes 限制 extractClientToken 为寻找 body token 而读取的字节数。
// 该函数对匿名请求也会执行,此前无上限读取请求体,攻击者可用超大 POST 打爆内存。
// 正常携带 token 的请求体只有几十字节,64 KiB 留有充分余量。
const maxTokenScanBytes = 64 << 10

// IdentityMiddleware 统一身份识别中间件，在路由栈最外层运行。
// 负责识别当前请求者身份（Admin / Client / Guest），并写入 Context。
// 身份识别统一委托给 IdentifyPrincipal;同时保留旧的 c.Set 键(role/uuid/
// api_key/session/client_uuid)以兼容现有 handler 与中间件。
func IdentityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := IdentifyPrincipal(c)
		SetPrincipal(c, p)

		// 写入兼容字段。
		c.Set("role", p.PrimaryRole())
		switch p.Type {
		case rpc.PrincipalAPIKey:
			// 旧逻辑:API Key 时记录裸 key 与固定占位 uuid。
			apiKey := c.GetHeader("Authorization")
			c.Set("api_key", apiKey[len("Bearer "):])
			c.Set("uuid", "00000000-0000-0000-0000-000000000000")
		case rpc.PrincipalUser:
			if session, err := c.Cookie("session_token"); err == nil && session != "" {
				c.Set("session", session)
				accounts.UpdateLatest(session, c.Request.UserAgent(), c.ClientIP())
			}
			c.Set("uuid", p.UserUUID)
		case rpc.PrincipalAgent:
			c.Set("client_uuid", p.ClientUUID)
		}

		c.Next()
	}
}

// RequireRole 声明式权限校验中间件，仅允许指定角色通过。
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		current := GetRole(c)
		for _, role := range allowedRoles {
			if current == role {
				c.Next()
				return
			}
		}
		RespondError(c, http.StatusUnauthorized, "Unauthorized.")
		c.Abort()
	}
}

// GetRole 获取当前请求的角色
func GetRole(c *gin.Context) string {
	role, exists := c.Get("role")
	if !exists {
		return RoleGuest
	}
	if s, ok := role.(string); ok {
		return s
	}
	return RoleGuest
}

// --- 私有站点访问控制 ---

var publicPaths = []string{
	"/ping",
	"/api/public",
	"/api/login",
	"/api/me",
	"/api/version",
	"/api/recent",
	"/api/admin",    // 由 RequireRole 处理
	"/api/clients/", // 由 RequireRole 处理
	"/api/preview/", // 预览令牌校验后放行
}

// PrivateSiteMiddleware 私有站点访问控制。
// 依赖 IdentityMiddleware 已设置的 role，对未认证的访客在私有站点模式下进行拦截。
func PrivateSiteMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 已认证用户直接放行
		if GetRole(c) != RoleGuest {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// 公开路径直接放行
		for _, p := range publicPaths {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}

		// 非 API 路径直接放行（静态资源等）
		if !strings.HasPrefix(path, "/api") {
			c.Next()
			return
		}

		// 非私有站点直接放行
		privateSite, err := config.GetAs[bool](config.PrivateSiteKey, false)
		if err != nil {
			RespondError(c, http.StatusInternalServerError, "Failed to get configuration.")
			c.Abort()
			return
		}
		if !privateSite {
			c.Next()
			return
		}

		// 临时访问许可
		if hasTempAccess(c) {
			c.Next()
			return
		}

		RespondError(c, http.StatusUnauthorized, "Private site is enabled, please login first.")
		c.Abort()
	}
}

func hasTempAccess(c *gin.Context) bool {
	tempKey, err := c.Cookie("temp_key")
	if err != nil {
		return false
	}
	expireAt, err := config.GetAs[int64]("tempory_share_token_expire_at", 0)
	if err != nil {
		return false
	}
	allowKey, err := config.GetAs[string]("tempory_share_token", "")
	if err != nil {
		return false
	}
	if allowKey == "" || tempKey != allowKey {
		return false
	}
	return expireAt >= time.Now().Unix()
}

func extractClientToken(c *gin.Context) string {
	token := c.Query("token")
	if token != "" {
		return token
	}
	// rpc2 约定:agent 经 ?Authorization=<token> 传入 client token。
	if token := c.Query("Authorization"); token != "" {
		return token
	}

	if c.Request.Method != http.MethodGet {
		// 只读前 maxTokenScanBytes+1 字节:多读 1 字节用于判断是否超限。
		bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, maxTokenScanBytes+1))
		if err != nil {
			return ""
		}
		// 把已消费的字节与剩余部分拼回去,保证下游 handler 仍能读到完整请求体。
		// 这里必须用 MultiReader 而不是直接用 bodyBytes,否则大请求体会被截断。
		c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(bodyBytes), c.Request.Body))

		// 请求体超过扫描上限:不做 body token 识别直接放弃。
		// 正常携带 token 的请求远小于该上限,因此不影响任何合法客户端。
		if len(bodyBytes) > maxTokenScanBytes {
			return ""
		}

		var bodyMap map[string]interface{}
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &bodyMap); err == nil {
				if tokenVal, exists := bodyMap["token"]; exists {
					if str, ok := tokenVal.(string); ok && str != "" {
						return str
					}
				}
			}
		}
	}

	return ""
}

func checkTokenAndGetUUID(token string) (string, error) {
	uuid, err := clients.GetClientUUIDByToken(token)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return uuid, nil
}

func isApiKeyValid(apiKey string) bool {
	apiKeyConfig, err := config.GetAs[string](config.ApiKeyKey, "")
	if err != nil {
		return false
	}

	if apiKeyConfig == "" || len(apiKeyConfig) < 12 {
		return false
	}
	return apiKey == "Bearer "+apiKeyConfig
}
