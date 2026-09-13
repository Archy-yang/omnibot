package tools

import (
	"context"
	agentpkg "omnibot/internal/service/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeFeishuStub 写一个假 lark-cli 脚本,返回其路径(stub 行为由脚本内容决定)。
func writeFeishuStub(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-lark-cli")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755))
	return p
}

func feishuTestConfig(binPath string, timeout time.Duration) FeishuCLIConfig {
	return FeishuCLIConfig{
		BinPath:     binPath,
		Timeout:     timeout,
		OutputLimit: 1024,
	}
}

func TestFeishuTool_Definition(t *testing.T) {
	tool := CreateFeishuTool(FeishuCLIConfig{})
	require.Equal(t, "feishu", tool.Name)
	require.NotEmpty(t, tool.Description)
	require.NotEmpty(t, tool.DisplayLabel)
	require.Contains(t, tool.Capabilities, agentpkg.CapBasic)
	// 参数 schema:args 必填数组,stdin 可选
	props, ok := tool.Parameters["properties"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, props, "args")
	require.Contains(t, props, "stdin")
	require.Equal(t, []string{"args"}, tool.Parameters["required"])
	// Execute 必须已装配
	require.NotNil(t, tool.Execute)
}

func TestFeishuTool_PassesArgsVerbatim(t *testing.T) {
	// stub 逐行回显参数,验证参数数组不经 shell、逐字传递
	bin := writeFeishuStub(t, `for a in "$@"; do echo "ARG:$a"; done`)
	tool := CreateFeishuTool(feishuTestConfig(bin, 5*time.Second))
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []interface{}{"docs", "+search", "--query", "周报 计划"},
	})
	require.NoError(t, err)
	for _, want := range []string{"ARG:docs", "ARG:+search", "ARG:--query", "ARG:周报 计划"} {
		require.Contains(t, out, want)
	}
}

func TestFeishuTool_StdinPassthrough(t *testing.T) {
	// stdin 走 '-' 管道:stub 即 cat,多行内容原样透传(免转义写入文档的推荐路径)
	bin := writeFeishuStub(t, `cat`)
	tool := CreateFeishuTool(feishuTestConfig(bin, 5*time.Second))
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"args":  []interface{}{"docs", "+create", "--content", "-"},
		"stdin": "第一行\n第二行 <xml> & 特殊字符",
	})
	require.NoError(t, err)
	require.Contains(t, out, "第一行\n第二行 <xml> & 特殊字符")
}

func TestFeishuTool_RejectYes(t *testing.T) {
	bin := writeFeishuStub(t, `echo "SHOULD_NOT_RUN"`)
	tool := CreateFeishuTool(feishuTestConfig(bin, 5*time.Second))
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []interface{}{"drive", "+delete", "--file-token", "xxx", "--yes"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.NotContains(t, err.Error(), "SHOULD_NOT_RUN") // stub 未被执行
}

func TestFeishuTool_DomainAllowlist(t *testing.T) {
	bin := writeFeishuStub(t, `echo "SHOULD_NOT_RUN"`)
	tool := CreateFeishuTool(feishuTestConfig(bin, 5*time.Second))

	// auth/config 能改凭证与授权状态,禁止对话触达
	for _, domain := range []string{"auth", "config", "event", "apps"} {
		_, err := tool.Execute(context.Background(), map[string]interface{}{
			"args": []interface{}{domain, "status"},
		})
		require.Error(t, err, "domain %q should be rejected", domain)
		require.NotContains(t, err.Error(), "SHOULD_NOT_RUN")
	}

	// 白名单域正常放行
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []interface{}{"base", "+record-list"},
	})
	require.NoError(t, err)
	require.Contains(t, out, "SHOULD_NOT_RUN")
}

func TestFeishuTool_Timeout(t *testing.T) {
	bin := writeFeishuStub(t, `sleep 2`)
	tool := CreateFeishuTool(feishuTestConfig(bin, 100*time.Millisecond))
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []interface{}{"docs", "+search"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "超时")
}

func TestFeishuTool_OutputTruncated(t *testing.T) {
	// 输出 100KB,上限 1KB → 截断并标注
	bin := writeFeishuStub(t, `head -c 102400 /dev/zero | tr "\0" "x"`)
	tool := CreateFeishuTool(feishuTestConfig(bin, 5*time.Second))
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []interface{}{"docs", "+search"},
	})
	require.NoError(t, err)
	require.Less(t, len(out), 2048)
	require.Contains(t, out, "截断")
}

func TestFeishuTool_MissingBinary(t *testing.T) {
	tool := CreateFeishuTool(feishuTestConfig("/nonexistent/path/lark-cli", 5*time.Second))
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []interface{}{"docs", "+search"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "lark-cli")
	require.True(t, strings.Contains(err.Error(), "未安装") || strings.Contains(err.Error(), "授权"))
}

func TestFeishuTool_EmptyArgs(t *testing.T) {
	bin := writeFeishuStub(t, `echo "SHOULD_NOT_RUN"`)
	tool := CreateFeishuTool(feishuTestConfig(bin, 5*time.Second))
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.Error(t, err)
}
