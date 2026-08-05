package event

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type ReferenceRole uint8

const (
	ReferenceNone ReferenceRole = iota
	ReferenceEvent
	ReferenceListenerMethod
)

type ReferenceOrigin uint8

const (
	OriginUnknown ReferenceOrigin = iota
	OriginDispatch
	OriginListener
)

type Reference struct {
	Role      ReferenceRole
	Origin    ReferenceOrigin
	Name      string
	Node      *cst.Node
	Container *cst.Node
	Class     string
	Method    string
}

func ReferenceAt(
	ctx context.Context,
	path string,
	root,
	node *cst.Node,
) (Reference, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return PHPReferenceAt(ctx, root, node)
	case ".yaml", ".yml":
		return YAMLReferenceAt(node)
	case ".xml":
		return XMLReferenceAt(node)
	default:
		return Reference{}, false
	}
}

func PHPReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) (Reference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return Reference{}, false
	}
	name := phpquery.StringValue(literal)
	resolver := php.NewNameResolver(root)
	className := resolvedClassName(phpquery.ClassAt(literal), resolver)

	if attribute := phpquery.AttributeAt(literal); attribute != nil &&
		isAsEventListener(attribute, resolver) {
		argument := phpquery.ArgumentIndex(attribute, literal)
		argumentName := ""
		if argument >= 0 {
			argumentName = phpquery.ArgumentName(
				phpquery.Argument(attribute, argument),
			)
		}
		switch {
		case strings.EqualFold(argumentName, "event") ||
			argumentName == "" && argument == 0:
			return Reference{
				Role:      ReferenceEvent,
				Origin:    OriginListener,
				Name:      name,
				Node:      literal,
				Container: attribute,
				Class:     className,
			}, true
		case strings.EqualFold(argumentName, "method") ||
			argumentName == "" && argument == 1:
			return Reference{
				Role:      ReferenceListenerMethod,
				Origin:    OriginListener,
				Name:      name,
				Node:      literal,
				Container: attribute,
				Class:     className,
			}, true
		}
	}

	if reference, found := phpServiceTagReference(
		literal,
		resolver,
	); found {
		return reference, true
	}

	if call := phpquery.CallAt(literal); call != nil &&
		strings.EqualFold(phpquery.CallMethodName(call), "dispatch") {
		index := phpquery.ArgumentIndex(call, literal)
		argumentName := ""
		if index >= 0 {
			argumentName = phpquery.ArgumentName(
				phpquery.Argument(call, index),
			)
		}
		first := phpquery.ArgumentExpression(call, 0)
		isLegacyFirst := index == 0 && first == literal
		isModernName := index == 1 ||
			strings.EqualFold(argumentName, "eventName")
		if isLegacyFirst || isModernName {
			reference := Reference{
				Role:      ReferenceEvent,
				Origin:    OriginDispatch,
				Name:      name,
				Node:      literal,
				Container: call,
				Class:     className,
				Method:    phpquery.MethodName(literal),
			}
			if validateDispatchReference(ctx, reference) {
				return reference, true
			}
		}
	}

	method := phpquery.MethodAt(literal)
	if method == nil ||
		!strings.EqualFold(
			phpquery.MethodName(method),
			"getSubscribedEvents",
		) ||
		!validateSubscriberClass(ctx, phpquery.ClassAt(method), resolver) {
		return Reference{}, false
	}
	return subscriberReference(literal, method, className)
}

func phpServiceTagReference(
	literal *phpsyntax.Node,
	nameResolver *php.NameResolver,
) (Reference, bool) {
	call := phpquery.CallAt(literal)
	if call == nil ||
		!strings.EqualFold(phpquery.CallMethodName(call), "tag") ||
		phpStringValue(phpquery.ArgumentExpression(call, 0)) !=
			"kernel.event_listener" {
		return Reference{}, false
	}
	options := phpquery.ArrayAt(phpquery.ArgumentExpression(call, 1))
	if options == nil || !nodeContains(options, literal) {
		return Reference{}, false
	}
	item := phpquery.ArrayItemAt(literal)
	if item == nil {
		return Reference{}, false
	}
	key := phpquery.ArrayItemKey(item)
	value := phpquery.ArrayItemValue(item)
	if key == nil || value == nil || !nodeContains(value, literal) {
		return Reference{}, false
	}
	property := phpStringValue(key)
	if property != "event" && property != "method" {
		return Reference{}, false
	}
	role := ReferenceEvent
	if property == "method" {
		role = ReferenceListenerMethod
	}
	return Reference{
		Role:      role,
		Origin:    OriginListener,
		Name:      phpquery.StringValue(literal),
		Node:      literal,
		Container: call,
		Class:     serviceTagClass(call, nameResolver),
	}, true
}

func subscriberReference(
	literal,
	method *phpsyntax.Node,
	className string,
) (Reference, bool) {
	for _, statement := range phpquery.Nodes(
		method,
		phpsyntax.PhpReturnStatement,
	) {
		if phpquery.FunctionLikeAt(statement) != method {
			continue
		}
		array := phpquery.DirectChild(statement, phpsyntax.PhpArray)
		if array == nil || !nodeContains(array, literal) {
			continue
		}
		for _, item := range phpquery.ArrayItems(array) {
			if !nodeContains(item, literal) {
				continue
			}
			key := phpquery.ArrayItemKey(item)
			if key != nil && nodeContains(key, literal) {
				return Reference{
					Role:      ReferenceEvent,
					Origin:    OriginListener,
					Name:      phpquery.StringValue(literal),
					Node:      literal,
					Container: item,
					Class:     className,
					Method:    phpquery.MethodName(method),
				}, true
			}
			value := phpquery.ArrayItemValue(item)
			if value != nil && nodeContains(value, literal) {
				return Reference{
					Role:      ReferenceListenerMethod,
					Origin:    OriginListener,
					Name:      phpquery.StringValue(literal),
					Node:      literal,
					Container: item,
					Class:     className,
					Method:    phpquery.MethodName(method),
				}, true
			}
		}
	}
	return Reference{}, false
}

