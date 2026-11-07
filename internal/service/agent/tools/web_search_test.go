package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/pkg/toolcore"
	searchsvc "omnibot/internal/service/search"
)

// N6 工具层:参数校验、用户身份、错误→可行动文案。

type fakeSearcher struct {
	gotUserID int64
	gotQuery  string
	gotCount  int
	out       string
	err       error
}

func (f *fakeSearcher) Search(_ context.Context, userID int64, query string, count int) (string, error) {
	f.gotUserID = userID
	f.gotQuery = query
	f.gotCount = count
	return f.out, f.err
}

func ctxWithUser(id int64) context.Context {
	return toolcore.WithUserID(context.Background(), id)
}

func TestWebSearchTool_Metadata(t *testing.T) {
	tool := CreateWebSearchTool(&fakeSearcher{})

	assert.Equal(t, "web_search", tool.Name)
	assert.Contains(t, tool.Description, "web_read", "描述里必须教会与 web_read 的分工")
	assert.Contains(t, tool.Capabilities, toolcore.CapResearch)
	assert.Contains(t, tool.Parameters["required"], "query")
}

func TestWebSearchTool_Success(t *testing.T) {
	fake := &fakeSearcher{out: "搜索「x」· 1 条结果"}
	tool := CreateWebSearchTool(fake)

	out, err := tool.Execute(ctxWithUser(7), map[string]interface{}{"query": "  golang 泛型  ", "count": float64(3)})
	require.NoError(t, err)
	assert.Equal(t, "搜索「x」· 1 条结果", out)
	assert.Equal(t, int64(7), fake.gotUserID)
	assert.Equal(t, "golang 泛型", fake.gotQuery, "query 应 trim")
	assert.Equal(t, 3, fake.gotCount)
}

func TestWebSearchTool_EmptyQueryFails(t *testing.T) {
	tool := CreateWebSearchTool(&fakeSearcher{})

	_, err := tool.Execute(ctxWithUser(1), map[string]interface{}{"query": "   "})
	require.Error(t, err)
}

// 未配置不是错误:返回可行动文案,让模型改用 web_read,不触发熔断计数。
func TestWebSearchTool_NotConfigured(t *testing.T) {
	tool := CreateWebSearchTool(&fakeSearcher{err: searchsvc.ErrNotConfigured})

	out, err := tool.Execute(ctxWithUser(1), map[string]interface{}{"query": "q"})
	require.NoError(t, err)
	assert.Contains(t, out, "还没配置")
	assert.Contains(t, out, "web_read")
}

func TestWebSearchTool_NoUserInContext(t *testing.T) {
	fake := &fakeSearcher{out: "should not be called"}
	tool := CreateWebSearchTool(fake)

	out, err := tool.Execute(context.Background(), map[string]interface{}{"query": "q"})
	require.NoError(t, err)
	assert.Contains(t, out, "还没配置")
	assert.Equal(t, int64(0), fake.gotUserID, "无用户身份不应发起检索")
}

func TestWebSearchTool_ProviderErrorsBecomeActionableText(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"unauthorized", &searchsvc.StatusError{Code: 401}, "密钥"},
		{"payment", &searchsvc.StatusError{Code: 402}, "余额"},
		{"forbidden", &searchsvc.StatusError{Code: 403}, "拒绝"},
		{"rate limited", &searchsvc.StatusError{Code: 429}, "限流"},
		{"server error", &searchsvc.StatusError{Code: 500}, "暂时不可用"},
		{"timeout", context.DeadlineExceeded, "超时"},
		{"unknown", errors.New("boom"), "暂时不可用"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := CreateWebSearchTool(&fakeSearcher{err: tc.err})
			out, err := tool.Execute(ctxWithUser(1), map[string]interface{}{"query": "q"})
			require.NoError(t, err, "基础设施错误不抛给模型")
			assert.Contains(t, out, tc.want)
			assert.NotContains(t, out, "boom", "内部错误细节不外泄")
		})
	}
}

func TestWebSearchTool_InvalidQuerySurfacesToModel(t *testing.T) {
	tool := CreateWebSearchTool(&fakeSearcher{err: searchsvc.ErrInvalidQuery})

	_, err := tool.Execute(ctxWithUser(1), map[string]interface{}{"query": "q"})
	require.Error(t, err, "参数错误要让模型看见并自我纠正")
}
