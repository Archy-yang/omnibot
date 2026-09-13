package subscription

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// RSS 自动发现测试(14-订阅源管理技术方案 §8 测试清单#1~#5):
// head 声明命中、约定路径兜底、零结果明确报错、多候选全量返回、非法候选过滤。

const validRSS = `<?xml version="1.0"?>
<rss version="2.0"><channel>
<title>测试博客</title><link>http://example.test/</link><description>测试用</description>
<item><title>第一篇</title><link>http://example.test/1</link></item>
</channel></rss>`

const validAtom = `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<title>原子博客</title><link href="http://example.test/"/><entry><title>条目一</title></entry>
</feed>`

// pageWith:包一层最小 HTML,注入 head 片段(如 feed <link> 声明)
func pageWith(links string) string {
	return fmt.Sprintf(`<!doctype html><html><head><title>某站</title>%s</head><body>正文</body></html>`, links)
}

// serve:按路径返回内容的最小站点
func serve(t *testing.T, paths map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := paths[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if body == "RSS" {
			body = validRSS
		} else if body == "ATOM" {
			body = validAtom
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscover_LinkTag(t *testing.T) {
	srv := serve(t, map[string]string{
		"/":         pageWith(`<link rel="alternate" type="application/rss+xml" href="/feed.xml">`),
		"/feed.xml": "RSS",
	})
	d := NewDiscoverer()

	feeds, err := d.Discover(srv.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if got := feeds[0].FeedURL; got != srv.URL+"/feed.xml" {
		t.Fatalf("expected resolved absolute feed url %s/feed.xml, got %s", srv.URL, got)
	}
	if feeds[0].Title != "测试博客" {
		t.Fatalf("expected title from feed metadata, got %q", feeds[0].Title)
	}
}

func TestDiscover_FallbackPaths(t *testing.T) {
	// 无 head 声明,/feed 约定路径命中
	srv := serve(t, map[string]string{
		"/":     pageWith(""),
		"/feed": "RSS",
	})
	d := NewDiscoverer()

	feeds, err := d.Discover(srv.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(feeds) != 1 || feeds[0].FeedURL != srv.URL+"/feed" {
		t.Fatalf("expected fallback /feed hit, got %+v err=%v", feeds, err)
	}
}

func TestDiscover_FallbackPathUnderSubpath(t *testing.T) {
	// 博客挂在主机子路径下(/blog/),feed 在 /blog/atom.xml,主机根无任何 feed。
	// 约定路径必须同时按用户给定子路径拼接(真实案例:ruanyifeng.com/blog/)。
	srv := serve(t, map[string]string{
		"/":              pageWith(""),
		"/blog/":         pageWith(""),
		"/blog/atom.xml": "ATOM",
	})
	d := NewDiscoverer()

	feeds, err := d.Discover(srv.URL + "/blog/")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(feeds) != 1 || feeds[0].FeedURL != srv.URL+"/blog/atom.xml" {
		t.Fatalf("expected subpath /blog/atom.xml hit, got %+v err=%v", feeds, err)
	}
}

func TestDiscover_NoFeed(t *testing.T) {
	srv := serve(t, map[string]string{"/": pageWith("")})
	d := NewDiscoverer()

	if _, err := d.Discover(srv.URL); err == nil {
		t.Fatal("expected explicit error when no feed found")
	}
}

func TestDiscover_MultipleFeeds(t *testing.T) {
	srv := serve(t, map[string]string{
		"/": pageWith(
			`<link rel="alternate" type="application/rss+xml" href="/feed.xml">` +
				`<link rel="alternate" type="application/atom+xml" href="/atom.xml">`),
		"/feed.xml": "RSS",
		"/atom.xml": "ATOM",
	})
	d := NewDiscoverer()

	feeds, err := d.Discover(srv.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(feeds) != 2 {
		t.Fatalf("expected 2 candidates returned for user to pick, got %d", len(feeds))
	}
}

func TestDiscover_InvalidCandidate(t *testing.T) {
	// 声明存在但内容是普通 HTML(非法 feed) → 过滤,不冒充结果
	srv := serve(t, map[string]string{
		"/":       pageWith(`<link rel="alternate" type="application/rss+xml" href="/broken">`),
		"/broken": pageWith(""),
	})
	d := NewDiscoverer()

	if _, err := d.Discover(srv.URL); err == nil {
		t.Fatal("expected error: declared but invalid feed must be filtered out")
	}
}