func YAMLReferenceAt(node *yamlsyntax.Node) (Reference, bool) {
	if node == nil || node.Kind() != yamlsyntax.YamlScalar {
		return Reference{}, false
	}
	pair := yamlquery.AncestorPair(node)
	if pair == nil || yamlquery.PairValue(pair) != node {
		return Reference{}, false
	}
	property := yamlquery.ScalarValue(yamlquery.PairKey(pair))
	if property != "event" && property != "method" {
		return Reference{}, false
	}
	tag := yamlListenerTagAt(pair)
	if tag == nil {
		return Reference{}, false
	}
	role := ReferenceEvent
	if property == "method" {
		role = ReferenceListenerMethod
	}
	return Reference{
		Role:      role,
		Origin:    OriginListener,
		Name:      yamlquery.ScalarValue(node),
		Node:      node,
		Container: tag,
	}, true
}

func XMLReferenceAt(node *cst.Node) (Reference, bool) {
	attribute := xmlquery.AttributeAt(node)
	if attribute == nil {
		return Reference{}, false
	}
	name := xmlquery.AttributeName(attribute)
	if name != "event" && name != "method" {
		return Reference{}, false
	}
	tag := xmlquery.ElementAt(attribute)
	if tag == nil || xmlquery.ElementName(tag) != "tag" ||
		xmlquery.AttributeValue(xmlquery.Attribute(tag, "name")) !=
			"kernel.event_listener" {
		return Reference{}, false
	}
	role := ReferenceEvent
	if name == "method" {
		role = ReferenceListenerMethod
	}
	return Reference{
		Role:      role,
		Origin:    OriginListener,
		Name:      xmlquery.AttributeValue(attribute),
		Node:      attribute,
		Container: tag,
	}, true
}

func PublicMethods(
	index *php.PHPIndex,
	className string,
) []semantic.Symbol {
	if index == nil || className == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: index.SemanticSnapshot(),
	}).All(types.Named(normalizeName(className)))
	seen := make(map[string]struct{}, len(members))
	var result []semantic.Symbol
	for _, member := range members {
		symbol := member.Symbol
		if symbol.Kind != semantic.MethodSymbol ||
			symbol.Visibility != semantic.Public ||
			strings.EqualFold(symbol.Name, "getSubscribedEvents") ||
			strings.HasPrefix(symbol.Name, "__") && symbol.Name != "__invoke" ||
			strings.HasPrefix(strings.ToLower(symbol.Name), "set") {
			continue
		}
		key := strings.ToLower(symbol.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, symbol)
	}
	sortSymbols(result)
	return result
}

func ResolveListener(
	index *Index,
	path string,
	offset uint32,
	reference Reference,
) (Occurrence, bool, error) {
	if reference.Class != "" {
		return Occurrence{
			Kind:   ListenerOccurrence,
			Class:  reference.Class,
			Method: reference.Name,
		}, true, nil
	}
	if index == nil {
		return Occurrence{}, false, nil
	}
	if reference.Node != nil {
		offset = reference.Node.RangeTrimmedTrivia().Start
	}
	return index.ListenerAt(path, offset)
}

func validateDispatchReference(
	ctx context.Context,
	reference Reference,
) bool {
	phpContext := php.GetPHPContext(ctx)
	if phpContext == nil || phpContext.Document == nil ||
		reference.Container == nil {
		return false
	}
	receiver := phpquery.CallReceiver(reference.Container)
	if receiver == nil {
		return false
	}
	receiverType := phpContext.Document.TypeOf(receiver).Type
	if receiverType.IsUnknown() {
		return false
	}
	return isDispatcherType(receiverType, phpContext.Snapshot)
}

func validateSubscriberClass(
	ctx context.Context,
	class *phpsyntax.Node,
	nameResolver *php.NameResolver,
) bool {
	if class == nil {
		return false
	}
	className := resolvedClassName(class, nameResolver)
	phpContext := php.GetPHPContext(ctx)
	if phpContext != nil && phpContext.Snapshot != nil &&
		className != "" {
		if phpContext.Snapshot.Relations().IsSubtype(
			types.Named(className),
			types.Named(eventSubscriberInterface),
		) {
			return true
		}
	}
	return isSubscriberClass(class, nameResolver)
}

func yamlListenerTagAt(node *yamlsyntax.Node) *yamlsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if !yamlquery.IsMapping(current) {
			continue
		}
		if yamlquery.ScalarValue(yamlquery.Property(current, "name")) ==
			"kernel.event_listener" {
			return current
		}
	}
	return nil
}

func nodeContains(parent, child *cst.Node) bool {
	if parent == nil || child == nil {
		return false
	}
	parentRange := parent.Range()
	childRange := child.Range()
	return childRange.Start >= parentRange.Start &&
		childRange.End <= parentRange.End
}

func sortSymbols(symbols []semantic.Symbol) {
	sort.Slice(symbols, func(left, right int) bool {
		return strings.ToLower(symbols[left].Name) <
			strings.ToLower(symbols[right].Name)
	})
}
