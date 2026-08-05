// Package suppression parses source-level diagnostic suppression directives
// and matches them to Shopware LSP's stable PHP diagnostic identifiers.
package suppression

import (
	"sort"
	"strings"
	"unicode"
)

type directive struct {
	identifiers []string
	wildcard    bool
}

// Set is an immutable, line-indexed collection of suppression directives.
// Source files without supported markers retain a zero-value Set.
type Set struct {
	lineStarts []int
	lines      map[int][]directive
}

// Parse recognizes PHPStan's ignore, ignore-line, and ignore-next-line forms,
// plus JetBrains' noinspection form. Ordinary ignore/noinspection comments
// apply to their inline statement or the next non-comment source line.
func Parse(source string) Set {
	if source == "" ||
		(!containsFoldASCII(source, "@phpstan-ignore") &&
			!containsFoldASCII(source, "@noinspection") &&
			!containsFoldASCII(source, "noinspection")) {
		return Set{}
	}
	textLines, starts := sourceLines(source)
	result := Set{
		lineStarts: starts,
		lines:      make(map[int][]directive),
	}
	for lineIndex, line := range textLines {
		lower := strings.ToLower(line)
		if marker := strings.Index(lower, "@phpstan-ignore-next-line"); marker >= 0 {
			result.add(
				lineIndex+1,
				parseDirective(line[marker+len("@phpstan-ignore-next-line"):], false),
				len(textLines),
			)
			continue
		}
		if marker := strings.Index(lower, "@phpstan-ignore-line"); marker >= 0 {
			result.add(
				lineIndex,
				parseDirective(line[marker+len("@phpstan-ignore-line"):], false),
				len(textLines),
			)
			continue
		}
		if marker := strings.Index(lower, "@phpstan-ignore"); marker >= 0 {
			target := directiveTarget(textLines, lineIndex, marker)
			result.add(
				target,
				parseDirective(line[marker+len("@phpstan-ignore"):], false),
				len(textLines),
			)
			continue
		}
		marker, length := noInspectionMarker(lower)
		if marker >= 0 {
			target := directiveTarget(textLines, lineIndex, marker)
			result.add(
				target,
				parseDirective(line[marker+length:], true),
				len(textLines),
			)
		}
	}
	if len(result.lines) == 0 {
		return Set{}
	}
	return result
}

func (set Set) add(line int, value directive, lineCount int) {
	if line < 0 || line >= lineCount {
		return
	}
	set.lines[line] = append(set.lines[line], value)
}

// Suppresses reports whether a directive targeting offset accepts code.
func (set Set) Suppresses(offset uint32, code string) bool {
	if len(set.lines) == 0 || len(set.lineStarts) == 0 || code == "" {
		return false
	}
	position := min(int(offset), set.lineStarts[len(set.lineStarts)-1])
	line := sort.Search(len(set.lineStarts), func(index int) bool {
		return set.lineStarts[index] > position
	}) - 1
	if line < 0 {
		line = 0
	}
	for _, candidate := range set.lines[line] {
		if candidate.wildcard {
			return true
		}
		for _, identifier := range candidate.identifiers {
			if identifierMatches(identifier, code) {
				return true
			}
		}
	}
	return false
}

func sourceLines(source string) ([]string, []int) {
	lines := strings.Split(source, "\n")
	starts := make([]int, len(lines))
	position := 0
	for index, line := range lines {
		starts[index] = position
		position += len(line) + 1
	}
	return lines, starts
}

func directiveTarget(lines []string, lineIndex, marker int) int {
	prefix := strings.TrimSpace(lines[lineIndex][:marker])
	if prefix != "" && !commentOnlyPrefix(prefix) {
		return lineIndex
	}
	if closeIndex := strings.Index(lines[lineIndex][marker:], "*/"); closeIndex >= 0 &&
		strings.TrimSpace(lines[lineIndex][marker+closeIndex+2:]) != "" {
		return lineIndex
	}
	return nextCodeLine(lines, lineIndex)
}

func commentOnlyPrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	return strings.HasPrefix(prefix, "//") ||
		strings.HasPrefix(prefix, "#") ||
		strings.HasPrefix(prefix, "/*") ||
		strings.HasPrefix(prefix, "*")
}

