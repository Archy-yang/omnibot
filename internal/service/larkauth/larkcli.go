// Package larkauth 实现用户级 lark-cli 授权编排(19-多用户LarkCLI授权技术方案 §4)。
// DB 存路由与状态;token 本体留在 lark-cli 自有存储(.enc 按 profile 隔离)。
// CLI 调用经 LarkCLI 窄接口(mock 化,真实实现 shell 出去,执行纪律同 feishu_tool:
// argv 直传不经 shell、超时、错误可读)。
package larkauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// LarkCLI lark-cli 的窄接口(真实实现 *cliReal,单测 mock)。
type LarkCLI interface {
	ProfileAdd(profile, appID, appSecret string) error
	// LoginStart 发起 device flow:返回验证地址/用户码/设备码(--no-wait --json)。
	LoginStart(profile, domains string) (verifyURL, userCode, deviceCode string, err error)
	// LoginComplete 收尾:轮询至用户确认或超时(ctx 控制)。
	LoginComplete(ctx context.Context, profile, deviceCode string) error
	// Status 授权身份回显(whoami)。
	Status(profile string) (openID, userName string, err error)
	Logout(profile string) error
}

// CLIConfig 真实实现配置。
type CLIConfig struct {
	BinPath        string        // 默认 lark-cli
	Domains        string        // LoginStart 的 --domain,默认 all
	CompleteTimeout time.Duration // LoginComplete 单次收尾超时,默认 5s(惰性收尾节奏)
}

func (c CLIConfig) bin() string {
	if c.BinPath != "" {
		return c.BinPath
	}
	return "lark-cli"
}

func (c CLIConfig) domains() string {
	if c.Domains != "" {
		return c.Domains
	}
	return "all"
}

func (c CLIConfig) completeTimeout() time.Duration {
	if c.CompleteTimeout > 0 {
		return c.CompleteTimeout
	}
	return 5 * time.Second
}

// NewRealCLI 构建真实 CLI 实现。
func NewRealCLI(cfg CLIConfig) LarkCLI { return &cliReal{cfg: cfg} }

type cliReal struct{ cfg CLIConfig }

// run 执行 lark-cli(argv 直传),返回 stdout;退出码非零时附尾部输出(错误信息对排障有价值)。
func (c *cliReal) run(ctx context.Context, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, c.cfg.bin(), args...) //nolint:gosec // 受控 bin + argv 直传
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		tail := errOut.String()
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return "", fmt.Errorf("lark-cli %s 失败: %s", args[0], strings.TrimSpace(tail))
	}
	return out.String(), nil
}

func (c *cliReal) ProfileAdd(profile, appID, appSecret string) error {
	cmd := exec.Command(c.cfg.bin(), "profile", "add", "--name", profile, "--app-id", appID, "--app-secret-stdin") //nolint:gosec
	cmd.Stdin = strings.NewReader(appSecret)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if strings.Contains(msg, "already") { // 幂等:同名 profile 已存在视为成功
			return nil
		}
		return fmt.Errorf("lark-cli profile add 失败: %s", msg)
	}
	return nil
}

// cliJSON lark-cli --json 输出的宽容解析:多候选字段名取首个非空(输出 schema 以实跑为准,容错缺字段)。
func pickString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (c *cliReal) LoginStart(profile, domains string) (string, string, string, error) {
	out, err := c.run(context.Background(), "--profile", profile, "auth", "login", "--no-wait", "--json", "--domain", domains)
	if err != nil {
		return "", "", "", err
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return "", "", "", fmt.Errorf("lark-cli auth login 输出解析失败(期望 JSON): %w", err)
	}
	// 嵌套容错:字段可能在顶层或 data 子对象
	if data, ok := m["data"].(map[string]interface{}); ok {
		for k, v := range data {
			if _, exists := m[k]; !exists {
				m[k] = v
			}
		}
	}
	verifyURL := pickString(m, "verification_url", "verificationUrl", "url", "verify_url")
	deviceCode := pickString(m, "device_code", "deviceCode", "device_introspection_code")
	userCode := pickString(m, "user_code", "userCode")
	if verifyURL == "" || deviceCode == "" {
		return "", "", "", fmt.Errorf("lark-cli auth login 输出缺少关键字段(verification_url/device_code)")
	}
	return verifyURL, userCode, deviceCode, nil
}

func (c *cliReal) LoginComplete(ctx context.Context, profile, deviceCode string) error {
	completeCtx, cancel := context.WithTimeout(ctx, c.cfg.completeTimeout())
	defer cancel()
	_, err := c.run(completeCtx, "--profile", profile, "auth", "login", "--device-code", deviceCode, "--json")
	return err
}

func (c *cliReal) Status(profile string) (string, string, error) {
	out, err := c.run(context.Background(), "--profile", profile, "whoami")
	if err != nil {
		return "", "", err
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return "", "", fmt.Errorf("lark-cli whoami 输出解析失败: %w", err)
	}
	openID, name := "", ""
	if o, ok := m["onBehalfOf"].(map[string]interface{}); ok {
		openID = pickString(o, "openId", "open_id")
		name = pickString(o, "userName", "user_name")
	}
	return openID, name, nil
}

func (c *cliReal) Logout(profile string) error {
	_, err := c.run(context.Background(), "--profile", profile, "auth", "logout")
	return err
}
