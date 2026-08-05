package twig

import (
	"bytes"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

// TwigFunction represents a function defined in a Twig extension.
type TwigFunction struct {
	Name            string
	Usage           string
	Method          string
	Line            int
	Parameters      []TwigParameter
	FilePath        string
	Deprecated      bool
	Deprecation     string
	DeprecatedRange cst.TextRange
}

// TwigFilter represents a filter defined in a Twig extension.
type TwigFilter struct {
	Name            string
	Usage           string
	Method          string
	Line            int
	Parameters      []TwigParameter
	FilePath        string
	Deprecated      bool
	Deprecation     string
	DeprecatedRange cst.TextRange
}

// TwigTest represents a test defined in a Twig extension.
type TwigTest struct {
	Name            string
	Usage           string
	Method          string
	Line            int
	Parameters      []TwigParameter
	FilePath        string
	Deprecated      bool
	Deprecation     string
	DeprecatedRange cst.TextRange
}

// TwigOperator represents an operator registered by a Twig extension.
//
// Twig <= 3.20 exposes operators through getOperators(), while Twig 3.21+
// also supports getExpressionParsers(). Keeping the declaration range makes
// the persisted model suitable for navigation without reparsing PHP files.
type TwigOperator struct {
	Name     string
	FilePath string
	Range    cst.TextRange
	Unary    bool
	Alias    bool
	Legacy   bool
}

// TwigParameter represents a parameter for a function or filter.
type TwigParameter struct {
	Name     string
	Type     string
	Optional bool
}

// ParseTwigExtension parses Twig extension declarations from the shared PHP CST.
func ParseTwigExtension(
	filePath string,
	root *phpsyntax.Node,
	content []byte,
) ([]TwigFunction, []TwigFilter, error) {
	return ParseTwigExtensionTree(filePath, root, content, phpsyntax.NewLineIndex(string(content)))
}

func ParseTwigExtensionTree(
	filePath string,
	root *phpsyntax.Node,
	content []byte,
	lineIndex *phpsyntax.LineIndex,
) ([]TwigFunction, []TwigFilter, error) {
	functions, filters, _, err := parseTwigExtensionTreeAll(
		filePath,
		root,
		content,
		lineIndex,
	)
	return functions, filters, err
}

// ParseTwigTests parses legacy TwigTest/Twig_SimpleTest registrations and
// modern #[AsTwigTest] declarations from the shared PHP CST.
func ParseTwigTests(
	filePath string,
	root *phpsyntax.Node,
	content []byte,
) []TwigTest {
	_, _, tests, _ := parseTwigExtensionTreeAll(
		filePath,
		root,
		content,
		phpsyntax.NewLineIndex(string(content)),
	)
	return tests
}

func parseTwigExtensionTreeAll(
	filePath string,
	root *phpsyntax.Node,
	content []byte,
	lineIndex *phpsyntax.LineIndex,
) ([]TwigFunction, []TwigFilter, []TwigTest, error) {
	if root == nil {
		return nil, nil, nil, nil
	}
	if !isTwigCallableDefinitionCandidate(content) {
		return nil, nil, nil, nil
	}

	classNode := findExtensionClass(root)
	var functions []TwigFunction
	var filters []TwigFilter
	var tests []TwigTest
	if classNode != nil {
		paramsByMethod := buildMethodParameterMap(classNode)
		deprecationsByMethod := buildMethodDeprecationMap(classNode)
		resolver := php.NewNameResolver(root)
		functions = append(
			functions,
			parseExtensionFunctions(
				filePath,
				classNode,
				paramsByMethod,
				deprecationsByMethod,
				lineIndex,
			)...,
		)
		filters = append(
			filters,
			parseExtensionFilters(
				filePath,
				classNode,
				paramsByMethod,
				deprecationsByMethod,
				lineIndex,
			)...,
		)
		tests = append(
			tests,
			parseExtensionTests(
				filePath,
				classNode,
				paramsByMethod,
				deprecationsByMethod,
				lineIndex,
				resolver,
			)...,
		)
	}
	attributeFunctions, attributeFilters, attributeTests :=
		parseAttributeTwigCallables(
			filePath,
			root,
			lineIndex,
		)
	functions = append(functions, attributeFunctions...)
	filters = append(filters, attributeFilters...)
	tests = append(tests, attributeTests...)
	return functions, filters, tests, nil
}

func isTwigCallableDefinitionCandidate(content []byte) bool {
	return bytes.Contains(content, []byte("TwigFunction")) ||
		bytes.Contains(content, []byte("TwigFilter")) ||
		bytes.Contains(content, []byte("TwigTest")) ||
		bytes.Contains(content, []byte("AsTwig")) ||
		bytes.Contains(content, []byte("Twig_SimpleFunction")) ||
		bytes.Contains(content, []byte("Twig_SimpleFilter")) ||
		bytes.Contains(content, []byte("Twig_SimpleTest")) ||
		bytes.Contains(content, []byte("Twig_Test"))
}

// ParseTwigOperators parses legacy getOperators() declarations and modern
// Twig 3.21+/4 expression-parser registrations from extension classes.
func ParseTwigOperators(
	filePath string,
	root *phpsyntax.Node,
	content []byte,
) []TwigOperator {
	if root == nil ||
		(!bytes.Contains(content, []byte("getOperators")) &&
			!bytes.Contains(content, []byte("getExpressionParsers"))) {
		return nil
	}

	var operators []TwigOperator
	resolver := php.NewNameResolver(root)
	for _, classNode := range phpquery.Classes(root) {
		if !isTwigExtensionClass(classNode, resolver) {
			continue
		}
		if method := methodNamed(classNode, "getOperators"); method != nil {
			operators = append(
				operators,
				parseLegacyTwigOperators(filePath, method)...,
			)
		}
		if method := methodNamed(
			classNode,
			"getExpressionParsers",
		); method != nil {
			operators = append(
				operators,
				parseTwigExpressionParsers(filePath, method)...,
			)
		}
	}
	return uniqueTwigOperators(operators)
}

func parseLegacyTwigOperators(
	filePath string,
	method *phpsyntax.Node,
) []TwigOperator {
	var operators []TwigOperator
	for _, returnNode := range phpquery.Nodes(
		method,
		phpsyntax.PhpReturnStatement,
	) {
		expression := firstDirectNode(returnNode)
		if expression == nil || expression.Kind() != phpsyntax.PhpArray {
			continue
		}
		for groupIndex, groupItem := range phpquery.ArrayItems(expression) {
			group := phpquery.ArrayItemValue(groupItem)
			if group == nil || group.Kind() != phpsyntax.PhpArray {
				continue
			}
			for _, operatorItem := range phpquery.ArrayItems(group) {
				key := phpquery.ArrayItemKey(operatorItem)
				name := phpquery.StringValue(key)
				if name == "" {
					continue
				}
				operators = append(operators, TwigOperator{
					Name:     name,
					FilePath: filePath,
					Range:    phpquery.StringContentRange(key),
					Unary:    groupIndex == 0,
					Legacy:   true,
				})
			}
		}
	}
	return operators
}

func parseTwigExpressionParsers(
	filePath string,
	method *phpsyntax.Node,
) []TwigOperator {
	var operators []TwigOperator
	for _, object := range phpquery.ObjectCreations(method) {
		className := lastNamePart(phpquery.ObjectClassName(object))
		unary := false
		var aliasIndex int
		switch className {
		case "BinaryOperatorExpressionParser":
			aliasIndex = 6
		case "UnaryOperatorExpressionParser":
			unary = true
			aliasIndex = 5
		default:
			continue
		}

		nameNode := phpquery.StringArgument(object, 1)
		if name := phpquery.StringValue(nameNode); name != "" {
			operators = append(operators, TwigOperator{
				Name:     name,
				FilePath: filePath,
				Range:    phpquery.StringContentRange(nameNode),
				Unary:    unary,
			})
		}

		aliases := argumentExpressionByNameOrPosition(
			object,
			"aliases",
			aliasIndex,
		)
		if aliases == nil || aliases.Kind() != phpsyntax.PhpArray {
			continue
		}
		for _, item := range phpquery.ArrayItems(aliases) {
			value := phpquery.ArrayItemValue(item)
			name := phpquery.StringValue(value)
			if name == "" {
				continue
			}
			operators = append(operators, TwigOperator{
				Name:     name,
				FilePath: filePath,
				Range:    phpquery.StringContentRange(value),
				Unary:    unary,
				Alias:    true,
			})
		}
	}
	return operators
}

func argumentExpressionByNameOrPosition(
	container *phpsyntax.Node,
	name string,
	position int,
) *phpsyntax.Node {
	arguments := phpquery.Arguments(container)
	if position >= 0 && position < len(arguments) &&
		phpquery.ArgumentName(arguments[position]) == "" {
		return phpquery.ArgumentExpression(container, position)
	}
	for index, argument := range arguments {
		if strings.EqualFold(phpquery.ArgumentName(argument), name) {
			return phpquery.ArgumentExpression(container, index)
		}
	}
	if position >= 0 && position < len(arguments) {
		return phpquery.ArgumentExpression(container, position)
	}
	return nil
}

func firstDirectNode(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		return child
	}
	return nil
}

