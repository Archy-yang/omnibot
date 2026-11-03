package api

import (
	"context"

	larkauthSvc "omnibot/internal/service/larkauth"
)

// imLarkAuthAdapter 把 larkauth.Service 适配成 IM pipeline 的窄面:
// IM 只需要「发起授权并拿到验证链接/用户码」,不关心 service 的其它能力。
type imLarkAuthAdapter struct {
	svc *larkauthSvc.Service
}

func newIMLarkAuthAdapter(svc *larkauthSvc.Service) imLarkAuthAdapter {
	return imLarkAuthAdapter{svc: svc}
}

// Relogin 用库存凭证重新发起 device flow,返回验证链接与用户码。
func (a imLarkAuthAdapter) Relogin(ctx context.Context, userID int64) (string, string, error) {
	view, err := a.svc.Relogin(ctx, userID)
	if err != nil {
		return "", "", err
	}
	return view.VerifyURL, view.UserCode, nil
}
