package middleware

import (
	"context"
	"strings"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/json"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// 白名单路径：无需鉴权直接放行
//var whitelistPaths = []string{
//	"/webhook/idl-update",
//	"/api/user/login",
//	"/api/user/sms",
//}

var whitelistPaths []string

// AuthMiddleware 全局Redis鉴权中间件
func AuthMiddleware() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		path := c.Path()

		// 1. 白名单路径直接放行
		if isWhitelist(string(path)) {
			c.Next(ctx)
			return
		}

		// 2. 从请求头提取Token

		token := c.GetHeader("Authorization")
		if token == nil || string(token) == "" {
			token = c.GetHeader("X-Token")
		}
		if token == nil || string(token) == "" {
			c.JSON(consts.StatusUnauthorized, map[string]interface{}{
				"code":    consts.StatusUnauthorized,
				"message": "Unauthorized",
			})
			c.Abort() // 终止后续流程
			return
		}
		authorization := string(token)

		// 去除 Bearer 前缀
		if strings.HasPrefix(authorization, "Bearer ") {
			authorization = strings.TrimPrefix(authorization, "Bearer ")
		}

		// 3. Redis校验Token有效性
		userStr, err := GetUserIdByToken(ctx, authorization)
		if err != nil || userStr == "" {
			c.JSON(consts.StatusUnauthorized, map[string]interface{}{
				"code":    consts.StatusUnauthorized,
				"message": "invalid or expired token",
			})
			c.Abort()
			return
		}
		var user map[string]any
		if err = json.Unmarshal([]byte(userStr), &user); err != nil {
			c.JSON(consts.StatusUnauthorized, map[string]interface{}{
				"code":    consts.StatusUnauthorized,
				"message": "invalid or unmarshal token",
			})
		}

		ctx = metainfo.WithPersistentValue(ctx, "x-user-id", user["id"].(string))
		ctx = metainfo.WithPersistentValue(ctx, "x-token", authorization)

		// 5. 放行，执行后续业务逻辑
		c.Next(ctx)
	}
}

// isWhitelist 路径白名单匹配
func isWhitelist(path string) bool {
	for _, wp := range whitelistPaths {
		// 精确匹配
		if path == wp {
			return true
		}
	}
	return false
}

// ReplaceWhitelistPaths 替换路径白名单
func ReplaceWhitelistPaths(paths []string) {
	whitelistPaths = paths
}