func uniqueTwigOperators(operators []TwigOperator) []TwigOperator {
	seen := make(map[string]struct{}, len(operators))
	result := make([]TwigOperator, 0, len(operators))
	for _, operator := range operators {
		key := operator.FilePath + "\x00" + operator.Range.String() +
			"\x00" + operator.Name
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, operator)
	}
	return result
}

func parseAttributeTwigCallables(
	filePath string,
	root *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
) ([]TwigFunction, []TwigFilter, []TwigTest) {
	resolver := php.NewNameResolver(root)
	namespace := phpquery.Namespace(root)
	var functions []TwigFunction
	var filters []TwigFilter
	var tests []TwigTest
	for _, classNode := range phpquery.Classes(root) {
		className := phpquery.ClassName(classNode)
		if namespace != "" {
			className = namespace + `\` + className
		}
		paramsByMethod := buildMethodParameterMap(classNode)
		deprecationsByMethod := buildMethodDeprecationMap(classNode)
		for _, method := range phpquery.Methods(classNode) {
			methodName := phpquery.MethodName(method)
			parameters := paramsByMethod[methodName]
			row, _ := lineIndex.Position(
				method.RangeTrimmedTrivia().Start,
			)
			for _, attribute := range phpquery.Attributes(method) {
				attributeName := strings.Trim(
					resolver.Resolve(phpquery.AttributeName(attribute)),
					`\`,
				)
				if !strings.EqualFold(
					attributeName,
					"Twig\\Attribute\\AsTwigFunction",
				) && !strings.EqualFold(
					attributeName,
					"Twig\\Attribute\\AsTwigFilter",
				) && !strings.EqualFold(
					attributeName,
					"Twig\\Attribute\\AsTwigTest",
				) {
					continue
				}
				name := twigCallableAttributeName(attribute)
				if name == "" || methodName == "" {
					continue
				}
				callback := className + "::" + methodName
				deprecation := twigAttributeCallableDeprecation(
					attribute,
					name,
					deprecationsByMethod[methodName],
				)
				if strings.EqualFold(
					attributeName,
					"Twig\\Attribute\\AsTwigFunction",
				) {
					functions = append(functions, TwigFunction{
						Name:            name,
						Usage:           buildUsage(name, parameters),
						Method:          callback,
						Line:            int(row) + 1,
						Parameters:      cloneParameters(parameters),
						FilePath:        filePath,
						Deprecated:      deprecation.deprecated,
						Deprecation:     deprecation.message,
						DeprecatedRange: deprecation.sourceRange,
					})
				} else if strings.EqualFold(
					attributeName,
					"Twig\\Attribute\\AsTwigFilter",
				) {
					filters = append(filters, TwigFilter{
						Name:            name,
						Usage:           buildUsage(name, parameters),
						Method:          callback,
						Line:            int(row) + 1,
						Parameters:      cloneParameters(parameters),
						FilePath:        filePath,
						Deprecated:      deprecation.deprecated,
						Deprecation:     deprecation.message,
						DeprecatedRange: deprecation.sourceRange,
					})
				} else {
					tests = append(tests, TwigTest{
						Name:            name,
						Usage:           buildUsage(name, parameters),
						Method:          callback,
						Line:            int(row) + 1,
						Parameters:      cloneParameters(parameters),
						FilePath:        filePath,
						Deprecated:      deprecation.deprecated,
						Deprecation:     deprecation.message,
						DeprecatedRange: deprecation.sourceRange,
					})
				}
			}
		}
	}
	return functions, filters, tests
}

func twigCallableAttributeName(attribute *phpsyntax.Node) string {
	positional := 0
	for _, argument := range phpquery.Arguments(attribute) {
		name := phpquery.ArgumentName(argument)
		if name != "" && !strings.EqualFold(name, "name") {
			continue
		}
		if name == "" && positional > 0 {
			positional++
			continue
		}
		expression := twigAttributeArgumentExpression(argument)
		if expression != nil && expression.Kind() == phpsyntax.PhpString {
			return phpquery.StringValue(expression)
		}
		positional++
	}
	return ""
}

func twigAttributeCallableDeprecation(
	attribute *phpsyntax.Node,
	callableName string,
	method twigCallableDeprecation,
) twigCallableDeprecation {
	if !method.deprecated {
		for _, trigger := range method.bodyTriggers {
			if messageDeprecatesTwigCallable(
				trigger.message,
				callableName,
			) {
				method.deprecated = true
				method.message = trigger.message
				method.sourceRange = trigger.sourceRange
				break
			}
		}
	}
	var option twigCallableDeprecation
	alternative := ""
	for _, argument := range phpquery.Arguments(attribute) {
		name := strings.ToLower(phpquery.ArgumentName(argument))
		if name == "" {
			continue
		}
		expression := twigAttributeArgumentExpression(argument)
		if expression == nil {
			continue
		}
		switch name {
		case "alternative":
			alternative = phpquery.StringValue(expression)
		case "deprecated":
			raw := strings.ToLower(strings.TrimSpace(expression.Text()))
			if raw == "false" || raw == "null" || raw == "0" {
				continue
			}
			option.deprecated = true
			option.sourceRange = argument.RangeTrimmedTrivia()
			if since := phpquery.StringValue(expression); since != "" {
				option.message = "Deprecated since " + since + "."
			}
		case "deprecationinfo":
			raw := strings.ToLower(strings.TrimSpace(expression.Text()))
			if raw == "false" || raw == "null" {
				continue
			}
			option.deprecated = true
			option.sourceRange = argument.RangeTrimmedTrivia()
			if strings.EqualFold(
				lastNamePart(phpquery.ObjectClassName(expression)),
				"DeprecatedCallableInfo",
			) {
				pkg := phpquery.StringValue(
					phpquery.StringArgument(expression, 0),
				)
				version := phpquery.StringValue(
					phpquery.StringArgument(expression, 1),
				)
				alternative = phpquery.StringValue(
					phpquery.StringArgument(expression, 2),
				)
				option.message = deprecatedSinceMessage(pkg, version)
			}
		}
	}
	if alternative != "" {
		if option.message != "" {
			option.message += " "
		}
		option.message += "Use " + alternative + " instead."
	}
	if !option.deprecated {
		return method
	}
	if option.message == "" {
		option.message = method.message
	}
	return option
}

func twigAttributeArgumentExpression(
	argument *phpsyntax.Node,
) *phpsyntax.Node {
	for child := range argument.ChildNodes() {
		if argument.Kind() == phpsyntax.PhpNamedArgument &&
			child.Kind() == phpsyntax.PhpName {
			continue
		}
		return child
	}
	return nil
}

func findExtensionClass(root *phpsyntax.Node) *phpsyntax.Node {
	resolver := php.NewNameResolver(root)
	for _, classNode := range phpquery.Classes(root) {
		if isTwigExtensionClass(classNode, resolver) {
			return classNode
		}
	}
	return nil
}

func parseExtensionFunctions(
	filePath string,
	classNode *phpsyntax.Node,
	paramsByMethod map[string][]TwigParameter,
	deprecationsByMethod map[string]twigCallableDeprecation,
	lineIndex *phpsyntax.LineIndex,
) []TwigFunction {
	method := methodNamed(classNode, "getFunctions")
	if method == nil {
		return nil
	}
	var functions []TwigFunction
	for _, object := range phpquery.ObjectCreations(method) {
		className := lastNamePart(phpquery.ObjectClassName(object))
		if className != "TwigFunction" && className != "Twig_SimpleFunction" {
			continue
		}
		name := phpquery.StringValue(phpquery.StringArgument(object, 0))
		callback := callbackName(phpquery.ArgumentExpression(object, 1))
		if name == "" || callback == "" {
			continue
		}
		row, _ := lineIndex.Position(object.RangeTrimmedTrivia().Start)
		parameters := paramsByMethod[callbackMethodName(callback)]
		deprecation := callableDeprecation(
			object,
			callback,
			deprecationsByMethod,
		)
		functions = append(functions, TwigFunction{
			Name:            name,
			Usage:           buildUsage(name, parameters),
			Method:          callback,
			Line:            int(row) + 1,
			Parameters:      cloneParameters(parameters),
			FilePath:        filePath,
			Deprecated:      deprecation.deprecated,
			Deprecation:     deprecation.message,
			DeprecatedRange: deprecation.sourceRange,
		})
	}
	return functions
}

func parseExtensionFilters(
	filePath string,
	classNode *phpsyntax.Node,
	paramsByMethod map[string][]TwigParameter,
	deprecationsByMethod map[string]twigCallableDeprecation,
	lineIndex *phpsyntax.LineIndex,
) []TwigFilter {
	method := methodNamed(classNode, "getFilters")
	if method == nil {
		return nil
	}
	var filters []TwigFilter
	for _, object := range phpquery.ObjectCreations(method) {
		className := lastNamePart(phpquery.ObjectClassName(object))
		if className != "TwigFilter" && className != "Twig_SimpleFilter" {
			continue
		}
		name := phpquery.StringValue(phpquery.StringArgument(object, 0))
		callback := callbackName(phpquery.ArgumentExpression(object, 1))
		if name == "" || callback == "" {
			continue
		}
		row, _ := lineIndex.Position(object.RangeTrimmedTrivia().Start)
		deprecation := callableDeprecation(
			object,
			callback,
			deprecationsByMethod,
		)
		filter := TwigFilter{
			Name:            name,
			Method:          callback,
			Line:            int(row) + 1,
			FilePath:        filePath,
			Deprecated:      deprecation.deprecated,
			Deprecation:     deprecation.message,
			DeprecatedRange: deprecation.sourceRange,
		}
		if strings.Contains(callback, "::") || strings.Contains(callback, "->") {
			filter.Parameters = cloneParameters(paramsByMethod[callbackMethodName(callback)])
			filter.Usage = buildUsage(name, filter.Parameters)
		}
		filters = append(filters, filter)
	}
	return filters
}

func parseExtensionTests(
	filePath string,
	classNode *phpsyntax.Node,
	paramsByMethod map[string][]TwigParameter,
	deprecationsByMethod map[string]twigCallableDeprecation,
	lineIndex *phpsyntax.LineIndex,
	resolver *php.NameResolver,
) []TwigTest {
	method := methodNamed(classNode, "getTests")
	if method == nil {
		return nil
	}
	var tests []TwigTest
	for _, object := range phpquery.ObjectCreations(method) {
		className := lastNamePart(phpquery.ObjectClassName(object))
		if className != "TwigTest" &&
			className != "Twig_SimpleTest" &&
			className != "Twig_Test" {
			continue
		}
		name := phpquery.StringValue(phpquery.StringArgument(object, 0))
		callback := callbackName(phpquery.ArgumentExpression(object, 1))
		if callback == "" {
			callback = twigTestNodeClassCallback(object, resolver)
		}
		if name == "" || callback == "" {
			continue
		}
		row, _ := lineIndex.Position(object.RangeTrimmedTrivia().Start)
		parameters := paramsByMethod[callbackMethodName(callback)]
		deprecation := callableDeprecation(
			object,
			callback,
			deprecationsByMethod,
		)
		tests = append(tests, TwigTest{
			Name:            name,
			Usage:           buildUsage(name, parameters),
			Method:          callback,
			Line:            int(row) + 1,
			Parameters:      cloneParameters(parameters),
			FilePath:        filePath,
			Deprecated:      deprecation.deprecated,
			Deprecation:     deprecation.message,
			DeprecatedRange: deprecation.sourceRange,
		})
	}
	return tests
}

func twigTestNodeClassCallback(
	object *phpsyntax.Node,
	resolver *php.NameResolver,
) string {
	options := phpquery.ArgumentExpression(object, 2)
	if options == nil || options.Kind() != phpsyntax.PhpArray {
		return ""
	}
	for _, item := range phpquery.ArrayItems(options) {
		if phpquery.StringValue(phpquery.ArrayItemKey(item)) != "node_class" {
			continue
		}
		value := phpquery.ArrayItemValue(item)
		className := phpquery.ClassConstantName(value)
		if className != "" {
			className = resolver.Resolve(className)
		} else {
			className = phpquery.StringValue(value)
		}
		className = strings.Trim(className, `\`)
		if className == "" {
			return ""
		}
		return className + "::compile"
	}
	return ""
}

func callbackName(expression *phpsyntax.Node) string {
	if expression == nil {
		return ""
	}
	switch expression.Kind() {
	case phpsyntax.PhpString:
		return phpquery.StringValue(expression)
	case phpsyntax.PhpArray:
		items := phpquery.ArrayItems(expression)
		if len(items) < 2 {
			return ""
		}
		receiver := compactCallbackReceiver(items[0].Text())
		method := ""
		for _, stringNode := range phpquery.Nodes(items[1], phpsyntax.PhpString) {
			method = phpquery.StringValue(stringNode)
			break
		}
		if method == "" {
			return ""
		}
		if receiver == "" {
			return method
		}
		if strings.HasPrefix(receiver, "$") {
			return receiver + "->" + method
		}
		return receiver + "::" + method
	case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall, phpsyntax.PhpFunctionCall:
		return phpquery.CallName(expression)
	}
	for _, call := range phpquery.Calls(expression) {
		if name := phpquery.CallName(call); name != "" {
			return name
		}
	}
	return ""
}

func methodNamed(classNode *phpsyntax.Node, name string) *phpsyntax.Node {
	for _, method := range phpquery.Methods(classNode) {
		if phpquery.MethodName(method) == name {
			return method
		}
	}
	return nil
}

func buildMethodParameterMap(classNode *phpsyntax.Node) map[string][]TwigParameter {
	result := make(map[string][]TwigParameter)
	for _, method := range phpquery.Methods(classNode) {
		name := phpquery.MethodName(method)
		if name == "" {
			continue
		}
		var parameters []TwigParameter
		for _, parameter := range phpquery.Parameters(method) {
			if paramName := phpquery.ParameterName(parameter); paramName != "" {
				parameters = append(parameters, TwigParameter{
					Name:     paramName,
					Type:     phpquery.ParameterType(parameter),
					Optional: phpquery.ParameterOptional(parameter),
				})
			}
		}
		result[name] = parameters
	}
	return result
}

type twigCallableDeprecation struct {
	deprecated   bool
	message      string
	sourceRange  cst.TextRange
	bodyTriggers []twigBodyDeprecation
}

type twigBodyDeprecation struct {
	message     string
	sourceRange cst.TextRange
}

func buildMethodDeprecationMap(
	classNode *phpsyntax.Node,
) map[string]twigCallableDeprecation {
	result := make(map[string]twigCallableDeprecation)
	for _, method := range phpquery.Methods(classNode) {
		name := phpquery.MethodName(method)
		if name == "" {
			continue
		}
		var deprecation twigCallableDeprecation
		for _, call := range phpquery.Calls(method) {
			callName := strings.ToLower(phpquery.CallMethodName(call))
			if callName != "trigger_deprecation" &&
				!strings.Contains(callName, "triggerdeprecation") {
				continue
			}
			if callName == "triggerdeprecation" &&
				call.Kind() != phpsyntax.PhpFunctionCall {
				// Twig's DeprecatedCallableInfo::triggerDeprecation() is
				// itself an explicit declaration that the callback is
				// deprecated. This is also the exact convention used by
				// the Symfony plugin.
				deprecation.deprecated = true
				deprecation.sourceRange = call.RangeTrimmedTrivia()
				break
			} else {
				if alternative, ok := deprecatedMethodAlternative(call); ok {
					deprecation.deprecated = true
					deprecation.sourceRange = call.RangeTrimmedTrivia()
					deprecation.message = "Callback method " + name +
						" is deprecated."
					if alternative != "" {
						deprecation.message += " Use " + alternative +
							" instead."
					}
					break
				}
				deprecation.bodyTriggers = append(
					deprecation.bodyTriggers,
					twigBodyDeprecation{
						message:     lastStringArgument(call),
						sourceRange: call.RangeTrimmedTrivia(),
					},
				)
			}
		}
		if deprecation.deprecated || len(deprecation.bodyTriggers) > 0 {
			result[name] = deprecation
		}
	}
	return result
}

func callableDeprecation(
	object *phpsyntax.Node,
	callback string,
	methods map[string]twigCallableDeprecation,
) twigCallableDeprecation {
	optionDeprecation := callableOptionDeprecation(object)
	methodDeprecation := methods[callbackMethodName(callback)]
	if !methodDeprecation.deprecated {
		callableName := phpquery.StringValue(
			phpquery.StringArgument(object, 0),
		)
		for _, trigger := range methodDeprecation.bodyTriggers {
			if !messageDeprecatesTwigCallable(
				trigger.message,
				callableName,
			) {
				continue
			}
			methodDeprecation.deprecated = true
			methodDeprecation.message = trigger.message
			methodDeprecation.sourceRange = trigger.sourceRange
			break
		}
	}
	if !optionDeprecation.deprecated {
		if methodDeprecation.deprecated {
			return methodDeprecation
		}
		return twigCallableDeprecation{}
	}
	if optionDeprecation.message == "" {
		optionDeprecation.message = methodDeprecation.message
	}
	return optionDeprecation
}

func callableOptionDeprecation(
	object *phpsyntax.Node,
) twigCallableDeprecation {
	options := phpquery.ArgumentExpression(object, 2)
	if options == nil || options.Kind() != phpsyntax.PhpArray {
		return twigCallableDeprecation{}
	}
	var result twigCallableDeprecation
	alternative := ""
	deprecatingPackage := ""
	for _, item := range phpquery.ArrayItems(options) {
		key := phpquery.StringValue(phpquery.ArrayItemKey(item))
		value := phpquery.ArrayItemValue(item)
		if value == nil {
			continue
		}
		switch key {
		case "alternative":
			alternative = phpquery.StringValue(value)
		case "deprecating_package":
			deprecatingPackage = phpquery.StringValue(value)
		case "deprecated":
			raw := strings.ToLower(strings.TrimSpace(value.Text()))
			if raw == "false" || raw == "null" || raw == "0" {
				continue
			}
			result.deprecated = true
			result.sourceRange = item.RangeTrimmedTrivia()
			if since := phpquery.StringValue(value); since != "" {
				result.message = "Deprecated since " + since + "."
			}
		case "deprecation_info":
			raw := strings.ToLower(strings.TrimSpace(value.Text()))
			if raw == "false" || raw == "null" {
				continue
			}
			result.deprecated = true
			result.sourceRange = item.RangeTrimmedTrivia()
			if strings.EqualFold(
				lastNamePart(phpquery.ObjectClassName(value)),
				"DeprecatedCallableInfo",
			) {
				pkg := phpquery.StringValue(
					phpquery.StringArgument(value, 0),
				)
				version := phpquery.StringValue(
					phpquery.StringArgument(value, 1),
				)
				alternative = phpquery.StringValue(
					phpquery.StringArgument(value, 2),
				)
				result.message = deprecatedSinceMessage(pkg, version)
			}
		}
	}
	if result.deprecated && result.message != "" &&
		deprecatingPackage != "" &&
		!strings.Contains(result.message, deprecatingPackage) {
		result.message = strings.TrimSuffix(result.message, ".") +
			" by " + deprecatingPackage + "."
	}
	if alternative != "" {
		if result.message != "" {
			result.message += " "
		}
		result.message += "Use " + alternative + " instead."
	}
	return result
}

func lastStringArgument(call *phpsyntax.Node) string {
	message := ""
	for index := range phpquery.Arguments(call) {
		if value := phpquery.StringValue(
			phpquery.StringArgument(call, index),
		); value != "" {
			message = value
		}
	}
	return message
}

func deprecatedMethodAlternative(call *phpsyntax.Node) (string, bool) {
	for _, nested := range phpquery.Calls(call) {
		if !strings.EqualFold(
			phpquery.CallMethodName(nested),
			"deprecatedMethodMessage",
		) {
			continue
		}
		return phpquery.StringValue(
			phpquery.StringArgument(nested, 3),
		), true
	}
	return "", false
}

func messageDeprecatesTwigCallable(message, callable string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	callable = strings.ToLower(strings.TrimSpace(callable))
	if message == "" || callable == "" ||
		!strings.Contains(message, "deprecated") ||
		(!strings.Contains(message, "function") &&
			!strings.Contains(message, "filter")) {
		return false
	}
	return strings.Contains(message, callable)
}

func deprecatedSinceMessage(pkg, version string) string {
	switch {
	case pkg != "" && version != "":
		return "Deprecated since " + pkg + " " + version + "."
	case version != "":
		return "Deprecated since " + version + "."
	case pkg != "":
		return "Deprecated by " + pkg + "."
	default:
		return ""
	}
}

func buildUsage(name string, parameters []TwigParameter) string {
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		names = append(names, parameter.Name)
	}
	return name + "(" + strings.Join(names, ", ") + ")"
}

func callbackMethodName(callback string) string {
	if index := strings.LastIndex(callback, "::"); index >= 0 {
		return callback[index+2:]
	}
	if index := strings.LastIndex(callback, "->"); index >= 0 {
		return callback[index+2:]
	}
	return callback
}

func compactCallbackReceiver(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "[](),")
	return strings.Join(strings.Fields(text), "")
}

func lastNamePart(name string) string {
	name = strings.TrimPrefix(name, "\\")
	if index := strings.LastIndex(name, "\\"); index >= 0 {
		return name[index+1:]
	}
	return name
}

func cloneParameters(parameters []TwigParameter) []TwigParameter {
	if len(parameters) == 0 {
		return nil
	}
	return append([]TwigParameter(nil), parameters...)
}
