package messenger

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const asMessageHandlerAttribute = "Symfony\\Component\\Messenger\\Attribute\\AsMessageHandler"

func parsePHP(
	file *indexer.ParsedFile,
	phpIndex *php.PHPIndex,
) []Occurrence {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	root := tree.Root
	nameResolver := php.NewNameResolver(root)
	var result []Occurrence
	for _, class := range phpquery.Classes(root) {
		className := resolvedClassName(class, nameResolver)
		if className == "" {
			continue
		}
		classRange := nodeNameRange(class)
		for _, attribute := range phpquery.Attributes(class) {
			if !isAsMessageHandler(attribute, nameResolver) {
				continue
			}
			methodName := stringArgument(
				attributeArgument(attribute, "method", 3),
			)
			if methodName == "" {
				methodName = "__invoke"
			}
			result = appendHandlerAttributes(
				result,
				attribute,
				className,
				methodName,
				classRange,
				methodNameRange(class, methodName),
				methodParameterMessageNames(
					class,
					methodName,
					nameResolver,
				),
				nameResolver,
			)
		}
		for _, method := range phpquery.Methods(class) {
			methodName := phpquery.MethodName(method)
			for _, attribute := range phpquery.Attributes(method) {
				if !isAsMessageHandler(attribute, nameResolver) {
					continue
				}
				result = appendHandlerAttributes(
					result,
					attribute,
					className,
					methodName,
					classRange,
					nodeNameRange(method),
					parameterMessageNames(method, nameResolver),
					nameResolver,
				)
			}
		}
		result = append(
			result,
			parseLegacySubscriber(class, className, nameResolver)...,
		)
	}
	result = append(
		result,
		parsePHPServiceTags(root, nameResolver)...,
	)
	result = append(
		result,
		parseDispatches(
			file.Path,
			root,
			nameResolver,
			phpIndex,
		)...,
	)
	return uniqueOccurrences(result)
}

func appendHandlerAttributes(
	result []Occurrence,
	attribute *phpsyntax.Node,
	className,
	methodName string,
	classRange,
	handlerRange phpsyntax.TextRange,
	inferred []string,
	nameResolver *php.NameResolver,
) []Occurrence {
	handlesNode := attributeArgument(attribute, "handles", 2)
	names := classExpressionNames(handlesNode, nameResolver)
	messageRange := nodeRange(handlesNode)
	if len(names) == 0 {
		names = inferred
	}
	if len(names) == 0 {
		names = []string{""}
	}
	for _, name := range names {
		rng := messageRange
		if rng.Len() == 0 {
			rng = attribute.RangeTrimmedTrivia()
		}
		result = append(result, Occurrence{
			Kind:         HandlerOccurrence,
			Source:       AttributeSource,
			Message:      name,
			Range:        rng,
			MessageRange: messageRange,
			HandlerRange: handlerRange,
			ClassRange:   classRange,
			Class:        className,
			Method:       methodName,
			Bus: stringArgument(
				attributeArgument(attribute, "bus", 0),
			),
			Transport: stringArgument(
				attributeArgument(attribute, "fromTransport", 1),
			),
			Priority: expressionText(
				attributeArgument(attribute, "priority", 4),
			),
		})
	}
	return result
}

