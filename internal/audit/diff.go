package audit

import (
	"fmt"
	"strings"
)

// UnifiedDiff 生成旧/新内容之间的 unified diff（用于修复预览）。
// 实现基于最长公共子序列的按行 diff，兼容人类与大多数工具阅读。
func UnifiedDiff(path string, oldLines, newLines []string) string {
	oldText := strings.Split(strings.Join(oldLines, "\n"), "\n")
	newText := strings.Split(strings.Join(newLines, "\n"), "\n")
	if len(oldText) == 1 && oldText[0] == "" {
		oldText = nil
	}
	if len(newText) == 1 && newText[0] == "" {
		newText = nil
	}

	var sb strings.Builder
	sb.WriteString("--- a/" + path + "\n")
	sb.WriteString("+++ b/" + path + "\n")

	// 计算编辑脚本（LCS）
	ops := diffOps(oldText, newText)

	// 分块：把相邻操作聚合成 hunk（间隔 <= 阈值行之内合并）
	threshold := 3
	var hunks []string
	var cur []struct {
		op   byte // ' ', '+', '-'
		text string
	}
	var curOldStart, curNewStart, curOldCnt, curNewCnt int
	flush := func() {
		if len(cur) == 0 {
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", curOldStart, curOldCnt, curNewStart, curNewCnt)
		for _, l := range cur {
			b.WriteString(string(l.op) + l.text + "\n")
		}
		hunks = append(hunks, b.String())
		cur = nil
	}

	oldIdx, newIdx := 0, 0
	for _, op := range ops {
		switch op.kind {
		case 'c':
			if len(cur) == 0 {
				curOldStart, curNewStart = oldIdx+1, newIdx+1
			}
			cur = append(cur, struct {
				op   byte
				text string
			}{' ', op.text})
			oldIdx++
			newIdx++
			curOldCnt = oldIdx - curOldStart + 1
			curNewCnt = newIdx - curNewStart + 1
		case '-':
			if len(cur) == 0 {
				curOldStart, curNewStart = oldIdx+1, newIdx+1
			} else if oldIdx+1-curOldStart > threshold || newIdx+1-curNewStart > threshold {
				flush()
				curOldStart, curNewStart = oldIdx+1, newIdx+1
				curOldCnt, curNewCnt = 0, 0
			}
			cur = append(cur, struct {
				op   byte
				text string
			}{'-', op.text})
			oldIdx++
			curOldCnt = oldIdx - curOldStart + 1
			curNewCnt = newIdx - curNewStart + 1
		case '+':
			if len(cur) == 0 {
				curOldStart, curNewStart = oldIdx+1, newIdx+1
			} else if oldIdx+1-curOldStart > threshold || newIdx+1-curNewStart > threshold {
				flush()
				curOldStart, curNewStart = oldIdx+1, newIdx+1
				curOldCnt, curNewCnt = 0, 0
			}
			cur = append(cur, struct {
				op   byte
				text string
			}{'+', op.text})
			newIdx++
			curOldCnt = oldIdx - curOldStart + 1
			curNewCnt = newIdx - curNewStart + 1
		}
	}
	flush()

	if len(hunks) == 0 {
		return sb.String() // 无改动：只有头
	}
	for _, h := range hunks {
		sb.WriteString(h)
	}
	return sb.String()
}

// diffOp 表示一个 LCS 编辑操作。
type diffOp struct {
	kind byte // 'c' common, '-' delete, '+' insert
	text string
}

// diffOps 用 LCS 动态规划求老/新文本的编辑操作序列。
func diffOps(oldText, newText []string) []diffOp {
	n, m := len(oldText), len(newText)
	// dp[i][j] = LCS 长度
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if oldText[i-1] == newText[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	var ops []diffOp
	i, j := n, m
	for i > 0 && j > 0 {
		if oldText[i-1] == newText[j-1] {
			ops = append(ops, diffOp{'c', oldText[i-1]})
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			ops = append(ops, diffOp{'-', oldText[i-1]})
			i--
		} else {
			ops = append(ops, diffOp{'+', newText[j-1]})
			j--
		}
	}
	for i > 0 {
		ops = append(ops, diffOp{'-', oldText[i-1]})
		i--
	}
	for j > 0 {
		ops = append(ops, diffOp{'+', newText[j-1]})
		j--
	}
	// 反转成从头到尾的顺序
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}
