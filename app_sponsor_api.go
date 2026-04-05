package main

import (
	"encoding/hex"

	"go-stock/backend/logger"

	"github.com/duke-git/lancet/v2/cryptor"
	"github.com/duke-git/lancet/v2/strutil"
)

func (a *App) GetSponsorInfo() map[string]any {
	return a.SponsorInfo
}

func (a *App) CheckSponsorCode(sponsorCode string) map[string]any {
	sponsorCode = strutil.Trim(sponsorCode)
	if sponsorCode == "" {
		return map[string]any{"code": 0, "message": "赞助码不能为空,请输入正确的赞助码!"}
	}

	encrypted, err := hex.DecodeString(sponsorCode)
	if err != nil {
		return map[string]any{
			"code": 0,
			"msg":  "赞助码格式错误,请输入正确的赞助码!",
		}
	}
	key, err := hex.DecodeString(BuildKey)
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
		return map[string]any{
			"code": 0,
			"msg":  "版本错误，不支持赞助码!",
		}
	}
	decrypt := cryptor.AesEcbDecrypt(encrypted, key)
	if decrypt == nil || len(decrypt) == 0 {
		return map[string]any{
			"code": 0,
			"msg":  "赞助码错误，请输入正确的赞助码!",
		}
	}
	return map[string]any{
		"code": 1,
		"msg":  "赞助码校验成功，感谢您的支持!",
	}
}