func nextCodeLine(lines []string, start int) int {
	inBlockComment := strings.Contains(lines[start], "/*") &&
		!strings.Contains(lines[start][strings.Index(lines[start], "/*"):], "*/")
	for index := start + 1; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if inBlockComment {
			closeIndex := strings.Index(line, "*/")
			if closeIndex < 0 {
				continue
			}
			inBlockComment = false
			line = strings.TrimSpace(line[closeIndex+2:])
			if line == "" {
				continue
			}
		}
		if line == "" || strings.HasPrefix(line, "//") ||
			strings.HasPrefix(line, "#") || strings.HasPrefix(line, "*") {
			continue
		}
		if strings.HasPrefix(line, "/*") {
			if closeIndex := strings.Index(line[2:], "*/"); closeIndex >= 0 {
				line = strings.TrimSpace(line[closeIndex+4:])
				if line != "" {
					return index
				}
				continue
			}
			inBlockComment = true
			continue
		}
		return index
	}
	return start + 1
}

func noInspectionMarker(lower string) (int, int) {
	if marker := strings.Index(lower, "@noinspection"); marker >= 0 {
		return marker, len("@noinspection")
	}
	if marker := strings.Index(lower, "noinspection"); marker >= 0 {
		return marker, len("noinspection")
	}
	return -1, 0
}

func parseDirective(source string, inspection bool) directive {
	source = strings.TrimSpace(source)
	if source == "" || strings.HasPrefix(source, "*/") ||
		strings.HasPrefix(source, "//") || strings.HasPrefix(source, "(") ||
		strings.HasPrefix(source, "-") {
		return directive{wildcard: true}
	}
	if reason := strings.IndexByte(source, '('); reason >= 0 {
		source = source[:reason]
	}
	fields := strings.FieldsFunc(source, func(character rune) bool {
		return unicode.IsSpace(character) || character == ','
	})
	identifiers := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "*/;:[]{}")
		if field == "" {
			continue
		}
		if !inspection && !strings.ContainsRune(field, '.') && field != "*" {
			break
		}
		identifiers = append(identifiers, strings.ToLower(field))
	}
	return directive{
		identifiers: identifiers,
		wildcard:    len(identifiers) == 0,
	}
}

func identifierMatches(identifier, code string) bool {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	code = strings.ToLower(strings.TrimSpace(code))
	if identifier == "*" || identifier == "all" || identifier == code {
		return true
	}
	switch code {
	case "php.returntype":
		return strings.HasPrefix(identifier, "return.") ||
			identifier == "cast.string" || identifier == "vartag.type" ||
			identifier == "phpreturndoctypemismatchinspection" ||
			identifier == "phpinconsistentreturnpointsinspection"
	case "php.arguments":
		return strings.HasPrefix(identifier, "argument.") ||
			strings.HasPrefix(identifier, "arguments.") ||
			identifier == "phpparamsinspection" ||
			identifier == "phpmethodparameterscountmismatchinspection"
	case "php.deprecated":
		return strings.Contains(identifier, "deprecated") ||
			identifier == "phpdeprecationinspection"
	case "php.undefined", "php.undefinedvariable":
		return strings.Contains(identifier, "notfound") ||
			strings.Contains(identifier, "undefined") ||
			strings.HasPrefix(identifier, "phpundefined")
	case "php.override":
		return strings.Contains(identifier, "override")
	case "php.inheritance":
		return strings.Contains(identifier, "inheritance") ||
			strings.Contains(identifier, "extends") ||
			strings.Contains(identifier, "implements")
	case "php.abstract":
		return strings.Contains(identifier, "abstract")
	case "php.visibility":
		return strings.Contains(identifier, "visibility") ||
			strings.Contains(identifier, "access") ||
			identifier == "phpaccessmodifiersinspection"
	case "php.parse":
		return strings.Contains(identifier, "syntax") ||
			strings.Contains(identifier, "parse")
	case "php.version":
		return strings.Contains(identifier, "languagelevel") ||
			strings.Contains(identifier, "phpversion") ||
			identifier == "version"
	}
	return false
}

func containsFoldASCII(source, needle string) bool {
	if needle == "" {
		return true
	}
	for index := 0; index+len(needle) <= len(source); index++ {
		if strings.EqualFold(source[index:index+len(needle)], needle) {
			return true
		}
	}
	return false
}
