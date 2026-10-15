// eval-memory — 记忆沉淀真实回放(24 号 M3 E2,doc 24 §7.0)。
//
// 对金样本(testdata/golden/*.json)用真实 LLM 跰沉淀管线(与生产同一
// DigestPipeline 代码路径,每金样本独立 sqlite 内存 DB,不碰生产数据),
// 与期望终态比对后输出命中/漏抽/误抽候选报告,供人工审阅反哺提示词。
//
// 用法:make eval-memory(或 go run ./cmd/eval-memory -config configs/config.yaml)
// 不进 CI;提示词调优后重放对比。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"omnibot/internal/db"
	"omnibot/internal/domain/conversation"
	agentpkg "omnibot/internal/service/agent"
	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/service/memory"
	userRepo "omnibot/internal/repository/user"
	userService "omnibot/internal/service/user"

	"omnibot/internal/pkg/crypto"
	"omnibot/pkg/config"

	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm"
)

// evalLLM 真实 LLM 适配 PipelineLLM;记录每次请求/响应用于报告溯源。
// 客户端用 agentpkg.OpenAILLMClient——与生产沉淀管线(userPipelineLLM)同款实现;
// 之前误用 internal/client/llm 老工厂,其 provider 实现在本机配置下全部失败
// ("all providers failed"),评估通路从未真正走通过。
type evalLLM struct {
	client          *agentpkg.OpenAILLMClient
	lastRequestLen  int
	lastOutput      string
	lastOutputError error
}

func (e *evalLLM) Complete(ctx context.Context, _ int64, system, user string) (string, error) {
	e.lastRequestLen = len(system) + len(user)
	content, _, err := e.client.ChatCompletion(ctx, []map[string]interface{}{
		{"role": "system", "content": system},
		{"role": "user", "content": user},
	}, nil)
	e.lastOutput = content
	e.lastOutputError = err
	return content, err
}

// newScratchDB 每金样本一个独立内存 DB(与生产 PG 同构的表集合,不碰生产数据)。
func newScratchDB() (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(
		&conversation.Message{},
		&memorydomain.DigestWatermark{},
		&memorydomain.Memory{},
		&memorydomain.MemoryMessageLink{},
		&memorydomain.Matter{},
		&memorydomain.MemoryBlacklist{},
		&memorydomain.StyleObservation{},
		&memorydomain.CognitionChange{},
	); err != nil {
		return nil, err
	}
	return db, nil
}

func main() {
	configPath := flag.String("config", "configs/config.yaml", "服务配置路径")
	fixturesDir := flag.String("fixtures", "internal/service/memory/testdata/golden", "金样本目录")
	outDir := flag.String("out", "eval-out", "报告输出目录")
	userID := flag.Int64("user", 1, "取该用户级的 LLM 配置(生产聊天实际使用的通路)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal("加载配置失败: %v", err)
	}
	// LLM 配置解析与生产同序:用户级配置优先(chat/沉淀实际走的通路),
	// 系统默认 provider 兜底。configs/config.yaml 的默认 provider api_key 通常为空,
	// 真实 key 在用户级配置(users llm 配置,加密存 DB)。
	apiKey, baseURL, model, source, resolveErr := resolveLLMConfig(cfg, *userID)
	if resolveErr != nil {
		fatal("解析 LLM 配置失败: %v", resolveErr)
	}
	llmClient := agentpkg.NewOpenAILLMClientWithTTFB(apiKey, baseURL, model, 120*time.Second, 120*time.Second)
	fmt.Fprintf(os.Stderr, "llm 来源: %s | model: %s | base_url: %s\n", source, model, baseURL)

	paths, err := filepath.Glob(filepath.Join(*fixturesDir, "*.json"))
	if err != nil || len(paths) == 0 {
		fatal("金样本目录无 fixture: %s", *fixturesDir)
	}
	sort.Strings(paths)

	ctx := context.Background()
	var b strings.Builder
	b.WriteString("# 记忆沉淀真实回放报告\n\n")
	fmt.Fprintf(&b, "- 时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- 金样本: %d 组(%s)\n\n", len(paths), *fixturesDir)

	totalPass, totalExpect := 0, 0
	failedFixtures := 0

	// 进度走 stderr(实时逐条,报告走 stdout 文件):每个金样本起跑/收尾都可见,
	// 避免真实 LLM 调用期间(单次可达数十秒)看起来像挂死。
	for i, path := range paths {
		fixture, err := memory.LoadGoldenFixture(path)
		if err != nil {
			fatal("加载 %s: %v", path, err)
		}
		fmt.Fprintf(os.Stderr, "[%d/%d] %s 调用 LLM 中...", i+1, len(paths), fixture.Name)
		started := time.Now()
		section := runFixture(ctx, llmClient, fixture)
		elapsed := time.Since(started).Round(time.Second)
		switch {
		case section.runErr != nil:
			fmt.Fprintf(os.Stderr, " ❌ 运行失败(%s): %v\n", elapsed, section.runErr)
		default:
			fmt.Fprintf(os.Stderr, " ✅ 命中 %d/%d,漏抽 %d,误抽候选 %d(%s)\n",
				section.passCount, section.expectCount,
				len(section.match.Fails),
				len(section.match.ExtraMatters)+len(section.match.ExtraFacts)+
					len(section.match.ExtraStyleNotes)+len(section.match.ExtraPreferenceChanges),
				elapsed)
		}
		totalExpect += section.expectCount
		totalPass += section.passCount
		if section.runErr != nil || len(section.match.Fails) > 0 {
			failedFixtures++
		}
		b.WriteString(section.markdown)
	}

	fmt.Fprintf(&b, "---\n\n## 汇总\n\n")
	fmt.Fprintf(&b, "- 断言命中: %d/%d\n", totalPass, totalExpect)
	fmt.Fprintf(&b, "- 有未命中/运行失败的金样本: %d/%d\n", failedFixtures, len(paths))
	b.WriteString("- 误抽候选(Extra)需人工判读,不自动计错;调提示词后重放对比。\n")

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("创建输出目录: %v", err)
	}
	outPath := filepath.Join(*outDir, "report-"+time.Now().Format("20060102-150405")+".md")
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		fatal("写报告: %v", err)
	}
	fmt.Printf("回放完成:命中 %d/%d,报告 %s\n", totalPass, totalExpect, outPath)
}

