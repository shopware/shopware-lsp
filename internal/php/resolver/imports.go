package resolver

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type ImportKind uint8

const (
	ClassImport ImportKind = iota
	FunctionImport
	ConstantImport
)

type Import struct {
	Kind   ImportKind
	Alias  string
	Target string
}

// ParseUseDeclaration parses simple, comma-separated, grouped, function, and
// const use declarations from their lossless CST text.
func ParseUseDeclaration(source string) []Import {
	var result []Import
	visitUseDeclaration(source, func(value Import) {
		result = append(result, value)
	})
	return result
}

// AddUseDeclaration parses a declaration directly into the context without
// materializing the compatibility result slice returned by ParseUseDeclaration.
func (c *NameContext) AddUseDeclaration(source string) {
	if c == nil {
		return
	}
	visitUseDeclaration(source, c.AddImport)
}

func visitUseDeclaration(source string, visit func(Import)) {
	if visit == nil {
		return
	}
	source = strings.TrimSpace(stripComments(source))
	if index := indexFoldASCII(source, "use "); index >= 0 {
		source = strings.TrimSpace(source[index+len("use "):])
	}
	source = strings.TrimSpace(strings.TrimSuffix(source, ";"))
	kind, source := consumeImportKind(source, ClassImport)

	if open := strings.IndexByte(source, '{'); open >= 0 {
		close := strings.LastIndexByte(source, '}')
		if close <= open {
			return
		}
		base := strings.Trim(strings.TrimSpace(source[:open]), "\\")
		visitImports(source[open+1:close], kind, base, visit)
		return
	}

	visitImports(source, kind, "", visit)
}

func (c *NameContext) AddImport(value Import) {
	if c == nil || value.Target == "" {
		return
	}
	alias := value.Alias
	if alias == "" {
		alias, _ = splitLast(value.Target)
	}
	switch value.Kind {
	case FunctionImport:
		if c.Imports.Functions == nil {
			c.Imports.Functions = make(map[string]string)
		}
		c.Imports.Functions[strings.ToLower(alias)] = value.Target
	case ConstantImport:
		if c.Imports.Constants == nil {
			c.Imports.Constants = make(map[string]string)
		}
		c.Imports.Constants[alias] = value.Target
	default:
		if c.Imports.Classes == nil {
			c.Imports.Classes = make(map[string]string)
		}
		c.Imports.Classes[strings.ToLower(alias)] = value.Target
	}
}

func consumeImportKind(source string, fallback ImportKind) (ImportKind, string) {
	source = strings.TrimSpace(source)
	if source == "" {
		return fallback, source
	}
	end := strings.IndexFunc(source, unicode.IsSpace)
	if end < 0 {
		end = len(source)
	}
	first := source[:end]
	if strings.EqualFold(first, "function") {
		return FunctionImport, strings.TrimSpace(source[end:])
	}
	if strings.EqualFold(first, "const") {
		return ConstantImport, strings.TrimSpace(source[end:])
	}
	return fallback, source
}

func parseImport(source string, kind ImportKind, base string) (Import, bool) {
	source = strings.TrimSpace(source)
	if source == "" {
		return Import{}, false
	}
	targetEnd := strings.IndexFunc(source, unicode.IsSpace)
	if targetEnd < 0 {
		targetEnd = len(source)
	}
	target := strings.Trim(source[:targetEnd], "\\")
	if base != "" {
		target = base + "\\" + target
	}
	alias, _ := splitLast(target)
	aliasField, beforeAlias := lastImportField(source)
	keyword, _ := lastImportField(beforeAlias)
	if aliasField != "" && strings.EqualFold(keyword, "as") {
		alias = aliasField
	}
	return Import{Kind: kind, Alias: alias, Target: target}, true
}

func lastImportField(source string) (field, before string) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", ""
	}
	start := strings.LastIndexFunc(source, unicode.IsSpace)
	if start < 0 {
		return source, ""
	}
	return source[start+1:], strings.TrimSpace(source[:start])
}

func splitLast(name string) (string, string) {
	if separator := strings.LastIndexByte(name, '\\'); separator >= 0 {
		return name[separator+1:], name[:separator]
	}
	return name, ""
}

func visitImports(
	source string,
	kind ImportKind,
	base string,
	visit func(Import),
) {
	start := 0
	depth := 0
	for index, value := range source {
		switch value {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		default:
			if value == ',' && depth == 0 {
				visitImport(source[start:index], kind, base, visit)
				start = index + 1
			}
		}
	}
	visitImport(source[start:], kind, base, visit)
}

func visitImport(
	source string,
	kind ImportKind,
	base string,
	visit func(Import),
) {
	itemKind, itemText := consumeImportKind(strings.TrimSpace(source), kind)
	if parsed, ok := parseImport(itemText, itemKind, base); ok {
		visit(parsed)
	}
}

func stripComments(source string) string {
	if !strings.Contains(source, "//") &&
		!strings.Contains(source, "/*") &&
		!strings.Contains(source, "#") {
		return source
	}
	var result strings.Builder
	result.Grow(len(source))
	for position := 0; position < len(source); {
		switch {
		case strings.HasPrefix(source[position:], "//"):
			position += 2
			for position < len(source) && source[position] != '\n' && source[position] != '\r' {
				position++
			}
		case strings.HasPrefix(source[position:], "/*"):
			position += 2
			for position+1 < len(source) && !strings.HasPrefix(source[position:], "*/") {
				position++
			}
			if position+1 < len(source) {
				position += 2
			}
		case source[position] == '#' && !strings.HasPrefix(source[position:], "#["):
			position++
			for position < len(source) && source[position] != '\n' && source[position] != '\r' {
				position++
			}
		default:
			_, width := utf8.DecodeRuneInString(source[position:])
			result.WriteString(source[position : position+width])
			position += width
		}
	}
	return result.String()
}

func indexFoldASCII(source, needle string) int {
	if needle == "" {
		return 0
	}
	if len(source) < len(needle) {
		return -1
	}
	first := lowerASCII(needle[0])
	maxStart := len(source) - len(needle)
	offset := 0
	for offset <= maxStart {
		index := indexFoldASCIIByte(source[offset:maxStart+1], first)
		if index < 0 {
			return -1
		}
		start := offset + index
		if strings.EqualFold(source[start:start+len(needle)], needle) {
			return start
		}
		offset = start + 1
	}
	return -1
}

func indexFoldASCIIByte(source string, lower byte) int {
	lowerIndex := strings.IndexByte(source, lower)
	if lower < 'a' || lower > 'z' {
		return lowerIndex
	}
	upperIndex := strings.IndexByte(source, lower-'a'+'A')
	if lowerIndex < 0 {
		return upperIndex
	}
	if upperIndex >= 0 && upperIndex < lowerIndex {
		return upperIndex
	}
	return lowerIndex
}

func lowerASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}
