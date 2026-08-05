package symfonyconfig

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

type RootReference struct {
	Name  string
	Node  *cst.Node
	Range cst.TextRange
}

type ResourceReference struct {
	Path  string
	Node  *cst.Node
	Range cst.TextRange
}

func RootReferenceAt(node *cst.Node) (RootReference, bool) {
	if reference, found := phpRootReferenceAt(node); found {
		return reference, true
	}
	return yamlRootReferenceAt(node)
}

func phpRootReferenceAt(node *phpsyntax.Node) (RootReference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return RootReference{}, false
	}
	item := phpquery.ArrayItemAt(literal)
	if item == nil || !sameNode(
		phpquery.ArrayItemKey(item),
		literal,
	) {
		return RootReference{}, false
	}
	array := parentArray(item)
	if array == nil || !isConfigEntriesArray(array) {
		return RootReference{}, false
	}
	name, found := staticConfigString(literal)
	if !found || name == "" || strings.HasPrefix(name, "when@") ||
		name == "imports" {
		return RootReference{}, false
	}
	return RootReference{
		Name:  name,
		Node:  literal,
		Range: phpquery.StringContentRange(literal),
	}, true
}

func ResourceReferenceAt(node *cst.Node) (ResourceReference, bool) {
	if reference, found := phpResourceReferenceAt(node); found {
		return reference, true
	}
	return yamlResourceReferenceAt(node)
}

func phpResourceReferenceAt(
	node *phpsyntax.Node,
) (ResourceReference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return ResourceReference{}, false
	}
	resourceItem := phpquery.ArrayItemAt(literal)
	if resourceItem == nil ||
		!sameNode(phpquery.ArrayItemValue(resourceItem), literal) ||
		stringNodeValue(phpquery.ArrayItemKey(resourceItem)) != "resource" {
		return ResourceReference{}, false
	}

	entryArray := parentArray(resourceItem)
	entryItem := parentArrayItem(entryArray)
	if entryItem == nil || arrayItemHasDirectArrow(entryItem) {
		return ResourceReference{}, false
	}
	importsArray := parentArray(entryItem)
	importsItem := parentArrayItem(importsArray)
	if importsItem == nil ||
		stringNodeValue(phpquery.ArrayItemKey(importsItem)) != "imports" ||
		!sameNode(phpquery.ArrayItemValue(importsItem), importsArray) {
		return ResourceReference{}, false
	}
	configArray := parentArray(importsItem)
	if configArray == nil || !isConfigEntriesArray(configArray) {
		return ResourceReference{}, false
	}
	path, found := staticConfigString(literal)
	if !found || path == "" {
		return ResourceReference{}, false
	}
	return ResourceReference{
		Path:  path,
		Node:  literal,
		Range: phpquery.StringContentRange(literal),
	}, true
}

func yamlRootReferenceAt(
	node *yamlsyntax.Node,
) (RootReference, bool) {
	scalar := yamlScalarAt(node)
	pair := yamlquery.AncestorPair(scalar)
	if scalar == nil || pair == nil ||
		!sameYAMLNode(yamlquery.PairKey(pair), scalar) {
		return RootReference{}, false
	}
	path := yamlquery.PairPath(scalar)
	if len(path) != 1 &&
		(len(path) != 2 ||
			!strings.HasPrefix(path[0], "when@")) {
		return RootReference{}, false
	}
	name := yamlquery.ScalarValue(scalar)
	if name == "" || name == "imports" ||
		strings.HasPrefix(name, "when@") {
		return RootReference{}, false
	}
	return RootReference{
		Name:  name,
		Node:  scalar,
		Range: yamlScalarContentRange(scalar),
	}, true
}

