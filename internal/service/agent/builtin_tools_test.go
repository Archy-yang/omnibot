package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMemoryProvider for testing search_memories
type mockMemoryProvider struct {
	memories []string
	err      error
}

func (m *mockMemoryProvider) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.memories, nil
}

func TestBuiltinTools_GetCurrentTime(t *testing.T) {
	tool := CreateGetCurrentTimeTool()
	result, err := tool.Execute(context.Background(), nil)
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestBuiltinTools_Calculator(t *testing.T) {
	tool := CreateCalculatorTool()

	result, err := tool.Execute(context.Background(), map[string]interface{}{"expression": "2 + 3"})
	require.NoError(t, err)
	assert.Equal(t, "5", result)

	result, err = tool.Execute(context.Background(), map[string]interface{}{"expression": "10 * 5"})
	require.NoError(t, err)
	assert.Equal(t, "50", result)

	result, err = tool.Execute(context.Background(), map[string]interface{}{"expression": "(2+3)*4"})
	require.NoError(t, err)
	assert.Equal(t, "20", result)

	_, err = tool.Execute(context.Background(), map[string]interface{}{"expression": "invalid"})
	assert.Error(t, err)
}

func TestBuiltinTools_Calculator_Security(t *testing.T) {
	tool := CreateCalculatorTool()

	_, err := tool.Execute(context.Background(), map[string]interface{}{"expression": "os.system('ls')"})
	assert.Error(t, err)
}

func TestBuiltinTools_SearchMemories(t *testing.T) {
	mockSvc := &mockMemoryProvider{memories: []string{"我喜欢简洁的回答", "我的生日是1月1日"}}
	tool := CreateSearchMemoriesTool(mockSvc)

	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "生日"})
	require.NoError(t, err)
	assert.Contains(t, result, "1月1日")
	assert.NotContains(t, result, "简洁")
}

func TestBuiltinTools_SearchMemories_NoMatch(t *testing.T) {
	mockSvc := &mockMemoryProvider{memories: []string{"我喜欢简洁的回答"}}
	tool := CreateSearchMemoriesTool(mockSvc)

	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "天气"})
	require.NoError(t, err)
	assert.Contains(t, result, "未找到")
}
