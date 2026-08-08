package pathmatch

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const maximumAntExpressionCacheEntries = 1024

var antExpressionCache = struct {
	sync.RWMutex
	values map[string]*regexp.Regexp
}{values: make(map[string]*regexp.Regexp)}

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
	antExpressionCache.RLock()
	compiled, found := antExpressionCache.values[pattern]
	antExpressionCache.RUnlock()
	if found {
		return compiled.MatchString(candidate)
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
	matcher, err := regexp.Compile(expression.String())
	if err != nil {
		return false
	}
	antExpressionCache.Lock()
	if actual := antExpressionCache.values[pattern]; actual != nil {
		matcher = actual
	} else {
		if len(antExpressionCache.values) >= maximumAntExpressionCacheEntries {
			clear(antExpressionCache.values)
		}
		antExpressionCache.values[pattern] = matcher
	}
	antExpressionCache.Unlock()
	return matcher.MatchString(candidate)
}
