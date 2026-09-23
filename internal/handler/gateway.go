package handler

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/bytedance/gopkg/util/logger"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/json"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/kitex/client/genericclient"
	"github.com/google/uuid"
	"github.com/hertz-contrib/websocket"
	"github.com/kouleen/gateway/internal/config"
	"github.com/kouleen/gateway/internal/generic"
)

var upgrader = websocket.HertzUpgrader{
	// 生产环境修改CheckOrigin
	CheckOrigin: func(c *app.RequestContext) bool {
		return true
	},
}

// isWebSocketRequest 判断是不是websocket升级请求
func isWebSocketRequest(c *app.RequestContext) bool {
	connHdr := string(c.GetHeader("Connection"))
	upgradeHdr := string(c.GetHeader("Upgrade"))
	return c.IsGet() &&
		strings.EqualFold(connHdr, "Upgrade") &&
		strings.EqualFold(upgradeHdr, "websocket")
}

// CustomRouteHandler 自定义路由统一转发入口
func CustomRouteHandler(ctx context.Context, c *app.RequestContext) {
	newUUID, _ := uuid.NewUUID()
	ctx = metainfo.WithPersistentValue(ctx, "x-trace-id", newUUID.String())
	// 匹配路由目标
	target, ok := config.MatchRoute(string(c.Method()) + ":" + string(c.Path()))
	if !ok {
		c.JSON(consts.StatusOK, map[string]interface{}{
			"sign":    time.Now().UnixMilli(),
			"code":    consts.StatusNotFound,
			"message": "not found",
			"traceId": newUUID.String(),
		})
		return
	}

	// 获取客户端
	cli, ok := generic.GetClient(target.ServiceKey)
	if !ok {
		c.JSON(consts.StatusOK, map[string]interface{}{
			"sign":    time.Now().UnixMilli(),
			"code":    consts.StatusNotFound,
			"message": "service not found",
			"traceId": newUUID.String(),
		})
		return
	}

	if isWebSocketRequest(c) {
		webSocketCallHandle(ctx, c, cli, &target)
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
		if err := c.BindJSON(&reqBody); err != nil && err.Error() != "EOF" {
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
	if er := convReqBody(reqBody); er != nil {
		c.JSON(consts.StatusOK, map[string]interface{}{
			"sign":    time.Now().UnixMilli(),
			"code":    consts.StatusInternalServerError,
			"message": er.Error(),
			"traceId": newUUID.String(),
		})
	}
	// 发起泛化调用
	resp, err := cli.GenericCall(ctx, target.RPCMethod, reqBody)
	convRespBody(resp, err)
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

func convReqBody(reqBody map[string]interface{}) error {
	if reqBody == nil {
		return nil
	}
	// 1. 先展开 params[xxx] 扁平字段
	if err := ExpandBracketKey(reqBody); err != nil {
		return err
	}
	return walkReqNode(reqBody, 0)
}

// 全局最大递归深度，防止恶意JSON栈溢出
const maxWalkDepth = 20

// walkReqNode
// isRoot = true 代表最外层，只有根节点才解析 current / size
func walkReqNode(node any, depth int) error {
	if node == nil {
		return nil
	}

	if depth > maxWalkDepth {
		return fmt.Errorf("json nested too deep, max depth %d", maxWalkDepth)
	}

	switch v := node.(type) {
	case map[string]interface{}:
		// 顶层才转 current、size
		if depth == 0 {
			if err := convertField(v, "current", false); err != nil {
				return err
			}
			if err := convertField(v, "size", false); err != nil {
				return err
			}
		}
		// 所有层级都转换这些字段
		err := convertField(v, "id", false)
		if err != nil {
			return err
		}
		err = convertField(v, "parentId", false)
		if err != nil {
			return err
		}
		err = convertField(v, "roleId", false)
		if err != nil {
			return err
		}
		err = convertField(v, "status", true)
		if err != nil {
			return err
		}
		err = convertField(v, "createdBy", false)
		if err != nil {
			return err
		}
		err = convertField(v, "updatedBy", false)
		if err != nil {
			return err
		}

		// 递归遍历子value（数组/嵌套map）
		for _, val := range v {
			if err = walkReqNode(val, depth+1); err != nil {
				return err
			}
		}

	case []interface{}:
		// ✅ 重点：数组分两种情况
		// 1. 元素是字符串数字：["123","456"] → 直接转 int64
		// 2. 元素是map：[ {id:"1"}, {id:"2"} ] → 递归map
		for idx, item := range v {
			switch it := item.(type) {
			case string, float64:
				// 数组元素本身是数字字符串，比如批量ids数组
				i64, err := toInt64(it)
				if err != nil {
					return fmt.Errorf("array index %d parse failed: %w", idx, err)
				}
				v[idx] = i64
			default:
				// map / 其他，继续往下递归
				if err := walkReqNode(item, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ExpandBracketKey 把 params[beginTime] 扁平key展开成嵌套map
// 输入: {"params[beginTime]":"xxx"}
// 输出: {"params": {"beginTime":"xxx"}}
func ExpandBracketKey(m map[string]interface{}) error {
	temp := make(map[string]interface{})
	var delKeys []string
	for k, val := range m {
		// 匹配 xxx[yyy]
		re := regexp.MustCompile(`^(.+)\[(.+)]$`)
		matches := re.FindStringSubmatch(k)
		if len(matches) != 3 {
			continue
		}
		delKeys = append(delKeys, k)
		rootKey := matches[1]
		subKey := matches[2]

		var rootMap map[string]interface{}
		if exist, ok := temp[rootKey]; ok {
			rootMap, ok = exist.(map[string]interface{})
			if !ok {
				return fmt.Errorf("key %s already exists and not a map", rootKey)
			}
		} else {
			rootMap = make(map[string]interface{})
			temp[rootKey] = rootMap
		}
		rootMap[subKey] = val
	}
	// 删除旧扁平key，合并新嵌套map
	for _, dk := range delKeys {
		delete(m, dk)
	}
	for k, v := range temp {
		m[k] = v
	}
	return nil
}

func convertField(m map[string]interface{}, key string, isInt8 bool) error {
	val, exist := m[key]
	if !exist {
		return nil
	}
	i64, err := toInt64(val)
	if err != nil {
		return fmt.Errorf("field %s parse error: %w", key, err)
	}
	if isInt8 {
		m[key] = int8(i64)
	} else {
		m[key] = i64
	}
	return nil
}

// toInt64 统一转换：float64 / string / int / int64
func toInt64(v any) (int64, error) {
	switch src := v.(type) {
	case float64:
		return int64(src), nil
	case string:
		return strconv.ParseInt(src, 10, 64)
	case int64:
		return src, nil
	case int:
		return int64(src), nil
	default:
		return 0, fmt.Errorf("unsupported type %T, value=%v", v, v)
	}
}

func convRespBody(resp any, err error) {
	if err != nil {
		return
	}
	walkNode(resp)
}

// walkNode 递归遍历节点，把 int64 的 id / createdBy / updatedBy 转字符串
func walkNode(node any) {
	if node == nil {
		return
	}
	switch v := node.(type) {
	case map[string]interface{}:
		// v 已经是map，这里 v 不可能是nil map（类型断言成功时map非nil）
		convertInt64Field(v, "id")
		convertInt64Field(v, "parentId")
		convertInt64Field(v, "userId")
		convertInt64Field(v, "roleId")
		convertInt64Field(v, "createdBy")
		convertInt64Field(v, "updatedBy")

		// 处理分页 records 数组
		if records, ok := v["records"].([]interface{}); ok {
			for _, item := range records {
				walkNode(item)
			}
		}
		// 处理菜单树 children 递归！
		if children, ok := v["children"].([]interface{}); ok {
			for _, child := range children {
				walkNode(child)
			}
		}

		// 处理菜单树 children 递归！
		if menuIds, ok := v["menuIds"].([]interface{}); ok {
			walkNode(menuIds)
		}

	case []interface{}:
		// 数组：逐个判断，如果是int64直接原地转string；否则继续递归
		for i, item := range v {
			if num, ok := item.(int64); ok {
				v[i] = strconv.FormatInt(num, 10)
			} else {
				walkNode(item)
			}
		}
	}
}

// convertInt64Field 安全转换单个字段：存在且是int64就转为string
func convertInt64Field(m map[string]interface{}, key string) {
	val, exist := m[key]
	if !exist {
		return
	}
	num, ok := val.(int64)
	if !ok {
		return
	}
	m[key] = strconv.FormatInt(num, 10)
}

func webSocketCallHandle(ctx context.Context, c *app.RequestContext, cli genericclient.Client, target *config.RouteTarget) {
	traceId, _ := metainfo.GetPersistentValue(ctx, "x-trace-id")
	if userId, exist := c.Get("userId"); exist {
		ctx = metainfo.WithPersistentValue(ctx, "x-user-id", userId.(string))
	}
	if err := upgrader.Upgrade(c, func(conn *websocket.Conn) {
		defer func(conn *websocket.Conn) {
			defer func(conn *websocket.Conn) {
				_ = conn.Close()
			}(conn)
			for {
				messageType, message, err := conn.ReadMessage()
				if err != nil {
					logger.CtxErrorf(ctx, "websocket read error: %v", err)
					return
				}

				// 解析ws payload为参数map
				var reqBody map[string]interface{}
				if err = json.Unmarshal(message, &reqBody); err != nil {
					respBytes, _ := json.Marshal(map[string]interface{}{
						"sign":    time.Now().UnixMilli(),
						"code":    consts.StatusBadRequest,
						"message": err.Error(),
						"traceId": traceId,
					})
					_ = conn.WriteMessage(messageType, respBytes)
					continue
				}
				// ws内每条消息都发起泛化调用
				logger.CtxInfof(ctx, "[%s]-WS Method: [%s] Path: [%s],request: %#v", traceId, string(c.Method()), string(c.Path()), reqBody)
				resp, err := cli.GenericCall(ctx, target.RPCMethod, reqBody)
				logger.CtxInfof(ctx, "[%s]-WS Method: [%s] Path: [%s],response: %#v,err: %+v", traceId, string(c.Method()), string(c.Path()), resp, err)
				var out map[string]interface{}
				if err != nil {
					out = map[string]interface{}{
						"sign":    time.Now().UnixMilli(),
						"code":    consts.StatusInternalServerError,
						"message": err.Error(),
						"traceId": traceId,
					}
				} else {
					out = map[string]interface{}{
						"sign":    time.Now().UnixMilli(),
						"code":    consts.StatusOK,
						"message": "success",
						"data":    resp,
						"traceId": traceId,
					}
				}
				outBytes, _ := json.Marshal(out)
				if err = conn.WriteMessage(messageType, outBytes); err != nil {
					logger.CtxErrorf(ctx, "websocket write error: %v", err)
					return
				}
			}
		}(conn)
	}); err != nil {
		c.JSON(consts.StatusOK, map[string]interface{}{
			"sign":    time.Now().UnixMilli(),
			"code":    consts.StatusBadRequest,
			"message": err.Error(),
			"traceId": traceId,
		})
		return
	}

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
