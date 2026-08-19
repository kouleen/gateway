package handler

import (
	"context"
	"log"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/kouleen/gateway/internal/config"
	"github.com/kouleen/gateway/internal/idlmanager"
	"golang.org/x/crypto/bcrypt"
)

// WebhookUpdate IDL仓库更新Webhook（兼容GitHub）
func WebhookUpdate(ctx context.Context, c *app.RequestContext) {
	secret := config.GlobalConfig.WebhookSecret
	if secret != "" {
		// 校验签名
		sign, ok := c.GetQuery("secret")
		if !ok {
			c.JSON(consts.StatusForbidden, map[string]string{"message": "secret not found"})
			return
		}
		reqSecret, err := bcrypt.GenerateFromPassword([]byte(sign), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(consts.StatusInternalServerError, map[string]string{"message": "bcrypt error"})
			return
		}
		if err = bcrypt.CompareHashAndPassword(reqSecret, []byte(secret)); err != nil {
			c.JSON(consts.StatusForbidden, map[string]string{"message": "secret incorrect"})
			return
		}
	}

	// 异步执行更新，不阻塞Webhook请求
	go func() {
		if err := idlmanager.GetManager().TriggerUpdate(); err != nil {
			log.Printf("IDL update failed: %v", err)
		}
	}()

	c.JSON(consts.StatusOK, map[string]string{"message": "update triggered"})
}