func yamlResourceReferenceAt(
	node *yamlsyntax.Node,
) (ResourceReference, bool) {
	scalar := yamlScalarAt(node)
	pair := yamlquery.AncestorPair(scalar)
	if scalar == nil || pair == nil ||
		!sameYAMLNode(yamlquery.PairValue(pair), scalar) ||
		yamlquery.ScalarValue(yamlquery.PairKey(pair)) != "resource" {
		return ResourceReference{}, false
	}
	pairPath := yamlquery.PairPath(scalar)
	if len(pairPath) != 2 ||
		pairPath[0] != "imports" ||
		pairPath[1] != "resource" {
		if len(pairPath) != 3 ||
			!strings.HasPrefix(pairPath[0], "when@") ||
			pairPath[1] != "imports" ||
			pairPath[2] != "resource" {
			return ResourceReference{}, false
		}
	}
	path := yamlquery.ScalarValue(scalar)
	if path == "" || strings.ContainsAny(path, "\r\n") {
		return ResourceReference{}, false
	}
	return ResourceReference{
		Path:  path,
		Node:  scalar,
		Range: yamlScalarContentRange(scalar),
	}, true
}

func yamlScalarAt(node *yamlsyntax.Node) *yamlsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == yamlsyntax.YamlScalar {
			return current
		}
		switch current.Kind() {
		case yamlsyntax.YamlPair,
			yamlsyntax.YamlMapping,
			yamlsyntax.YamlFlowMapping,
			yamlsyntax.YamlSequence,
			yamlsyntax.YamlFlowSequence:
			return nil
		}
	}
	return nil
}

func sameYAMLNode(left, right *yamlsyntax.Node) bool {
	return left != nil && right != nil &&
		(left == right || left.Range() == right.Range())
}

func yamlScalarContentRange(node *yamlsyntax.Node) cst.TextRange {
	if node == nil {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' || text[0] == '"') &&
		text[len(text)-1] == text[0] &&
		rng.End > rng.Start+1 {
		rng.Start++
		rng.End--
	}
	return rng
}

func isConfigEntriesArray(array *phpsyntax.Node) bool {
	if isAcceptedConfigRootArray(array) {
		return true
	}
	whenItem := parentArrayItem(array)
	if whenItem == nil ||
		!sameNode(phpquery.ArrayItemValue(whenItem), array) ||
		!strings.HasPrefix(
			stringNodeValue(phpquery.ArrayItemKey(whenItem)),
			"when@",
		) {
		return false
	}
	return isAcceptedConfigRootArray(parentArray(whenItem))
}

func isAcceptedConfigRootArray(array *phpsyntax.Node) bool {
	if array == nil || array.Kind() != phpsyntax.PhpArray {
		return false
	}
	if parent := array.Parent(); parent != nil &&
		parent.Kind() == phpsyntax.PhpReturnStatement &&
		phpquery.ClassAt(parent) == nil {
		return true
	}
	call := phpquery.CallAt(array)
	if call == nil ||
		!strings.EqualFold(phpquery.CallMethodName(call), "config") ||
		!sameNode(phpquery.ArgumentExpression(call, 0), array) {
		return false
	}
	return true
}

func parentArray(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == phpsyntax.PhpArray {
			return parent
		}
		if parent.Kind() == phpsyntax.PhpArrayItem {
			continue
		}
		break
	}
	return nil
}

func parentArrayItem(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == phpsyntax.PhpArrayItem {
			return parent
		}
		if parent.Kind() == phpsyntax.PhpArgument ||
			parent.Kind() == phpsyntax.PhpNamedArgument {
			continue
		}
		break
	}
	return nil
}

func sameNode(left, right *phpsyntax.Node) bool {
	if left == nil || right == nil {
		return false
	}
	return left == right || left.Range() == right.Range()
}

func stringNodeValue(node *phpsyntax.Node) string {
	value, _ := staticConfigString(node)
	return value
}

func arrayItemHasDirectArrow(item *phpsyntax.Node) bool {
	if item == nil || item.Kind() != phpsyntax.PhpArrayItem {
		return false
	}
	for index := 0; index < item.ChildCount(); index++ {
		child := item.Child(index)
		if child.Kind() == phpsyntax.TkArrow {
			return true
		}
	}
	return false
}
