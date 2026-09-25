package tools

// weather_test.go — 天气查询工具测试(NMC 中央气象台 REST 源,Phase: 内置 weather 工具)。
//
// 全部走 httptest mock 三个 NMC 端点(/rest/province/all → /rest/province/{code} →
// /rest/weather?stationid=),不打真站。真站联调走一次 live smoke。
//
// 跑法: go test ./internal/service/agent/tools/ -run TestWeatherTool -v

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nmcMockServer 模拟 NMC 三端点。provinceHitCount 统计 /rest/province/all 命中数(缓存测试用)。
type nmcMockServer struct {
	srv              *httptest.Server
	provinceHitCount atomic.Int32
}

// fixture:1 个省(北京市,站点 Wqsps=北京);weather 返回带 9999 缺测的完整样例。
const nmcFixtureProvinces = `[{"code":"ABJ","name":"北京市"}]`

const nmcFixtureABJ = `[{"code":"Wqsps","province":"北京市","city":"北京","url":"/publish/forecast/ABJ/beijing.html"}]`

const nmcFixtureWeather = `{
  "code": 0,
  "data": {
    "real": {
      "station": {"code": "Wqsps", "province": "北京市", "city": "北京"},
      "publish_time": "2026-09-25 19:35",
      "weather": {"info": "晴", "temperature": 21.7, "feelst": 24.4, "humidity": 86.0},
      "wind": {"direct": "西北风", "power": "微风"},
      "warn": {"alert": "9999", "issuecontent": "9999", "signaltype": "9999", "signallevel": "9999"}
    },
    "air": {"aqi": 57, "text": "良"},
    "predict": {
      "publish_time": "2026-09-25 20:00",
      "detail": [
        {"date": "2026-09-25", "day": {"weather": {"info": "9999", "temperature": "9999"}, "wind": {"direct": "9999", "power": "9999"}}, "night": {"weather": {"info": "多云", "temperature": "18"}, "wind": {"direct": "西南风", "power": "微风"}}},
        {"date": "2026-09-26", "day": {"weather": {"info": "小雨", "temperature": "25"}, "wind": {"direct": "东北风", "power": "微风"}}, "night": {"weather": {"info": "小雨", "temperature": "17"}, "wind": {"direct": "北风", "power": "微风"}}, "precipitation": 9.1},
        {"date": "2026-09-27", "day": {"weather": {"info": "多云", "temperature": "24"}, "wind": {"direct": "南风", "power": "3~4级"}}, "night": {"weather": {"info": "晴", "temperature": "16"}, "wind": {"direct": "南风", "power": "微风"}}}
      ]
    }
  }
}`

func newNMCMockServer(t *testing.T, weatherByStation map[string]string) *nmcMockServer {
	t.Helper()
	m := &nmcMockServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/province/all", func(w http.ResponseWriter, r *http.Request) {
		m.provinceHitCount.Add(1)
		_, _ = w.Write([]byte(nmcFixtureProvinces))
	})
	mux.HandleFunc("/rest/province/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(nmcFixtureABJ))
	})
	mux.HandleFunc("/rest/weather", func(w http.ResponseWriter, r *http.Request) {
		stationID := r.URL.Query().Get("stationid")
		body, ok := weatherByStation[stationID]
		if !ok {
			body = nmcFixtureWeather
		}
		_, _ = w.Write([]byte(body))
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

// runWeather 构造指向 mock 的天气工具并执行,返回 (输出, 错误)。
func runWeather(t *testing.T, srvURL string, args map[string]interface{}) (string, error) {
	t.Helper()
	tool := newWeatherTool(srvURL)
	require.Equal(t, "weather", tool.Name)
	return tool.Execute(context.Background(), args)
}

// TestWeatherTool_Basic 基本查询:城市解析 → 实况+预报+AQI 一次性返回。
func TestWeatherTool_Basic(t *testing.T) {
	m := newNMCMockServer(t, nil)

	out, err := runWeather(t, m.srv.URL, map[string]interface{}{"city": "北京"})
	require.NoError(t, err)

	// 实况
	assert.Contains(t, out, "晴")
	assert.Contains(t, out, "21.7")
	assert.Contains(t, out, "24.4", "体感温度")
	assert.Contains(t, out, "西北风")
	// 空气质量
	assert.Contains(t, out, "57")
	assert.Contains(t, out, "良")
	// 7 天预报(样例 3 天)
	assert.Contains(t, out, "09-26")
	assert.Contains(t, out, "小雨")
	assert.Contains(t, out, "17~25")
	// 发布时间(可溯源性)
	assert.Contains(t, out, "19:35")
}

// TestWeatherTool_NormalizeCity 名称归一化:"北京市""海淀区"这类带行政区划后缀的输入
// 应能与站点名("北京")匹配。
func TestWeatherTool_NormalizeCity(t *testing.T) {
	m := newNMCMockServer(t, nil)

	for _, city := range []string{"北京市", "北京"} {
		out, err := runWeather(t, m.srv.URL, map[string]interface{}{"city": city})
		require.NoError(t, err, "city=%s 应能解析", city)
		assert.Contains(t, out, "晴")
	}
}

// TestWeatherTool_MissingSentinel 9999 缺测哨兵不得出现在给 LLM 的输出里:
// 当日白天缺测时用夜间数据兜底,而不是输出 "9999"。
func TestWeatherTool_MissingSentinel(t *testing.T) {
	m := newNMCMockServer(t, nil)

	out, err := runWeather(t, m.srv.URL, map[string]interface{}{"city": "北京"})
	require.NoError(t, err)
	assert.NotContains(t, out, "9999", "缺测哨兵必须过滤")
	assert.Contains(t, out, "多云", "白天缺测时应用夜间 info 兜底")
}

// TestWeatherTool_Ambiguous 同名城市不猜:返回候选列表引导补 province 参数。
func TestWeatherTool_Ambiguous(t *testing.T) {
	// 两个省都有"朝阳":北京市 + 辽宁省
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/province/all", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"code":"ABJ","name":"北京市"},{"code":"ALN","name":"辽宁省"}]`))
	})
	mux.HandleFunc("/rest/province/ABJ", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"code":"MjXfi","province":"北京市","city":"朝阳"}]`))
	})
	mux.HandleFunc("/rest/province/ALN", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"code":"XyZ12","province":"辽宁省","city":"朝阳"}]`))
	})
	mux.HandleFunc("/rest/weather", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(nmcFixtureWeather))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := runWeather(t, srv.URL, map[string]interface{}{"city": "朝阳"})
	require.Error(t, err, "同名城市必须报歧义,不得擅自选一个")
	msg := err.Error()
	assert.Contains(t, msg, "北京市")
	assert.Contains(t, msg, "辽宁省")
	assert.Contains(t, msg, "province", "错误信息应提示补 province 参数")
}

