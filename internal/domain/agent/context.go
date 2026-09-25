package agent

import "context"

// turnIDContextKey 承载当前逻辑 Turn 的 ctx key。
// 放 domain/agent(无依赖的中立包)而非 service/agent:service/chat 的消息服务
// 也要从 ctx 读 TurnID 给消息落库,避免 service 间循环依赖。
type turnIDContextKey struct{}

// WithTurnID 把当前逻辑 Turn 注入 ctx(handler 在 SaveUserMessage 返回 TurnID 后注入,
// 供 assistant 回复落库与 delegate 捕获 origin_turn_id)。
func WithTurnID(ctx context.Context, turnID int64) context.Context {
	return context.WithValue(ctx, turnIDContextKey{}, turnID)
}

// TurnIDFromContext 从 ctx 取 TurnID,无则返回 0(无 Turn 上下文,如存量路径/系统调用)。
func TurnIDFromContext(ctx context.Context) int64 {
	if id, ok := ctx.Value(turnIDContextKey{}).(int64); ok {
		return id
	}
	return 0
}
