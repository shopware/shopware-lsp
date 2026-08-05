package doctrine

import (
	"context"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

type DQLFunction struct {
	Name  string
	Class string
	File  string
	Range cst.TextRange
}

func DQLFunctionsInDocument(
	path string,
	root *phpsyntax.Node,
) []DQLFunction {
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	var result []DQLFunction
	for _, class := range phpquery.Classes(root) {
		if !strings.EqualFold(phpquery.ClassName(class), "Parser") ||
			!strings.EqualFold(
				resolver.Resolve(phpquery.ClassName(class)),
				"Doctrine\\ORM\\Query\\Parser",
			) {
			continue
		}
		for _, property := range phpquery.Properties(class) {
			if !isDQLFunctionProperty(property) {
				continue
			}
			arrays := phpquery.Arrays(property)
			if len(arrays) == 0 {
				continue
			}
			for _, item := range phpquery.ArrayItems(arrays[0]) {
				key := phpquery.StringAt(phpquery.ArrayItemKey(item))
				value := phpquery.ArrayItemValue(item)
				name := phpquery.StringValue(key)
				className := phpquery.ClassConstantName(value)
				if name == "" || className == "" {
					continue
				}
				result = append(result, DQLFunction{
					Name:  name,
					Class: strings.TrimLeft(resolver.Resolve(className), `\`),
					File:  path,
					Range: ReferenceRange(Reference{
						Kind: StringReference,
						Node: key,
					}),
				})
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result
}

func isDQLFunctionProperty(property *phpsyntax.Node) bool {
	for _, variable := range phpquery.PropertyVariables(property) {
		switch strings.ToLower(phpquery.VariableName(variable)) {
		case "stringfunctions", "numericfunctions", "datetimefunctions":
			return true
		}
	}
	return false
}

func (idx *Index) DQLFunctions() ([]DQLFunction, error) {
	if idx == nil || idx.dqlFunctions == nil {
		return nil, nil
	}
	values, err := idx.dqlFunctions.GetAllValues()
	if err != nil {
		return nil, err
	}
	merged := make(map[string]DQLFunction)
	for _, value := range values {
		if value.Name != "" {
			merged[strings.ToLower(value.Name)] = value
		}
	}
	result := make([]DQLFunction, 0, len(merged))
	for _, value := range merged {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (idx *Index) DQLFunction(
	name string,
) (DQLFunction, bool, error) {
	if idx == nil || idx.dqlFunctions == nil || name == "" {
		return DQLFunction{}, false, nil
	}
	values, err := idx.dqlFunctions.GetValues(strings.ToLower(name))
	if err != nil || len(values) == 0 {
		return DQLFunction{}, false, err
	}
	return values[0], true, nil
}

func (idx *Index) QueryFunctionReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
	offset uint32,
) (DQLFunction, cst.TextRange, bool) {
	query, found := idx.queryContextAt(ctx, root, node)
	if !found || query.Literal == nil {
		return DQLFunction{}, cst.TextRange{}, false
	}
	name, rng := dqlWordAt(query.Literal, offset)
	if name == "" {
		return DQLFunction{}, cst.TextRange{}, false
	}
	value := phpquery.StringValue(query.Literal)
	base := ReferenceRange(Reference{
		Kind: StringReference,
		Node: query.Literal,
	}).Start
	end := int(rng.End - base)
	for end < len(value) &&
		(value[end] == ' ' || value[end] == '\t' ||
			value[end] == '\r' || value[end] == '\n') {
		end++
	}
	if end >= len(value) || value[end] != '(' {
		return DQLFunction{}, cst.TextRange{}, false
	}
	function, exists, err := idx.DQLFunction(name)
	if err != nil || !exists {
		return DQLFunction{}, cst.TextRange{}, false
	}
	return function, rng, true
}