// TestWeatherTool_ProvinceDisambiguates 带 province 参数消歧,直达目标站。
func TestWeatherTool_ProvinceDisambiguates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/province/all", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"code":"ABJ","name":"北京市"},{"code":"ALN","name":"辽宁省"}]`))
	})
	mux.HandleFunc("/rest/province/ABJ", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"code":"MjXfi","province":"北京市","city":"朝阳"}]`))
	})
	mux.HandleFunc("/rest/province/ALN", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"code":"XyZ12","province":"辽宁省","city":"朝阳"}]`))
	})
	mux.HandleFunc("/rest/weather", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(nmcFixtureWeather))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	out, err := runWeather(t, srv.URL, map[string]interface{}{"city": "朝阳", "province": "北京市"})
	require.NoError(t, err)
	assert.Contains(t, out, "晴")
}

// TestWeatherTool_StationCache 站点表进程内缓存:同一工具实例第二次查询不得重拉省列表
// (省列表端点只命中 1 次)。注意缓存挂在 client 上,须用同一工具实例执行两次。
func TestWeatherTool_StationCache(t *testing.T) {
	m := newNMCMockServer(t, nil)
	tool := newWeatherTool(m.srv.URL)

	_, err := tool.Execute(context.Background(), map[string]interface{}{"city": "北京"})
	require.NoError(t, err)
	_, err = tool.Execute(context.Background(), map[string]interface{}{"city": "北京"})
	require.NoError(t, err)

	assert.Equal(t, int32(1), m.provinceHitCount.Load(), "省列表应命中缓存,只拉 1 次")
}

// TestWeatherTool_UnknownCity 查不到的城市:报友好错误,不输出内部细节。
func TestWeatherTool_UnknownCity(t *testing.T) {
	m := newNMCMockServer(t, nil)

	_, err := runWeather(t, m.srv.URL, map[string]interface{}{"city": "亚特兰蒂斯"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "亚特兰蒂斯")
}

// TestWeatherTool_EmptyCity 缺 city 参数:明确报错。
func TestWeatherTool_EmptyCity(t *testing.T) {
	m := newNMCMockServer(t, nil)

	_, err := runWeather(t, m.srv.URL, map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "city")
}

// TestWeatherTool_AirMissingSentinel 空气质量缺测(上游给 aqi=9999,live smoke 实测):
// 不得输出 "AQI 9999" 这类哨兵泄漏。
func TestWeatherTool_AirMissingSentinel(t *testing.T) {
	noAir := strings.Replace(nmcFixtureWeather, `"aqi": 57, "text": "良"`, `"aqi": 9999, "text": "9999"`, 1)
	m := newNMCMockServer(t, map[string]string{"Wqsps": noAir})

	out, err := runWeather(t, m.srv.URL, map[string]interface{}{"city": "北京"})
	require.NoError(t, err)
	assert.NotContains(t, out, "9999", "AQI 缺测哨兵必须过滤")
	assert.NotContains(t, out, "空气质量", "缺测时不出现空气质量行")
}

// TestWeatherTool_Warning 有预警时输出预警行;无预警(9999)不出"预警"空行。
func TestWeatherTool_Warning(t *testing.T) {
	withWarn := strings.Replace(nmcFixtureWeather,
		`"warn": {"alert": "9999", "issuecontent": "9999", "signaltype": "9999", "signallevel": "9999"}`,
		`"warn": {"alert": "O", "issuecontent": "北京市气象台发布暴雨橙色预警信号", "signaltype": "暴雨", "signallevel": "橙色"}`, 1)
	m := newNMCMockServer(t, map[string]string{"Wqsps": withWarn})

	out, err := runWeather(t, m.srv.URL, map[string]interface{}{"city": "北京"})
	require.NoError(t, err)
	assert.Contains(t, out, "暴雨")
	assert.Contains(t, out, "橙色")
	assert.Contains(t, out, "北京市气象台发布暴雨橙色预警信号")
}

// TestWeatherTool_UpstreamError 上游返回非 0 code/坏 JSON:报"查询失败"类友好错误,不 panic。
func TestWeatherTool_UpstreamError(t *testing.T) {
	m := newNMCMockServer(t, map[string]string{"Wqsps": `{"code":1,"msg":"error","data":""}`})

	_, err := runWeather(t, m.srv.URL, map[string]interface{}{"city": "北京"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "天气")
}

// 编译期占位结束
