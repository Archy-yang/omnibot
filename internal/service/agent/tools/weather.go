package tools

// weather.go — 天气查询工具,内置(零配置,无 key)。
//
// 数据源:中央气象台 NMC(https://www.nmc.cn)公开 REST 接口,三步链路:
//   1. GET /rest/province/all            → 省列表
//   2. GET /rest/province/{provinceCode} → 该省城市+站点(站点ID是不透明串,如北京城区="Wqsps")
//   3. GET /rest/weather?stationid={id}  → 实况 + 7天预报 + 空气质量 + 预警,纯 JSON
//
// 站点表懒加载 + 7 天 TTL 进程内缓存(参考 valenovo/china-weather-agent-skill 同策略);
// 名称归一化(去 市/区/县/旗 后缀);同名城市不擅自选,返回候选列表引导补 province。
// 9999 为 NMC 缺测哨兵,输出前过滤。
//
// 参考:github.com/valenovo/china-weather-agent-skill(nmc_weather.py 的 Go 移植,裁剪为
// 单一"城市天气查询"场景;逐小时预报/雷达图/排行等重能力未纳入,按需再加)。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	agentpkg "omnibot/internal/service/agent"
)

const (
	nmcDefaultBaseURL      = "https://www.nmc.cn"
	nmcRequestTimeout      = 12 * time.Second
	nmcStationCacheTTL     = 7 * 24 * time.Hour
	nmcMissingSentinel     = "9999" // NMC 缺测哨兵
	nmcMaxForecastDaysText = "7天"
)

// CreateWeatherTool 创建 weather 工具(生产入口,指向真站)。
func CreateWeatherTool() agentpkg.Tool {
	return newWeatherTool(nmcDefaultBaseURL)
}

// newWeatherTool 构造天气工具。baseURL 可注入(httpmock/httptest),测试用。
func newWeatherTool(baseURL string) agentpkg.Tool {
	client := &nmcClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: nmcRequestTimeout},
	}
	return agentpkg.Tool{
		Name:         "weather",
		DisplayLabel: "查询了天气",
		Capabilities: []string{agentpkg.CapResearch, agentpkg.CapBasic},
		Description: "查询中国城市天气:实况(温度/体感/风/湿度)、未来7天预报、空气质量(AQI)、生效预警。" +
			"传 city(如 北京/海淀/朝阳);同名城市(如朝阳)传 province(如 北京市)消歧。" +
			"仅支持中国境内城市。",
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"city"},
			"properties": map[string]interface{}{
				"city": map[string]interface{}{
					"type":        "string",
					"description": "城市/区县名,如 北京、海淀、朝阳",
				},
				"province": map[string]interface{}{
					"type":        "string",
					"description": "省/直辖市名,同名城市消歧用,如 北京市。非必填",
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			city, _ := args["city"].(string)
			city = strings.TrimSpace(city)
			if city == "" {
				return "", fmt.Errorf("city 参数不能为空,如 北京")
			}
			province, _ := args["province"].(string)

			station, err := client.resolveStation(ctx, city, province)
			if err != nil {
				return "", err
			}
			wx, err := client.fetchWeather(ctx, station.Code)
			if err != nil {
				return "", fmt.Errorf("天气查询失败(%s): %w", station.City, err)
			}
			return formatWeather(station, wx), nil
		},
	}
}

// ---- 站点解析 ----

// nmcStation NMC 城市站点(站点 Code 是不透明串,非气象站号)。
type nmcStation struct {
	Code     string `json:"code"`
	Province string `json:"province"`
	City     string `json:"city"`
}

// nmcClient NMC REST 客户端 + 站点表缓存。
type nmcClient struct {
	baseURL string
	http    *http.Client

	mu           sync.Mutex
	stations     []nmcStation
	stationsLoad time.Time
}

