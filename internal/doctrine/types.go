package doctrine

import (
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type TypeDeclaration struct {
	Name  string
	Class string
	File  string
	Range cst.TextRange
}

// TypeDeclarations discovers conventional custom DBAL Type subclasses.
// Doctrine allows an arbitrary runtime registration name, but the conventional
// FooBarType => foo_bar spelling gives useful completion and navigation without
// executing project code.
func TypeDeclarations(index *php.PHPIndex) []TypeDeclaration {
	if index == nil {
		return nil
	}
	snapshot := index.SemanticSnapshot()
	var result []TypeDeclaration
	seen := make(map[string]struct{})
	for _, symbol := range index.ClassSymbols() {
		if !snapshot.IsSubtypeOf(symbol.FullyQualified, "Doctrine\\DBAL\\Types\\Type") {
			continue
		}
		name := doctrineTypeDeclaredName(index, symbol.FullyQualified)
		if name == "" {
			short := strings.TrimSuffix(symbol.Name, "Type")
			name = snakeCaseDoctrineType(short)
		}
		if name == "" {
			continue
		}
		key := strings.ToLower(name + "|" + symbol.FullyQualified)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, TypeDeclaration{
			Name:  name,
			Class: symbol.FullyQualified,
			File:  symbol.Path,
			Range: symbol.SelectionRange,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].Class < result[right].Class
	})
	return result
}

func doctrineTypeDeclaredName(
	index *php.PHPIndex,
	class string,
) string {
	for _, method := range index.Methods(class) {
		if !strings.EqualFold(method.Name, "getName") {
			continue
		}
		for _, literal := range method.LiteralReturns() {
			if literal.Type.Kind() != types.LiteralStringKind {
				continue
			}
			if name := strings.TrimSpace(literal.Value); name != "" {
				return name
			}
		}
		for _, returned := range method.ConstantReturns() {
			receiver := doctrineConstantReturnReceiver(
				index,
				method,
				returned,
			)
			if receiver == "" {
				continue
			}
			for _, constant := range index.FindConstants(
				receiver,
				returned.Name,
			) {
				if constant.Type.Kind() != types.LiteralStringKind {
					continue
				}
				if name := strings.TrimSpace(constant.Type.Name()); name != "" {
					return name
				}
			}
		}
		return ""
	}
	return ""
}

func doctrineConstantReturnReceiver(
	index *php.PHPIndex,
	method semantic.Symbol,
	returned semantic.ConstantReturn,
) string {
	receiver := strings.TrimSpace(returned.Receiver)
	switch strings.ToLower(receiver) {
	case "self", "static", "parent":
		snapshot := index.SemanticSnapshot()
		owner, found := snapshot.Symbol(method.Container)
		if !found {
			return ""
		}
		if !strings.EqualFold(receiver, "parent") {
			return owner.FullyQualified
		}
		if len(owner.Extends()) != 0 {
			return owner.Extends()[0]
		}
		return ""
	default:
		return strings.TrimPrefix(receiver, "\\")
	}
}

func snakeCaseDoctrineType(value string) string {
	var result strings.Builder
	var previousLower bool
	for _, character := range value {
		if unicode.IsUpper(character) {
			if previousLower && result.Len() != 0 {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(character))
			previousLower = false
			continue
		}
		result.WriteRune(unicode.ToLower(character))
		previousLower = unicode.IsLetter(character) ||
			unicode.IsDigit(character)
	}
	return strings.Trim(result.String(), "_")
}
