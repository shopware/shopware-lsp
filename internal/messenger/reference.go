package messenger

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const messageSubscriberInterface = "Symfony\\Component\\Messenger\\Handler\\MessageSubscriberInterface"

type HandlerMethodReference struct {
	Name  string
	Class string
	Node  *cst.Node
}

type ReferenceRole uint8

const (
	ReferenceNone ReferenceRole = iota
	ReferenceMessage
	ReferenceHandlerMethod
)

type Reference struct {
	Role   ReferenceRole
	Name   string
	Node   *cst.Node
	Class  string
	Method string
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
		return YAMLReferenceAt(root, node)
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
	if legacy, found := HandlerMethodReferenceAt(
		ctx,
		root,
		node,
	); found {
		return Reference{
			Role:   ReferenceHandlerMethod,
			Name:   legacy.Name,
			Node:   legacy.Node,
			Class:  legacy.Class,
			Method: legacy.Name,
		}, true
	}
	nameResolver := php.NewNameResolver(root)
	if attribute := phpquery.AttributeAt(node); attribute != nil &&
		isAsMessageHandler(attribute, nameResolver) {
		className := resolvedClassName(
			phpquery.ClassAt(attribute),
			nameResolver,
		)
		for index, argument := range phpquery.Arguments(attribute) {
			expression := phpquery.ArgumentExpression(attribute, index)
			if expression == nil || !nodeContains(expression, node) {
				continue
			}
			name := strings.ToLower(phpquery.ArgumentName(argument))
			if name == "" {
				switch index {
				case 2:
					name = "handles"
				case 3:
					name = "method"
				}
			}
			switch name {
			case "handles":
				return Reference{
					Role:  ReferenceMessage,
					Name:  messageName(expression, nameResolver),
					Node:  expression,
					Class: className,
				}, true
			case "method":
				return Reference{
					Role:   ReferenceHandlerMethod,
					Name:   stringNodeValue(expression),
					Node:   expression,
					Class:  className,
					Method: stringNodeValue(expression),
				}, true
			}
		}
	}

	call := phpquery.CallAt(node)
	if call == nil ||
		!strings.EqualFold(phpquery.CallMethodName(call), "tag") ||
		stringArgument(phpquery.ArgumentExpression(call, 0)) !=
			"messenger.message_handler" {
		return Reference{}, false
	}
	options := phpquery.ArrayAt(phpquery.ArgumentExpression(call, 1))
	item := phpquery.ArrayItemAt(node)
	if options == nil || item == nil || !nodeContains(options, item) {
		return Reference{}, false
	}
	key := strings.ToLower(
		phpquery.StringValue(phpquery.ArrayItemKey(item)),
	)
	value := phpquery.ArrayItemValue(item)
	if value == nil || !nodeContains(value, node) {
		return Reference{}, false
	}
	className := serviceTagClass(call, nameResolver)
	switch key {
	case "handles":
		return Reference{
			Role:  ReferenceMessage,
			Name:  messageName(value, nameResolver),
			Node:  value,
			Class: className,
		}, true
	case "method":
		return Reference{
			Role:   ReferenceHandlerMethod,
			Name:   stringNodeValue(value),
			Node:   value,
			Class:  className,
			Method: stringNodeValue(value),
		}, true
	default:
		return Reference{}, false
	}
}

func YAMLReferenceAt(
	root,
	node *yamlsyntax.Node,
) (Reference, bool) {
	scalar := yamlScalarAt(node)
	if scalar == nil {
		return Reference{}, false
	}
	pair := yamlquery.AncestorPair(scalar)
	if pair == nil || yamlquery.PairValue(pair) != scalar {
		return Reference{}, false
	}
	property := strings.ToLower(
		yamlquery.ScalarValue(yamlquery.PairKey(pair)),
	)
	if property != "handles" && property != "method" {
		return Reference{}, false
	}
	tag := yamlTagMappingAt(pair)
	if tag == nil ||
		yamlquery.ScalarValue(yamlquery.Property(tag, "name")) !=
			"messenger.message_handler" {
		return Reference{}, false
	}
	className, service := yamlServiceAt(root, scalar)
	value := yamlquery.ScalarValue(scalar)
	if property == "handles" {
		return Reference{
			Role:  ReferenceMessage,
			Name:  normalizeName(value),
			Node:  scalar,
			Class: className,
		}, true
	}
	return Reference{
		Role:   ReferenceHandlerMethod,
		Name:   value,
		Node:   scalar,
		Class:  className,
		Method: value,
	}, service != "" || className != ""
}

