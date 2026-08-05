package symfony

import (
	"strings"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

const routingConfiguratorClass = "Symfony\\Component\\Routing\\Loader\\Configurator\\RoutingConfigurator"

// parsePHPRoutes parses Symfony Route attributes and RoutingConfigurator
// definitions from the shared PHP CST.
func parsePHPRoutes(filePath string, root *phpsyntax.Node, content []byte) []Route {
	return parsePHPRoutesWithLineIndex(filePath, root, content, phpsyntax.NewLineIndex(string(content)))
}

// ParsePHPRoutesTree extracts routes from an already parsed PHP document.
// On-demand LSP features use this entry point so unsaved route declarations
// share the same lossless tree as indexing without reparsing the document.
func ParsePHPRoutesTree(
	filePath string,
	tree *phpsyntax.Tree,
	lineIndex *phpsyntax.LineIndex,
) []Route {
	if tree == nil || tree.Root == nil {
		return nil
	}
	if lineIndex == nil {
		lineIndex = phpsyntax.NewLineIndex(tree.Source)
	}
	return parsePHPRoutesWithLineIndex(
		filePath,
		tree.Root,
		[]byte(tree.Source),
		lineIndex,
	)
}

func parsePHPRoutesWithLineIndex(filePath string, root *phpsyntax.Node, content []byte, lineIndex *phpsyntax.LineIndex) []Route {
	if root == nil {
		return nil
	}
	namespace := phpquery.Namespace(root)
	var routes []Route

	for _, classNode := range phpquery.Classes(root) {
		className := phpquery.ClassName(classNode)
		basePath := ""
		for _, attribute := range routeAttributes(classNode) {
			classRoute := extractRouteFromAttribute(attribute, lineIndex)
			if basePath == "" {
				basePath = classRoute.Path
			}
		}

		for _, methodNode := range phpquery.Methods(classNode) {
			methodName := phpquery.MethodName(methodNode)
			if methodName == "" {
				continue
			}
			for _, attribute := range routeAttributes(methodNode) {
				route := extractRouteFromAttribute(attribute, lineIndex)
				if basePath != "" && route.Path != "" {
					route.Path = joinRoutePath(basePath, route.Path)
				}
				controller := className + "::" + methodName
				if namespace != "" {
					controller = namespace + "\\" + controller
				}
				route.Controller = controller
				route.FilePath = filePath
				if route.Name != "" || route.Path != "" {
					routes = append(routes, route)
				}
			}
		}
	}

	// Preserve support for attributes outside a class declaration.
	if !isTestFile(filePath) {
		for _, attribute := range phpquery.Nodes(root, phpsyntax.PhpAttribute) {
			if phpquery.ClassAt(attribute) != nil || phpquery.MethodAt(attribute) != nil ||
				!isRouteAttribute(attribute) {
				continue
			}
			route := extractRouteFromAttribute(attribute, lineIndex)
			route.FilePath = filePath
			if route.Name != "" || route.Path != "" {
				routes = append(routes, route)
			}
		}
	}

	routes = append(
		routes,
		parsePHPConfiguratorRoutes(filePath, root, lineIndex)...,
	)

	return routes
}

func isTestFile(filePath string) bool {
	return strings.Contains(filePath, "testdata/")
}

func routeAttributes(node *phpsyntax.Node) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, attribute := range phpquery.Attributes(node) {
		if isRouteAttribute(attribute) {
			result = append(result, attribute)
		}
	}
	return result
}

func isRouteAttribute(attribute *phpsyntax.Node) bool {
	name := strings.TrimPrefix(phpquery.AttributeName(attribute), "\\")
	if index := strings.LastIndex(name, "\\"); index >= 0 {
		name = name[index+1:]
	}
	return name == "Route"
}

func extractRouteFromAttribute(node *phpsyntax.Node, lineIndex *phpsyntax.LineIndex) Route {
	var route Route
	if node == nil {
		return route
	}
	line, _ := lineIndex.Position(node.RangeTrimmedTrivia().Start)
	route.Line = int(line) + 1

	positional := 0
	arguments := phpquery.IterateArguments(node)
	for arguments.Next() {
		argument := arguments.Node()
		if phpquery.ArgumentName(argument) == "methods" {
			route.Methods = phpRouteMethodValues(argument)
			continue
		}
		value := firstStringValue(argument)
		if value == "" {
			continue
		}
		switch phpquery.ArgumentName(argument) {
		case "name":
			route.Name = value
		case "path":
			route.Path = value
		case "controller":
			route.Controller = value
		case "":
			switch positional {
			case 0:
				route.Path = value
			case 1:
				route.Name = value
			}
			positional++
		}
	}
	return route
}

