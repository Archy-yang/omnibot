package agent

import (
	"context"
	"sync"
)

// ControlSignal 工具执行期控制信号(Phase 6,16-架构迭代路线图 §13)。
// 工具通过 ctx 上报,ReAct 运行时在每个工具执行后消费——不再依赖 prompt
// 要求模型"记得停止"(§13:仅靠 prompt 是不可靠的)。
//
// V1 只有 Suspend 有消费方(消费方:request_input → 运行时终止本轮);
// Stop/Fatal 为预留常量,定义先行,接入后续迭代。
type ControlSignal string

const (
	ControlSuspend ControlSignal = "suspend" // 挂起等输入(request_input):停止剩余工具 + 停止下一轮 ReAct
	ControlStop    ControlSignal = "stop"    // 预留:主动结束本轮
	ControlFatal   ControlSignal = "fatal"   // 预留:不可恢复错误
)

// controlHolder ctx 内的信号容器(粘滞:首个信号优先,不被后续上报覆盖)。
type controlHolder struct {
	mu     sync.Mutex
	signal ControlSignal
}

// WithControlSignal 在 ctx 注入信号容器(ReAct 运行时启动时调一次)。
func WithControlSignal(ctx context.Context) context.Context {
	return context.WithValue(ctx, controlSignalKey{}, &controlHolder{})
}

type controlSignalKey struct{}

// RaiseControlSignal 工具执行体内上报控制信号。未注入 holder(如单测直调工具)时静默忽略。
func RaiseControlSignal(ctx context.Context, sig ControlSignal) {
	h, ok := ctx.Value(controlSignalKey{}).(*controlHolder)
	if !ok || h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.signal == "" { // 粘滞:首个信号胜出
		h.signal = sig
	}
}

// PeekControlSignal 运行时读取当前信号(空串=无信号)。不消费,循环内可重复读。
func PeekControlSignal(ctx context.Context) ControlSignal {
	h, ok := ctx.Value(controlSignalKey{}).(*controlHolder)
	if !ok || h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.signal
}
