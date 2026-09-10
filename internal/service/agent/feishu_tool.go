package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"time"
)

// 飞书 CLI 桥接(13-Skill与MCP插件系统技术方案 §7,M5):
// Agent 经由受控的 builtin skill 执行 lark-cli,以用户身份操作飞书全业务域。
// 身份与 token 管理完全委托 CLI(device flow 授权 + 系统钥匙串自动刷新),本工具零凭证管理。
//
// 安全收口(方案 D2/D3):
//   - exec.Command 参数数组直传,不经 shell,天然防注入
//   - 拒绝 --yes(高危写确认标记),delete 等高危操作本期不开放给 Agent
//   - 域白名单:auth/config(凭证与授权)、event/apps(事件与部署)不可经对话触达
//   - 超时 + 输出截断,防长任务挂起与上下文撑爆

const (
	feishuDefaultBin        = "lark-cli"
	feishuDefaultTimeout    = 60 * time.Second
	feishuDefaultOutLimit   = 32 * 1024
	feishuTruncateMarkFmt   = "\n...[输出已截断,原始长度 %d 字节,仅保留前 %d 字节]"
	feishuExitTimeoutFormat = "lark-cli 执行超时(%s)。可拆小任务、缩小查询范围后重试"
)

// feishuAllowedDomains 域白名单。排除:auth/config(凭证与授权状态)、event/apps(事件订阅与应用部署)。
var feishuAllowedDomains = map[string]bool{
	"docs": true, "base": true, "drive": true, "im": true, "wiki": true,
	"sheets": true, "calendar": true, "task": true, "mail": true, "markdown": true,
	"contact": true, "mindnotes": true, "note": true, "minutes": true, "vc": true,
	"okr": true, "approval": true, "attendance": true, "whiteboard": true,
	// Agent 自省与兜底:查参数 schema、读内置 skill 指南、原始 API 逃生口
	"schema": true, "skills": true, "api": true,
}

// FeishuCLIConfig CLI 桥接配置(零值字段取默认)。
type FeishuCLIConfig struct {
	BinPath     string        // lark-cli 可执行路径,默认 "lark-cli"(PATH 查找)
	Timeout     time.Duration // 单次执行超时,默认 60s
	OutputLimit int           // 输出截断上限(字节),默认 32KB
}

func (c FeishuCLIConfig) binPath() string {
	if c.BinPath != "" {
		return c.BinPath
	}
	return feishuDefaultBin
}

func (c FeishuCLIConfig) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return feishuDefaultTimeout
}

func (c FeishuCLIConfig) outputLimit() int {
	if c.OutputLimit > 0 {
		return c.OutputLimit
	}
	return feishuDefaultOutLimit
}

// CreateFeishuTool 飞书 CLI 桥接工具:args 逐字传给 lark-cli,stdin 走 '-' 管道免转义。
func CreateFeishuTool(cfg FeishuCLIConfig) Tool {
	return Tool{
		Name: "feishu",
		Description: "以用户身份操作飞书（执行 lark-cli）。支持文档读写编辑(docs)、多维表格(base)、消息(im)、" +
			"云盘(drive)、知识库(wiki)、表格(sheets)、日历/任务/邮件等。用法：args 传子命令与参数数组，" +
			"如 [\"docs\",\"+search\",\"--query\",\"周报\"]；多行内容(如文档正文)用参数 \"-\" 配合 stdin 传入，" +
			"如 [\"docs\",\"+create\",\"--title\",\"标题\",\"--doc-format\",\"markdown\",\"--content\",\"-\"]。" +
			"参数不确定时先调 [\"schema\",\"<service.resource.method>\"] 或 <domain> 子命令加 --help 查看。" +
			"高危写操作(--yes)不可用；鉴权/配置类子命令不可用。",
		DisplayLabel: "操作了飞书",
		Capabilities: []string{CapBasic},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"args": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "lark-cli 子命令与参数，逐字传递。如 [\"docs\",\"+fetch\",\"--doc\",\"<文档URL或token>\"]",
				},
				"stdin": map[string]interface{}{
					"type":        "string",
					"description": "可选。通过标准输入传给命令的内容(配合参数 \"-\")，适合多行文档正文，避免转义问题",
				},
			},
			"required": []string{"args"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cliArgs, err := parseFeishuArgs(args)
			if err != nil {
				return "", err
			}
			if err := validateFeishuArgs(cliArgs); err != nil {
				return "", err
			}
			return runFeishuCLI(ctx, cfg, cliArgs, feishuStdin(args))
		},
	}
}

// parseFeishuArgs 提取 args 数组(LLM 传来的是 []interface{})。
func parseFeishuArgs(args map[string]interface{}) ([]string, error) {
	raw, ok := args["args"].([]interface{})
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("args 必须是非空字符串数组,如 [\"docs\",\"+search\",\"--query\",\"关键词\"]")
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("args 元素必须是字符串")
		}
		out = append(out, s)
	}
	return out, nil
}

// feishuStdin 提取可选 stdin(空串 = 不传)。
func feishuStdin(args map[string]interface{}) string {
	if s, ok := args["stdin"].(string); ok {
		return s
	}
	return ""
}

// validateFeishuArgs 安全校验:域白名单 + 高危标记。
func validateFeishuArgs(cliArgs []string) error {
	if !feishuAllowedDomains[cliArgs[0]] {
		return fmt.Errorf("不允许执行 lark-cli 的 %q 子命令(可用域:docs/base/drive/im/wiki/sheets/calendar/task/mail 等;鉴权与配置类命令不可经对话执行)", cliArgs[0])
	}
	for _, a := range cliArgs {
		if a == "--yes" {
			return fmt.Errorf("不允许通过对话执行高危写操作(--yes);删除等高危操作请用户在飞书中手动完成")
		}
	}
	return nil
}

// runFeishuCLI 执行 lark-cli:超时控制 + stdin 透传 + 输出截断。
func runFeishuCLI(ctx context.Context, cfg FeishuCLIConfig, cliArgs []string, stdin string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()

	cmd := exec.CommandContext(runCtx, cfg.binPath(), cliArgs...) //nolint:gosec // 白名单校验后的受控执行
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	runErr := cmd.Run()
	out := stdout.String()
	if stderr.Len() > 0 {
		out += "\n" + stderr.String()
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf(feishuExitTimeoutFormat, cfg.timeout())
	}
	if runErr != nil {
		if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist) {
			return "", fmt.Errorf("lark-cli 未安装或不可执行,请先安装并完成一次 lark-cli auth login 授权")
		}
		// CLI 自身的错误信息(JSON,含修复建议)对 LLM 有价值,附在错误里返回
		return "", fmt.Errorf("lark-cli 执行失败: %s", tailFeishuOutput(out, cfg.outputLimit()))
	}
	return truncateFeishuOutput(out, cfg.outputLimit()), nil
}

// truncateFeishuOutput 超限时截断并标注。
func truncateFeishuOutput(out string, limit int) string {
	if len(out) <= limit {
		return out
	}
	return out[:limit] + fmt.Sprintf(feishuTruncateMarkFmt, len(out), limit)
}

// tailFeishuOutput 错误场景保留尾部(CLI 的错误信息通常在末尾)。
func tailFeishuOutput(out string, limit int) string {
	if len(out) <= limit {
		return out
	}
	return "...[前文已省略]\n" + out[len(out)-limit:]
}