// loadStations 拉取全国站点表(省列表 → 逐省城市),缓存 TTL 7 天。
// 全国约 2000+ 站,首次调用多几次请求,之后零开销。
func (c *nmcClient) loadStations(ctx context.Context) ([]nmcStation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stations != nil && time.Since(c.stationsLoad) < nmcStationCacheTTL {
		return c.stations, nil
	}

	var provinces []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := c.getJSON(ctx, "/rest/province/all", &provinces); err != nil {
		return nil, fmt.Errorf("获取省列表失败: %w", err)
	}
	if len(provinces) == 0 {
		return nil, fmt.Errorf("获取省列表失败: 返回为空")
	}

	var all []nmcStation
	// 逐省拉城市表:并发(小工作池)控制首调延迟——串行 34 省实测 ~15s,并发后 ~2s。
	// 单省失败不拖垮整体(站点表照常建),跳过。
	const workers = 8
	type provinceResult struct {
		stations []nmcStation
	}
	sem := make(chan struct{}, workers)
	results := make(chan provinceResult, len(provinces))
	var wg sync.WaitGroup
	for _, p := range provinces {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var sts []nmcStation
			if err := c.getJSON(ctx, "/rest/province/"+code, &sts); err == nil {
				results <- provinceResult{stations: sts}
			}
		}(p.Code)
	}
	wg.Wait()
	close(results)
	for r := range results {
		all = append(all, r.stations...)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("获取城市站点表失败")
	}
	c.stations = all
	c.stationsLoad = time.Now()
	return all, nil
}

// resolveStation 城市名 → 站点。归一化匹配;0 命中报"未找到";多命中且未给 province
// 时返回候选列表(同名城市不擅自选,如 朝阳:北京/辽宁)。
func (c *nmcClient) resolveStation(ctx context.Context, city, province string) (*nmcStation, error) {
	stations, err := c.loadStations(ctx)
	if err != nil {
		return nil, err
	}
	wantCity := normalizePlaceName(city)
	wantProv := normalizePlaceName(province)

	var hits []nmcStation
	for _, s := range stations {
		if normalizePlaceName(s.City) != wantCity {
			continue
		}
		if wantProv != "" && normalizePlaceName(s.Province) != wantProv {
			continue
		}
		hits = append(hits, s)
	}
	switch {
	case len(hits) == 0:
		if wantProv != "" {
			return nil, fmt.Errorf("未找到城市: %s(%s省/市)。请确认名称", city, province)
		}
		return nil, fmt.Errorf("未找到城市: %s。请确认名称;仅支持中国境内城市", city)
	case len(hits) == 1:
		return &hits[0], nil
	default:
		// 多命中但都同省(如同名区县不同市但省相同的情况极少)或已按 province 过滤仍多:
		// 取第一个并附候选信息;否则报歧义。
		if wantProv != "" {
			return &hits[0], nil
		}
		var candidates []string
		for _, h := range hits {
			candidates = append(candidates, h.Province+" "+h.City)
		}
		return nil, fmt.Errorf("\"%s\"有多个匹配地,请补 province 参数消歧。候选: %s",
			city, strings.Join(candidates, " / "))
	}
}

// normalizePlaceName 地名归一化:去空白与行政区划后缀(市/区/县/旗/盟/自治州等),
// 使 "北京市"≈"北京"、"海淀区"≈"海淀"。
func normalizePlaceName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	for _, suffix := range []string{"特别行政区", "维吾尔自治区", "回族自治区", "壮族自治区", "自治区", "自治州", "地区", "林区", "新区", "市", "区", "县", "旗", "盟"} {
		if strings.HasSuffix(s, suffix) && len(s) > len(suffix) {
			s = strings.TrimSuffix(s, suffix)
			break // 一次去一层("市辖区"类多次嵌套不在天气场景)
		}
	}
	return s
}

// ---- 天气查询与解析 ----

// nmcWeather /rest/weather 返回结构(只取需要字段)。
type nmcWeather struct {
	Code int `json:"code"`
	Data struct {
		Real struct {
			PublishTime string `json:"publish_time"`
			Weather     struct {
				Info        string  `json:"info"`
				Temperature float64 `json:"temperature"`
				FeelSt      float64 `json:"feelst"`
				Humidity    float64 `json:"humidity"`
			} `json:"weather"`
			Wind struct {
				Direct string `json:"direct"`
				Power  string `json:"power"`
			} `json:"wind"`
			Warn struct {
				IssueContent string `json:"issuecontent"`
				SignalType   string `json:"signaltype"`
				SignalLevel  string `json:"signallevel"`
			} `json:"warn"`
		} `json:"real"`
		Air struct {
			AQI  int    `json:"aqi"`
			Text string `json:"text"`
		} `json:"air"`
		Predict struct {
			PublishTime string `json:"publish_time"`
			Detail      []struct {
				Date string `json:"date"`
				Day  struct {
					Weather struct {
						Info        string `json:"info"`
						Temperature string `json:"temperature"`
					} `json:"weather"`
				} `json:"day"`
				Night struct {
					Weather struct {
						Info        string `json:"info"`
						Temperature string `json:"temperature"`
					} `json:"weather"`
				} `json:"night"`
				Precipitation float64 `json:"precipitation"`
			} `json:"detail"`
		} `json:"predict"`
	} `json:"data"`
}

