package handler

import (
	"context"
	"time"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/bytedance/gopkg/util/logger"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
	"github.com/kouleen/gateway/internal/config"
	"github.com/kouleen/gateway/internal/generic"
)

// CustomRouteHandler 自定义路由统一转发入口
func CustomRouteHandler(ctx context.Context, c *app.RequestContext) {
	newUUID, _ := uuid.NewUUID()

	// 匹配路由目标
	target, ok := config.MatchRoute(string(c.Path()))
	if !ok {
		c.JSON(consts.StatusNotFound, map[string]interface{}{
			"sign":    time.Now().UnixMilli(),
			"code":    consts.StatusNotFound,
			"message": "route not found",
			"traceId": newUUID.String(),
		})
		return
	}

	// 获取客户端
	cli, ok := generic.GetClient(target.ServiceKey)
	if !ok {
		c.JSON(consts.StatusNotFound, map[string]interface{}{
			"sign":    time.Now().UnixMilli(),
			"code":    consts.StatusNotFound,
			"message": "service not found",
			"traceId": newUUID.String(),
		})
		return
	}

	// 组装请求参数
	reqBody := make(map[string]interface{})

	// GET请求合并Query参数
	c.QueryArgs().VisitAll(func(key, value []byte) {
		reqBody[string(key)] = string(value)
	})

	// 非GET请求解析JSON Body
	if string(c.Method()) != "GET" {
		if err := c.BindJSON(&reqBody); err != nil {
			c.JSON(consts.StatusOK, map[string]interface{}{
				"sign":    time.Now().UnixMilli(),
				"code":    consts.StatusInternalServerError,
				"message": err.Error(),
				"traceId": newUUID.String(),
			})
			return
		}
	}

	// 合并路径参数（Hertz中Params是字段，不是函数）
	for _, param := range c.Params {
		reqBody[param.Key] = param.Value
	}

	if userId, exist := c.Get("userId"); exist {
		// WithValue：单跳透传，只传给直接下游；
		// WithPersistentValue：持续透传，整条调用链都往下传（网关推荐用这个）
		ctx = metainfo.WithPersistentValue(ctx, "x-user-id", userId.(string))

	}
	ctx = metainfo.WithPersistentValue(ctx, "x-trace-id", newUUID.String())

	logger.CtxInfof(ctx, "[%s]-Request Method: [%s] Path: [%s],request: %#v", newUUID.String(), string(c.Method()), string(c.Path()), reqBody)
	// 发起泛化调用
	resp, err := cli.GenericCall(ctx, target.RPCMethod, reqBody)
	logger.CtxInfof(ctx, "[%s]-Response Method: [%s] Path: [%s],response: %#v,err: %+v", newUUID.String(), string(c.Method()), string(c.Path()), resp, err)
	if err != nil {
		c.JSON(consts.StatusOK, map[string]interface{}{
			"sign":    time.Now().UnixMilli(),
			"code":    consts.StatusInternalServerError,
			"message": err.Error(),
			"traceId": newUUID.String(),
		})
		return
	}

	c.JSON(consts.StatusOK, map[string]interface{}{
		"sign":    time.Now().UnixMilli(),
		"code":    consts.StatusOK,
		"message": "success",
		"data":    resp,
		"traceId": newUUID.String(),
	})
}

// DirectCallHandler 直调模式（兼容旧版，调试用）
func DirectCallHandler(ctx context.Context, c *app.RequestContext) {
	serviceKey := c.Param("service")
	methodName := c.Param("method")

	cli, ok := generic.GetClient(serviceKey)
	if !ok {
		c.JSON(consts.StatusNotFound, map[string]interface{}{"code": -1, "message": "service not found"})
		return
	}

	var reqBody map[string]interface{}
	if err := c.BindJSON(&reqBody); err != nil {
		c.JSON(consts.StatusOK, map[string]interface{}{
			"sign":    time.Now().UnixMilli(),
			"code":    consts.StatusInternalServerError,
			"message": err.Error(),
		})
		return
	}

	resp, err := cli.GenericCall(ctx, methodName, reqBody)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, map[string]interface{}{"code": -3, "message": err.Error()})
		return
	}

	c.JSON(consts.StatusOK, map[string]interface{}{"code": consts.StatusOK, "message": "success", "data": resp})
}
