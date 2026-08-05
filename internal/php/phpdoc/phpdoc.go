// Package phpdoc parses type-bearing PHPDoc tags into the shared semantic type
// algebra. It intentionally does not depend on CST or the workspace index.
package phpdoc

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/types"
)

type Document struct {
	Summary    string
	Params     map[string]types.Type
	ParamTags  map[string][]string
	Return     types.Type
	Vars       map[string]types.Type
	Properties []Property
	Methods    []Method
	Templates  []Template
	Extends    []types.Type
	Implements []types.Type
	Uses       []types.Type
	Throws     []types.Type
	Assertions []Assertion
	Aliases    map[string]types.Type
	Imports    map[string]TypeImport
	Deprecated bool
	Internal   bool
}

// TypeImport records a PHPStan/Psalm type alias imported from another
// class-like declaration. Name is the alias as declared by the source class.
type TypeImport struct {
	Name string
	From string
}

type Property struct {
	Name      string
	Type      types.Type
	ReadOnly  bool
	WriteOnly bool
}

type Method struct {
	Name       string
	ReturnType types.Type
	Parameters []Parameter
	Static     bool
}

type Parameter struct {
	Name     string
	Type     types.Type
	Optional bool
	Variadic bool
	Tags     []string
}

type Template struct {
	Name          string
	Bound         types.Type
	Default       types.Type
	Covariant     bool
	Contravariant bool
}

type Assertion struct {
	Target      string
	Type        types.Type
	WhenTrue    bool
	Conditional bool
	Negated     bool
}

func Parse(source string) Document {
	var document Document
	var summary []string
	lines := newLogicalLineScanner(source)
	for {
		line, found := lines.next()
		if !found {
			break
		}
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "@") {
			if len(document.Params) == 0 && document.Return.IsUnknown() &&
				len(document.Properties) == 0 && len(document.Methods) == 0 {
				summary = append(summary, line)
			}
			continue
		}
		tag, value := splitTag(line)
		switch tag {
		case "@param", "@phpstan-param", "@psalm-param":
			typeText, name, description := splitTypeAndVariable(value)
			if parsed := parseType(typeText); !parsed.IsUnknown() && name != "" {
				if document.Params == nil {
					document.Params = make(map[string]types.Type)
				}
				document.Params[name] = parsed
			}
			if tags := assistantTags(description); name != "" && len(tags) != 0 {
				if document.ParamTags == nil {
					document.ParamTags = make(map[string][]string)
				}
				document.ParamTags[name] = tags
			}
		case "@return", "@phpstan-return", "@psalm-return":
			typeText, _ := splitTypeAndDescription(value)
			document.Return = parseType(typeText)
		case "@var", "@phpstan-var", "@psalm-var":
			typeText, name, _ := splitTypeAndVariable(value)
			if parsed := parseType(typeText); !parsed.IsUnknown() {
				if document.Vars == nil {
					document.Vars = make(map[string]types.Type)
				}
				document.Vars[name] = parsed
			}
		case "@property", "@property-read", "@property-write":
			typeText, name, _ := splitTypeAndVariable(value)
			if parsed := parseType(typeText); !parsed.IsUnknown() && name != "" {
				document.Properties = append(document.Properties, Property{
					Name:      strings.TrimPrefix(name, "$"),
					Type:      parsed,
					ReadOnly:  tag == "@property-read",
					WriteOnly: tag == "@property-write",
				})
			}
		case "@method":
			if method, ok := parseMethod(value); ok {
				document.Methods = append(document.Methods, method)
			}
		case "@template", "@template-covariant", "@template-contravariant",
			"@phpstan-template", "@psalm-template":
			if template, ok := parseTemplate(value); ok {
				template.Covariant = strings.Contains(tag, "covariant")
				template.Contravariant = strings.Contains(tag, "contravariant")
				document.Templates = append(document.Templates, template)
			}
		case "@extends", "@template-extends", "@phpstan-extends":
			document.Extends = appendType(document.Extends, value)
		case "@implements", "@template-implements", "@phpstan-implements":
			document.Implements = appendType(document.Implements, value)
		case "@use", "@template-use", "@phpstan-use":
			document.Uses = appendType(document.Uses, value)
		case "@throws":
			document.Throws = appendType(document.Throws, value)
		case "@phpstan-assert", "@psalm-assert",
			"@phpstan-assert-if-true", "@psalm-assert-if-true",
			"@phpstan-assert-if-false", "@psalm-assert-if-false":
			typeText, target, _ := splitTypeAndVariable(value)
			typeText = strings.TrimSpace(typeText)
			negated := strings.HasPrefix(typeText, "!")
			typeText = strings.TrimPrefix(typeText, "!")
			typeText = strings.TrimPrefix(typeText, "=")
			if parsed := parseType(typeText); !parsed.IsUnknown() && target != "" {
				document.Assertions = append(document.Assertions, Assertion{
					Target:      target,
					Type:        parsed,
					WhenTrue:    strings.HasSuffix(tag, "-if-true"),
					Conditional: strings.Contains(tag, "-if-"),
					Negated:     negated,
				})
			}
		case "@phpstan-type", "@psalm-type":
			name, typeText := splitFirst(value)
			if name != "" {
				if document.Aliases == nil {
					document.Aliases = make(map[string]types.Type)
				}
				document.Aliases[name] = parseType(strings.TrimPrefix(strings.TrimSpace(typeText), "="))
			}
		case "@phpstan-import-type", "@psalm-import-type":
			localName, imported, ok := parseTypeImport(value)
			if ok {
				if document.Imports == nil {
					document.Imports = make(map[string]TypeImport)
				}
				document.Imports[localName] = imported
			}
		case "@deprecated":
			document.Deprecated = true
		case "@internal":
			document.Internal = true
		}
	}
	document.Summary = strings.Join(summary, " ")
	return document
}

