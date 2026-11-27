package api

import (
	"context"

	"omnibot/internal/channel/im"
	agenttools "omnibot/internal/service/agent/tools"
	larkauthSvc "omnibot/internal/service/larkauth"
)

// larkAuthEnsurer 授权服务在本文件所需的最小能力(接口最小化,便于单测)。
// *larkauth.Service 满足它。
type larkAuthEnsurer interface {
	EnsureAuth(ctx context.Context, userID int64, channel, target string) (larkauthSvc.View, error)
}

// imLarkAuthAdapter 把 larkauth.Service 适配成 IM pipeline 的窄面:
// IM 只需要「幂等地查/发起授权」,不关心 service 的其它能力。
type imLarkAuthAdapter struct {
	svc larkAuthEnsurer
}

func newIMLarkAuthAdapter(svc larkAuthEnsurer) imLarkAuthAdapter {
	return imLarkAuthAdapter{svc: svc}
}

// EnsureAuth 幂等发起/复用 device flow。channel/target 记到授权行上,
// 收尾成功后由后台 sweeper 推回同一会话。
func (a imLarkAuthAdapter) EnsureAuth(ctx context.Context, userID int64, channel, target string) (im.LarkAuthPrompt, error) {
	view, err := a.svc.EnsureAuth(ctx, userID, channel, target)
	if err != nil {
		return im.LarkAuthPrompt{}, err
	}
	return im.LarkAuthPrompt{
		Status:    view.Status,
		UserName:  view.UserName,
		VerifyURL: view.VerifyURL,
		UserCode:  view.UserCode,
		ExpiresIn: view.ExpiresIn,
	}, nil
}

// 编译期断言:适配器必须满足 IM 的窄面。
var _ im.LarkAuthCommander = imLarkAuthAdapter{}

// toolsLarkAuthAdapter 把 larkauth.Service 适配成 feishu 工具的 AuthEnsurer:
// 工具层只看 status/verify_url,不依赖授权服务的内部结构。
type toolsLarkAuthAdapter struct {
	svc larkAuthEnsurer
}

func newToolsLarkAuthAdapter(svc larkAuthEnsurer) toolsLarkAuthAdapter {
	return toolsLarkAuthAdapter{svc: svc}
}

func (a toolsLarkAuthAdapter) EnsureAuthPrompt(ctx context.Context, userID int64, channel, target string) (agenttools.AuthPrompt, error) {
	view, err := a.svc.EnsureAuth(ctx, userID, channel, target)
	if err != nil {
		return agenttools.AuthPrompt{}, err
	}
	return agenttools.AuthPrompt{
		Status:    view.Status,
		VerifyURL: view.VerifyURL,
		UserName:  view.UserName,
	}, nil
}

// 编译期断言:必须满足工具层的窄面。
var _ agenttools.AuthEnsurer = toolsLarkAuthAdapter{}
