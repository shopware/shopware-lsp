package doctrine

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

type MagicMethod struct {
	Name       string
	Prefix     string
	Entity     string
	Fields     []Field
	Unknown    []string
	Node       *cst.Node
	NameRange  cst.TextRange
	ReturnType string
}

type MagicMethodCompletion struct {
	Name       string
	Field      Field
	Entity     string
	ReturnType string
}

func (idx *Index) MagicMethodAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) (MagicMethod, bool) {
	call := phpquery.CallAt(node)
	if call == nil {
		return MagicMethod{}, false
	}
	name := phpquery.CallMethodName(call)
	prefix, suffix, returnType := magicMethodParts(name)
	if prefix == "" || suffix == "" {
		return MagicMethod{}, false
	}
	entity := idx.RepositoryEntityForCall(ctx, root, call)
	if entity == "" {
		return MagicMethod{}, false
	}
	fields, err := idx.Fields(entity)
	if err != nil {
		return MagicMethod{}, false
	}
	criteria := splitMagicCriteria(suffix)
	magic := MagicMethod{
		Name:       name,
		Prefix:     prefix,
		Entity:     entity,
		Node:       call,
		NameRange:  callMethodRange(call, name),
		ReturnType: returnTypeForMagic(returnType, entity),
	}
	for _, criterion := range criteria {
		field, found := magicField(fields, criterion)
		if found {
			magic.Fields = append(magic.Fields, field)
		} else {
			magic.Unknown = append(magic.Unknown, criterion)
		}
	}
	return magic, true
}

func (idx *Index) MagicMethodCompletionsAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) []MagicMethodCompletion {
	call := phpquery.CallAt(node)
	if call == nil {
		return nil
	}
	entity := idx.RepositoryEntityForCall(ctx, root, call)
	if entity == "" {
		return nil
	}
	fields, err := idx.Fields(entity)
	if err != nil {
		return nil
	}
	var result []MagicMethodCompletion
	seen := make(map[string]struct{})
	for _, field := range fields {
		suffix := magicFieldSuffix(field.Name)
		if suffix == "" {
			continue
		}
		for _, prefix := range []string{"findOneBy", "findBy", "countBy"} {
			name := prefix + suffix
			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			returnType := entity + "|null"
			switch prefix {
			case "findBy":
				returnType = "list<" + entity + ">"
			case "countBy":
				returnType = "int"
			}
			result = append(result, MagicMethodCompletion{
				Name:       name,
				Field:      field,
				Entity:     entity,
				ReturnType: returnType,
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func magicMethodParts(name string) (string, string, string) {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"findOneBy", "findBy", "countBy"} {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return prefix, name[len(prefix):], prefix
		}
	}
	return "", "", ""
}

func returnTypeForMagic(kind, entity string) string {
	switch kind {
	case "findBy":
		return "list<" + entity + ">"
	case "countBy":
		return "int"
	default:
		return entity + "|null"
	}
}

func splitMagicCriteria(value string) []string {
	var result []string
	start := 0
	for position := 1; position < len(value); position++ {
		separatorLength := 0
		switch {
		case strings.HasPrefix(value[position:], "And"):
			separatorLength = len("And")
		case strings.HasPrefix(value[position:], "Or"):
			separatorLength = len("Or")
		}
		if separatorLength == 0 ||
			position+separatorLength >= len(value) ||
			!unicode.IsUpper(rune(value[position+separatorLength])) {
			continue
		}
		result = append(result, value[start:position])
		position += separatorLength - 1
		start = position + 1
	}
	result = append(result, value[start:])
	return result
}

func magicField(fields []Field, suffix string) (Field, bool) {
	normalized := normalizeMagicField(suffix)
	for _, field := range fields {
		if normalizeMagicField(field.Name) == normalized {
			return field, true
		}
	}
	return Field{}, false
}

func normalizeMagicField(value string) string {
	var result strings.Builder
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			result.WriteRune(unicode.ToLower(character))
		}
	}
	return result.String()
}

func magicFieldSuffix(value string) string {
	var result strings.Builder
	upper := true
	for _, character := range value {
		if character == '.' || character == '_' || character == '-' {
			upper = true
			continue
		}
		if upper {
			result.WriteRune(unicode.ToUpper(character))
			upper = false
		} else {
			result.WriteRune(character)
		}
	}
	return result.String()
}

func callMethodRange(
	call *phpsyntax.Node,
	name string,
) cst.TextRange {
	if call == nil || name == "" {
		return cst.TextRange{}
	}
	var result cst.TextRange
	for _, candidate := range phpquery.Nodes(call, phpsyntax.PhpName) {
		if strings.EqualFold(phpquery.NameValue(candidate), name) {
			result = candidate.RangeTrimmedTrivia()
		}
	}
	if result.Len() == 0 {
		return call.RangeTrimmedTrivia()
	}
	return result
}
