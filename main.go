package main

import (
	"time"

	"github.com/cloudwego/hertz/pkg/app/middlewares/server/recovery"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/hertz-contrib/cors"
	"github.com/hertz-contrib/logger/accesslog"
	"github.com/kouleen/gateway/internal/config"
	"github.com/kouleen/gateway/internal/idlmanager"
	middleware2 "github.com/kouleen/gateway/internal/middleware"
	"github.com/kouleen/gateway/internal/router"
)

func main() {
	// 1. 加载配置
	cfg := config.LoadConfig()

	// 2. 初始化Redis
	middleware2.InitRedis(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)

	// 3. 初始化IDL管理器（克隆仓库+加载客户端+启动热更新能力）
	idlmanager.InitManager(cfg)

	// 4. 创建Hertz实例
	h := server.Default(
		server.WithHostPorts(cfg.ListenAddr),
		server.WithReadTimeout(30*time.Second),
	)

	// 5. 注册全局中间件（顺序很重要：异常捕获 → 访问日志 → 跨域 → 鉴权 → 业务）
	h.Use(accesslog.New())      // 访问日志
	h.Use(recovery.Recovery())  // 异常恢复
	h.Use(cors.New(cors.Config{ // 跨域
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:    []string{"Content-Type", "Authorization"},
		MaxAge:          12 * time.Hour,
	}))

	h.Use(middleware2.AuthMiddleware()) // 鉴权中间件

	// 6. 注册路由
	router.RegisterRoutes(h)

	// 7. 启动网关
	h.Spin()
}