type fixtureSection struct {
	markdown    string
	passCount   int
	expectCount int
	runErr      error
	match       *memory.GoldenMatchResult
}

// resolveLLMConfig 与生产 userPipelineLLM 同序:用户级配置(DB,加密存储)优先,
// 系统默认 provider 兜底。返回 (key, baseURL, model, 来源描述, err)。
func resolveLLMConfig(cfg *config.Config, userID int64) (string, string, string, string, error) {
	// 用户级:连生产 DB 读解密后的配置(评估工具本来就是人工手动跑,只读)
	// 解密需先初始化全局密钥,与装配点(internal/api/wire.go)同序同源:
	// config security.encrypt_key → 环境变量 LLM_CONFIG_ENCRYPT_KEY → legacy 默认密钥
	encryptMaterial := cfg.Security.EncryptKey
	if encryptMaterial == "" {
		encryptMaterial = os.Getenv("LLM_CONFIG_ENCRYPT_KEY")
	}
	if encryptMaterial != "" {
		crypto.Init(encryptMaterial)
	} else {
		crypto.InitWithKey(crypto.LegacyDefaultKey)
	}
	dbConn, err := db.InitDB(&cfg.Database)
	if err != nil {
		return "", "", "", "", fmt.Errorf("连 DB 读用户级 LLM 配置: %w", err)
	}
	svc := userService.NewLLMConfigService(userRepo.NewLLMConfigRepository(dbConn.GetGormDB()))
	apiKey, baseURL, model, hasCustom, err := svc.GetConfigForUser(userID)
	if err != nil {
		return "", "", "", "", fmt.Errorf("读用户 %d 的 LLM 配置: %w", userID, err)
	}
	if hasCustom {
		return apiKey, baseURL, model, fmt.Sprintf("用户级配置(user %d)", userID), nil
	}

	// 系统默认 provider 兜底
	providerCfg, ok := cfg.LLM.Providers[cfg.LLM.Routing.Default]
	if !ok || providerCfg.APIKey == "" {
		return "", "", "", "", fmt.Errorf("用户 %d 无 LLM 配置,且默认 provider %q 未配置 api_key", userID, cfg.LLM.Routing.Default)
	}
	return providerCfg.APIKey, providerCfg.BaseURL, providerCfg.Model,
		fmt.Sprintf("系统默认(%s)", cfg.LLM.Routing.Default), nil
}