func parseTypeImport(source string) (string, TypeImport, bool) {
	fields := strings.Fields(source)
	if len(fields) < 3 {
		return "", TypeImport{}, false
	}
	from := -1
	for index, field := range fields {
		if strings.EqualFold(field, "from") {
			from = index
			break
		}
	}
	if from != 1 || from+1 >= len(fields) {
		return "", TypeImport{}, false
	}
	importedName := fields[0]
	sourceClass := fields[from+1]
	localName := importedName
	if from+3 < len(fields) && strings.EqualFold(fields[from+2], "as") {
		localName = fields[from+3]
	}
	if importedName == "" || sourceClass == "" || localName == "" {
		return "", TypeImport{}, false
	}
	return localName, TypeImport{
		Name: importedName,
		From: sourceClass,
	}, true
}

func appendType(values []types.Type, source string) []types.Type {
	typeText, _ := splitTypeAndDescription(source)
	value := parseType(typeText)
	if value.IsUnknown() {
		return values
	}
	return append(values, value)
}

func parseType(source string) types.Type {
	value, err := types.Parse(strings.TrimSpace(source))
	if err != nil {
		return types.Error()
	}
	return value
}

func parseTemplate(source string) (Template, bool) {
	name, rest := splitFirst(source)
	if name == "" {
		return Template{}, false
	}
	template := Template{Name: name, Bound: types.Mixed(), Default: types.Unknown()}
	rest = strings.TrimSpace(rest)
	for _, keyword := range []string{" of ", " as "} {
		if index := strings.Index(" "+rest, keyword); index >= 0 {
			boundAndDefault := strings.TrimSpace((" " + rest)[index+len(keyword):])
			if equal := topLevelIndex(boundAndDefault, '='); equal >= 0 {
				template.Bound = parseType(boundAndDefault[:equal])
				template.Default = parseType(boundAndDefault[equal+1:])
			} else {
				typeText, _ := splitTypeAndDescription(boundAndDefault)
				template.Bound = parseType(typeText)
			}
			return template, true
		}
	}
	if equal := topLevelIndex(rest, '='); equal >= 0 {
		template.Default = parseType(rest[equal+1:])
	}
	return template, true
}

func parseMethod(source string) (Method, bool) {
	source = strings.TrimSpace(source)
	method := Method{}
	if strings.HasPrefix(strings.ToLower(source), "static ") {
		method.Static = true
		source = strings.TrimSpace(source[len("static "):])
	}
	open := topLevelIndex(source, '(')
	close := strings.LastIndexByte(source, ')')
	if open < 0 || close < open {
		return Method{}, false
	}
	prefix := strings.TrimSpace(source[:open])
	parts := strings.Fields(prefix)
	if len(parts) == 0 {
		return Method{}, false
	}
	method.Name = parts[len(parts)-1]
	if len(parts) > 1 {
		method.ReturnType = parseType(strings.Join(parts[:len(parts)-1], " "))
	} else {
		method.ReturnType = types.Mixed()
	}
	for _, parameterText := range splitTopLevel(source[open+1:close], ',') {
		parameterText = strings.TrimSpace(parameterText)
		if parameterText == "" {
			continue
		}
		typeText, name, description := splitTypeAndVariable(parameterText)
		if name == "" {
			continue
		}
		parameterType := parseType(typeText)
		if strings.TrimSpace(typeText) == "" {
			parameterType = types.Mixed()
		}
		method.Parameters = append(method.Parameters, Parameter{
			Name:     strings.TrimSuffix(name, "="),
			Type:     parameterType,
			Optional: strings.Contains(description, "=") || strings.HasSuffix(name, "="),
			Variadic: strings.Contains(name, "..."),
			Tags:     assistantTags(description),
		})
	}
	return method, true
}

