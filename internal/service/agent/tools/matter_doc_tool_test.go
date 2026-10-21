package tools

// matter_doc_tool 测试(23 号 F2):对话内把档案同步到飞书文档。
// 窄接口 + 自有 sentinel 错误(包纪律:不 import service 实现,wire 适配)。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/pkg/toolcore"
)

type fakeMatterDocSyncer struct {
	result MatterDocSyncResult
	err    error
	userID int64
	matID  int64
}

func (f *fakeMatterDocSyncer) SyncMatter(_ context.Context, userID, matterID int64) (MatterDocSyncResult, error) {
	f.userID = userID
	f.matID = matterID
	return f.result, f.err
}

func matterDocToolContext(t *testing.T) context.Context {
	t.Helper()
	return toolcore.WithUserID(context.Background(), 42)
}

func TestMatterDocSyncTool_Create(t *testing.T) {
	syncer := &fakeMatterDocSyncer{result: MatterDocSyncResult{
		DocURL: "https://feishu.example/doc/x", Created: true, SyncedAt: time.Now(),
	}}
	tool := CreateMatterDocSyncTool(syncer)

	out, err := tool.Execute(matterDocToolContext(t), map[string]interface{}{"matter_id": float64(9)})
	require.NoError(t, err)
	assert.Equal(t, int64(42), syncer.userID, "userID 必须来自 ctx(auth 归属)")
	assert.Equal(t, int64(9), syncer.matID)
	assert.Contains(t, out, "已创建")
	assert.Contains(t, out, "https://feishu.example/doc/x")
}

func TestMatterDocSyncTool_Update(t *testing.T) {
	syncer := &fakeMatterDocSyncer{result: MatterDocSyncResult{
		DocURL: "https://feishu.example/doc/x", Created: false, SyncedAt: time.Now(),
	}}
	tool := CreateMatterDocSyncTool(syncer)

	out, err := tool.Execute(matterDocToolContext(t), map[string]interface{}{"matter_id": float64(9)})
	require.NoError(t, err)
	assert.Contains(t, out, "已更新")
	assert.False(t, strings.Contains(out, "已创建"))
}

func TestMatterDocSyncTool_MissingMatterID(t *testing.T) {
	tool := CreateMatterDocSyncTool(&fakeMatterDocSyncer{})

	_, err := tool.Execute(matterDocToolContext(t), map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matter_id")
}

func TestMatterDocSyncTool_NotFound(t *testing.T) {
	tool := CreateMatterDocSyncTool(&fakeMatterDocSyncer{err: ErrMatterDocNotFound})

	_, err := tool.Execute(matterDocToolContext(t), map[string]interface{}{"matter_id": float64(999)})
	require.ErrorIs(t, err, ErrMatterDocNotFound)
	assert.Contains(t, err.Error(), "不存在", "错误信息要模型可读:提示先查档案")
}

func TestMatterDocSyncTool_Unauthorized(t *testing.T) {
	tool := CreateMatterDocSyncTool(&fakeMatterDocSyncer{err: ErrMatterDocUnauthorized})

	_, err := tool.Execute(matterDocToolContext(t), map[string]interface{}{"matter_id": float64(9)})
	require.ErrorIs(t, err, ErrMatterDocUnauthorized)
	assert.Contains(t, err.Error(), "飞书", "错误信息要模型可读:提示先完成授权")
}

func TestMatterDocSyncTool_OtherErrorPassthrough(t *testing.T) {
	tool := CreateMatterDocSyncTool(&fakeMatterDocSyncer{err: errors.New("cli boom")})

	_, err := tool.Execute(matterDocToolContext(t), map[string]interface{}{"matter_id": float64(9)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cli boom")
}
