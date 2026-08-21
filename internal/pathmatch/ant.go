package pathmatch

import (
	"fmt"
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

// Matcher is an immutable set of compiled Ant/LSP-style glob patterns.
type Matcher struct {
	expressions []*regexp.Regexp
}

// Compile validates and compiles patterns for repeated matching.
func Compile(patterns []string) (Matcher, error) {
	result := Matcher{expressions: make([]*regexp.Regexp, 0, len(patterns))}
	for _, pattern := range patterns {
		compiled, err := compileAnt(pattern)
		if err != nil {
			return Matcher{}, fmt.Errorf("compile glob %q: %w", pattern, err)
		}
		result.expressions = append(result.expressions, compiled)
	}
	return result, nil
}

// Match reports whether any compiled pattern matches candidate.
func (m Matcher) Match(candidate string) bool {
	candidate = normalizeCandidate(candidate)
	for _, expression := range m.expressions {
		if expression.MatchString(candidate) {
			return true
		}
	}
	return false
}

// Ant matches slash-normalized paths against Ant-style globs. A double-star
// segment can cross directories; a single star and question mark cannot.
func Ant(pattern, candidate string) bool {
	pattern = normalizePattern(pattern)
	antExpressionCache.RLock()
	compiled, found := antExpressionCache.values[pattern]
	antExpressionCache.RUnlock()
	if found {
		return compiled.MatchString(normalizeCandidate(candidate))
	}
	compiled, err := compileAnt(pattern)
	if err != nil {
		return false
	}
	antExpressionCache.Lock()
	if actual := antExpressionCache.values[pattern]; actual != nil {
		compiled = actual
	} else {
		if len(antExpressionCache.values) >= maximumAntExpressionCacheEntries {
			clear(antExpressionCache.values)
		}
		antExpressionCache.values[pattern] = compiled
	}
	antExpressionCache.Unlock()
	return compiled.MatchString(normalizeCandidate(candidate))
}

func compileAnt(pattern string) (*regexp.Regexp, error) {
	pattern = normalizePattern(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("pattern must not be empty")
	}
	var expression strings.Builder
	expression.WriteByte('^')
	if err := appendAntExpression(&expression, pattern); err != nil {
		return nil, err
	}
	expression.WriteByte('$')
	return regexp.Compile(expression.String())
}

func appendAntExpression(expression *strings.Builder, pattern string) error {
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
		case '[':
			closing := strings.IndexByte(pattern[index+1:], ']')
			if closing < 0 {
				return fmt.Errorf("unclosed character class")
			}
			closing += index + 1
			content := pattern[index+1 : closing]
			if content == "" || content == "!" {
				return fmt.Errorf("empty character class")
			}
			if strings.Contains(content, "/") {
				return fmt.Errorf("character class must not match a path separator")
			}
			expression.WriteByte('[')
			switch content[0] {
			case '!':
				expression.WriteByte('^')
				content = content[1:]
			case '^':
				expression.WriteByte('\\')
			}
			expression.WriteString(strings.ReplaceAll(content, `\`, `\\`))
			expression.WriteByte(']')
			index = closing
		case '{':
			closing := strings.IndexByte(pattern[index+1:], '}')
			if closing < 0 {
				return fmt.Errorf("unclosed alternative group")
			}
			closing += index + 1
			alternatives := strings.Split(pattern[index+1:closing], ",")
			if len(alternatives) < 2 {
				return fmt.Errorf("alternative group must contain a comma")
			}
			expression.WriteString("(?:")
			for alternativeIndex, alternative := range alternatives {
				if alternative == "" {
					return fmt.Errorf("alternative group must not contain an empty value")
				}
				if alternativeIndex > 0 {
					expression.WriteByte('|')
				}
				if err := appendAntExpression(expression, alternative); err != nil {
					return err
				}
			}
			expression.WriteByte(')')
			index = closing
		case ']', '}':
			return fmt.Errorf("unexpected %q", pattern[index])
		default:
			expression.WriteString(
				regexp.QuoteMeta(pattern[index : index+1]),
			)
		}
	}
	return nil
}

func normalizePattern(pattern string) string {
	pattern = filepath.ToSlash(strings.ReplaceAll(strings.TrimSpace(pattern), `\`, "/"))
	return strings.TrimPrefix(pattern, "./")
}

func normalizeCandidate(candidate string) string {
	candidate = filepath.ToSlash(strings.ReplaceAll(candidate, `\`, "/"))
	return strings.TrimPrefix(candidate, "./")
}