func firstStringValue(node *phpsyntax.Node) string {
	for _, stringNode := range phpquery.Nodes(node, phpsyntax.PhpString) {
		if value := phpquery.StringValue(stringNode); value != "" {
			return value
		}
	}
	return ""
}

func joinRoutePath(basePath, path string) string {
	if strings.HasSuffix(basePath, "/") || strings.HasPrefix(path, "/") {
		return basePath + path
	}
	return basePath + "/" + path
}

type phpRouteConfiguratorState struct {
	namePrefix string
	pathPrefix string
	routeIndex int
	isRoute    bool
}

type phpRouteConfiguratorEvaluator struct {
	filePath      string
	lineIndex     *phpsyntax.LineIndex
	resolver      *php.NameResolver
	rootVariables map[string]struct{}
	variables     map[string]phpRouteConfiguratorState
	routesByAdd   map[*phpsyntax.Node]int
	routes        []Route
}

func parsePHPConfiguratorRoutes(
	filePath string,
	root *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
) []Route {
	if root == nil || lineIndex == nil ||
		!strings.Contains(root.Text(), "RoutingConfigurator") ||
		!strings.Contains(root.Text(), "->add") {
		return nil
	}

	resolver := php.NewNameResolver(root)
	var routes []Route
	for _, function := range phpquery.Nodes(
		root,
		phpsyntax.PhpClosure,
		phpsyntax.PhpFunctionDeclaration,
		phpsyntax.PhpArrowFunction,
	) {
		rootVariables := routingConfiguratorParameters(function, resolver)
		if len(rootVariables) == 0 {
			continue
		}
		evaluator := phpRouteConfiguratorEvaluator{
			filePath:      filePath,
			lineIndex:     lineIndex,
			resolver:      resolver,
			rootVariables: rootVariables,
			variables:     make(map[string]phpRouteConfiguratorState),
			routesByAdd:   make(map[*phpsyntax.Node]int),
		}
		evaluator.evaluate(function)
		routes = append(routes, evaluator.routes...)
	}
	return routes
}

func routingConfiguratorParameters(
	function *phpsyntax.Node,
	resolver *php.NameResolver,
) map[string]struct{} {
	result := make(map[string]struct{})
	for _, parameter := range phpquery.Parameters(function) {
		if !isRoutingConfiguratorType(phpquery.ParameterType(parameter), resolver) {
			continue
		}
		if name := phpquery.ParameterName(parameter); name != "" {
			result[name] = struct{}{}
		}
	}
	return result
}

func isRoutingConfiguratorType(value string, resolver *php.NameResolver) bool {
	for _, member := range strings.FieldsFunc(value, func(value rune) bool {
		switch value {
		case '|', '&', '(', ')':
			return true
		default:
			return false
		}
	}) {
		member = strings.TrimSpace(strings.TrimPrefix(member, "?"))
		if member != "" && strings.EqualFold(
			resolver.Resolve(member),
			routingConfiguratorClass,
		) {
			return true
		}
	}
	return false
}

func (e *phpRouteConfiguratorEvaluator) evaluate(function *phpsyntax.Node) {
	for _, statement := range phpquery.ExpressionStatements(function) {
		if phpquery.FunctionLikeAt(statement) != function {
			continue
		}
		e.evaluateExpression(statement)
	}

	// Arrow functions have no expression statement around their body.
	if function.Kind() == phpsyntax.PhpArrowFunction {
		e.evaluateExpression(function)
	}
}

func (e *phpRouteConfiguratorEvaluator) evaluateExpression(root *phpsyntax.Node) {
	for _, call := range outermostPHPCalls(root) {
		e.resolve(call)
	}

	assignedVariable := phpquery.AssignedVariable(root)
	if assignedVariable == "" {
		return
	}
	if value := phpAssignmentValue(root); value != nil {
		if state, ok := e.resolve(value); ok {
			e.variables[assignedVariable] = state
		} else {
			delete(e.variables, assignedVariable)
		}
	}
}

