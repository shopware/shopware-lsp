package environment

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type sourceLine struct {
	start uint32
	text  string
}

func parseDotEnv(source string) []Occurrence {
	var result []Occurrence
	for _, line := range sourceLines(source) {
		text := line.text
		cursor := skipSpace(text, 0)
		if strings.HasPrefix(text[cursor:], "export") {
			next := cursor + len("export")
			if next < len(text) && isSpace(text[next]) {
				cursor = skipSpace(text, next)
			}
		}
		nameStart := cursor
		if cursor >= len(text) || !isNameStart(text[cursor]) {
			continue
		}
		cursor++
		for cursor < len(text) && isNamePart(text[cursor]) {
			cursor++
		}
		nameEnd := cursor
		cursor = skipSpace(text, cursor)
		if cursor >= len(text) || text[cursor] != '=' {
			continue
		}
		cursor = skipSpace(text, cursor+1)
		value := dotenvValue(text[cursor:])
		result = append(result, Occurrence{
			Kind:   DeclarationOccurrence,
			Source: DotEnvSource,
			Name:   text[nameStart:nameEnd],
			Value:  value,
			Range: cst.TextRange{
				Start: line.start + uint32(nameStart),
				End:   line.start + uint32(len(text)),
			},
			NameRange: cst.TextRange{
				Start: line.start + uint32(nameStart),
				End:   line.start + uint32(nameEnd),
			},
		})
	}
	return result
}

func dotenvValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value[0] == '\'' || value[0] == '"' {
		quote := value[0]
		escaped := false
		for index := 1; index < len(value); index++ {
			switch {
			case escaped:
				escaped = false
			case value[index] == '\\' && quote == '"':
				escaped = true
			case value[index] == quote:
				return value[1:index]
			}
		}
		return strings.TrimPrefix(value, string(quote))
	}
	for index, current := range []byte(value) {
		if current == '#' && (index == 0 || isSpace(value[index-1])) {
			return strings.TrimSpace(value[:index])
		}
	}
	return strings.TrimSpace(value)
}

func parseDockerfile(source string) []Occurrence {
	var result []Occurrence
	for _, line := range sourceLines(source) {
		cursor := skipSpace(line.text, 0)
		if len(line.text)-cursor < 3 ||
			!strings.EqualFold(line.text[cursor:cursor+3], "ENV") {
			continue
		}
		cursor += 3
		if cursor >= len(line.text) || !isSpace(line.text[cursor]) {
			continue
		}
		cursor = skipSpace(line.text, cursor)
		tokens := shellTokens(line.text[cursor:], cursor)
		if len(tokens) == 0 {
			continue
		}
		if strings.Contains(tokens[0].value, "=") {
			for _, token := range tokens {
				equals := strings.IndexByte(token.value, '=')
				if equals <= 0 {
					continue
				}
				name := token.value[:equals]
				if !validName(name) {
					continue
				}
				nameStart := token.start
				result = append(result, Occurrence{
					Kind:   DeclarationOccurrence,
					Source: DockerfileSource,
					Name:   name,
					Value:  strings.Trim(token.value[equals+1:], `"'`),
					Range: cst.TextRange{
						Start: line.start + uint32(token.start),
						End:   line.start + uint32(token.end),
					},
					NameRange: cst.TextRange{
						Start: line.start + uint32(nameStart),
						End: line.start +
							uint32(nameStart+len(name)),
					},
				})
			}
			continue
		}
		name := strings.Trim(tokens[0].value, `"'`)
		if !validName(name) {
			continue
		}
		valueStart := tokens[0].end
		value := ""
		if valueStart < len(line.text) {
			value = strings.TrimSpace(line.text[valueStart:])
			value = strings.Trim(value, `"'`)
		}
		result = append(result, Occurrence{
			Kind:   DeclarationOccurrence,
			Source: DockerfileSource,
			Name:   name,
			Value:  value,
			Range: cst.TextRange{
				Start: line.start + uint32(tokens[0].start),
				End:   line.start + uint32(len(line.text)),
			},
			NameRange: cst.TextRange{
				Start: line.start + uint32(tokens[0].start),
				End: line.start +
					uint32(tokens[0].start+len(name)),
			},
		})
	}
	return result
}

type shellToken struct {
	value      string
	start, end int
}

func shellTokens(value string, base int) []shellToken {
	var result []shellToken
	for cursor := 0; cursor < len(value); {
		cursor = skipSpace(value, cursor)
		if cursor >= len(value) || value[cursor] == '#' {
			break
		}
		start := cursor
		var quote byte
		escaped := false
		for cursor < len(value) {
			current := value[cursor]
			switch {
			case escaped:
				escaped = false
			case current == '\\':
				escaped = true
			case quote != 0 && current == quote:
				quote = 0
			case quote == 0 && (current == '\'' || current == '"'):
				quote = current
			case quote == 0 && isSpace(current):
				goto done
			}
			cursor++
		}
	done:
		result = append(result, shellToken{
			value: value[start:cursor],
			start: base + start,
			end:   base + cursor,
		})
	}
	return result
}

func parseDockerCompose(source string) []Occurrence {
	var result []Occurrence
	environmentIndent := -1
	for _, line := range sourceLines(source) {
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line.text) - len(strings.TrimLeft(line.text, " \t"))
		if environmentIndent >= 0 {
			if indent <= environmentIndent {
				environmentIndent = -1
			} else if occurrence, found := composeDeclaration(line, indent); found {
				result = append(result, occurrence)
				continue
			}
		}
		header := strings.TrimSpace(stripYAMLComment(line.text))
		if strings.EqualFold(header, "environment:") {
			environmentIndent = indent
		}
	}
	return result
}

