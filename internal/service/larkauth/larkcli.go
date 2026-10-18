// Package larkauth 实现用户级 lark-cli 授权编排(19-多用户LarkCLI授权技术方案 §4)。
// DB 存路由与状态;token 本体留在 lark-cli 自有存储(.enc 按 profile 隔离)。
// CLI 调用经 LarkCLI 窄接口(mock 化,真实实现 shell 出去,执行纪律同 feishu_tool:
// argv 直传不经 shell、超时、错误可读)。
package larkauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// LarkCLI lark-cli 的窄接口(真实实现 *cliReal,单测 mock)。
// userID 贯穿所有方法:27 号 DB 物化后,执行必须按用户 hydrate/回写(Executor 依赖它)。
type LarkCLI interface {
	ProfileAdd(userID int64, profile, appID, appSecret string) error
	// LoginStart 发起 device flow:返回验证地址/用户码/设备码(--no-wait --json)。
	LoginStart(userID int64, profile, domains string) (verifyURL, userCode, deviceCode string, err error)
	// LoginComplete 收尾:轮询至用户确认或超时(ctx 控制)。
	LoginComplete(ctx context.Context, userID int64, profile, deviceCode string) error
	// Status 授权身份回显(whoami)。
	Status(userID int64, profile string) (openID, userName string, err error)
	Logout(userID int64, profile string) error
}

// Runner 统一执行入口(27 号 larkhome.Executor;nil = 直跑本地 CLI,单机 volume 兜底形态)。
type Runner interface {
	RunLarkCLI(ctx context.Context, userID int64, stdin string, args ...string) (string, error)
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

// NewRealCLI 构建真实 CLI 实现。runner 非 nil 时走 27 号物化执行(HOME 物化+回写),否则直跑本地。
func NewRealCLI(cfg CLIConfig) LarkCLI { return &cliReal{cfg: cfg} }

type cliReal struct {
	cfg    CLIConfig
	runner Runner
}

// SetRunner 注入统一执行入口(装配点调用;nil 安全)。
func (c *cliReal) SetRunner(r Runner) { c.runner = r }

// run 执行 lark-cli(argv 直传),返回 stdout;退出码非零时附尾部输出(错误信息对排障有价值)。
// 注意:退出码不参与上层成败判定——实测收尾成功也可能非零退出(见 PollAuth)。
// 超时被杀必须以 context.DeadlineExceeded 类型透出(上层据此判"还没确认");WaitDelay 防
// 管道被孙进程占用导致 Run 滞留(实测曾把 5s 封顶拖到 10.7s)。
func (c *cliReal) run(ctx context.Context, userID int64, stdin string, args ...string) (string, error) {
	if c.runner != nil {
		return c.runner.RunLarkCLI(ctx, userID, stdin, args...)
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, c.cfg.bin(), args...) //nolint:gosec // 受控 bin + argv 直传
	cmd.WaitDelay = time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", context.DeadlineExceeded
		}
		tail := errOut.String()
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return "", fmt.Errorf("lark-cli %s 失败: %s", args[0], strings.TrimSpace(tail))
	}
	return out.String(), nil
}

// ProfileAdd 幂等写入 profile:**必须保证 secret 落盘**。
// 旧实现把 "already" 错误静默当成功——profile 已存在时 secret 不会更新,
// 存量壳 profile(如宿主迁移来的、secret 在 macOS Keychain 里的)会让
// device flow 报 "missing required parameter: client_secret"(2026-10-18 实测)。
// 故遇 "already" 一律删除重建,保证凭据必写入。
func (c *cliReal) ProfileAdd(userID int64, profile, appID, appSecret string) error {
	add := func() error {
		_, err := c.run(context.Background(), userID, appSecret, "profile", "add", "--name", profile, "--app-id", appID, "--app-secret-stdin")
		return err
	}
	err := add()
	if err == nil || !strings.Contains(err.Error(), "already") {
		return err
	}
	// 已存在(同名 profile / 同 app-id):删除重建
	if _, rmErr := c.run(context.Background(), userID, "", "profile", "remove", profile); rmErr != nil {
		return fmt.Errorf("lark-cli profile remove 失败(重授权前清理 %s): %w", profile, rmErr)
	}
	return add()
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

func (c *cliReal) LoginStart(userID int64, profile, domains string) (string, string, string, error) {
	out, err := c.run(context.Background(), userID, "", "--profile", profile, "auth", "login", "--no-wait", "--json", "--domain", domains)
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

func (c *cliReal) LoginComplete(ctx context.Context, userID int64, profile, deviceCode string) error {
	completeCtx, cancel := context.WithTimeout(ctx, c.cfg.completeTimeout())
	defer cancel()
	_, err := c.run(completeCtx, userID, "", "--profile", profile, "auth", "login", "--device-code", deviceCode, "--json")
	return err
}

func (c *cliReal) Status(userID int64, profile string) (string, string, error) {
	out, err := c.run(context.Background(), userID, "", "--profile", profile, "whoami")
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

func (c *cliReal) Logout(userID int64, profile string) error {
	_, err := c.run(context.Background(), userID, "", "--profile", profile, "auth", "logout")
	return err
}