func outermostPHPCalls(root *phpsyntax.Node) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, call := range phpquery.Calls(root) {
		nested := false
		for parent := call.Parent(); parent != nil && parent != root; parent = parent.Parent() {
			switch parent.Kind() {
			case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall,
				phpsyntax.PhpFunctionCall:
				nested = true
			}
			if nested {
				break
			}
		}
		if !nested {
			result = append(result, call)
		}
	}
	return result
}

func phpAssignmentValue(root *phpsyntax.Node) *phpsyntax.Node {
	assignments := phpquery.Nodes(root, phpsyntax.PhpAssignmentExpression)
	if len(assignments) == 0 {
		return nil
	}
	var leftSeen bool
	for child := range assignments[0].ChildNodes() {
		if !leftSeen {
			leftSeen = true
			continue
		}
		return child
	}
	return nil
}

func (e *phpRouteConfiguratorEvaluator) resolve(
	expression *phpsyntax.Node,
) (phpRouteConfiguratorState, bool) {
	if expression == nil {
		return phpRouteConfiguratorState{}, false
	}
	if expression.Kind() == phpsyntax.PhpVariable {
		name := "$" + phpquery.VariableName(expression)
		if state, ok := e.variables[name]; ok {
			return state, true
		}
		if _, ok := e.rootVariables[name]; ok {
			return phpRouteConfiguratorState{routeIndex: -1}, true
		}
		return phpRouteConfiguratorState{}, false
	}
	if expression.Kind() != phpsyntax.PhpMemberCall {
		for child := range expression.ChildNodes() {
			if state, ok := e.resolve(child); ok {
				return state, true
			}
		}
		return phpRouteConfiguratorState{}, false
	}

	receiver := phpquery.CallReceiver(expression)
	state, ok := e.resolve(receiver)
	if !ok {
		return phpRouteConfiguratorState{}, false
	}

	switch phpquery.CallMethodName(expression) {
	case "namePrefix":
		if state.isRoute {
			return state, true
		}
		if value, found := phpRouteLiteralArgument(expression, 0); found {
			state.namePrefix += value
		}
	case "prefix":
		if state.isRoute {
			return state, true
		}
		if value, found := phpRouteLiteralArgument(expression, 0); found {
			state.pathPrefix = mergePHPRoutePaths(state.pathPrefix, value)
		}
	case "add":
		if state.isRoute {
			return phpRouteConfiguratorState{}, false
		}
		return e.addRoute(expression, state)
	case "controller":
		if state.isRoute {
			if controller := e.controllerArgument(
				phpquery.ArgumentExpression(expression, 0),
			); controller != "" {
				e.routes[state.routeIndex].Controller = controller
			}
		}
	case "defaults":
		if state.isRoute {
			if controller := e.defaultsController(expression); controller != "" {
				e.routes[state.routeIndex].Controller = controller
			}
		}
	case "methods":
		if state.isRoute {
			if methods := phpRouteMethods(expression); len(methods) != 0 {
				e.routes[state.routeIndex].Methods = methods
			}
		}
	}
	return state, true
}

func (e *phpRouteConfiguratorEvaluator) addRoute(
	call *phpsyntax.Node,
	configurator phpRouteConfiguratorState,
) (phpRouteConfiguratorState, bool) {
	if index, exists := e.routesByAdd[call]; exists {
		return phpRouteConfiguratorState{
			routeIndex: index,
			isRoute:    true,
		}, true
	}
	name, ok := phpRouteLiteralArgument(call, 0)
	if !ok || strings.TrimSpace(name) == "" {
		return phpRouteConfiguratorState{}, false
	}
	path, _ := phpRouteLiteralArgument(call, 1)
	path = mergePHPRoutePaths(configurator.pathPrefix, path)
	lineNode := phpquery.ArgumentExpression(call, 0)
	if lineNode == nil {
		lineNode = call
	}
	line, _ := e.lineIndex.Position(lineNode.RangeTrimmedTrivia().Start)
	e.routes = append(e.routes, Route{
		Name:     configurator.namePrefix + name,
		Path:     path,
		FilePath: e.filePath,
		Line:     int(line) + 1,
	})
	index := len(e.routes) - 1
	e.routesByAdd[call] = index
	return phpRouteConfiguratorState{
		routeIndex: index,
		isRoute:    true,
	}, true
}

