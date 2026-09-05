// Package search 提供自建联网搜索：本地倒排索引（BM25）+ 多源 meta 搜索
// + 网页抓取/预览。全部纯标准库实现，不依赖任何第三方搜索 API。
package search

import (
	"strings"
	"unicode"
)

// Tokenize 把文本切分为检索词元：
//   - 英文/数字混合单词：转小写，长度 >= 2，纯数字剔除；
//   - 中文：相邻两字组成 bigram（"你好世界" → 你好/好世/世界），
//     兼顾召回率与零依赖的无词典分词。
func Tokenize(s string) []string {
	runes := []rune(s)
	var out []string
	var en []rune
	flush := func() {
		if len(en) >= 2 {
			t := strings.ToLower(string(en))
			if !isNumeric(t) {
				out = append(out, t)
			}
		}
		en = en[:0]
	}
	for _, r := range runes {
		if isAlpha(r) || isDigit(r) {
			en = append(en, r)
			continue
		}
		flush()
	}
	flush()
	// 中文 bigram（第二个 pass，避免与上面互斥逻辑纠缠）
	for i := 0; i < len(runes); i++ {
		if isCJK(runes[i]) && i+1 < len(runes) && isCJK(runes[i+1]) {
			out = append(out, string([]rune{runes[i], runes[i+1]}))
		}
	}
	// 去重（索引侧需要集合语义；查询侧调用方按需保留频次）
	if len(out) > 1 {
		seen := make(map[string]bool, len(out))
		uniq := out[:0]
		for _, t := range out {
			if !seen[t] {
				seen[t] = true
				uniq = append(uniq, t)
			}
		}
		return uniq
	}
	return out
}

func isAlpha(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// isCJK 判定中日韩统一表意文字区。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		r >= 0x3040 && r <= 0x30FF || // 假名
		r >= 0xAC00 && r <= 0xD7AF // 谚文
}

func isNumeric(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if !isDigit(r) {
			return false
		}
	}
	return true
}
