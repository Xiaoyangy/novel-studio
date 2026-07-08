package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// WebResearchTool Architect 的联网研究工具：按查询搜索网页（免 key 的
// DuckDuckGo HTML 端点），或抓取指定 URL 的正文摘要。产出同时追加到
// meta/web_research_log.md 留痕——写作侧引用网络事实必须能审计到来源，
// 之后 build-rag / zero-init 扫描项目文件时自然进入种子索引。
//
// 设计边界：
//   - 只做设定研究（题材现实支架、种族/文化/制度谱系、行业细节），不拉热点炒作；
//   - 结果是"参考素材"，转化规则仍由 web_reference_brief / 写作规范约束；
//   - SSRF 防护：拒绝解析到私网/回环地址的目标。
type WebResearchTool struct {
	store  *store.Store
	client *http.Client
}

func NewWebResearchTool(st *store.Store) *WebResearchTool {
	return &WebResearchTool{
		store: st,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (t *WebResearchTool) Name() string { return "web_research" }
func (t *WebResearchTool) Description() string {
	return "联网研究：query=搜索网页（返回标题/链接/摘要），url=抓取该网页正文摘要。用于题材现实支架、种族/文化/制度谱系、行业与地域细节的资料补全；结果自动登记到 meta/web_research_log.md。purpose 必填：这次检索要解决什么设定问题。"
}
func (t *WebResearchTool) Label() string { return "联网研究" }

func (t *WebResearchTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *WebResearchTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *WebResearchTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("purpose", schema.String("这次检索要解决的设定问题（写入研究台账）")).Required(),
		schema.Property("query", schema.String("搜索关键词；与 url 二选一")),
		schema.Property("url", schema.String("要抓取正文的网页地址；与 query 二选一")),
		schema.Property("max_results", schema.Int("搜索结果数上限，默认 6，最大 10")),
	)
}

type webResearchArgs struct {
	Purpose    string `json:"purpose"`
	Query      string `json:"query"`
	URL        string `json:"url"`
	MaxResults int    `json:"max_results"`
}

type webSearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (t *WebResearchTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args webResearchArgs
	if err := unmarshalToolArgs(raw, &args); err != nil {
		return nil, err
	}
	args.Purpose = strings.TrimSpace(args.Purpose)
	args.Query = strings.TrimSpace(args.Query)
	args.URL = strings.TrimSpace(args.URL)
	if args.Purpose == "" {
		return nil, fmt.Errorf("purpose 必填：说明这次检索要解决什么设定问题: %w", errs.ErrToolArgs)
	}
	if (args.Query == "") == (args.URL == "") {
		return nil, fmt.Errorf("query 与 url 必须二选一: %w", errs.ErrToolArgs)
	}

	result := map[string]any{"purpose": args.Purpose}
	var logBody strings.Builder
	switch {
	case args.Query != "":
		hits, err := t.search(ctx, args.Query, args.MaxResults)
		if err != nil {
			return nil, fmt.Errorf("web_research 搜索失败（可稍后重试或换 url 直抓）: %w", err)
		}
		result["query"] = args.Query
		result["results"] = hits
		fmt.Fprintf(&logBody, "查询：%s\n\n", args.Query)
		for _, hit := range hits {
			fmt.Fprintf(&logBody, "- [%s](%s) — %s\n", hit.Title, hit.URL, hit.Snippet)
		}
	default:
		text, title, err := t.fetchReadable(ctx, args.URL)
		if err != nil {
			return nil, fmt.Errorf("web_research 抓取失败: %w", err)
		}
		result["url"] = args.URL
		result["title"] = title
		result["excerpt"] = text
		fmt.Fprintf(&logBody, "抓取：[%s](%s)\n\n%s\n", title, args.URL, text)
	}
	result["retrieved_at"] = time.Now().Format(time.RFC3339)
	result["usage_note"] = "参考素材：转化为本书设定时须换名/换皮并遵守 web_reference 使用边界，不得原文照搬。"
	t.appendLog(args.Purpose, logBody.String())

	out, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return out, nil
}

var (
	ddgResultRe        = regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe       = regexp.MustCompile(`(?s)<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
	bingResultRe       = regexp.MustCompile(`(?s)<h2[^>]*>.*?<a\s[^>]*?href="(https?://[^"]+)"[^>]*>(.*?)</a>`)
	bingSnippetRe      = regexp.MustCompile(`(?s)<p class="[^"]*b_[^"]*"[^>]*>(.*?)</p>`)
	bingInternalLinkRe = regexp.MustCompile(`(?i)^https?://([a-z0-9.-]+\.)?(bing\.com|microsoft\.com|msn\.|go\.microsoft)`)
	tagRe              = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe            = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLineRe        = regexp.MustCompile(`\n{3,}`)
	scriptRe           = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head)[^>]*>.*?</\s*(script|style|noscript|svg|head)\s*>`)
	titleRe            = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func (t *WebResearchTool) search(ctx context.Context, query string, maxResults int) ([]webSearchHit, error) {
	if maxResults <= 0 {
		maxResults = 6
	}
	if maxResults > 10 {
		maxResults = 10
	}
	// Bing 主、DDG 兜底：DDG 的 html 端点在部分网络（如国内）不可达，cn.bing.com 直连可达
	// 且全球通用；先 Bing，空/失败再回退 DDG，两者都失败才报错。
	hits, bingErr := t.searchBing(ctx, query, maxResults)
	if bingErr == nil && len(hits) > 0 {
		return hits, nil
	}
	ddgHits, ddgErr := t.searchDDG(ctx, query, maxResults)
	if ddgErr == nil && len(ddgHits) > 0 {
		return ddgHits, nil
	}
	if bingErr != nil || ddgErr != nil {
		return nil, fmt.Errorf("搜索失败（Bing: %v；DDG: %v）", bingErr, ddgErr)
	}
	return nil, fmt.Errorf("搜索引擎未返回结果（可能被限流）")
}

// searchBing 用 cn.bing.com 检索并解析 <h2> 标题锚点为结果（含最佳努力的摘要）。
func (t *WebResearchTool) searchBing(ctx context.Context, query string, maxResults int) ([]webSearchHit, error) {
	endpoint := "https://cn.bing.com/search?setlang=zh-CN&ensearch=0&q=" + url.QueryEscape(query)
	body, err := t.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	matches := bingResultRe.FindAllStringSubmatch(body, -1)
	snippets := bingSnippetRe.FindAllStringSubmatch(body, -1)
	hits := make([]webSearchHit, 0, maxResults)
	for _, m := range matches {
		u := m[1]
		if bingInternalLinkRe.MatchString(u) { // 跳过 bing/微软内链
			continue
		}
		title := cleanHTMLText(m[2])
		if title == "" {
			continue
		}
		hit := webSearchHit{URL: html.UnescapeString(u), Title: title}
		if len(hits) < len(snippets) {
			hit.Snippet = cleanHTMLText(snippets[len(hits)][1])
		}
		hits = append(hits, hit)
		if len(hits) >= maxResults {
			break
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("Bing 未返回结果（可能被限流或页面结构变化）")
	}
	return hits, nil
}

// searchDDG 用 DuckDuckGo html 端点检索（部分网络不可达，作兜底）。
func (t *WebResearchTool) searchDDG(ctx context.Context, query string, maxResults int) ([]webSearchHit, error) {
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	body, err := t.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	links := ddgResultRe.FindAllStringSubmatch(body, maxResults)
	snippets := ddgSnippetRe.FindAllStringSubmatch(body, maxResults)
	if len(links) == 0 {
		return nil, fmt.Errorf("DDG 未返回结果（可能被限流）")
	}
	hits := make([]webSearchHit, 0, len(links))
	for i, m := range links {
		hit := webSearchHit{
			URL:   decodeDDGLink(m[1]),
			Title: cleanHTMLText(m[2]),
		}
		if i < len(snippets) {
			hit.Snippet = cleanHTMLText(snippets[i][1])
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

// decodeDDGLink DDG HTML 端点的链接是 /l/?uddg=<escaped> 形式。
func decodeDDGLink(raw string) string {
	raw = html.UnescapeString(raw)
	if u, err := url.Parse(raw); err == nil {
		if target := u.Query().Get("uddg"); target != "" {
			if decoded, err := url.QueryUnescape(target); err == nil {
				return decoded
			}
		}
	}
	return raw
}

func (t *WebResearchTool) fetchReadable(ctx context.Context, target string) (text, title string, err error) {
	body, err := t.get(ctx, target)
	if err != nil {
		return "", "", err
	}
	if m := titleRe.FindStringSubmatch(body); len(m) > 1 {
		title = cleanHTMLText(m[1])
	}
	cleaned := cleanHTMLText(scriptRe.ReplaceAllString(body, " "))
	const maxRunes = 4000
	runes := []rune(cleaned)
	if len(runes) > maxRunes {
		cleaned = string(runes[:maxRunes]) + "\n…（正文超长已截断）"
	}
	return cleaned, title, nil
}

func (t *WebResearchTool) get(ctx context.Context, target string) (string, error) {
	if err := guardWebTarget(target); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) novel-studio-research/1.0")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.6")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	const maxBody = 2 << 20 // 2MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// guardWebTarget SSRF 防护：只允许 http/https，且目标不得解析到回环/私网地址。
func guardWebTarget(target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("非法 URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("仅支持 http/https，收到 %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("解析主机失败: %w", err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("目标 %s 解析到受限地址 %s，已拒绝", host, ip)
		}
	}
	return nil
}

func cleanHTMLText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = spaceRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			kept = append(kept, line)
		}
	}
	s = strings.Join(kept, "\n")
	return strings.TrimSpace(blankLineRe.ReplaceAllString(s, "\n\n"))
}

// appendLog 研究台账：meta/web_research_log.md，追加式留痕。
func (t *WebResearchTool) appendLog(purpose, body string) {
	path := filepath.Join(t.store.Dir(), "meta", "web_research_log.md")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n## %s · %s\n\n%s\n", time.Now().Format("2006-01-02 15:04"), purpose, body)
}
