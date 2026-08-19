package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/kouleen/gateway/internal/config"
	"github.com/kouleen/gateway/internal/idlmanager"
)

// WebhookIDLUpdate IDL仓库更新Webhook（兼容GitHub）
func WebhookIDLUpdate(ctx context.Context, c *app.RequestContext) {
	secret := config.GlobalConfig.WebhookSecret
	if secret != "" {
		// 校验签名
		signature := c.GetHeader("X-Hub-Signature-256")
		if signature == nil || string(signature) == "" {
			c.JSON(consts.StatusForbidden, map[string]string{"message": "missing signature"})
			return
		}

		body := c.Request.Body()
		expected := "sha256=" + hmacSha256(body, []byte(secret))
		if !hmac.Equal(signature, []byte(expected)) {
			c.JSON(consts.StatusForbidden, map[string]string{"message": "invalid signature"})
			return
		}
	}

	// 异步执行更新，不阻塞Webhook请求
	go func() {
		if err := idlmanager.GetManager().TriggerUpdate(); err != nil {
			log.Printf("IDL update failed: %v", err)
		}
	}()

	c.JSON(consts.StatusOK, map[string]string{"msg": "update triggered"})
}

func hmacSha256(data, key []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