func parseLegacySubscriber(
	class *phpsyntax.Node,
	className string,
	nameResolver *php.NameResolver,
) []Occurrence {
	var result []Occurrence
	for _, method := range phpquery.Methods(class) {
		if !strings.EqualFold(
			phpquery.MethodName(method),
			"getHandledMessages",
		) {
			continue
		}
		for _, statement := range phpquery.Nodes(
			method,
			phpsyntax.PhpReturnStatement,
		) {
			if phpquery.FunctionLikeAt(statement) != method {
				continue
			}
			array := phpquery.DirectChild(statement, phpsyntax.PhpArray)
			for _, item := range phpquery.ArrayItems(array) {
				messageNode := phpquery.ArrayItemKey(item)
				value := phpquery.ArrayItemValue(item)
				messageNames := classExpressionNames(
					messageNode,
					nameResolver,
				)
				handlerName, handlerNode := handlerMethodValue(value)
				for _, messageName := range messageNames {
					result = append(result, Occurrence{
						Kind:         HandlerOccurrence,
						Source:       SubscriberSource,
						Message:      messageName,
						Range:        nodeRange(messageNode),
						MessageRange: nodeRange(messageNode),
						HandlerRange: nodeRange(handlerNode),
						ClassRange:   nodeNameRange(class),
						Class:        className,
						Method:       handlerName,
					})
				}
			}
		}
		for _, yield := range phpquery.Nodes(
			method,
			phpsyntax.PhpYieldExpression,
		) {
			if phpquery.FunctionLikeAt(yield) != method {
				continue
			}
			children := directChildren(yield)
			if len(children) < 2 {
				continue
			}
			handlerName, handlerNode := handlerMethodValue(children[1])
			for _, messageName := range classExpressionNames(
				children[0],
				nameResolver,
			) {
				result = append(result, Occurrence{
					Kind:         HandlerOccurrence,
					Source:       SubscriberSource,
					Message:      messageName,
					Range:        nodeRange(children[0]),
					MessageRange: nodeRange(children[0]),
					HandlerRange: nodeRange(handlerNode),
					ClassRange:   nodeNameRange(class),
					Class:        className,
					Method:       handlerName,
				})
			}
		}
	}
	return result
}

func handlerMethodValue(
	value *phpsyntax.Node,
) (string, *phpsyntax.Node) {
	if value == nil {
		return "", nil
	}
	if value.Kind() == phpsyntax.PhpString {
		return phpquery.StringValue(value), value
	}
	array := phpquery.ArrayAt(value)
	if array == nil || !nodeContains(value, array) {
		return "", nil
	}
	for _, item := range phpquery.ArrayItems(array) {
		key := phpquery.ArrayItemKey(item)
		itemValue := phpquery.ArrayItemValue(item)
		if strings.EqualFold(phpquery.StringValue(key), "method") &&
			itemValue != nil && itemValue.Kind() == phpsyntax.PhpString {
			return phpquery.StringValue(itemValue), itemValue
		}
	}
	return "", nil
}

func parsePHPServiceTags(
	root *phpsyntax.Node,
	nameResolver *php.NameResolver,
) []Occurrence {
	var result []Occurrence
	for _, call := range phpquery.Calls(root) {
		if !strings.EqualFold(phpquery.CallMethodName(call), "tag") ||
			stringArgument(phpquery.ArgumentExpression(call, 0)) !=
				"messenger.message_handler" {
			continue
		}
		className := serviceTagClass(call, nameResolver)
		options := phpquery.ArrayAt(phpquery.ArgumentExpression(call, 1))
		handlesNode := phpArrayProperty(options, "handles")
		methodNode := phpArrayProperty(options, "method")
		methodName := stringArgument(methodNode)
		if methodName == "" {
			methodName = "__invoke"
		}
		names := classExpressionNames(handlesNode, nameResolver)
		if len(names) == 0 {
			names = []string{""}
		}
		for _, name := range names {
			rng := nodeRange(handlesNode)
			if rng.Len() == 0 {
				rng = call.RangeTrimmedTrivia()
			}
			result = append(result, Occurrence{
				Kind:         HandlerOccurrence,
				Source:       ServiceTagSource,
				Message:      name,
				Range:        rng,
				MessageRange: nodeRange(handlesNode),
				HandlerRange: nodeRange(methodNode),
				Class:        className,
				Method:       methodName,
				Bus: stringArgument(
					phpArrayProperty(options, "bus"),
				),
				Transport: stringArgument(
					phpArrayProperty(options, "from_transport"),
				),
				Priority: expressionText(
					phpArrayProperty(options, "priority"),
				),
			})
		}
	}
	return result
}