func composeDeclaration(
	line sourceLine,
	indent int,
) (Occurrence, bool) {
	text := stripYAMLComment(line.text)
	cursor := indent
	if cursor >= len(text) {
		return Occurrence{}, false
	}
	if text[cursor] == '-' {
		cursor = skipSpace(text, cursor+1)
		valueStart := cursor
		valueEnd := len(strings.TrimRight(text, " \t"))
		if valueStart >= valueEnd {
			return Occurrence{}, false
		}
		raw := strings.Trim(text[valueStart:valueEnd], `"'`)
		name := raw
		value := ""
		if equals := strings.IndexByte(raw, '='); equals >= 0 {
			name = raw[:equals]
			value = raw[equals+1:]
		}
		if !validName(name) {
			return Occurrence{}, false
		}
		quoteAdjustment := 0
		if text[valueStart] == '\'' || text[valueStart] == '"' {
			quoteAdjustment = 1
		}
		nameStart := valueStart + quoteAdjustment
		return declarationOccurrence(
			line,
			DockerComposeSource,
			name,
			value,
			nameStart,
			nameStart+len(name),
			valueStart,
			valueEnd,
		), true
	}

	colon := yamlMappingColon(text[cursor:])
	if colon < 0 {
		return Occurrence{}, false
	}
	colon += cursor
	rawName := strings.TrimSpace(text[cursor:colon])
	name := strings.Trim(rawName, `"'`)
	if !validName(name) {
		return Occurrence{}, false
	}
	nameOffset := strings.Index(text[cursor:colon], name)
	if nameOffset < 0 {
		return Occurrence{}, false
	}
	nameStart := cursor + nameOffset
	value := strings.TrimSpace(text[colon+1:])
	value = strings.Trim(value, `"'`)
	return declarationOccurrence(
		line,
		DockerComposeSource,
		name,
		value,
		nameStart,
		nameStart+len(name),
		cursor,
		len(strings.TrimRight(text, " \t")),
	), true
}

func declarationOccurrence(
	line sourceLine,
	source SourceKind,
	name,
	value string,
	nameStart,
	nameEnd,
	start,
	end int,
) Occurrence {
	return Occurrence{
		Kind:   DeclarationOccurrence,
		Source: source,
		Name:   name,
		Value:  value,
		Range: cst.TextRange{
			Start: line.start + uint32(start),
			End:   line.start + uint32(end),
		},
		NameRange: cst.TextRange{
			Start: line.start + uint32(nameStart),
			End:   line.start + uint32(nameEnd),
		},
	}
}

func stripYAMLComment(value string) string {
	var quote byte
	escaped := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		switch {
		case escaped:
			escaped = false
		case current == '\\' && quote == '"':
			escaped = true
		case quote != 0 && current == quote:
			quote = 0
		case quote == 0 && (current == '\'' || current == '"'):
			quote = current
		case quote == 0 && current == '#':
			return strings.TrimRight(value[:index], " \t")
		}
	}
	return value
}

func yamlMappingColon(value string) int {
	var quote byte
	for index := 0; index < len(value); index++ {
		switch {
		case quote != 0 && value[index] == quote:
			quote = 0
		case quote == 0 && (value[index] == '\'' || value[index] == '"'):
			quote = value[index]
		case quote == 0 && value[index] == ':':
			return index
		}
	}
	return -1
}

func sourceLines(source string) []sourceLine {
	var result []sourceLine
	for start := 0; start <= len(source); {
		end := strings.IndexByte(source[start:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += start
		}
		text := strings.TrimSuffix(source[start:end], "\r")
		result = append(result, sourceLine{
			start: uint32(start),
			text:  text,
		})
		if end == len(source) {
			break
		}
		start = end + 1
	}
	return result
}

func isDotEnvPath(path string) bool {
	base := filepath.Base(path)
	return strings.EqualFold(base, ".env") ||
		hasPrefixFold(base, ".env.") ||
		strings.EqualFold(filepath.Ext(base), ".env")
}

func isDockerfilePath(path string) bool {
	base := filepath.Base(path)
	return strings.EqualFold(base, "dockerfile") ||
		hasPrefixFold(base, "dockerfile.")
}

func isDockerComposePath(path string) bool {
	base := filepath.Base(path)
	extension := filepath.Ext(base)
	if !strings.EqualFold(extension, ".yaml") &&
		!strings.EqualFold(extension, ".yml") {
		return false
	}
	stem := strings.TrimSuffix(base, extension)
	return strings.EqualFold(stem, "compose") ||
		hasPrefixFold(stem, "compose.") ||
		strings.EqualFold(stem, "docker-compose") ||
		hasPrefixFold(stem, "docker-compose.")
}

func hasPrefixFold(source, prefix string) bool {
	return len(source) >= len(prefix) &&
		strings.EqualFold(source[:len(prefix)], prefix)
}

func skipSpace(value string, cursor int) int {
	for cursor < len(value) && isSpace(value[cursor]) {
		cursor++
	}
	return cursor
}

func isSpace(value byte) bool {
	return value == ' ' || value == '\t'
}

func isNameStart(value byte) bool {
	return value == '_' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
}

func isNamePart(value byte) bool {
	return isNameStart(value) || value >= '0' && value <= '9'
}

func validName(value string) bool {
	if value == "" || !isNameStart(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isNamePart(value[index]) {
			return false
		}
	}
	return true
}
