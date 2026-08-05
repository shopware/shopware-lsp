package pathmatch

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Ant matches slash-normalized paths against Ant-style globs. A double-star
// segment can cross directories; a single star and question mark cannot.
func Ant(pattern, candidate string) bool {
	pattern = filepath.ToSlash(strings.ReplaceAll(
		strings.TrimSpace(pattern),
		`\`,
		"/",
	))
	candidate = filepath.ToSlash(strings.ReplaceAll(candidate, `\`, "/"))
	pattern = strings.TrimPrefix(pattern, "./")
	candidate = strings.TrimPrefix(candidate, "./")
	if pattern == "" {
		return false
	}
	var expression strings.Builder
	expression.WriteByte('^')
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				index++
				if index+1 < len(pattern) && pattern[index+1] == '/' {
					expression.WriteString("(?:.*/)?")
					index++
				} else {
					expression.WriteString(".*")
				}
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(
				regexp.QuoteMeta(pattern[index : index+1]),
			)
		}
	}
	expression.WriteByte('$')
	matched, err := regexp.MatchString(expression.String(), candidate)
	return err == nil && matched
}