func parseDispatches(
	path string,
	root *phpsyntax.Node,
	nameResolver *php.NameResolver,
	phpIndex *php.PHPIndex,
) []Occurrence {
	var document *semantic.Document
	analyze := func() *semantic.Document {
		if document == nil && phpIndex != nil {
			document = phpIndex.AnalyzeDocument(path, 0, root)
		}
		return document
	}
	var result []Occurrence
	for _, call := range phpquery.Calls(root) {
		if !strings.EqualFold(phpquery.CallMethodName(call), "dispatch") {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		isBus, known := classifyMessageBusReceiver(
			call,
			receiver,
			nameResolver,
		)
		if !known {
			currentDocument := analyze()
			if currentDocument != nil {
				isBus = isMessageBusType(
					currentDocument.TypeOf(receiver).Type,
					phpIndex,
				)
			}
		}
		if !isBus {
			continue
		}
		messageNode := phpquery.ArgumentExpression(call, 0)
		if firstObjectCreation(messageNode) == nil {
			analyze()
		}
		names := expressionMessageNames(
			messageNode,
			nameResolver,
			document,
		)
		for _, name := range names {
			rng := nodeRange(messageNode)
			if object := firstObjectCreation(messageNode); object != nil {
				rng = nodeRange(object)
			}
			result = append(result, Occurrence{
				Kind:         DispatchOccurrence,
				Source:       DispatchSource,
				Message:      name,
				Range:        rng,
				MessageRange: rng,
				Class: resolvedClassName(
					phpquery.ClassAt(call),
					nameResolver,
				),
				Method: phpquery.MethodName(call),
			})
		}
	}
	return result
}

func classifyMessageBusReceiver(
	call *phpsyntax.Node,
	receiver *phpsyntax.Node,
	nameResolver *php.NameResolver,
) (bool, bool) {
	if receiver == nil {
		return false, true
	}
	declared := declaredReceiverType(call, receiver, nameResolver)
	if declared != "" {
		return isMessageBusName(declared), true
	}
	text := strings.ToLower(
		strings.ReplaceAll(receiver.Text(), "_", ""),
	)
	switch {
	case strings.Contains(text, "messagebus"):
		return true, true
	case strings.Contains(text, "eventdispatcher"),
		strings.Contains(text, "eventbus"):
		return false, true
	default:
		return false, false
	}
}

func isMessageBusType(
	value types.Type,
	phpIndex *php.PHPIndex,
) bool {
	if value.Kind() == types.UnionKind ||
		value.Kind() == types.IntersectionKind {
		for _, part := range value.Arguments() {
			if isMessageBusType(part, phpIndex) {
				return true
			}
		}
		return false
	}
	name := normalizeName(value.Name())
	if isMessageBusName(name) {
		return true
	}
	return phpIndex != nil && name != "" &&
		phpIndex.SemanticSnapshot().Relations().IsSubtype(
			value,
			types.Named(
				"Symfony\\Component\\Messenger\\MessageBusInterface",
			),
		)
}

func isMessageBusName(value string) bool {
	value = strings.ToLower(normalizeName(value))
	return value == strings.ToLower(
		"Symfony\\Component\\Messenger\\MessageBusInterface",
	) || strings.HasSuffix(value, `\messagebusinterface`)
}

func expressionMessageNames(
	node *phpsyntax.Node,
	nameResolver *php.NameResolver,
	document *semantic.Document,
) []string {
	if object := firstObjectCreation(node); object != nil {
		return []string{normalizeName(nameResolver.Resolve(
			phpquery.ObjectClassName(object),
		))}
	}
	if document == nil || node == nil {
		return nil
	}
	return uniqueNames(objectTypeNames(document.TypeOf(node).Type))
}

func classExpressionNames(
	node *phpsyntax.Node,
	nameResolver *php.NameResolver,
) []string {
	if node == nil {
		return nil
	}
	if className := phpquery.ClassConstantName(node); className != "" {
		return []string{normalizeName(nameResolver.Resolve(className))}
	}
	if node.Kind() == phpsyntax.PhpString {
		value := normalizeName(phpquery.StringValue(node))
		if strings.Contains(value, `\`) {
			return []string{value}
		}
	}
	return nil
}

func methodParameterMessageNames(
	class *phpsyntax.Node,
	methodName string,
	nameResolver *php.NameResolver,
) []string {
	for _, method := range phpquery.Methods(class) {
		if strings.EqualFold(phpquery.MethodName(method), methodName) {
			return parameterMessageNames(method, nameResolver)
		}
	}
	return nil
}

func parameterMessageNames(
	method *phpsyntax.Node,
	nameResolver *php.NameResolver,
) []string {
	parameters := phpquery.Parameters(method)
	if len(parameters) == 0 {
		return nil
	}
	var result []string
	for _, part := range strings.FieldsFunc(
		phpquery.ParameterType(parameters[0]),
		func(value rune) bool {
			return value == '|' || value == '&'
		},
	) {
		part = strings.TrimPrefix(strings.TrimSpace(part), "?")
		switch strings.ToLower(strings.TrimPrefix(part, `\`)) {
		case "", "array", "bool", "callable", "false", "float", "int",
			"iterable", "mixed", "never", "null", "object", "string",
			"true", "void":
			continue
		}
		result = append(
			result,
			normalizeName(nameResolver.Resolve(part)),
		)
	}
	return uniqueNames(result)
}

func parseYAML(file *indexer.ParsedFile) []Occurrence {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	root := yamlquery.RootValue(tree.Root)
	services := yamlquery.Property(root, "services")
	if !yamlquery.IsMapping(services) {
		return nil
	}
	var result []Occurrence
	for _, servicePair := range yamlquery.Pairs(services) {
		serviceID := yamlquery.ScalarValue(yamlquery.PairKey(servicePair))
		definition := yamlquery.PairValue(servicePair)
		if !yamlquery.IsMapping(definition) {
			continue
		}
		className := normalizeName(
			yamlquery.ScalarValue(yamlquery.Property(definition, "class")),
		)
		if className == "" && strings.Contains(serviceID, `\`) {
			className = normalizeName(serviceID)
		}
		tags := yamlquery.Property(definition, "tags")
		if yamlquery.IsSequence(tags) {
			for _, item := range yamlquery.Items(tags) {
				result = append(
					result,
					parseYAMLHandlerTag(
						yamlquery.ItemValue(item),
						serviceID,
						className,
					)...,
				)
			}
		} else {
			result = append(
				result,
				parseYAMLHandlerTag(tags, serviceID, className)...,
			)
		}
	}
	return uniqueOccurrences(result)
}

func parseYAMLHandlerTag(
	tag *yamlsyntax.Node,
	service,
	className string,
) []Occurrence {
	if tag == nil {
		return nil
	}
	if tag.Kind() == yamlsyntax.YamlScalar {
		if yamlquery.ScalarValue(tag) != "messenger.message_handler" {
			return nil
		}
		return []Occurrence{{
			Kind:    HandlerOccurrence,
			Source:  ServiceTagSource,
			Range:   tag.RangeTrimmedTrivia(),
			Class:   className,
			Method:  "__invoke",
			Service: service,
		}}
	}
	if !yamlquery.IsMapping(tag) ||
		yamlquery.ScalarValue(yamlquery.Property(tag, "name")) !=
			"messenger.message_handler" {
		return nil
	}
	handlesNode := yamlquery.Property(tag, "handles")
	methodNode := yamlquery.Property(tag, "method")
	message := normalizeName(yamlquery.ScalarValue(handlesNode))
	methodName := yamlquery.ScalarValue(methodNode)
	if methodName == "" {
		methodName = "__invoke"
	}
	rng := yamlNodeRange(handlesNode)
	if rng.Len() == 0 {
		rng = tag.RangeTrimmedTrivia()
	}
	return []Occurrence{{
		Kind:         HandlerOccurrence,
		Source:       ServiceTagSource,
		Message:      message,
		Range:        rng,
		MessageRange: yamlNodeRange(handlesNode),
		HandlerRange: yamlNodeRange(methodNode),
		Class:        className,
		Method:       methodName,
		Service:      service,
		Bus: yamlquery.ScalarValue(
			yamlquery.Property(tag, "bus"),
		),
		Transport: yamlquery.ScalarValue(
			yamlquery.Property(tag, "from_transport"),
		),
		Priority: yamlquery.ScalarValue(
			yamlquery.Property(tag, "priority"),
		),
	}}
}

func parseXML(file *indexer.ParsedFile) []Occurrence {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	var result []Occurrence
	for _, serviceNode := range xmlquery.Elements(tree.Root, "service") {
		attributes := xmlquery.AttributeValues(serviceNode)
		serviceID := attributes["id"]
		className := normalizeName(attributes["class"])
		if className == "" && strings.Contains(serviceID, `\`) {
			className = normalizeName(serviceID)
		}
		for _, tag := range xmlquery.ChildElements(serviceNode, "tag") {
			values := xmlquery.AttributeValues(tag)
			if values["name"] != "messenger.message_handler" {
				continue
			}
			handlesNode := xmlquery.Attribute(tag, "handles")
			methodNode := xmlquery.Attribute(tag, "method")
			methodName := values["method"]
			if methodName == "" {
				methodName = "__invoke"
			}
			rng := xmlNodeRange(handlesNode)
			if rng.Len() == 0 {
				rng = tag.RangeTrimmedTrivia()
			}
			result = append(result, Occurrence{
				Kind:         HandlerOccurrence,
				Source:       ServiceTagSource,
				Message:      normalizeName(values["handles"]),
				Range:        rng,
				MessageRange: xmlNodeRange(handlesNode),
				HandlerRange: xmlNodeRange(methodNode),
				Class:        className,
				Method:       methodName,
				Service:      serviceID,
				Bus:          values["bus"],
				Transport:    values["from-transport"],
				Priority:     values["priority"],
			})
		}
	}
	return uniqueOccurrences(result)
}

func isAsMessageHandler(
	attribute *phpsyntax.Node,
	nameResolver *php.NameResolver,
) bool {
	return strings.EqualFold(
		normalizeName(nameResolver.Resolve(
			phpquery.AttributeName(attribute),
		)),
		asMessageHandlerAttribute,
	)
}

func attributeArgument(
	attribute *phpsyntax.Node,
	name string,
	fallback int,
) *phpsyntax.Node {
	for index, argument := range phpquery.Arguments(attribute) {
		if strings.EqualFold(phpquery.ArgumentName(argument), name) {
			return phpquery.ArgumentExpression(attribute, index)
		}
	}
	if fallback < 0 {
		return nil
	}
	argument := phpquery.Argument(attribute, fallback)
	if argument == nil || phpquery.ArgumentName(argument) != "" {
		return nil
	}
	return phpquery.ArgumentExpression(attribute, fallback)
}

func stringArgument(node *phpsyntax.Node) string {
	if node == nil || node.Kind() != phpsyntax.PhpString {
		return ""
	}
	return phpquery.StringValue(node)
}

func expressionText(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	return strings.TrimSpace(node.Text())
}

func nodeRange(node *phpsyntax.Node) phpsyntax.TextRange {
	if node == nil {
		return phpsyntax.TextRange{}
	}
	return node.RangeTrimmedTrivia()
}

func nodeNameRange(node *phpsyntax.Node) phpsyntax.TextRange {
	return nodeRange(phpquery.DirectChild(node, phpsyntax.PhpName))
}

func methodNameRange(
	class *phpsyntax.Node,
	name string,
) phpsyntax.TextRange {
	for _, method := range phpquery.Methods(class) {
		if strings.EqualFold(phpquery.MethodName(method), name) {
			return nodeNameRange(method)
		}
	}
	return phpsyntax.TextRange{}
}

func phpArrayProperty(
	array *phpsyntax.Node,
	name string,
) *phpsyntax.Node {
	if array == nil {
		return nil
	}
	for _, item := range phpquery.ArrayItems(array) {
		if strings.EqualFold(
			phpquery.StringValue(phpquery.ArrayItemKey(item)),
			name,
		) {
			return phpquery.ArrayItemValue(item)
		}
	}
	return nil
}

func serviceTagClass(
	tagCall *phpsyntax.Node,
	nameResolver *php.NameResolver,
) string {
	statement := tagCall
	for statement != nil &&
		statement.Kind() != phpsyntax.PhpExpressionStatement {
		statement = statement.Parent()
	}
	if statement == nil {
		statement = phpquery.FunctionLikeAt(tagCall)
	}
	for _, call := range phpquery.Calls(statement) {
		switch strings.ToLower(phpquery.CallMethodName(call)) {
		case "set":
			if className := classArgument(
				phpquery.ArgumentExpression(call, 1),
				nameResolver,
			); className != "" {
				return className
			}
			if className := classArgument(
				phpquery.ArgumentExpression(call, 0),
				nameResolver,
			); className != "" {
				return className
			}
		case "get":
			if className := classArgument(
				phpquery.ArgumentExpression(call, 0),
				nameResolver,
			); className != "" {
				return className
			}
		}
	}
	return ""
}

func classArgument(
	node *phpsyntax.Node,
	nameResolver *php.NameResolver,
) string {
	if node == nil {
		return ""
	}
	if className := phpquery.ClassConstantName(node); className != "" {
		return normalizeName(nameResolver.Resolve(className))
	}
	if node.Kind() == phpsyntax.PhpString {
		value := phpquery.StringValue(node)
		if strings.Contains(value, `\`) {
			return normalizeName(value)
		}
	}
	return ""
}

func declaredReceiverType(
	call,
	receiver *phpsyntax.Node,
	nameResolver *php.NameResolver,
) string {
	text := compactExpression(receiver.Text())
	if strings.HasPrefix(text, "$this->") {
		member := text[strings.LastIndex(text, "->")+2:]
		class := phpquery.ClassAt(call)
		for _, property := range phpquery.Properties(class) {
			for _, variable := range phpquery.PropertyVariables(property) {
				if strings.EqualFold(
					phpquery.VariableName(variable),
					member,
				) {
					return resolveFirstType(
						phpquery.PropertyType(property),
						nameResolver,
					)
				}
			}
		}
		for _, method := range phpquery.Methods(class) {
			if strings.EqualFold(
				phpquery.MethodName(method),
				"__construct",
			) {
				if value := parameterTypeByName(
					method,
					member,
					nameResolver,
				); value != "" {
					return value
				}
			}
		}
		return ""
	}
	if receiver.Kind() != phpsyntax.PhpVariable {
		return ""
	}
	return parameterTypeByName(
		phpquery.FunctionLikeAt(call),
		phpquery.VariableName(receiver),
		nameResolver,
	)
}

func parameterTypeByName(
	function *phpsyntax.Node,
	name string,
	nameResolver *php.NameResolver,
) string {
	for _, parameter := range phpquery.Parameters(function) {
		if strings.EqualFold(
			strings.TrimPrefix(phpquery.ParameterName(parameter), "$"),
			strings.TrimPrefix(name, "$"),
		) {
			return resolveFirstType(
				phpquery.ParameterType(parameter),
				nameResolver,
			)
		}
	}
	return ""
}

func resolveFirstType(
	value string,
	nameResolver *php.NameResolver,
) string {
	names := resolveTypeNames(value, nameResolver)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func resolveTypeNames(
	value string,
	nameResolver *php.NameResolver,
) []string {
	var result []string
	for _, candidate := range strings.FieldsFunc(value, func(r rune) bool {
		return r == '|' || r == '&'
	}) {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "?")
		switch strings.ToLower(strings.TrimPrefix(candidate, `\`)) {
		case "", "array", "bool", "callable", "false", "float", "int",
			"iterable", "mixed", "never", "null", "object", "self",
			"static", "string", "true", "void":
			continue
		}
		result = append(
			result,
			normalizeName(nameResolver.Resolve(candidate)),
		)
	}
	return uniqueNames(result)
}

func firstObjectCreation(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	if node.Kind() == phpsyntax.PhpObjectCreation {
		return node
	}
	objects := phpquery.ObjectCreations(node)
	if len(objects) == 0 {
		return nil
	}
	return objects[0]
}

func compactExpression(value string) string {
	return strings.Map(func(character rune) rune {
		switch character {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return character
		}
	}, value)
}

func yamlNodeRange(node *yamlsyntax.Node) phpsyntax.TextRange {
	if node == nil {
		return phpsyntax.TextRange{}
	}
	return node.RangeTrimmedTrivia()
}

func xmlNodeRange(node *xmlsyntax.Node) phpsyntax.TextRange {
	if node == nil {
		return phpsyntax.TextRange{}
	}
	return node.RangeTrimmedTrivia()
}
