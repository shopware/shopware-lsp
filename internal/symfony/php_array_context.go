package symfony

import (
	"slices"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

type PHPArrayServiceMethodReference struct {
	Service     string
	Class       string
	Method      string
	Range       cst.TextRange
	AllowStatic bool
}

// PHPArrayServiceReferenceAt classifies values in Symfony's legacy/native PHP
// array service configuration. Only top-level returned arrays and App::config
// roots are accepted, so ordinary application arrays do not gain container
// semantics.
func PHPArrayServiceReferenceAt(
	node *phpsyntax.Node,
) PHPConfigReferenceKind {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return PHPConfigReferenceNone
	}
	if call := phpquery.CallAt(literal); call != nil &&
		call.Kind() == phpsyntax.PhpFunctionCall &&
		phpquery.StringArgumentIndex(literal) == 0 {
		switch shortPHPCallName(phpquery.CallMethodName(call)) {
		case "service", "service_closure", "param",
			"tagged_iterator", "tagged_locator":
			return PHPConfigReferenceNone
		}
	}
	path, found := phpArrayServicePath(literal)
	if !found {
		return PHPConfigReferenceNone
	}
	value := strings.TrimSpace(phpquery.StringValue(literal))
	if len(path) == 0 {
		if strings.HasPrefix(value, "@") &&
			!strings.HasPrefix(value, "@@") {
			return PHPConfigReferenceService
		}
		return PHPConfigReferenceNone
	}
	switch {
	case len(path) == 1 &&
		(path[0] == "decorates" || path[0] == "parent"):
		return PHPConfigReferenceService
	case len(path) == 1 && path[0] == "class":
		return PHPConfigReferenceClass
	case len(path) == 2 && path[0] == "factory" && path[1] == "0":
		return PHPConfigReferenceService
	case phpArrayArgumentPath(path):
		if value == "" || strings.HasPrefix(value, "%") {
			return PHPConfigReferenceParameter
		}
		if strings.HasPrefix(value, "@") &&
			!strings.HasPrefix(value, "@@") {
			return PHPConfigReferenceService
		}
		if _, _, service := ParseServiceReference(value); service {
			return PHPConfigReferenceService
		}
		if strings.Contains(value, "\\") {
			return PHPConfigReferenceClass
		}
	case phpArrayTagPath(path):
		return PHPConfigReferenceTag
	}
	return PHPConfigReferenceNone
}

func PHPArrayParameterCompletionAt(node *phpsyntax.Node) bool {
	return PHPArrayServiceReferenceAt(node) == PHPConfigReferenceParameter
}

func PHPArrayServiceContextAt(node *phpsyntax.Node) bool {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return false
	}
	_, found := phpArrayServicePath(literal)
	return found
}