func runFixture(ctx context.Context, llmClient *agentpkg.OpenAILLMClient, fixture *memory.GoldenFixture) fixtureSection {
	sec := fixtureSection{markdown: ""}
	sec.expectCount = len(fixture.Expected.Facts) + len(fixture.Expected.Matters) +
		len(fixture.Expected.LoopClosedContents) + len(fixture.Expected.LoopOpenContents) +
		len(fixture.Expected.StyleNotes) + len(fixture.Expected.PreferenceChanges)

	head := &strings.Builder{}
	fmt.Fprintf(head, "## %s\n\n%s\n\n", fixture.Name, fixture.Description)

	db, err := newScratchDB()
	if err != nil {
		sec.runErr = err
		sec.markdown = head.String() + fmt.Sprintf("> 初始化 scratch DB 失败: %v\n\n", err)
		return sec
	}

	if _, err := memory.SeedGoldenFixture(db, fixture); err != nil {
		sec.runErr = err
		sec.markdown = head.String() + fmt.Sprintf("> 预置失败: %v\n\n", err)
		return sec
	}

	adapter := &evalLLM{client: llmClient}
	pipeline := memory.NewDigestPipeline(
		memoryrepo.NewWatermarkRepository(db),
		memoryrepo.NewMemoryRepository(db),
		memoryrepo.NewMatterRepository(db),
		&memory.GoldenDBSource{DB: db},
		adapter,
		nil, // 无向量也能对账(与生产降级路径一致)
		1,   // 阈值 1:金样本消息一次性进入沉淀
		0,   // 构造器会把 0 规范化为默认值,下方显式关闭切分
	)
	pipeline.SetSilenceGap(0) // 整窗收口:金样本语义为单批(构造器规范化绕行)
	if err := pipeline.RunOnce(ctx, fixture.UserID); err != nil {
		sec.runErr = err
		sec.markdown = head.String() + fmt.Sprintf("> 管线运行失败: %v\n\n", err)
		return sec
	}

	state, err := memory.LoadGoldenState(db, fixture.UserID)
	if err != nil {
		sec.runErr = err
		sec.markdown = head.String() + fmt.Sprintf("> 读回终态失败: %v\n\n", err)
		return sec
	}

	res := memory.MatchGolden(state, &fixture.Expected)
	sec.match = res
	sec.passCount = len(res.Passes)

	sec.markdown = head.String() + renderResult(res, adapter, state)
	return sec
}

func renderResult(res *memory.GoldenMatchResult, adapter *evalLLM, state *memory.GoldenState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- **命中**: %d 项\n", len(res.Passes))
	if len(res.Fails) > 0 {
		b.WriteString("- **漏抽/未命中**:\n")
		for _, f := range res.Fails {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}
	appendExtras := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "- **%s(误抽候选,人工判)**:\n", title)
		for _, it := range items {
			fmt.Fprintf(&b, "  - %s\n", it)
		}
	}
	appendExtras("事项", res.ExtraMatters)
	appendExtras("事实", res.ExtraFacts)
	appendExtras("风格观察", res.ExtraStyleNotes)
	appendExtras("认知变化", res.ExtraPreferenceChanges)

	b.WriteString("\n<details><summary>LLM 原始输出(溯源)</summary>\n\n```\n")
	if adapter.lastOutputError != nil {
		fmt.Fprintf(&b, "(error: %v)\n", adapter.lastOutputError)
	} else {
		b.WriteString(strings.TrimSpace(adapter.lastOutput))
	}
	b.WriteString("\n```\n\n</details>\n\n")

	// 落库终态全文(供人工核对归类)
	b.WriteString("<details><summary>落库终态</summary>\n\n```\n")
	for _, f := range state.Facts {
		fmt.Fprintf(&b, "[fact] %s\n", f.Content)
	}
	for _, l := range state.Loops {
		fmt.Fprintf(&b, "[loop/%s] %s\n", l.LoopStatus, l.Content)
	}
	for _, m := range state.Matters {
		fmt.Fprintf(&b, "[matter/%s] %s: %s\n", m.Status, m.Title, m.StateDesc)
	}
	for _, n := range state.StyleNotes {
		fmt.Fprintf(&b, "[style/%s] %s\n", n.Dimension, n.Content)
	}
	for _, c := range state.PreferenceChanges {
		fmt.Fprintf(&b, "[change] %s\n", c.Content)
	}
	b.WriteString("```\n\n</details>\n\n")
	return b.String()
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "eval-memory: "+format+"\n", args...)
	os.Exit(1)
}