func XMLReferenceAt(node *xmlsyntax.Node) (Reference, bool) {
	attribute := xmlquery.AttributeAt(node)
	if attribute == nil {
		return Reference{}, false
	}
	property := strings.ToLower(xmlquery.AttributeName(attribute))
	if property != "handles" && property != "method" {
		return Reference{}, false
	}
	tag := xmlquery.ElementAt(attribute)
	if tag == nil || xmlquery.ElementName(tag) != "tag" ||
		xmlquery.AttributeValue(xmlquery.Attribute(tag, "name")) !=
			"messenger.message_handler" {
		return Reference{}, false
	}
	service := xmlquery.ParentElement(tag)
	for service != nil && xmlquery.ElementName(service) != "service" {
		service = xmlquery.ParentElement(service)
	}
	className := ""
	if service != nil {
		className = normalizeName(
			xmlquery.AttributeValue(xmlquery.Attribute(service, "class")),
		)
		if className == "" {
			id := xmlquery.AttributeValue(xmlquery.Attribute(service, "id"))
			if strings.Contains(id, `\`) {
				className = normalizeName(id)
			}
		}
	}
	value := xmlquery.AttributeValue(attribute)
	if property == "handles" {
		return Reference{
			Role:  ReferenceMessage,
			Name:  normalizeName(value),
			Node:  attribute,
			Class: className,
		}, true
	}
	return Reference{
		Role:   ReferenceHandlerMethod,
		Name:   value,
		Node:   attribute,
		Class:  className,
		Method: value,
	}, true
}

// HandlerMethodReferenceAt recognizes legacy Messenger subscriber method
// declarations in MessageSubscriberInterface::getHandledMessages().
//
// Supported forms include:
//
//	return [Message::class => 'handle'];
//	yield Message::class => ['method' => 'handle'];
func HandlerMethodReferenceAt(
	ctx context.Context,
	root,
	node *phpsyntax.Node,
) (HandlerMethodReference, bool) {
	literal := phpquery.StringAt(node)
	if literal == nil {
		return HandlerMethodReference{}, false
	}
	method := phpquery.MethodAt(literal)
	if method == nil ||
		!strings.EqualFold(
			phpquery.MethodName(method),
			"getHandledMessages",
		) ||
		phpquery.FunctionLikeAt(literal) != method {
		return HandlerMethodReference{}, false
	}
	class := phpquery.ClassAt(method)
	nameResolver := php.NewNameResolver(root)
	className := resolvedClassName(class, nameResolver)
	if !isMessageSubscriber(ctx, class, className, nameResolver) ||
		!isHandlerMethodValue(literal, method) {
		return HandlerMethodReference{}, false
	}
	return HandlerMethodReference{
		Name:  phpquery.StringValue(literal),
		Class: className,
		Node:  literal,
	}, true
}

func PublicHandlerMethods(
	index *php.PHPIndex,
	className string,
) []semantic.Symbol {
	if index == nil || className == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: index.SemanticSnapshot(),
	}).All(types.Named(strings.TrimPrefix(className, `\`)))
	seen := make(map[string]struct{}, len(members))
	var result []semantic.Symbol
	for _, member := range members {
		symbol := member.Symbol
		if symbol.Kind != semantic.MethodSymbol ||
			symbol.Visibility != semantic.Public ||
			strings.EqualFold(symbol.Name, "getHandledMessages") ||
			strings.HasPrefix(symbol.Name, "__") {
			continue
		}
		key := strings.ToLower(symbol.Name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, symbol)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result
}

func isHandlerMethodValue(
	literal,
	method *phpsyntax.Node,
) bool {
	if yield := ancestor(
		literal,
		phpsyntax.PhpYieldExpression,
	); yield != nil && phpquery.FunctionLikeAt(yield) == method {
		children := directChildren(yield)
		if len(children) < 2 || !nodeContains(children[1], literal) {
			return false
		}
		if children[1] == literal {
			return true
		}
		return isNamedMethodOption(literal, children[1])
	}

	statement := ancestor(literal, phpsyntax.PhpReturnStatement)
	if statement == nil || phpquery.FunctionLikeAt(statement) != method {
		return false
	}
	array := phpquery.DirectChild(statement, phpsyntax.PhpArray)
	if array == nil || !nodeContains(array, literal) {
		return false
	}
	for _, item := range phpquery.ArrayItems(array) {
		if !nodeContains(item, literal) {
			continue
		}
		key := phpquery.ArrayItemKey(item)
		value := phpquery.ArrayItemValue(item)
		if key == nil || value == nil || !nodeContains(value, literal) {
			return false
		}
		if value == literal {
			return true
		}
		return isNamedMethodOption(literal, value)
	}
	return false
}

func isNamedMethodOption(
	literal,
	value *phpsyntax.Node,
) bool {
	item := phpquery.ArrayItemAt(literal)
	if item == nil || !nodeContains(value, item) {
		return false
	}
	key := phpquery.ArrayItemKey(item)
	itemValue := phpquery.ArrayItemValue(item)
	return key != nil && itemValue != nil &&
		strings.EqualFold(phpquery.StringValue(key), "method") &&
		nodeContains(itemValue, literal)
}

func isMessageSubscriber(
	ctx context.Context,
	class *phpsyntax.Node,
	className string,
	nameResolver *php.NameResolver,
) bool {
	if class == nil {
		return false
	}
	phpContext := php.GetPHPContext(ctx)
	if phpContext != nil && phpContext.Snapshot != nil &&
		className != "" &&
		phpContext.Snapshot.Relations().IsSubtype(
			types.Named(className),
			types.Named(messageSubscriberInterface),
		) {
		return true
	}
	for _, implemented := range phpquery.ClassImplements(class) {
		if strings.EqualFold(
			strings.TrimPrefix(nameResolver.Resolve(implemented), `\`),
			messageSubscriberInterface,
		) {
			return true
		}
	}
	return false
}

func resolvedClassName(
	class *phpsyntax.Node,
	nameResolver *php.NameResolver,
) string {
	if class == nil {
		return ""
	}
	return strings.TrimPrefix(
		nameResolver.Resolve(phpquery.ClassName(class)),
		`\`,
	)
}

func ancestor(
	node *phpsyntax.Node,
	kind phpsyntax.Kind,
) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func directChildren(node *phpsyntax.Node) []*phpsyntax.Node {
	if node == nil {
		return nil
	}
	var result []*phpsyntax.Node
	for child := range node.ChildNodes() {
		result = append(result, child)
	}
	return result
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

func messageName(
	node *phpsyntax.Node,
	nameResolver *php.NameResolver,
) string {
	if className := phpquery.ClassConstantName(node); className != "" {
		return normalizeName(nameResolver.Resolve(className))
	}
	return normalizeName(stringNodeValue(node))
}

func stringNodeValue(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	if literal := phpquery.StringAt(node); literal != nil {
		return phpquery.StringValue(literal)
	}
	return ""
}

func yamlScalarAt(node *yamlsyntax.Node) *yamlsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == yamlsyntax.YamlScalar {
			return current
		}
	}
	return nil
}

func yamlTagMappingAt(node *yamlsyntax.Node) *yamlsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if yamlquery.IsMapping(current) &&
			yamlquery.Property(current, "name") != nil {
			return current
		}
	}
	return nil
}

func yamlServiceAt(
	root,
	node *yamlsyntax.Node,
) (string, string) {
	path := yamlquery.PairPath(node)
	if len(path) < 2 || path[0] != "services" {
		return "", ""
	}
	serviceID := path[1]
	rootValue := yamlquery.RootValue(root)
	services := yamlquery.Property(rootValue, "services")
	definition := yamlquery.Property(services, serviceID)
	className := normalizeName(
		yamlquery.ScalarValue(
			yamlquery.Property(definition, "class"),
		),
	)
	if className == "" && strings.Contains(serviceID, `\`) {
		className = normalizeName(serviceID)
	}
	return className, serviceID
}
