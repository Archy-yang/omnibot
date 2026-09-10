package feishu

import (
	"context"
	"strings"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"
)

// 飞书记忆命令测试(高级记忆系统PRD AC4.3):飞书用户可在对话中管理记忆,与微信命令对齐。

type fakeMemoryCmdService struct {
	memories []*memorydomain.Memory
	cleared  bool
	deleted  []int64
	remember string
}

func (f *fakeMemoryCmdService) Remember(_ context.Context, _ int64, content string) (*memorydomain.Memory, error) {
	f.remember = content
	m := memorydomain.NewMemory(42, content)
	f.memories = append(f.memories, m)
	return m, nil
}

func (f *fakeMemoryCmdService) List(_ context.Context, _ int64) ([]*memorydomain.Memory, error) {
	return f.memories, nil
}

func (f *fakeMemoryCmdService) Clear(_ context.Context, _ int64) error {
	f.cleared = true
	f.memories = nil
	return nil
}

func (f *fakeMemoryCmdService) Delete(_ context.Context, _ int64, memoryID int64) (bool, error) {
	f.deleted = append(f.deleted, memoryID)
	return true, nil
}

func memoryCmdHandler(t *testing.T, svc *fakeMemoryCmdService) *MessageHandler {
	t.Helper()
	h := newHandler(nil, nil, nil, nil, &mockSender{})
	h.SetMemoryService(svc)
	return h
}

// TestFeishuMemoryCommand_List 含来源标记与记录时间(PRD AC4.3)。
func TestFeishuMemoryCommand_List(t *testing.T) {
	auto := memorydomain.NewAutoMemory(42, "用户是后端工程师", nil)
	manual := memorydomain.NewMemory(42, "用户偏好简洁回复")
	manual.CreatedAt = time.Date(2026, 8, 30, 0, 0, 0, 0, time.Local)
	svc := &fakeMemoryCmdService{memories: []*memorydomain.Memory{auto, manual}}
	h := memoryCmdHandler(t, svc)

	reply, handled := h.handleMemoryCommand(context.Background(), 42, "#我的记忆")
	if !handled {
		t.Fatal("#我的记忆 应被识别为命令")
	}
	for _, want := range []string{"用户是后端工程师", "自动记忆", "用户偏好简洁回复", "2026-08-30"} {
		if !strings.Contains(reply, want) {
			t.Errorf("输出缺 %q:\n%s", want, reply)
		}
	}
}

// TestFeishuMemoryCommand_Empty 空库给出引导。
func TestFeishuMemoryCommand_Empty(t *testing.T) {
	h := memoryCmdHandler(t, &fakeMemoryCmdService{})

	reply, handled := h.handleMemoryCommand(context.Background(), 42, "#我的记忆")
	if !handled || !strings.Contains(reply, "还没有") {
		t.Errorf("空库应给引导, handled=%v reply=%q", handled, reply)
	}
}

// TestFeishuMemoryCommand_DeleteByIndex 按序号删除。
func TestFeishuMemoryCommand_DeleteByIndex(t *testing.T) {
	m := memorydomain.NewMemory(42, "用户偏好简洁回复")
	svc := &fakeMemoryCmdService{memories: []*memorydomain.Memory{m}}
	h := memoryCmdHandler(t, svc)

	reply, handled := h.handleMemoryCommand(context.Background(), 42, "#删除记忆 1")
	if !handled {
		t.Fatal("#删除记忆 1 应被识别")
	}
	if len(svc.deleted) != 1 || svc.deleted[0] != m.ID {
		t.Errorf("应删除序号 1 对应的记忆, deleted=%v", svc.deleted)
	}
	if !strings.Contains(reply, "已删除") {
		t.Errorf("应回复删除成功, got %q", reply)
	}
}

// TestFeishuMemoryCommand_RememberAndClear 与微信对齐的 #记住/#清空记忆。
func TestFeishuMemoryCommand_RememberAndClear(t *testing.T) {
	svc := &fakeMemoryCmdService{}
	h := memoryCmdHandler(t, svc)

	reply, handled := h.handleMemoryCommand(context.Background(), 42, "#记住 用户在学 Go")
	if !handled || !strings.Contains(reply, "已记住") {
		t.Errorf("#记住 应生效, handled=%v reply=%q", handled, reply)
	}
	if svc.remember != "用户在学 Go" {
		t.Errorf("content = %q", svc.remember)
	}

	_, handled = h.handleMemoryCommand(context.Background(), 42, "#清空记忆")
	if !handled || !svc.cleared {
		t.Errorf("#清空记忆 应生效, handled=%v cleared=%v", handled, svc.cleared)
	}
}

// TestFeishuMemoryCommand_NotCommand 普通消息不拦截。
func TestFeishuMemoryCommand_NotCommand(t *testing.T) {
	svc := &fakeMemoryCmdService{}
	h := memoryCmdHandler(t, svc)

	_, handled := h.handleMemoryCommand(context.Background(), 42, "今天天气怎么样")
	if handled {
		t.Error("普通消息不应被记忆命令拦截")
	}
	// 部分类命令格式(带内容但格式不对)不拦截
	_, handled = h.handleMemoryCommand(context.Background(), 42, "#删除记忆")
	if handled {
		t.Error("#删除记忆 无序号不应拦截")
	}
}
