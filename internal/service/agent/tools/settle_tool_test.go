package tools

// settle 工具测试(28号 N1):对话内档案收口/loop 收口。
// 窄接口 + 自有 sentinel(包纪律:不 import service 实现,wire 适配)。

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/pkg/toolcore"
)

type fakeSettleManager struct {
	result   MatterSettleResult
	err      error
	cands    []string
	closed   bool
	query    string
	action   string
	userID   int64
	matterID int64
	target   *time.Time
}

func (f *fakeSettleManager) SettleMatter(_ context.Context, userID, matterID int64, action string) (MatterSettleResult, error) {
	f.userID, f.matterID, f.action = userID, matterID, action
	return f.result, f.err
}

func (f *fakeSettleManager) CloseLoopByQuery(_ context.Context, userID int64, query string) ([]string, bool, error) {
	f.userID, f.query = userID, query
	return f.cands, f.closed, f.err
}

func (f *fakeSettleManager) SetTargetMatter(_ context.Context, userID, matterID int64, target *time.Time) (string, error) {
	f.userID, f.matterID, f.target = userID, matterID, target
	if f.err != nil {
		return "", f.err
	}
	return "完成体检", nil
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

// 28号 N2:set_target 设目标日。
func TestManageMatterTool_SetTarget(t *testing.T) {
	m := &fakeSettleManager{}
	tool := CreateManageMatterTool(m)

	out, err := tool.Execute(settleCtx(t), map[string]interface{}{
		"matter_id": float64(10), "action": "set_target", "target_date": "2026-10-31",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(42), m.userID)
	require.NotNil(t, m.target)
	assert.Equal(t, "2026-10-31", m.target.Format("2006-01-02"))
	assert.Contains(t, out, "完成体检")
	assert.Contains(t, out, "2026-10-31")

	// 非法格式仍报错(留空/清除走 TestManageMatterTool_ClearTarget)
	_, err = tool.Execute(settleCtx(t), map[string]interface{}{
		"matter_id": float64(10), "action": "set_target", "target_date": "月底",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "YYYY-MM-DD")

	// 未命中 sentinel 透传
	m2 := &fakeSettleManager{err: ErrMatterLifecycleNotFound}
	tool2 := CreateManageMatterTool(m2)
	_, err = tool2.Execute(settleCtx(t), map[string]interface{}{
		"matter_id": float64(999), "action": "set_target", "target_date": "2026-10-31",
	})
	require.ErrorIs(t, err, ErrMatterLifecycleNotFound)
}

// 29号 N1(D):set_target 留空或 clear = 清除目标日(恢复普通事项)。
func TestManageMatterTool_ClearTarget(t *testing.T) {
	m := &fakeSettleManager{}
	tool := CreateManageMatterTool(m)

	// target_date 留空 → 清除
	out, err := tool.Execute(settleCtx(t), map[string]interface{}{"matter_id": float64(10), "action": "set_target"})
	require.NoError(t, err)
	assert.Nil(t, m.target, "留空 = 清除(target 必须为 nil)")
	assert.Contains(t, out, "已清除")
	assert.Contains(t, out, "完成体检")

	// target_date="clear" → 同清除
	m.target = nil
	out, err = tool.Execute(settleCtx(t), map[string]interface{}{
		"matter_id": float64(10), "action": "set_target", "target_date": "clear",
	})
	require.NoError(t, err)
	assert.Nil(t, m.target)
	assert.Contains(t, out, "已清除")
}

// 29号 N1(B):create_matter 对话内建档。
type fakeMatterCreator struct {
	userID    int64
	title     string
	stateDesc string
	target    *time.Time

	id      int64
	created bool
	err     error
}

func (f *fakeMatterCreator) CreateMatterByTitle(_ context.Context, userID int64, title, stateDesc string, target *time.Time) (int64, bool, error) {
	f.userID, f.title, f.stateDesc, f.target = userID, title, stateDesc, target
	return f.id, f.created, f.err
}

func TestCreateMatterTool_New(t *testing.T) {
	f := &fakeMatterCreator{id: 7, created: true}
	tool := CreateCreateMatterTool(f)

	out, err := tool.Execute(settleCtx(t), map[string]interface{}{
		"title": "十月体检", "state_desc": "准备约机构", "target_date": "2026-10-31",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(42), f.userID)
	assert.Equal(t, "十月体检", f.title)
	assert.Equal(t, "准备约机构", f.stateDesc)
	require.NotNil(t, f.target)
	assert.Equal(t, "2026-10-31", f.target.Format("2006-01-02"))
	assert.Contains(t, out, "已建档")
	assert.Contains(t, out, "十月体检")
	assert.Contains(t, out, "7", "matter_id 要回显,模型后续 manage_matter 要用")
}

func TestCreateMatterTool_IdempotentFallback(t *testing.T) {
	f := &fakeMatterCreator{id: 7, created: false}
	tool := CreateCreateMatterTool(f)

	out, err := tool.Execute(settleCtx(t), map[string]interface{}{"title": "十月体检"})
	require.NoError(t, err)
	assert.Contains(t, out, "已有档案")
	assert.Contains(t, out, "matter_id=7", "回落文案也要带 matter_id 供后续工具用")
}

func TestCreateMatterTool_Validation(t *testing.T) {
	tool := CreateCreateMatterTool(&fakeMatterCreator{})

	// 缺 title
	_, err := tool.Execute(settleCtx(t), map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "title")

	// target_date 非法
	_, err = tool.Execute(settleCtx(t), map[string]interface{}{"title": "X", "target_date": "月底"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "YYYY-MM-DD")

	// target_date 缺省 → nil(不带目标)
	f := &fakeMatterCreator{id: 1, created: true}
	tool2 := CreateCreateMatterTool(f)
	_, err = tool2.Execute(settleCtx(t), map[string]interface{}{"title": "X"})
	require.NoError(t, err)
	assert.Nil(t, f.target)
}
