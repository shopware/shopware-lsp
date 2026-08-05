// Package textutil contains small, allocation-conscious text transforms shared
// by language frontends and semantic analysis.
package textutil

import "strings"

// CompactWhitespace removes ASCII space, tab, carriage-return, and line-feed
// bytes. It returns source unchanged when no compaction is necessary.
func CompactWhitespace(source string) string {
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case ' ', '\t', '\r', '\n':
			return compactWhitespaceFrom(source, index)
		}
	}
	return source
}

func compactWhitespaceFrom(source string, first int) string {
	var result strings.Builder
	result.Grow(len(source))
	result.WriteString(source[:first])
	start := first + 1
	for index := start; index < len(source); index++ {
		switch source[index] {
		case ' ', '\t', '\r', '\n':
			result.WriteString(source[start:index])
			start = index + 1
		}
	}
	result.WriteString(source[start:])
	return result.String()
}
