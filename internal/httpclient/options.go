// Package httpclient resolves Symfony HttpClient option declarations and
// references from the shared PHP semantic index.
package httpclient

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const ClientInterface = "Symfony\\Contracts\\HttpClient\\HttpClientInterface"

type Option struct {
	Name    string
	Type    types.Type
	Default string
	File    string
	Range   cst.TextRange
}

type Reference struct {
	Name  string
	Range cst.TextRange
	Call  *phpsyntax.Node
	Array *phpsyntax.Node
}

func ReferenceAt(node *phpsyntax.Node) (Reference, bool) {
	stringNode := phpquery.StringAt(node)
	if stringNode == nil {
		return Reference{}, false
	}
	item := phpquery.ArrayItemAt(stringNode)
	if item == nil || phpquery.ArrayItemKey(item) != stringNode {
		return Reference{}, false
	}
	array := phpquery.ArrayAt(item)
	call := phpquery.CallAt(array)
	if array == nil || call == nil {
		return Reference{}, false
	}
	index := phpquery.ArgumentIndex(call, array)
	if index < 0 || phpquery.ArgumentExpression(call, index) != array {
		return Reference{}, false
	}
	method := strings.ToLower(phpquery.CallMethodName(call))
	argumentName := strings.ToLower(phpquery.ArgumentName(array))
	switch method {
	case "request":
		if argumentName != "options" &&
			(argumentName != "" || index != 2) {
			return Reference{}, false
		}
	case "withoptions":
		if argumentName != "options" &&
			(argumentName != "" || index != 0) {
			return Reference{}, false
		}
	default:
		return Reference{}, false
	}
	return Reference{
		Name:  phpquery.StringValue(stringNode),
		Range: phpquery.StringContentRange(stringNode),
		Call:  call,
		Array: array,
	}, true
}

func Validate(
	ctx context.Context,
	index *php.PHPIndex,
	reference Reference,
	source []byte,
) bool {
	return index != nil && reference.Call != nil &&
		index.IsMethodCalledOnClass(
			ctx,
			reference.Call,
			source,
			ClientInterface,
		)
}

func Options(index *php.PHPIndex) []Option {
	if index == nil {
		return nil
	}
	var result []Option
	for _, constant := range index.FindConstants(
		ClientInterface,
		"OPTIONS_DEFAULTS",
	) {
		for _, item := range constant.ConstantArray {
			result = append(result, Option{
				Name:    item.Key,
				Type:    item.Type,
				Default: item.Value,
				File:    constant.Path,
				Range:   item.KeyRange,
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

func UsedOptionNames(reference Reference) map[string]struct{} {
	result := make(map[string]struct{})
	if reference.Array == nil {
		return result
	}
	for _, item := range phpquery.ArrayItems(reference.Array) {
		key := phpquery.ArrayItemKey(item)
		if key == nil || key.Kind() != phpsyntax.PhpString {
			continue
		}
		if phpquery.StringContentRange(key) == reference.Range {
			continue
		}
		name := phpquery.StringValue(key)
		if name != "" {
			result[name] = struct{}{}
		}
	}
	return result
}