func phpRouteLiteralArgument(
	call *phpsyntax.Node,
	index int,
) (string, bool) {
	expression := phpquery.ArgumentExpression(call, index)
	if expression == nil || expression.Kind() != phpsyntax.PhpString {
		return "", false
	}
	text := strings.TrimSpace(expression.Text())
	if len(text) < 2 ||
		(text[0] != '\'' && text[0] != '"') ||
		text[len(text)-1] != text[0] {
		return "", false
	}
	return phpquery.StringValue(expression), true
}

func mergePHPRoutePaths(prefix, path string) string {
	if prefix == "" {
		return path
	}
	if path == "" {
		return prefix
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}

func (e *phpRouteConfiguratorEvaluator) controllerArgument(
	expression *phpsyntax.Node,
) string {
	if expression == nil {
		return ""
	}
	if expression.Kind() == phpsyntax.PhpString {
		return normalizePHPRouteController(phpquery.StringValue(expression))
	}
	if expression.Kind() == phpsyntax.PhpArray {
		items := phpquery.ArrayItems(expression)
		if len(items) != 2 {
			return ""
		}
		className := e.controllerArgument(phpquery.ArrayItemValue(items[0]))
		methodExpression := phpquery.ArrayItemValue(items[1])
		if className == "" || methodExpression == nil ||
			methodExpression.Kind() != phpsyntax.PhpString {
			return ""
		}
		method := phpquery.StringValue(methodExpression)
		if method == "" {
			return ""
		}
		return className + "::" + method
	}
	if className := phpquery.ClassConstantName(expression); className != "" {
		return normalizePHPRouteController(e.resolver.Resolve(className))
	}
	return ""
}

func (e *phpRouteConfiguratorEvaluator) defaultsController(
	call *phpsyntax.Node,
) string {
	array := phpquery.ArgumentExpression(call, 0)
	if array == nil || array.Kind() != phpsyntax.PhpArray {
		return ""
	}
	for _, item := range phpquery.ArrayItems(array) {
		key := phpquery.ArrayItemKey(item)
		if key == nil || key.Kind() != phpsyntax.PhpString ||
			phpquery.StringValue(key) != "_controller" {
			continue
		}
		return e.controllerArgument(phpquery.ArrayItemValue(item))
	}
	return ""
}

func phpRouteMethods(call *phpsyntax.Node) []string {
	array := phpquery.ArgumentExpression(call, 0)
	return phpRouteMethodValues(array)
}

func phpRouteMethodValues(node *phpsyntax.Node) []string {
	if node == nil {
		return nil
	}
	array := node
	if array.Kind() != phpsyntax.PhpArray {
		arrays := phpquery.Nodes(node, phpsyntax.PhpArray)
		if len(arrays) == 0 {
			return nil
		}
		array = arrays[0]
	}
	var methods []string
	seen := make(map[string]struct{})
	for _, item := range phpquery.ArrayItems(array) {
		expression := phpquery.ArrayItemValue(item)
		method := phpRouteMethodValue(expression)
		key := strings.ToLower(method)
		if method == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		methods = append(methods, method)
	}
	return methods
}

func phpRouteMethodValue(expression *phpsyntax.Node) string {
	if expression == nil {
		return ""
	}
	if expression.Kind() == phpsyntax.PhpString {
		return phpquery.StringValue(expression)
	}
	if expression.Kind() != phpsyntax.PhpMemberAccess &&
		expression.Kind() != phpsyntax.PhpScopedAccess {
		return ""
	}

	text := strings.TrimSpace(expression.Text())
	separator := strings.LastIndex(text, "::")
	if separator < 0 {
		return ""
	}
	constant := strings.TrimSpace(text[separator+2:])
	if !strings.HasPrefix(constant, "METHOD_") {
		return ""
	}
	method := strings.TrimPrefix(constant, "METHOD_")
	switch method {
	case "CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST",
		"PURGE", "PUT", "TRACE":
		return method
	default:
		return ""
	}
}

func normalizePHPRouteController(value string) string {
	return strings.TrimLeft(strings.ReplaceAll(value, "/", "\\"), "\\")
}
