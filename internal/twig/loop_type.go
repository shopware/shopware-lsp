package twig

import (
	"strings"

	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// loopVariableType resolves the key and value variables introduced by
// enclosing Twig for loops. Only the loop body owns those variables: Twig's
// for-else branch and the collection expression itself remain outside that
// scope.
func (r PHPAccessResolver) loopVariableType(
	templatePath string,
	root,
	nameNode *twigsyntax.Node,
	name string,
) (types.Type, bool) {
	if nameNode == nil || name == "" {
		return types.Unknown(), false
	}
	for current := nameNode.Parent(); current != nil; current = current.Parent() {
		if current.Kind() != twigsyntax.TwigFor ||
			!twigForBodyContains(current, nameNode) {
			continue
		}
		start := directChild(current, twigsyntax.TwigForBlock)
		if start == nil {
			continue
		}
		names, collection := twigForBinding(start)
		if len(names) == 0 || collection == nil {
			continue
		}
		keyType, valueType := r.twigIterableTypes(
			r.expressionType(templatePath, root, collection),
		)
		switch {
		case len(names) == 1 && names[0] == name:
			return valueType, true
		case len(names) > 1 && names[0] == name:
			return keyType, true
		case len(names) > 1 && names[1] == name:
			return valueType, true
		}
	}
	return types.Unknown(), false
}

func twigForBodyContains(
	loop,
	node *twigsyntax.Node,
) bool {
	if loop == nil || node == nil {
		return false
	}
	for child := range loop.ChildNodes() {
		if child.Kind() == twigsyntax.Body {
			return isDescendantNode(node, child)
		}
	}
	return false
}

func twigForBinding(
	start *twigsyntax.Node,
) ([]string, *twigsyntax.Node) {
	if start == nil {
		return nil, nil
	}
	inOffset, found := firstTokenOffset(start, twigsyntax.TkIn)
	if !found {
		return nil, nil
	}
	var names []string
	var collection *twigsyntax.Node
	for child := range start.ChildNodes() {
		switch child.Kind() {
		case twigsyntax.TwigLiteralName:
			if child.Range().Start < inOffset {
				name, _ := literalName(child)
				if name != "" {
					names = append(names, name)
				}
			}
		case twigsyntax.TwigExpression:
			if child.Range().Start >= inOffset && collection == nil {
				collection = child
			}
		}
	}
	return names, collection
}

func (r PHPAccessResolver) twigIterableTypes(
	value types.Type,
) (types.Type, types.Type) {
	switch value.Kind() {
	case types.ArrayKind, types.IterableKind:
		if value.ArgumentCount() == 2 {
			return value.Argument(0), value.Argument(1)
		}
	case types.ListKind:
		if value.ArgumentCount() == 1 {
			return types.Int(), value.Argument(0)
		}
	case types.ArrayShapeKind:
		var values []types.Type
		for fieldIndex := 0; fieldIndex < value.FieldCount(); fieldIndex++ {
			field := value.Field(fieldIndex)
			values = append(values, field.Type)
		}
		return types.ArrayKey(), unionKnownTypes(values)
	case types.ObjectKind:
		if !r.twigTraversableObject(value) {
			break
		}
		switch value.ArgumentCount() {
		case 1:
			return types.ArrayKey(), value.Argument(0)
		case 2:
			return value.Argument(0), value.Argument(1)
		}
	case types.UnionKind, types.IntersectionKind:
		var keys, values []types.Type
		for index := 0; index < value.ArgumentCount(); index++ {
			member := value.Argument(index)
			key, element := r.twigIterableTypes(member)
			if !key.IsUnknown() {
				keys = append(keys, key)
			}
			if !element.IsUnknown() {
				values = append(values, element)
			}
		}
		return unionKnownTypes(keys), unionKnownTypes(values)
	}
	return types.Unknown(), types.Unknown()
}

// IterableTypes returns the key and element types exposed by a value in a
// Twig for loop. Arrays, lists, iterable generics, shapes, traversable PHP
// objects, unions, and intersections are supported.
func (r PHPAccessResolver) IterableTypes(
	value types.Type,
) (types.Type, types.Type) {
	return r.twigIterableTypes(value)
}

func (r PHPAccessResolver) twigTraversableObject(
	value types.Type,
) bool {
	name := strings.Trim(value.Name(), "\\")
	if name == "" {
		return false
	}
	snapshot := r.snapshot()
	for _, target := range []string{
		"Traversable",
		"Iterator",
		"IteratorAggregate",
	} {
		if snapshot.IsSubtypeOf(name, target) {
			return true
		}
	}
	return false
}