func assistantTags(source string) []string {
	if !strings.Contains(source, "#") {
		return nil
	}
	var result []string
	var seen map[string]struct{}
	for offset := 0; offset < len(source); {
		start := strings.IndexByte(source[offset:], '#')
		if start < 0 {
			break
		}
		start += offset + 1
		end := start
		for end < len(source) {
			value := source[end]
			if value >= 'a' && value <= 'z' ||
				value >= 'A' && value <= 'Z' ||
				value >= '0' && value <= '9' ||
				value == '_' {
				end++
				continue
			}
			break
		}
		if end > start {
			tag := source[start:end]
			key := strings.ToLower(tag)
			if _, duplicate := seen[key]; !duplicate {
				if seen == nil {
					seen = make(map[string]struct{})
				}
				seen[key] = struct{}{}
				result = append(result, tag)
			}
		}
		offset = end
		if offset <= start {
			offset = start + 1
		}
	}
	return result
}

type logicalLineScanner struct {
	source     string
	pending    string
	hasPending bool
}

func newLogicalLineScanner(source string) logicalLineScanner {
	source = strings.TrimSpace(source)
	source = strings.TrimPrefix(source, "/**")
	source = strings.TrimSuffix(source, "*/")
	return logicalLineScanner{source: source}
}

func (s *logicalLineScanner) next() (string, bool) {
	line, found := s.nextPhysical()
	if !found || !strings.HasPrefix(line, "@") {
		return line, found
	}

	var joined strings.Builder
	for {
		continuation, available := s.nextPhysical()
		if !available {
			break
		}
		if strings.HasPrefix(continuation, "@") {
			s.pending = continuation
			s.hasPending = true
			break
		}
		if joined.Len() == 0 {
			joined.Grow(len(line) + len(continuation) + 1)
			joined.WriteString(line)
		}
		joined.WriteByte(' ')
		joined.WriteString(continuation)
	}
	if joined.Len() != 0 {
		return joined.String(), true
	}
	return line, true
}

func (s *logicalLineScanner) nextPhysical() (string, bool) {
	if s.hasPending {
		s.hasPending = false
		return s.pending, true
	}
	for len(s.source) > 0 {
		line := s.source
		if end := strings.IndexByte(s.source, '\n'); end >= 0 {
			line = s.source[:end]
			s.source = s.source[end+1:]
		} else {
			s.source = ""
		}
		line = strings.TrimSpace(
			strings.TrimPrefix(strings.TrimSpace(line), "*"),
		)
		if line != "" {
			return line, true
		}
	}
	return "", false
}

func splitTag(line string) (string, string) {
	tag, rest := splitFirst(line)
	return strings.ToLower(tag), strings.TrimSpace(rest)
}

func splitTypeAndVariable(source string) (string, string, string) {
	depth := 0
	quote := rune(0)
	for index, value := range source {
		switch {
		case quote != 0:
			if value == quote {
				quote = 0
			}
		case value == '\'' || value == '"':
			quote = value
		case value == '<' || value == '{' || value == '(' || value == '[':
			depth++
		case value == '>' || value == '}' || value == ')' || value == ']':
			if depth > 0 {
				depth--
			}
		case value == '$' && depth == 0:
			typeText := strings.TrimSpace(source[:index])
			typeText = strings.TrimSpace(strings.TrimSuffix(typeText, "..."))
			typeText = strings.TrimSpace(strings.TrimSuffix(typeText, "&"))
			name, rest := splitFirst(source[index:])
			return typeText, name, rest
		}
	}
	typeText, rest := splitTypeAndDescription(source)
	return typeText, "", rest
}

func splitTypeAndDescription(source string) (string, string) {
	depth := 0
	quote := rune(0)
	for index, value := range source {
		switch {
		case quote != 0:
			if value == quote {
				quote = 0
			}
		case value == '\'' || value == '"':
			quote = value
		case value == '<' || value == '{' || value == '(' || value == '[':
			depth++
		case value == '>' || value == '}' || value == ')' || value == ']':
			if depth > 0 {
				depth--
			}
		case (value == ' ' || value == '\t') && depth == 0:
			return strings.TrimSpace(source[:index]), strings.TrimSpace(source[index:])
		}
	}
	return strings.TrimSpace(source), ""
}

func splitFirst(source string) (string, string) {
	source = strings.TrimSpace(source)
	for index, value := range source {
		if value == ' ' || value == '\t' {
			return source[:index], strings.TrimSpace(source[index:])
		}
	}
	return source, ""
}

func splitTopLevel(source string, separator rune) []string {
	var result []string
	start := 0
	depth := 0
	for index, value := range source {
		switch value {
		case '<', '{', '(', '[':
			depth++
		case '>', '}', ')', ']':
			if depth > 0 {
				depth--
			}
		default:
			if value == separator && depth == 0 {
				result = append(result, source[start:index])
				start = index + 1
			}
		}
	}
	return append(result, source[start:])
}

func topLevelIndex(source string, target rune) int {
	depth := 0
	for index, value := range source {
		if value == target && depth == 0 {
			return index
		}
		switch value {
		case '<', '{', '(', '[':
			depth++
		case '>', '}', ')', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return -1
}
