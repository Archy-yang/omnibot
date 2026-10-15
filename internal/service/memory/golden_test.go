package memory

// golden_test.go — 金样本确定性测试(24 号 M3 E1,doc 24 §7.0)。
// 脚本化 LLM 输出跑真实管线,断言 DB 终态与 expected 一致(共享 matcher,
// 与 make eval-memory 真实回放同语义)。这是提示词/解析/应用逻辑改动的回归护栏。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/stretchr/testify/require"
)

func TestGoldenFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "golden", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "金样本目录不能为空")

	for _, path := range paths {
		fixture, err := LoadGoldenFixture(path)
		require.NoError(t, err, "加载 %s", path)
		t.Run(fixture.Name, func(t *testing.T) {
			p, db, llm, _ := pipelineSetup(t)
			// 关闭段落切分(silenceGap=0 = 旧契约整窗收口;金样本验证对账/抽取/落库,
			// 非切分边界——后者有 M8.1 专属测试)
			p.silenceGap = 0
			// 消息源换 DB-backed(与 eval-memory 真实回放同构,非 fake 全局切片)
			p.source = &GoldenDBSource{DB: db}

			// 预置对话区间与世界观快照(与 eval-memory 真实回放同一套 seed)
			_, err := SeedGoldenFixture(db, fixture)
			require.NoError(t, err)

			llm.resp = string(fixture.ScriptedOutput)
			require.NoError(t, p.RunOnce(context.Background(), fixture.UserID))

			// 单批沉淀:切分被关闭,恰好 1 次 LLM 调用(多于 1 = 意外切分,fixture 语义被破坏)
			require.Equal(t, 1, llm.calls, "应恰好一次 LLM 调用")

			state, err := LoadGoldenState(db, fixture.UserID)
			require.NoError(t, err)
			res := MatchGolden(state, &fixture.Expected)
			require.Empty(t, res.Fails,
				"金样本 %s 有断言未命中:\n%s\n\n(命中 %d 项;误抽候选:matters=%v facts=%v style=%v changes=%v)",
				fixture.Name, strings.Join(res.Fails, "\n"),
				len(res.Passes), res.ExtraMatters, res.ExtraFacts, res.ExtraStyleNotes, res.ExtraPreferenceChanges)
		})
	}
}

// TestMatchGolden_Semantics matcher 自身语义:负断言/贪心消费/loop 状态匹配。
func TestMatchGolden_Semantics(t *testing.T) {
	state := &GoldenState{
		Facts: []memorydomain.Memory{
			{Content: "他的女儿叫安安,今年 5 岁", Kind: "fact"},
			{Content: "他关注财经消息", Kind: "fact"},
		},
		Loops: []memorydomain.Memory{
			{Content: "待发会议纪要", Kind: "loop", LoopStatus: "closed"},
		},
	}

	// 负断言:contains 命中但 not_contains 命中 → 找不到匹配
	res := MatchGolden(state, &GoldenExpectation{
		Facts: []ExpectedFact{{ContentContains: []string{"安安"}, ContentNotContains: []string{"5 岁"}}},
	})
	require.Len(t, res.Fails, 1)

	// 贪心消费:两个 expected 消费两条 fact;多余 fact 进 Extra
	res = MatchGolden(state, &GoldenExpectation{
		Facts: []ExpectedFact{{ContentContains: []string{"安安"}}, {ContentContains: []string{"财经"}}},
	})
	require.Empty(t, res.Fails)
	require.Empty(t, res.ExtraFacts)

	res = MatchGolden(state, &GoldenExpectation{
		Facts: []ExpectedFact{{ContentContains: []string{"安安"}}},
	})
	require.Empty(t, res.Fails)
	require.Len(t, res.ExtraFacts, 1, "未被 expected 覆盖的 fact 进误抽候选")

	// loop:closed 命中 / open 不命中
	res = MatchGolden(state, &GoldenExpectation{
		LoopClosedContents: []string{"会议纪要"},
		LoopOpenContents:   []string{"会议纪要"},
	})
	require.Len(t, res.Fails, 1, "同一 loop 不可能既 closed 又 open")
	require.Len(t, res.Passes, 1)
}
