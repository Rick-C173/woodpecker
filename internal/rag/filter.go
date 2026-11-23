// Package rag 摄取管道纯逻辑：格式过滤（R1/R1b）、切块（R1 三策略）、拉取解析。
// 上游文档：PRD §6 共享规则、技术选型 §6.2。
package rag

import (
	"encoding/json"
	"path"
	"strings"
)

// supportedExts R1 支持格式清单（按扩展名识别）：
// Markdown、纯文本、常见代码/配置源文件。不在清单内：上传拒绝、仓库/网页静默跳过。
var supportedExts = map[string]bool{
	".md": true, ".markdown": true, ".txt": true,
	".go": true, ".py": true, ".java": true, ".js": true, ".ts": true,
	".tsx": true, ".jsx": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true,
	".rs": true, ".rb": true, ".php": true, ".cs": true, ".swift": true, ".kt": true,
	".sql": true, ".sh": true, ".bash": true, ".yaml": true, ".yml": true,
	".json": true, ".toml": true, ".ini": true, ".xml": true, ".html": true,
	".css": true, ".proto": true, ".graphql": true, ".dockerfile": true, ".makefile": true,
}

// IsSupportedExt R1：扩展名是否受支持（小写比较；Dockerfile/Makefile 等无扩展名特判）。
func IsSupportedExt(p string) bool {
	base := strings.ToLower(path.Base(p))
	if base == "dockerfile" || base == "makefile" || base == "gnumakefile" {
		return true
	}
	return supportedExts[strings.ToLower(path.Ext(p))]
}

// defaultExcludeRules R1b 默认敏感文件排除清单（glob，路径匹配用 /）。
var defaultExcludeRules = []string{
	".env*", "*.pem", "*.key", "id_rsa*", "credentials*", "*secret*", "*.p12", "secrets/",
}

// ExclusionRules R1b 排除规则（settings 键 exclusion_rules 可由管理员覆盖）。
type ExclusionRules struct {
	Patterns []string `json:"patterns"`
}

// ParseExclusionRules 解析 settings 值；空值或解析失败回退默认清单。
func ParseExclusionRules(raw string) ExclusionRules {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ExclusionRules{Patterns: defaultExcludeRules}
	}
	var r ExclusionRules
	if err := json.Unmarshal([]byte(raw), &r); err != nil || len(r.Patterns) == 0 {
		return ExclusionRules{Patterns: defaultExcludeRules}
	}
	return r
}

// IsExcluded 判断相对路径是否命中排除规则：
//   - 形如 secrets/ 的目录规则匹配该路径的任意层目录；
//   - 其余 glob 与完整路径及文件名匹配（.env* 这类点前缀规则只命中文件名）；
//   - 文档自身 ignore_rules（用户配置）与 R1b 清单取并集。
func IsExcluded(relPath string, userRules []string, exclusion ExclusionRules) bool {
	relPath = strings.ReplaceAll(relPath, "\\", "/")
	relPath = strings.TrimPrefix(relPath, "./")
	if relPath == "" {
		return false
	}
	name := path.Base(relPath)
	for _, pat := range append(userRules, exclusion.Patterns...) {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		// 目录规则（以 / 结尾）：匹配任意层目录
		if strings.HasSuffix(pat, "/") {
			dir := strings.TrimSuffix(pat, "/")
			if matchSegment(relPath, dir) {
				return true
			}
			continue
		}
		if matchGlob(relPath, pat) || matchGlob(name, pat) {
			return true
		}
	}
	return false
}

// matchGlob 含 / 的模式与完整路径匹配，否则与任意后缀段匹配（含文件名）。
func matchGlob(s, pattern string) bool {
	if strings.Contains(pattern, "/") {
		return globMatch(s, pattern)
	}
	// 任意目录层下的文件名匹配：/a/b.env 命中 .env*
	for i := 0; i < len(s); i++ {
		if s[i] == '/' && globMatch(s[i+1:], pattern) {
			return true
		}
	}
	return globMatch(s, pattern)
}

// matchSegment 目录名匹配路径中任意一段。
func matchSegment(relPath, dir string) bool {
	for _, seg := range strings.Split(relPath, "/") {
		if globMatch(seg, dir) {
			return true
		}
	}
	return false
}

// globMatch 递归实现 * 与 ? 通配（路径分隔符不特殊处理）。
func globMatch(s, pattern string) bool {
	// 迭代实现，避免 recursion（gocritic 风格）；指针法匹配
	si, pi := 0, 0
	starS, starP := -1, -1
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]):
			si++
			pi++
		case pi < len(pattern) && pattern[pi] == '*':
			starS, starP = si, pi
			pi++
		case starP >= 0:
			starS++
			si = starS
			pi = starP + 1
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