// PHPArrayServiceMethodAt resolves method-name strings in configured calls and
// factory tuples. Receiver class constants and import aliases are normalized
// immediately; service IDs remain available for container-based resolution.
func PHPArrayServiceMethodAt(
	root,
	node *phpsyntax.Node,
) (PHPArrayServiceMethodReference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return PHPArrayServiceMethodReference{}, false
	}
	path, found := phpArrayServicePath(literal)
	if !found {
		return PHPArrayServiceMethodReference{}, false
	}
	reference := PHPArrayServiceMethodReference{
		Method: phpquery.StringValue(literal),
		Range:  phpquery.StringContentRange(literal),
	}
	resolver := php.NewNameResolver(root)
	switch {
	case len(path) == 3 &&
		path[0] == "calls" &&
		path[2] == "0":
		entry := phpArrayServiceEntryAt(literal)
		if entry == nil {
			return PHPArrayServiceMethodReference{}, false
		}
		id, _ := staticPHPValue(
			nodeText(phpquery.ArrayItemKey(entry)),
			resolver,
			"",
		)
		definition := phpArrayNode(phpquery.ArrayItemValue(entry))
		if classNode := phpArrayOptionValue(
			definition,
			"class",
			resolver,
			"",
		); classNode != nil {
			reference.Class, _ = staticPHPValue(
				classNode.Text(),
				resolver,
				"",
			)
			if reference.Class != "" {
				reference.Class = `\` +
					strings.TrimPrefix(reference.Class, `\`)
			}
		}
		if reference.Class == "" && strings.Contains(id, "\\") {
			reference.Class = `\` + strings.TrimPrefix(id, `\`)
		}
		reference.Service = id
	case len(path) == 2 &&
		path[0] == "factory" &&
		path[1] == "1":
		entry := phpArrayServiceEntryAt(literal)
		definition := phpArrayNode(phpquery.ArrayItemValue(entry))
		factory := phpArrayNode(phpArrayOptionValue(
			definition,
			"factory",
			resolver,
			"",
		))
		items := phpquery.ArrayItems(factory)
		if len(items) < 2 {
			return PHPArrayServiceMethodReference{}, false
		}
		receiver := phpquery.ArrayItemValue(items[0])
		value, static := staticPHPValue(
			nodeText(receiver),
			resolver,
			"",
		)
		if !static || value == "" {
			return PHPArrayServiceMethodReference{}, false
		}
		if service, _, serviceFound := ParseServiceReference(value); serviceFound {
			reference.Service = service
		} else {
			reference.Class = `\` + strings.TrimPrefix(value, `\`)
		}
		reference.AllowStatic = true
	default:
		return PHPArrayServiceMethodReference{}, false
	}
	if reference.Class == "" && reference.Service == "" {
		return PHPArrayServiceMethodReference{}, false
	}
	return reference, true
}

func phpArrayArgumentPath(path []string) bool {
	return len(path) >= 2 && path[0] == "arguments" ||
		len(path) >= 4 && path[0] == "calls" && path[2] == "1"
}

func phpArrayTagPath(path []string) bool {
	return len(path) == 2 && path[0] == "tags" ||
		len(path) == 3 && path[0] == "tags" && path[2] == "name"
}

func phpArrayServicePath(node *phpsyntax.Node) ([]string, bool) {
	current := node
	var reversed []string
	for current != nil {
		item := phpArrayValueItemAt(current)
		if item == nil {
			return nil, false
		}
		if phpArrayIsServiceEntry(item) {
			slices.Reverse(reversed)
			return reversed, true
		}
		array := phpArrayParent(item)
		if array == nil {
			return nil, false
		}
		segment, found := phpArrayItemSegment(array, item)
		if !found {
			return nil, false
		}
		reversed = append(reversed, segment)
		current = array
	}
	return nil, false
}

func phpArrayServiceEntryAt(node *phpsyntax.Node) *phpsyntax.Node {
	current := node
	for current != nil {
		item := phpArrayValueItemAt(current)
		if item == nil {
			return nil
		}
		if phpArrayIsServiceEntry(item) {
			return item
		}
		current = phpArrayParent(item)
	}
	return nil
}

func phpArrayIsServiceEntry(item *phpsyntax.Node) bool {
	if item == nil || phpquery.ArrayItemKey(item) == nil {
		return false
	}
	servicesArray := phpArrayParent(item)
	if servicesArray == nil {
		return false
	}
	servicesItem := phpArrayValueItemAt(servicesArray)
	if servicesItem == nil ||
		phpArrayStaticString(phpquery.ArrayItemKey(servicesItem)) != "services" ||
		!phpArraySameNode(
			phpquery.ArrayItemValue(servicesItem),
			servicesArray,
		) {
		return false
	}
	configArray := phpArrayParent(servicesItem)
	return phpArrayAcceptedRoot(configArray)
}

func phpArrayAcceptedRoot(array *phpsyntax.Node) bool {
	if array == nil || array.Kind() != phpsyntax.PhpArray {
		return false
	}
	if parent := array.Parent(); parent != nil &&
		parent.Kind() == phpsyntax.PhpReturnStatement &&
		phpquery.FunctionLikeAt(parent) == nil &&
		phpquery.ClassAt(parent) == nil {
		return true
	}
	call := phpquery.CallAt(array)
	return call != nil &&
		call.Kind() == phpsyntax.PhpScopedCall &&
		strings.EqualFold(phpquery.CallMethodName(call), "config") &&
		phpArraySameNode(phpquery.ArgumentExpression(call, 0), array) &&
		phpquery.FunctionLikeAt(call) == nil &&
		phpquery.ClassAt(call) == nil
}

func phpArrayValueItemAt(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != phpsyntax.PhpArrayItem {
			continue
		}
		value := phpquery.ArrayItemValue(current)
		if value != nil && phpArrayRangeContains(value.Range(), node.Range()) {
			return current
		}
		return nil
	}
	return nil
}

func phpArrayParent(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == phpsyntax.PhpArray {
			return current
		}
		if current.Kind() != phpsyntax.PhpArrayItem {
			continue
		}
	}
	return nil
}

func phpArrayItemSegment(
	array,
	item *phpsyntax.Node,
) (string, bool) {
	if key := phpquery.ArrayItemKey(item); key != nil {
		value := phpArrayStaticString(key)
		return value, value != ""
	}
	for index, candidate := range phpquery.ArrayItems(array) {
		if phpArraySameNode(candidate, item) {
			return strconv.Itoa(index), true
		}
	}
	return "", false
}

func phpArrayStaticString(node *phpsyntax.Node) string {
	if literal := phpquery.StringAt(node); literal != nil &&
		phpArraySameNode(literal, node) {
		return phpquery.StringValue(literal)
	}
	return strings.TrimSpace(nodeText(node))
}

func nodeText(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	return node.Text()
}

func phpArraySameNode(left, right *phpsyntax.Node) bool {
	return left != nil && right != nil &&
		(left == right || left.Range() == right.Range())
}

func phpArrayRangeContains(outer, inner cst.TextRange) bool {
	return inner.Start >= outer.Start && inner.End <= outer.End
}
