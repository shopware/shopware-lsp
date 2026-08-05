package event

import (
	"bytes"
	"regexp"
	"sort"
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

const (
	eventSubscriberInterface = "Symfony\\Component\\EventDispatcher\\EventSubscriberInterface"
	asEventListenerAttribute = "Symfony\\Component\\EventDispatcher\\Attribute\\AsEventListener"
)

var classConstantNamePattern = regexp.MustCompile(
	`(?i)([A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*)\s*=\s*$`,
)

type eventSpec struct {
	name       string
	expression string
	eventType  string
	rng        phpsyntax.TextRange
}

type recordCollector struct {
	path    string
	records map[string]*Record
}

func newRecordCollector(path string) *recordCollector {
	return &recordCollector{
		path:    path,
		records: make(map[string]*Record),
	}
}

func (collector *recordCollector) add(
	spec eventSpec,
	occurrence Occurrence,
) {
	occurrence.File = collector.path
	if occurrence.Range.Len() == 0 {
		occurrence.Range = spec.rng
	}
	if occurrence.EventType == "" {
		occurrence.EventType = spec.eventType
	}
	key := "name:" + strings.ToLower(spec.name)
	if spec.name == "" {
		key = "expression:" + strings.ToLower(spec.expression)
	}
	if spec.name == "" && spec.expression == "" {
		target := occurrence.Class
		if target == "" {
			target = occurrence.Service
		}
		if target == "" && occurrence.Method == "" {
			return
		}
		key = "inferred:" + strings.ToLower(
			target+"::"+occurrence.Method,
		)
	}
	record := collector.records[key]
	if record == nil {
		record = &Record{
			Name:       spec.name,
			Expression: spec.expression,
			EventType:  spec.eventType,
			File:       collector.path,
		}
		collector.records[key] = record
	}
	if record.EventType == "" {
		record.EventType = occurrence.EventType
	}
	record.Occurrences = append(record.Occurrences, occurrence)
}

func (collector *recordCollector) values() []Record {
	result := make([]Record, 0, len(collector.records))
	for _, record := range collector.records {
		record.Occurrences = uniqueOccurrences(record.Occurrences)
		result = append(result, *record)
	}
	sort.Slice(result, func(left, right int) bool {
		return recordKey(result[left]) < recordKey(result[right])
	})
	return result
}

func isPHPEventCandidate(content []byte) bool {
	return bytes.Contains(content, []byte("getSubscribedEvents")) ||
		bytes.Contains(content, []byte("AsEventListener")) ||
		bytes.Contains(content, []byte("kernel.event_listener")) ||
		bytes.Contains(content, []byte("->dispatch"))
}

func parsePHP(file *indexer.ParsedFile) []Record {
	root := file.SyntaxTree().Root
	if root == nil {
		return nil
	}
	resolver := php.NewNameResolver(root)
	collector := newRecordCollector(file.Path)

	for _, class := range phpquery.Classes(root) {
		className := resolvedClassName(class, resolver)
		if className == "" {
			continue
		}
		methods := classMethodMap(class)
		if isSubscriberClass(class, resolver) ||
			hasSubscriberMethod(class) {
			parseSubscriber(
				collector,
				class,
				className,
				methods,
				resolver,
			)
		}
		parseListenerAttributes(
			collector,
			class,
			className,
			methods,
			resolver,
		)
	}

	parsePHPServiceTags(collector, root, resolver)
	parseDispatches(collector, root, resolver)
	return collector.values()
}

func hasSubscriberMethod(class *phpsyntax.Node) bool {
	for _, method := range phpquery.Methods(class) {
		if strings.EqualFold(
			phpquery.MethodName(method),
			"getSubscribedEvents",
		) {
			return true
		}
	}
	return false
}

func parseClassConstants(
	class *phpsyntax.Node,
	className,
	path string,
) []Constant {
	body := phpquery.ClassBody(class)
	if body == nil {
		return nil
	}
	var result []Constant
	for child := range body.ChildNodes() {
		if child.Kind() != phpsyntax.PhpClassConstDeclaration {
			continue
		}
		text := child.Text()
		for _, literal := range phpquery.Nodes(child, phpsyntax.PhpString) {
			relative := int(literal.Range().Start - child.Range().Start)
			if relative < 0 || relative > len(text) {
				continue
			}
			match := classConstantNamePattern.FindStringSubmatch(text[:relative])
			if len(match) != 2 {
				continue
			}
			value := phpquery.StringValue(literal)
			if value == "" {
				continue
			}
			result = append(result, Constant{
				Expression: className + "::" + match[1],
				Value:      value,
				File:       path,
				Range:      literal.RangeTrimmedTrivia(),
			})
		}
	}
	return result
}

func parseSubscriber(
	collector *recordCollector,
	class *phpsyntax.Node,
	className string,
	methods map[string]*phpsyntax.Node,
	resolver *php.NameResolver,
) {
	for _, method := range phpquery.Methods(class) {
		if !strings.EqualFold(
			phpquery.MethodName(method),
			"getSubscribedEvents",
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
			if array == nil {
				continue
			}
			for _, item := range phpquery.ArrayItems(array) {
				key := phpquery.ArrayItemKey(item)
				if key == nil {
					continue
				}
				spec := phpEventSpec(key, resolver, className)
				if spec.name == "" && spec.expression == "" {
					continue
				}
				listenerMethods := subscriberMethods(
					phpquery.ArrayItemValue(item),
				)
				for _, listener := range listenerMethods {
					eventType := listenerEventType(
						methods[strings.ToLower(listener.name)],
						resolver,
					)
					collector.add(spec, Occurrence{
						Kind:        ListenerOccurrence,
						Source:      SubscriberSource,
						Range:       spec.rng,
						MethodRange: listener.rng,
						Class:       className,
						Method:      listener.name,
						Priority:    listener.priority,
						EventType:   eventType,
					})
				}
			}
		}
	}
}

type subscriberMethod struct {
	name     string
	priority string
	rng      phpsyntax.TextRange
}

func subscriberMethods(node *phpsyntax.Node) []subscriberMethod {
	if node == nil {
		return nil
	}
	if node.Kind() == phpsyntax.PhpString {
		return []subscriberMethod{{
			name: phpquery.StringValue(node),
			rng:  node.RangeTrimmedTrivia(),
		}}
	}
	if node.Kind() != phpsyntax.PhpArray {
		return nil
	}
	items := phpquery.ArrayItems(node)
	if len(items) == 0 {
		return nil
	}
	first := phpquery.ArrayItemValue(items[0])
	if first != nil && first.Kind() == phpsyntax.PhpString {
		return []subscriberMethod{{
			name:     phpquery.StringValue(first),
			priority: arrayItemText(items, 1),
			rng:      first.RangeTrimmedTrivia(),
		}}
	}
	var result []subscriberMethod
	for _, item := range items {
		value := phpquery.ArrayItemValue(item)
		if value == nil {
			continue
		}
		if value.Kind() == phpsyntax.PhpString {
			result = append(result, subscriberMethod{
				name: phpquery.StringValue(value),
				rng:  value.RangeTrimmedTrivia(),
			})
			continue
		}
		if value.Kind() != phpsyntax.PhpArray {
			continue
		}
		nested := phpquery.ArrayItems(value)
		if len(nested) == 0 {
			continue
		}
		method := phpquery.ArrayItemValue(nested[0])
		if method == nil || method.Kind() != phpsyntax.PhpString {
			continue
		}
		result = append(result, subscriberMethod{
			name:     phpquery.StringValue(method),
			priority: arrayItemText(nested, 1),
			rng:      method.RangeTrimmedTrivia(),
		})
	}
	return result
}

func arrayItemText(items []*phpsyntax.Node, index int) string {
	if index < 0 || index >= len(items) {
		return ""
	}
	value := phpquery.ArrayItemValue(items[index])
	if value == nil {
		return ""
	}
	return strings.TrimSpace(value.Text())
}

func parseListenerAttributes(
	collector *recordCollector,
	class *phpsyntax.Node,
	className string,
	methods map[string]*phpsyntax.Node,
	resolver *php.NameResolver,
) {
	for _, attribute := range phpquery.Attributes(class) {
		if !isAsEventListener(attribute, resolver) {
			continue
		}
		methodNode := attributeArgument(attribute, "method", -1)
		methodName := phpStringValue(methodNode)
		if methodName == "" {
			methodName = "__invoke"
		}
		spec := phpEventSpec(
			attributeArgument(attribute, "event", 0),
			resolver,
			className,
		)
		eventType := listenerEventType(
			methods[strings.ToLower(methodName)],
			resolver,
		)
		if spec.name == "" && spec.expression == "" && eventType != "" {
			spec.name = eventType
			spec.eventType = eventType
			spec.rng = attribute.RangeTrimmedTrivia()
		}
		collector.add(spec, Occurrence{
			Kind:        ListenerOccurrence,
			Source:      AttributeSource,
			Range:       spec.rng,
			MethodRange: nodeRange(methodNode),
			Class:       className,
			Method:      methodName,
			Priority: expressionText(
				attributeArgument(attribute, "priority", -1),
			),
			EventType: eventType,
		})
	}

	for _, method := range phpquery.Methods(class) {
		for _, attribute := range phpquery.Attributes(method) {
			if !isAsEventListener(attribute, resolver) {
				continue
			}
			methodNode := attributeArgument(attribute, "method", -1)
			methodName := phpStringValue(methodNode)
			if methodName == "" {
				methodName = phpquery.MethodName(method)
			}
			spec := phpEventSpec(
				attributeArgument(attribute, "event", 0),
				resolver,
				className,
			)
			eventType := listenerEventType(method, resolver)
			if spec.name == "" && spec.expression == "" && eventType != "" {
				spec.name = eventType
				spec.eventType = eventType
				spec.rng = attribute.RangeTrimmedTrivia()
			}
			collector.add(spec, Occurrence{
				Kind:        ListenerOccurrence,
				Source:      AttributeSource,
				Range:       spec.rng,
				MethodRange: nodeRange(methodNode),
				Class:       className,
				Method:      methodName,
				Priority: expressionText(
					attributeArgument(attribute, "priority", -1),
				),
				EventType: eventType,
			})
		}
	}
}

func parsePHPServiceTags(
	collector *recordCollector,
	root *phpsyntax.Node,
	resolver *php.NameResolver,
) {
	for _, call := range phpquery.Calls(root) {
		if !strings.EqualFold(phpquery.CallMethodName(call), "tag") ||
			phpStringValue(phpquery.ArgumentExpression(call, 0)) !=
				"kernel.event_listener" {
			continue
		}
		className := serviceTagClass(call, resolver)
		options := phpquery.ArrayAt(phpquery.ArgumentExpression(call, 1))
		eventNode := phpArrayProperty(options, "event")
		methodNode := phpArrayProperty(options, "method")
		methodName := phpStringValue(methodNode)
		if methodName == "" {
			methodName = "__invoke"
		}
		spec := phpEventSpec(eventNode, resolver, className)
		if spec.rng.Len() == 0 {
			spec.rng = call.RangeTrimmedTrivia()
		}
		collector.add(spec, Occurrence{
			Kind:        ListenerOccurrence,
			Source:      ServiceTagSource,
			Range:       spec.rng,
			MethodRange: nodeRange(methodNode),
			Class:       className,
			Method:      methodName,
			Priority: expressionText(
				phpArrayProperty(options, "priority"),
			),
		})
	}
}

func parseDispatches(
	collector *recordCollector,
	root *phpsyntax.Node,
	resolver *php.NameResolver,
) {
	for _, call := range phpquery.Calls(root) {
		if !strings.EqualFold(phpquery.CallMethodName(call), "dispatch") ||
			!isEventDispatcherCall(call, resolver) {
			continue
		}
		className := resolvedClassName(phpquery.ClassAt(call), resolver)
		var spec eventSpec
		first := phpquery.ArgumentExpression(call, 0)
		second := phpquery.ArgumentExpression(call, 1)
		if first != nil && first.Kind() == phpsyntax.PhpString {
			spec = phpEventSpec(first, resolver, className)
			spec.eventType = expressionEventType(
				second,
				resolver,
			)
		} else if len(phpquery.Arguments(call)) > 1 {
			spec = phpEventSpec(
				second,
				resolver,
				className,
			)
		}
		if spec.name == "" && spec.expression == "" {
			spec = phpEventSpec(first, resolver, className)
		}
		if spec.name == "" && spec.expression == "" {
			continue
		}
		if spec.eventType == "" {
			spec.eventType = expressionEventType(first, resolver)
		}
		collector.add(spec, Occurrence{
			Kind:      DispatchOccurrence,
			Source:    DispatchSource,
			Range:     spec.rng,
			Class:     className,
			Method:    phpquery.MethodName(call),
			EventType: spec.eventType,
		})
	}
}

func isEventDispatcherCall(
	call *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return false
	}
	if declared := declaredReceiverType(call, receiver, resolver); declared != "" {
		return strings.Contains(
			strings.ToLower(declared),
			"eventdispatcher",
		)
	}
	text := strings.ToLower(strings.ReplaceAll(receiver.Text(), "_", ""))
	return strings.Contains(text, "eventdispatcher") ||
		(strings.Contains(text, "dispatcher") &&
			!strings.Contains(text, "message") &&
			!strings.Contains(text, "bus") &&
			!strings.Contains(text, "queue"))
}

