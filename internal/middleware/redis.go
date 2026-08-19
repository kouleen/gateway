package middleware

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

var redisClient *redis.Client

// InitRedis 初始化Redis连接（全局单例）
func InitRedis(addr, password string, db int) {
	redisClient = redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		PoolSize:     10,
	})

	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("connect redis failed: %v", err)
	}
	log.Println("redis initialized successfully")
}

// GetUserIdByToken 根据Token查询用户ID
// Key约定：auth:token:{token}  value: 用户ID
func GetUserIdByToken(ctx context.Context, token string) (string, error) {
	return redisClient.Get(ctx, "auth:token:"+token).Result()
}
