package generic

import (
	"sync"

	"github.com/cloudwego/kitex/client/genericclient"
)

var clientPool *sync.Map

// InitPool 初始化客户端池
func InitPool() {
	clientPool = &sync.Map{}
}

// ReplacePool 原子替换整个客户端池（热更新专用）
func ReplacePool(newPool *sync.Map) {
	clientPool = newPool
}

// GetClient 获取指定服务的泛化客户端
func GetClient(serviceKey string) (genericclient.Client, bool) {
	if clientPool == nil {
		return nil, false
	}
	val, ok := clientPool.Load(serviceKey)
	if !ok {
		return nil, false
	}
	return val.(genericclient.Client), true
}
