package tools

// settle 工具测试(28号 N1):对话内档案收口/loop 收口。
// 窄接口 + 自有 sentinel(包纪律:不 import service 实现,wire 适配)。

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/pkg/toolcore"
)

type fakeSettleManager struct {
	result    MatterSettleResult
	err       error
	cands     []string
	closed    bool
	query     string
	action    string
	userID    int64
	matterID  int64
}

func (f *fakeSettleManager) SettleMatter(_ context.Context, userID, matterID int64, action string) (MatterSettleResult, error) {
	f.userID, f.matterID, f.action = userID, matterID, action
	return f.result, f.err
}

func (f *fakeSettleManager) CloseLoopByQuery(_ context.Context, userID int64, query string) ([]string, bool, error) {
	f.userID, f.query = userID, query
	return f.cands, f.closed, f.err
}

func settleCtx(t *testing.T) context.Context {
	t.Helper()
	return toolcore.WithUserID(context.Background(), 42)
}

func TestManageMatterTool_CloseWithCascade(t *testing.T) {
	m := &fakeSettleManager{result: MatterSettleResult{Title: "父亲的诊治", CascadedLoops: 2}}
	tool := CreateManageMatterTool(m)

	out, err := tool.Execute(settleCtx(t), map[string]interface{}{"matter_id": float64(10), "action": "close"})
	require.NoError(t, err)
	assert.Equal(t, int64(42), m.userID)
	assert.Equal(t, int64(10), m.matterID)
	assert.Equal(t, "close", m.action)
	assert.Contains(t, out, "已完结")
	assert.Contains(t, out, "父亲的诊治")
	assert.Contains(t, out, "2 条待办", "级联数要回显")
	assert.Contains(t, out, "重新打开", "反悔路径要提示")
}

func TestManageMatterTool_ArchiveReopen(t *testing.T) {
	m := &fakeSettleManager{result: MatterSettleResult{Title: "行程"}}
	tool := CreateManageMatterTool(m)

	out, err := tool.Execute(settleCtx(t), map[string]interface{}{"matter_id": float64(6), "action": "archive"})
	require.NoError(t, err)
	assert.Contains(t, out, "已归档")
	assert.Equal(t, "archive", m.action)

	out, err = tool.Execute(settleCtx(t), map[string]interface{}{"matter_id": float64(6), "action": "reopen"})
	require.NoError(t, err)
	assert.Contains(t, out, "已重新打开")
	assert.Equal(t, "reopen", m.action)
}

func TestManageMatterTool_Validation(t *testing.T) {
	tool := CreateManageMatterTool(&fakeSettleManager{})

	// 缺 matter_id
	_, err := tool.Execute(settleCtx(t), map[string]interface{}{"action": "close"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matter_id")

	// 缺 action
	_, err = tool.Execute(settleCtx(t), map[string]interface{}{"matter_id": float64(1)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action")

	// 非法 action
	_, err = tool.Execute(settleCtx(t), map[string]interface{}{"matter_id": float64(1), "action": "delete"})
	require.Error(t, err)

	// 未命中 sentinel 透传
	tool2 := CreateManageMatterTool(&fakeSettleManager{err: ErrMatterLifecycleNotFound})
	_, err = tool2.Execute(settleCtx(t), map[string]interface{}{"matter_id": float64(999), "action": "close"})
	require.ErrorIs(t, err, ErrMatterLifecycleNotFound)
}

func TestCloseLoopTool_ThreeStates(t *testing.T) {
	m := &fakeSettleManager{}
	tool := CreateCloseLoopTool(m)

	// 唯一命中 → closed
	m.closed = true
	out, err := tool.Execute(settleCtx(t), map[string]interface{}{"loop_query": "复诊"})
	require.NoError(t, err)
	assert.Contains(t, out, "已收口")
	assert.Equal(t, "复诊", m.query)

	// 多命中 → 候选列表
	m.closed = false
	m.cands = []string{"查证 AI 冷战回应", "整理 AI 冷战资料"}
	out, err = tool.Execute(settleCtx(t), map[string]interface{}{"loop_query": "AI 冷战"})
	require.NoError(t, err)
	assert.Contains(t, out, "多条")
	assert.Contains(t, out, "查证 AI 冷战回应")

	// 零命中
	m.cands = nil
	out, err = tool.Execute(settleCtx(t), map[string]interface{}{"loop_query": "不存在"})
	require.NoError(t, err)
	assert.Contains(t, out, "没有找到")

	// 空关键词
	_, err = tool.Execute(settleCtx(t), map[string]interface{}{})
	require.Error(t, err)
}

func TestCloseLoopTool_Unauthorized(t *testing.T) {
	tool := CreateCloseLoopTool(&fakeSettleManager{err: ErrMatterLifecycleNotFound})
	_, err := tool.Execute(settleCtx(t), map[string]interface{}{"loop_query": "x"})
	require.ErrorIs(t, err, ErrMatterLifecycleNotFound)
}