func (c *nmcClient) getJSON(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	// NMC 对无 UA/Referer 的请求可能拒绝(实测必须带)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/124.0 Safari/537.36")
	req.Header.Set("Referer", nmcDefaultBaseURL+"/")
	req.Header.Set("Accept", "application/json,text/plain,*/*")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("上游返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (c *nmcClient) fetchWeather(ctx context.Context, stationID string) (*nmcWeather, error) {
	var wx nmcWeather
	if err := c.getJSON(ctx, "/rest/weather?stationid="+stationID, &wx); err != nil {
		return nil, err
	}
	if wx.Code != 0 {
		return nil, fmt.Errorf("上游返回异常(code=%d)", wx.Code)
	}
	return &wx, nil
}

// formatWeather 组织给 LLM 读的紧凑文本。缺测(9999)过滤:白天缺测用夜间兜底。
func formatWeather(s *nmcStation, wx *nmcWeather) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s(%s) 实况", s.City, s.Province)
	if t := wx.Data.Real.PublishTime; t != "" && t != nmcMissingSentinel {
		fmt.Fprintf(&b, " [%s发布]", t)
	}
	b.WriteByte('\n')

	r := wx.Data.Real.Weather
	fmt.Fprintf(&b, "%s %.1f°C", orDash(r.Info), r.Temperature)
	if r.FeelSt != 0 {
		fmt.Fprintf(&b, "(体感 %.1f°C)", r.FeelSt)
	}
	w := wx.Data.Real.Wind
	if w.Direct != "" && w.Direct != nmcMissingSentinel {
		fmt.Fprintf(&b, " %s%s", w.Direct, w.Power)
	}
	if r.Humidity != 0 {
		fmt.Fprintf(&b, ",湿度 %.0f%%", r.Humidity)
	}
	b.WriteByte('\n')

	air := wx.Data.Air
	// AQI 缺测时上游给 9999(live smoke 实测),不得当作有效值输出
	if air.AQI > 0 && air.AQI != 9999 {
		if air.Text != "" {
			fmt.Fprintf(&b, "空气质量: AQI %d(%s)\n", air.AQI, air.Text)
		} else {
			fmt.Fprintf(&b, "空气质量: AQI %d\n", air.AQI)
		}
	}

	warn := wx.Data.Real.Warn
	if warn.IssueContent != "" && warn.IssueContent != nmcMissingSentinel {
		fmt.Fprintf(&b, "⚠ 预警: %s %s - %s\n", warn.SignalType, warn.SignalLevel, warn.IssueContent)
	}

	if len(wx.Data.Predict.Detail) > 0 {
		fmt.Fprintf(&b, "%s预报:\n", nmcMaxForecastDaysText)
		for _, d := range wx.Data.Predict.Detail {
			info := d.Day.Weather.Info
			hi := d.Day.Weather.Temperature
			if info == nmcMissingSentinel || hi == nmcMissingSentinel { // 白天缺测用夜间兜底
				info = d.Night.Weather.Info
				hi = ""
			}
			lo := d.Night.Weather.Temperature
			line := fmt.Sprintf("%s %s", d.Date[5:], orDash(info))
			switch {
			case hi != "" && hi != nmcMissingSentinel && lo != "" && lo != nmcMissingSentinel:
				line += fmt.Sprintf(" %s~%s°C", lo, hi)
			case lo != "" && lo != nmcMissingSentinel:
				line += fmt.Sprintf(" %s°C", lo)
			}
			if d.Precipitation > 0 {
				line += fmt.Sprintf(" 降水 %.1fmm", d.Precipitation)
			}
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// orDash 缺测哨兵/空值 → "-"。
func orDash(s string) string {
	if s == "" || s == nmcMissingSentinel {
		return "-"
	}
	return s
}
