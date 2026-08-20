package config

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// Config 网关全局配置
type Config struct {
	ListenAddr    string   // 网关监听地址
	EtcdEndpoints []string // etcd地址列表
	IDLRepoURL    string   // IDL仓库Git地址
	IDLRepoBranch string   // IDL仓库分支
	IDLLocalPath  string   // IDL本地存储路径
	WebhookSecret string   // Webhook签名密钥
	RedisAddr     string   // Redis地址
	RedisPassword string   // Redis密码
	RedisDB       int      // Redis库号
}

var GlobalConfig *Config

// LoadConfig 从环境变量加载配置
func LoadConfig() *Config {
	GlobalConfig = &Config{
		ListenAddr:    getEnv("LISTEN_ADDR", ":8888"),
		EtcdEndpoints: strings.Split(getEnv("ETCD_ENDPOINTS", "127.0.0.1:2379"), ","),
		IDLRepoURL:    getEnv("IDL_REPO_URL", ""),
		IDLRepoBranch: getEnv("IDL_REPO_BRANCH", ""),
		IDLLocalPath:  getEnv("IDL_LOCAL_PATH", "/opt/idl-repo"),
		WebhookSecret: getEnv("WEBHOOK_SECRET", ""),
		RedisAddr:     getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),
	}
	return GlobalConfig
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if key == "" {
		return defaultValue
	}
	if v := os.Getenv(key); v != "" {
		if iv, err := strconv.Atoi(v); err == nil {
			return iv
		}
	}
	return defaultValue
}

// RouteTarget 路由目标
type RouteTarget struct {
	ServiceKey string // 服务标识
	RPCMethod  string // RPC方法名
}

var (
	routeTable = make(map[string]RouteTarget)
	routeMu    sync.RWMutex
)

// ReplaceRouteTable 原子替换路由表（热更新专用）
func ReplaceRouteTable(newTable map[string]RouteTarget) {
	routeMu.Lock()
	defer routeMu.Unlock()
	routeTable = newTable
}

// MatchRoute 匹配路由目标
func MatchRoute(fullPath string) (RouteTarget, bool) {
	routeMu.RLock()
	defer routeMu.RUnlock()
	target, ok := routeTable[fullPath]
	return target, ok
}

// RouteRule 单条路由规则
type RouteRule struct {
	Path       string `yaml:"path"`
	Method     string `yaml:"method"`
	HTTPMethod string `yaml:"http_method"`
}

// ServerConfig 单个服务配置
type ServerConfig struct {
	ServiceName string      `yaml:"service_name"`
	IDLPath     string      `yaml:"idl_path"`
	Routes      []RouteRule `yaml:"routes"`
}

// ServersConfig 服务配置根
type ServersConfig struct {
	Servers map[string]ServerConfig `yaml:"servers"`
}

type WhitePathConfig struct {
	WhitePaths []string `yaml:"white_path"`
}
