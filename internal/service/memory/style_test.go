package memory

import (
	"context"
	"testing"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 24 号 M1:风格与认知证据层——沉淀管线顺带抽取,常态空数组,单轮上限 3 条。

// T1 抽取落库:style_notes/preference_changes 带 source ids;未知 dimension 归 tone;上限 3 条截断。
func TestDigest_StyleEvidence(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[],
		"style_notes":[
			{"dimension":"tone","content":"用户偏好直接、少客套","source_message_ids":[1]},
			{"dimension":"weird_dim","content":"未知维度应归 tone","source_message_ids":[2]},
			{"dimension":"length","content":"偏好短句","source_message_ids":[2]},
			{"dimension":"humor","content":"第四条应被截断","source_message_ids":[3]}
		],
		"preference_changes":[
			{"content":"咖啡从美式改为燕麦拿铁","source_message_ids":[1,2]},
			{"content":"第二条变化","source_message_ids":[3]},
			{"content":"第三条","source_message_ids":[3]},
			{"content":"第四条应被截断","source_message_ids":[3]}
		]}`

	require.NoError(t, p.RunOnce(context.Background(), 42))

	var obs []memorydomain.StyleObservation
	require.NoError(t, db.Where("user_id = ?", 42).Order("id").Find(&obs).Error)
	require.Len(t, obs, 3, "style_notes 上限 3 条")
	assert.Equal(t, "tone", obs[0].Dimension)
	assert.Equal(t, "tone", obs[1].Dimension, "未知 dimension 归 tone")
	assert.Equal(t, "length", obs[2].Dimension)
	assert.Equal(t, []int64{1}, obs[0].SourceMessageIDs)

	var changes []memorydomain.CognitionChange
	require.NoError(t, db.Where("user_id = ?", 42).Order("id").Find(&changes).Error)
	require.Len(t, changes, 3, "preference_changes 上限 3 条")
	assert.Equal(t, "咖啡从美式改为燕麦拿铁", changes[0].Content)
	assert.Equal(t, []int64{1, 2}, changes[0].SourceMessageIDs)
}

// T2 常态空数组:零写入,管线照常完成。
func TestDigest_StyleEvidenceEmpty(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[],"style_notes":[],"preference_changes":[]}`

	require.NoError(t, p.RunOnce(context.Background(), 42))

	var obs []memorydomain.StyleObservation
	require.NoError(t, db.Find(&obs).Error)
	assert.Empty(t, obs)
}

// T3 画像查询与删除(归属隔离)。
func TestStyleProfile_ListAndDelete(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[],
		"style_notes":[{"dimension":"tone","content":"直接","source_message_ids":[1]}],
		"preference_changes":[{"content":"从 X 改为 Y","source_message_ids":[2]}]}`
	require.NoError(t, p.RunOnce(context.Background(), 42))

	svc := NewMemoryService(
		memoryrepo.NewMemoryRepository(db),
		memoryrepo.NewMatterRepository(db), nil, nil,
	).(*memoryService)

	view, err := svc.StyleProfile(42, 50)
	require.NoError(t, err)
	require.Len(t, view.Observations, 1)
	require.Len(t, view.Changes, 1)
	assert.Equal(t, "直接", view.Observations[0].Content)

	// 他人删除 → 未命中
	ok, err := svc.DeleteStyleObservation(99, view.Observations[0].ID)
	require.NoError(t, err)
	assert.False(t, ok)

	// 本删除成功
	ok, err = svc.DeleteStyleObservation(42, view.Observations[0].ID)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = svc.DeleteCognitionChange(42, view.Changes[0].ID)
	require.NoError(t, err)
	assert.True(t, ok)

	view, err = svc.StyleProfile(42, 50)
	require.NoError(t, err)
	assert.Empty(t, view.Observations)
	assert.Empty(t, view.Changes)
}
