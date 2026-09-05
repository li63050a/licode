package search

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	stripBlockREs = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<script[^>]*>[\s\S]*?</script>`),
		regexp.MustCompile(`(?is)<style[^>]*>[\s\S]*?</style>`),
		regexp.MustCompile(`(?is)<noscript[^>]*>[\s\S]*?</noscript>`),
		regexp.MustCompile(`(?is)<head[^>]*>[\s\S]*?</head>`),
		regexp.MustCompile(`(?is)<iframe[^>]*>[\s\S]*?</iframe>`),
		regexp.MustCompile(`(?is)<object[^>]*>[\s\S]*?</object>`),
		regexp.MustCompile(`(?is)<embed[^>]*>[\s\S]*?</embed>`),
		regexp.MustCompile(`(?is)<svg[^>]*>[\s\S]*?</svg>`),
	}
	reComment     = regexp.MustCompile(`(?s)<!--.*?-->`)
	reAnyTag      = regexp.MustCompile(`(?s)<[^>]*>`)
	reTitle       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reMetaDesc    = regexp.MustCompile(`(?is)<meta[^>]+name=["']description["'][^>]+content=["'](.*?)["']`)
	reNumEntity   = regexp.MustCompile(`&#(x?[0-9a-fA-F]+);`)
	entityReplace = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
		"&#39;", "'", "&#x27;", "'", "&nbsp;", " ", "&ensp;", " ", "&emsp;", " ", "&middot;", "·",
		"&#34;", `"`,
	)
)

// decodeEntities 解码命名与数字（十进制/十六进制）HTML 实体。
func decodeEntities(s string) string {
	s = entityReplace.Replace(s)
	return reNumEntity.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-1] // 去掉 &# 与 ;
		base := 10
		if strings.HasPrefix(inner, "x") || strings.HasPrefix(inner, "X") {
			base = 16
			inner = inner[1:]
		}
		n, err := strconv.ParseUint(inner, base, 21)
		if err != nil || n == 0 {
			return m
		}
		return string(rune(n))
	})
}

// ExtractHTML 从网页 HTML 中提取标题与正文纯文本（剔除脚本/样式/注释，合并空白）。
func ExtractHTML(b []byte) (title, text string) {
	s := string(b)
	if m := reTitle.FindStringSubmatch(s); len(m) > 1 {
		title = cleanText(m[1])
	}
	if title == "" {
		if m := reMetaDesc.FindStringSubmatch(s); len(m) > 1 {
			title = cleanText(m[1])
		}
	}
	body := string(b)
	for _, re := range stripBlockREs {
		body = re.ReplaceAllString(body, " ")
	}
	body = reComment.ReplaceAllString(body, " ")
	body = reAnyTag.ReplaceAllString(body, " ")
	text = cleanText(body)
	if len(text) > 200000 {
		text = text[:200000]
	}
	if title == "" && text != "" {
		title = firstRunes(text, 60)
	}
	return title, text
}

// cleanText 去标签残留内的多余空白并合并连续空白。
func cleanText(s string) string {
	s = decodeEntities(s)
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r)
	}), " ")
	return strings.TrimSpace(s)
}

// StripHTMLTags 去掉片段里的残余标签与实体（用于搜索结果摘要）。
func StripHTMLTags(s string) string {
	s = reAnyTag.ReplaceAllString(s, " ")
	return cleanText(s)
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// snippetAround 提取 query 词元首次命中点附近的一段文本作为摘要。
func snippetAround(text string, toks []string, radius int) string {
	if text == "" {
		return ""
	}
	idx := -1
	for _, t := range toks {
		if i := strings.Index(strings.ToLower(text), t); i >= 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return firstRunes(text, radius*2)
	}
	start := idx - radius/2
	if start < 0 {
		start = 0
	}
	end := idx + radius
	r := []rune(text)
	if start > len(r) {
		start = len(r)
	}
	if end > len(r) {
		end = len(r)
	}
	seg := string(r[start:end])
	// 沿边界对齐整词/停用字符
	seg = strings.TrimLeft(seg, " \t\r\n，。；：！？、,.;:!?")
	seg = strings.TrimRight(seg, " \t\r\n") + "…"
	return seg
}
