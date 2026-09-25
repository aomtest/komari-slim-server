package public

import (
	"strings"
	"testing"

	"github.com/aomtest/komari-slim-server/internal/config"
)

// 这组测试守护一个曾经静默失效的替换：
// 服务端原先用 strings.NewReplacer 匹配前端源码里的字面量
// "<title>Komari Monitor</title>"，前端把标题改成 komari-slim 后，
// 站点名注入就不再生效，且没有任何报错或测试失败。
// 现在改为匹配标签结构，下面的用例确保"前端改标题文案"不再破坏它。
func TestInjectSiteConfigReplacesTitleRegardlessOfOriginalText(t *testing.T) {
	// 模拟前端 index.html：标题文案是任意值，服务端都必须能替换
	html := `<html><head><title>whatever-the-frontend-put-here</title></head><body>x</body></html>`

	got := injectSiteConfig(html, map[string]any{
		config.SitenameKey: "我的站点",
	})

	if !strings.Contains(got, "<title>我的站点</title>") {
		t.Fatalf("站点名未注入标题，得到: %s", got)
	}
	if strings.Contains(got, "whatever-the-frontend-put-here") {
		t.Fatalf("旧标题未被替换: %s", got)
	}
}

func TestInjectSiteConfigReplacesAllDescriptionMetas(t *testing.T) {
	html := `<head>` +
		`<meta name="description" content="default desc" />` +
		`<meta property="og:description" content="default desc" />` +
		`<meta name="twitter:description" content="default desc" />` +
		`</head>`

	got := injectSiteConfig(html, map[string]any{
		config.DescriptionKey: "新描述",
	})

	if n := strings.Count(got, `content="新描述"`); n != 3 {
		t.Fatalf("期望 3 处描述被替换，实际 %d 处: %s", n, got)
	}
	if strings.Contains(got, "default desc") {
		t.Fatalf("仍有未替换的默认描述: %s", got)
	}
}

func TestInjectSiteConfigInjectsCustomHeadAndBody(t *testing.T) {
	html := `<html><head></head><body>content</body></html>`

	got := injectSiteConfig(html, map[string]any{
		config.CustomHeadKey: `<script>1</script>`,
		config.CustomBodyKey: `<div>2</div>`,
	})

	if !strings.Contains(got, `<script>1</script></head>`) {
		t.Fatalf("自定义 head 未注入: %s", got)
	}
	if !strings.Contains(got, `<div>2</div></body>`) {
		t.Fatalf("自定义 body 未注入: %s", got)
	}
}

// 站点名里的 $ 不应被 ReplaceAllString 当作反向引用展开。
func TestInjectSiteConfigDoesNotExpandDollarInValues(t *testing.T) {
	html := `<head><title>old</title><meta name="description" content="d" /></head>`

	got := injectSiteConfig(html, map[string]any{
		config.SitenameKey:    "$1$2",
		config.DescriptionKey: "$1",
	})

	if !strings.Contains(got, "<title>$1$2</title>") {
		t.Fatalf("标题里的 $ 被错误展开: %s", got)
	}
	if !strings.Contains(got, `content="$1"`) {
		t.Fatalf("描述里的 $ 被错误展开: %s", got)
	}
}

// 缺少配置项时不应 panic，也不应把占位符留下。
func TestInjectSiteConfigHandlesMissingKeys(t *testing.T) {
	html := `<head><title>old</title></head><body>b</body>`

	got := injectSiteConfig(html, map[string]any{})

	if !strings.Contains(got, "<title></title>") {
		t.Fatalf("缺配置时应注入空标题而非 panic，得到: %s", got)
	}
}
