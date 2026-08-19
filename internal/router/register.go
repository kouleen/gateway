package router

import (
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/kouleen/gateway/internal/handler"
)

// RegisterRoutes 注册所有路由
func RegisterRoutes(h *server.Hertz) {
	apiGroup := h.Group("/api")
	{
		// Webhook入口
		apiGroup.GET("/webhook/update", handler.WebhookUpdate)

		// 直调模式（调试用）
		//apiGroup.POST("/:service/:method", handler.DirectCallHandler)

		// 自定义路由通配入口（所有自定义路径走这里）
		apiGroup.Any("/*path", handler.CustomRouteHandler)
	}
}