func declaredReceiverType(
	call,
	receiver *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	text := compactPHPExpression(receiver.Text())
	if strings.HasPrefix(text, "$this->") {
		member := text[strings.LastIndex(text, "->")+2:]
		class := phpquery.ClassAt(call)
		for _, property := range phpquery.Properties(class) {
			for _, variable := range phpquery.PropertyVariables(property) {
				if strings.EqualFold(
					phpquery.VariableName(variable),
					member,
				) {
					return resolvePHPType(
						phpquery.PropertyType(property),
						resolver,
					)
				}
			}
		}
		for _, method := range phpquery.Methods(class) {
			if !strings.EqualFold(
				phpquery.MethodName(method),
				"__construct",
			) {
				continue
			}
			if value := parameterTypeByName(
				method,
				member,
				resolver,
			); value != "" {
				return value
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
		resolver,
	)
}

func parameterTypeByName(
	function *phpsyntax.Node,
	name string,
	resolver *php.NameResolver,
) string {
	for _, parameter := range phpquery.Parameters(function) {
		if strings.EqualFold(
			strings.TrimPrefix(phpquery.ParameterName(parameter), "$"),
			strings.TrimPrefix(name, "$"),
		) {
			return resolvePHPType(
				phpquery.ParameterType(parameter),
				resolver,
			)
		}
	}
	return ""
}

func isDispatcherType(
	value types.Type,
	snapshot *semantic.Snapshot,
) bool {
	if value.Kind() == types.UnionKind ||
		value.Kind() == types.IntersectionKind {
		for _, part := range value.Arguments() {
			if isDispatcherType(part, snapshot) {
				return true
			}
		}
		return false
	}
	name := strings.TrimPrefix(value.Name(), `\`)
	for _, target := range dispatcherInterfaces {
		if strings.EqualFold(name, target) {
			return true
		}
		if snapshot != nil && snapshot.Relations().IsSubtype(
			value,
			types.Named(target),
		) {
			return true
		}
	}
	return false
}

var dispatcherInterfaces = []string{
	"Symfony\\Contracts\\EventDispatcher\\EventDispatcherInterface",
	"Symfony\\Component\\EventDispatcher\\EventDispatcherInterface",
	"Psr\\EventDispatcher\\EventDispatcherInterface",
}

func expressionEventType(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	if node == nil {
		return ""
	}
	if object := firstObjectCreation(node); object != nil {
		return normalizeName(
			resolver.Resolve(phpquery.ObjectClassName(object)),
		)
	}
	return ""
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

func phpEventSpec(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
	currentClass string,
) eventSpec {
	if node == nil {
		return eventSpec{}
	}
	if node.Kind() == phpsyntax.PhpString {
		return eventSpec{
			name: phpquery.StringValue(node),
			rng:  node.RangeTrimmedTrivia(),
		}
	}
	if object := firstObjectCreation(node); object != nil {
		name := normalizeName(
			resolver.Resolve(phpquery.ObjectClassName(object)),
		)
		return eventSpec{
			name:      name,
			eventType: name,
			rng:       object.RangeTrimmedTrivia(),
		}
	}
	text := compactPHPExpression(node.Text())
	separator := strings.LastIndex(text, "::")
	if separator < 0 {
		return eventSpec{}
	}
	className := text[:separator]
	member := text[separator+2:]
	switch strings.ToLower(className) {
	case "self", "static":
		className = currentClass
	case "parent":
		className = ""
	default:
		className = normalizeName(resolver.Resolve(className))
	}
	if className == "" || member == "" {
		return eventSpec{}
	}
	if strings.EqualFold(member, "class") {
		return eventSpec{
			name:      className,
			eventType: className,
			rng:       node.RangeTrimmedTrivia(),
		}
	}
	return eventSpec{
		expression: className + "::" + member,
		rng:        node.RangeTrimmedTrivia(),
	}
}

func compactPHPExpression(value string) string {
	return strings.Map(func(character rune) rune {
		switch character {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return character
		}
	}, value)
}

func resolvedClassName(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	if class == nil {
		return ""
	}
	name := phpquery.ClassName(class)
	if name == "" {
		return ""
	}
	return normalizeName(resolver.Resolve(name))
}

func isSubscriberClass(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	for _, implemented := range phpquery.ClassImplements(class) {
		if strings.EqualFold(
			normalizeName(resolver.Resolve(implemented)),
			eventSubscriberInterface,
		) {
			return true
		}
	}
	return false
}

func isAsEventListener(
	attribute *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	return strings.EqualFold(
		normalizeName(resolver.Resolve(phpquery.AttributeName(attribute))),
		asEventListenerAttribute,
	)
}

func classMethodMap(
	class *phpsyntax.Node,
) map[string]*phpsyntax.Node {
	result := make(map[string]*phpsyntax.Node)
	for _, method := range phpquery.Methods(class) {
		result[strings.ToLower(phpquery.MethodName(method))] = method
	}
	return result
}

func listenerEventType(
	method *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	if method == nil {
		return ""
	}
	parameters := phpquery.Parameters(method)
	if len(parameters) == 0 {
		return ""
	}
	return resolvePHPType(phpquery.ParameterType(parameters[0]), resolver)
}

func resolvePHPType(
	value string,
	resolver *php.NameResolver,
) string {
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
		return normalizeName(resolver.Resolve(candidate))
	}
	return ""
}

func attributeArgument(
	attribute *phpsyntax.Node,
	name string,
	fallback int,
) *phpsyntax.Node {
	return callArgument(attribute, name, fallback)
}

func callArgument(
	call *phpsyntax.Node,
	name string,
	fallback int,
) *phpsyntax.Node {
	for index, argument := range phpquery.Arguments(call) {
		if strings.EqualFold(phpquery.ArgumentName(argument), name) {
			return phpquery.ArgumentExpression(call, index)
		}
	}
	if fallback < 0 {
		return nil
	}
	argument := phpquery.Argument(call, fallback)
	if argument == nil || phpquery.ArgumentName(argument) != "" {
		return nil
	}
	return phpquery.ArgumentExpression(call, fallback)
}

func phpStringValue(node *phpsyntax.Node) string {
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

func phpArrayProperty(
	array *phpsyntax.Node,
	name string,
) *phpsyntax.Node {
	if array == nil {
		return nil
	}
	for _, item := range phpquery.ArrayItems(array) {
		key := phpquery.ArrayItemKey(item)
		if key != nil && key.Kind() == phpsyntax.PhpString &&
			strings.EqualFold(phpquery.StringValue(key), name) {
			return phpquery.ArrayItemValue(item)
		}
	}
	return nil
}

func serviceTagClass(
	tagCall *phpsyntax.Node,
	resolver *php.NameResolver,
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
			if className := phpClassArgument(
				phpquery.ArgumentExpression(call, 1),
				resolver,
			); className != "" {
				return className
			}
			if className := phpClassArgument(
				phpquery.ArgumentExpression(call, 0),
				resolver,
			); className != "" {
				return className
			}
		case "get":
			if className := phpClassArgument(
				phpquery.ArgumentExpression(call, 0),
				resolver,
			); className != "" {
				return className
			}
		}
	}
	return ""
}

func phpClassArgument(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	if node == nil {
		return ""
	}
	if className := phpquery.ClassConstantName(node); className != "" {
		return normalizeName(resolver.Resolve(className))
	}
	if node.Kind() == phpsyntax.PhpString {
		value := phpquery.StringValue(node)
		if strings.Contains(value, `\`) {
			return normalizeName(value)
		}
	}
	return ""
}

func parseYAML(file *indexer.ParsedFile) []Record {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	root := yamlquery.RootValue(tree.Root)
	services := yamlquery.Property(root, "services")
	if !yamlquery.IsMapping(services) {
		return nil
	}
	collector := newRecordCollector(file.Path)
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
				parseYAMLListenerTag(
					collector,
					yamlquery.ItemValue(item),
					serviceID,
					className,
				)
			}
		} else {
			parseYAMLListenerTag(
				collector,
				tags,
				serviceID,
				className,
			)
		}
	}
	return collector.values()
}

func parseYAMLListenerTag(
	collector *recordCollector,
	tag *yamlsyntax.Node,
	service,
	className string,
) {
	if tag == nil {
		return
	}
	if tag.Kind() == yamlsyntax.YamlScalar {
		if yamlquery.ScalarValue(tag) != "kernel.event_listener" {
			return
		}
		collector.add(eventSpec{rng: tag.RangeTrimmedTrivia()}, Occurrence{
			Kind:    ListenerOccurrence,
			Source:  ServiceTagSource,
			Range:   tag.RangeTrimmedTrivia(),
			Class:   className,
			Method:  "__invoke",
			Service: service,
		})
		return
	}
	if !yamlquery.IsMapping(tag) ||
		yamlquery.ScalarValue(yamlquery.Property(tag, "name")) !=
			"kernel.event_listener" {
		return
	}
	eventNode := yamlquery.Property(tag, "event")
	methodNode := yamlquery.Property(tag, "method")
	methodName := yamlquery.ScalarValue(methodNode)
	if methodName == "" {
		methodName = "__invoke"
	}
	eventName := yamlquery.ScalarValue(eventNode)
	eventType := ""
	if looksLikeClassName(eventName) {
		eventType = normalizeName(eventName)
	}
	rng := yamlNodeRange(eventNode)
	if rng.Len() == 0 {
		rng = tag.RangeTrimmedTrivia()
	}
	collector.add(eventSpec{
		name:      eventName,
		eventType: eventType,
		rng:       rng,
	}, Occurrence{
		Kind:        ListenerOccurrence,
		Source:      ServiceTagSource,
		Range:       rng,
		MethodRange: yamlNodeRange(methodNode),
		Class:       className,
		Method:      methodName,
		Service:     service,
		Priority: yamlquery.ScalarValue(
			yamlquery.Property(tag, "priority"),
		),
		EventType: eventType,
	})
}

func yamlNodeRange(node *yamlsyntax.Node) phpsyntax.TextRange {
	if node == nil {
		return phpsyntax.TextRange{}
	}
	return node.RangeTrimmedTrivia()
}

func parseXML(file *indexer.ParsedFile) []Record {
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	collector := newRecordCollector(file.Path)
	for _, serviceNode := range xmlquery.Elements(tree.Root, "service") {
		attributes := xmlquery.AttributeValues(serviceNode)
		serviceID := attributes["id"]
		className := normalizeName(attributes["class"])
		if className == "" && strings.Contains(serviceID, `\`) {
			className = normalizeName(serviceID)
		}
		for _, tag := range xmlquery.ChildElements(serviceNode, "tag") {
			tagAttributes := xmlquery.AttributeValues(tag)
			if tagAttributes["name"] != "kernel.event_listener" {
				continue
			}
			eventAttribute := xmlquery.Attribute(tag, "event")
			methodAttribute := xmlquery.Attribute(tag, "method")
			methodName := tagAttributes["method"]
			if methodName == "" {
				methodName = "__invoke"
			}
			eventName := tagAttributes["event"]
			eventType := ""
			if looksLikeClassName(eventName) {
				eventType = normalizeName(eventName)
			}
			rng := xmlNodeRange(eventAttribute)
			if rng.Len() == 0 {
				rng = tag.RangeTrimmedTrivia()
			}
			collector.add(eventSpec{
				name:      eventName,
				eventType: eventType,
				rng:       rng,
			}, Occurrence{
				Kind:        ListenerOccurrence,
				Source:      ServiceTagSource,
				Range:       rng,
				MethodRange: xmlNodeRange(methodAttribute),
				Class:       className,
				Method:      methodName,
				Service:     serviceID,
				Priority:    tagAttributes["priority"],
				EventType:   eventType,
			})
		}
	}
	return collector.values()
}

func xmlNodeRange(node *xmlsyntax.Node) phpsyntax.TextRange {
	if node == nil {
		return phpsyntax.TextRange{}
	}
	return node.RangeTrimmedTrivia()
}
