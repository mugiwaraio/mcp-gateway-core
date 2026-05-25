// Package config 提供 mcp-gateway 系列网关共用的配置加载助手：
//   - ExpandEnvVars: YAML 文本 ${VAR} 展开（注释行识别 + missing 收集）
//   - ValidatePATToken: PAT 字符串通用校验（前缀 + 拒 PLACEHOLDER）
//   - PATPrefix: 三库统一的 PAT 前缀常量
//
// 不抽 Config struct 本体：DB / Loki / Prometheus 字段各异，schema 留 consumer
// 自己维护；core 只抽这些跨库一字不差的通用基元。
package config

import (
	"os"
	"regexp"
	"strings"
)

// EnvVarPattern 匹配 ${NAME}，仅允许字母数字下划线。
// 导出供 caller 测试或自定义校验场景；一般直接用 ExpandEnvVars 即可。
var EnvVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnvVars 把 s 中所有 ${VAR} 替换为 os.Getenv("VAR")，返回 (expanded, missing)。
//
// 不存在或为空的环境变量会被替换为空字符串（fail-open）；caller 应在启动期对
// 返回的 missing 列表打 WARN，便于及早发现"配置引用但未注入"的字段缺失。
//
// missing 仅统计**非注释行**中的 ${VAR}：YAML 注释里写示例用的占位符（如
// `# admin_password: "${MYSQL_ADMIN_PASSWORD}"`）不应被当作"运维忘了注入"。
// 注释行仍执行替换（替换为空即可，YAML 后续解析会忽略整行），只是不计入 missing。
//
// missing 去重保序（同一变量多次出现只入一次，按首次位置排）。
func ExpandEnvVars(s string) (expanded string, missing []string) {
	commentLines := findCommentLines(s)
	seen := map[string]bool{}
	out := EnvVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		val, ok := os.LookupEnv(name)
		if (!ok || val == "") && !seen[name] {
			// 检查该 match 是否位于注释行
			idx := strings.Index(s, match)
			if idx >= 0 && !inCommentLine(commentLines, idx) {
				seen[name] = true
				missing = append(missing, name)
			}
		}
		return val
	})
	return out, missing
}

// findCommentLines 返回 s 中"YAML 注释区域"的 [start, end) 区间集合。
// 覆盖两种形态：
//  1. 整行注释：首个非空白字符是 # → 整行算注释
//  2. 行尾注释：行内出现引号外的 # → [#位置, 行尾) 算注释
//
// 引号内的 # 是字面字符（YAML 标准），不开启注释。简化引号识别为单/双引号配对，
// 不处理转义嵌套（实际配置一般不会写出 `"a\"#b"` 这种），足够覆盖示例占位场景。
func findCommentLines(s string) [][2]int {
	var out [][2]int
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			trimmed := strings.TrimLeft(line, " \t")
			switch {
			case strings.HasPrefix(trimmed, "#"):
				out = append(out, [2]int{start, i})
			default:
				if h := indexHashOutsideQuotes(line); h >= 0 {
					out = append(out, [2]int{start + h, i})
				}
			}
			start = i + 1
		}
	}
	return out
}

// indexHashOutsideQuotes 返回 line 中第一个不在引号内的 `#` 字节位置；找不到返回 -1。
// 引号识别：" 或 ' 配对，配对内的 # 视为字面字符不开启注释。
func indexHashOutsideQuotes(line string) int {
	inDouble, inSingle := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '#' && !inDouble && !inSingle:
			return i
		}
	}
	return -1
}

func inCommentLine(ranges [][2]int, pos int) bool {
	for _, r := range ranges {
		if pos >= r[0] && pos < r[1] {
			return true
		}
	}
	return false
}
