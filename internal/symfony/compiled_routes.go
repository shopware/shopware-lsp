package symfony

import (
	"regexp"
	"strconv"
	"strings"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

var legacyAsseticRoutePattern = regexp.MustCompile(
	`^_assetic_[0-9a-z]+[_0-9+]*$`,
)

// ParseCompiledRoutes parses Symfony's generated URL-generator catalogs:
// Symfony 4+ return arrays, pre-2.8 declaredRoutes properties, and Symfony
// 2.8+ constructor assignments. Generated path tokens are stored in reverse
// order, so this restores their source path. Modern canonical aliases and
// legacy I18nRoutingBundle names are normalized to the public route names.
func ParseCompiledRoutes(filePath string, content []byte) []Route {
	tree := phpparser.Parse(string(content)).Tree
	if tree == nil || tree.Root == nil {
		return nil
	}
	lineIndex := phpsyntax.NewLineIndex(tree.Source)
	routes := make(map[string]Route)
	order := make([]string, 0)
	for _, statement := range phpquery.Nodes(
		tree.Root,
		phpsyntax.PhpReturnStatement,
	) {
		array := directCompiledArray(statement)
		if array == nil {
			continue
		}
		for _, item := range phpquery.ArrayItems(array) {
			nameNode := phpquery.StringAt(phpquery.ArrayItemKey(item))
			config := directCompiledArray(phpquery.ArrayItemValue(item))
			name := compiledPHPString(nameNode)
			if name == "" || config == nil {
				continue
			}
			route := compiledRouteFromArray(
				filePath,
				name,
				nameNode,
				config,
				lineIndex,
			)
			if route.Path == "" {
				continue
			}
			addCompiledRoute(routes, &order, route, true)

			canonical := compiledArrayStringValue(
				compiledArrayElement(config, 1),
				"_canonical_route",
			)
			if canonical == "" {
				continue
			}
			canonicalRoute := route
			canonicalRoute.Name = canonical
			addCompiledRoute(routes, &order, canonicalRoute, false)
		}
	}
	for _, class := range phpquery.Classes(tree.Root) {
		if !compiledRouteGeneratorClass(class) {
			continue
		}
		for _, property := range phpquery.Properties(class) {
			if !compiledDeclaredRoutesProperty(property) {
				continue
			}
			collectLegacyCompiledRoutes(
				routes,
				&order,
				filePath,
				directCompiledArray(property),
				lineIndex,
			)
		}
		for _, method := range phpquery.Methods(class) {
			if !strings.EqualFold(phpquery.MethodName(method), "__construct") {
				continue
			}
			for _, assignment := range phpquery.Nodes(
				method,
				phpsyntax.PhpAssignmentExpression,
			) {
				left, right := compiledAssignmentOperands(assignment)
				if !compiledDeclaredRoutesAccess(left) {
					continue
				}
				collectLegacyCompiledRoutes(
					routes,
					&order,
					filePath,
					directCompiledArray(right),
					lineIndex,
				)
			}
		}
	}
	result := make([]Route, 0, len(order))
	for _, name := range order {
		result = append(result, routes[name])
	}
	return result
}

func addCompiledRoute(
	routes map[string]Route,
	order *[]string,
	route Route,
	overwrite bool,
) {
	if route.Name == "" {
		return
	}
	if _, exists := routes[route.Name]; exists {
		if overwrite {
			routes[route.Name] = route
		}
		return
	}
	routes[route.Name] = route
	*order = append(*order, route.Name)
}

func compiledRouteGeneratorClass(class *phpsyntax.Node) bool {
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration {
		return false
	}
	names := append(
		append([]string(nil), phpquery.ClassExtends(class)...),
		phpquery.ClassImplements(class)...,
	)
	for _, name := range names {
		normalized := strings.ToLower(strings.TrimPrefix(
			strings.TrimSpace(name),
			`\`,
		))
		if normalized == "urlgenerator" ||
			normalized == "urlgeneratorinterface" ||
			strings.HasSuffix(
				normalized,
				`\routing\generator\urlgenerator`,
			) ||
			strings.HasSuffix(
				normalized,
				`\routing\generator\urlgeneratorinterface`,
			) {
			return true
		}
	}
	return false
}

func compiledDeclaredRoutesProperty(property *phpsyntax.Node) bool {
	for _, variable := range phpquery.PropertyVariables(property) {
		if phpquery.VariableName(variable) == "declaredRoutes" {
			return true
		}
	}
	return false
}

func compiledAssignmentOperands(
	assignment *phpsyntax.Node,
) (*phpsyntax.Node, *phpsyntax.Node) {
	if assignment == nil ||
		assignment.Kind() != phpsyntax.PhpAssignmentExpression {
		return nil, nil
	}
	var operands []*phpsyntax.Node
	for child := range assignment.ChildNodes() {
		operands = append(operands, child)
	}
	if len(operands) < 2 {
		return nil, nil
	}
	return operands[0], operands[len(operands)-1]
}

func compiledDeclaredRoutesAccess(node *phpsyntax.Node) bool {
	if node == nil {
		return false
	}
	text := strings.Join(strings.Fields(node.Text()), "")
	return strings.HasSuffix(text, "::$declaredRoutes")
}

func collectLegacyCompiledRoutes(
	routes map[string]Route,
	order *[]string,
	filePath string,
	array *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
) {
	array = directCompiledArray(array)
	if array == nil {
		return
	}
	for _, item := range phpquery.ArrayItems(array) {
		nameNode := phpquery.StringAt(phpquery.ArrayItemKey(item))
		config := directCompiledArray(phpquery.ArrayItemValue(item))
		name := compiledPHPString(nameNode)
		if name == "" ||
			legacyAsseticRoutePattern.MatchString(name) ||
			config == nil {
			continue
		}
		name = normalizeLegacyCompiledRouteName(name)
		route := compiledRouteFromArray(
			filePath,
			name,
			nameNode,
			config,
			lineIndex,
		)
		if route.Path == "" {
			continue
		}
		// The reference plugin stores the last localized variant under the
		// normalized public name.
		addCompiledRoute(routes, order, route, true)
	}
}

func normalizeLegacyCompiledRouteName(name string) string {
	if len(name) >= 8 &&
		name[0] >= 'a' && name[0] <= 'z' &&
		name[1] >= 'a' && name[1] <= 'z' &&
		name[2:8] == "__RG__" {
		return name[8:]
	}
	return name
}

func compiledRouteFromArray(
	filePath,
	name string,
	nameNode,
	config *phpsyntax.Node,
	lineIndex *phpsyntax.LineIndex,
) Route {
	line := uint32(0)
	if nameNode != nil && lineIndex != nil {
		line, _ = lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
	}
	defaults := compiledArrayElement(config, 1)
	return Route{
		Name: name,
		Path: compiledRoutePath(
			compiledArrayElement(config, 3),
		),
		Controller: normalizePHPRouteController(
			compiledArrayStringValue(defaults, "_controller"),
		),
		FilePath: filePath,
		Line:     int(line) + 1,
	}
}

func compiledRoutePath(tokens *phpsyntax.Node) string {
	tokens = directCompiledArray(tokens)
	if tokens == nil {
		return ""
	}
	items := phpquery.ArrayItems(tokens)
	var result strings.Builder
	for index := len(items) - 1; index >= 0; index-- {
		token := directCompiledArray(phpquery.ArrayItemValue(items[index]))
		switch compiledArrayStringAt(token, 0) {
		case "text":
			result.WriteString(compiledArrayStringAt(token, 1))
		case "variable":
			name := compiledArrayStringAt(token, 3)
			if name == "" {
				continue
			}
			result.WriteString(compiledArrayStringAt(token, 1))
			result.WriteByte('{')
			result.WriteString(name)
			result.WriteByte('}')
		}
	}
	return result.String()
}

func compiledArrayStringValue(array *phpsyntax.Node, key string) string {
	array = directCompiledArray(array)
	if array == nil {
		return ""
	}
	for _, item := range phpquery.ArrayItems(array) {
		if compiledPHPString(phpquery.ArrayItemKey(item)) != key {
			continue
		}
		return compiledPHPString(phpquery.ArrayItemValue(item))
	}
	return ""
}

func compiledArrayStringAt(array *phpsyntax.Node, index int) string {
	return compiledPHPString(compiledArrayElement(array, index))
}

func compiledArrayElement(
	array *phpsyntax.Node,
	wanted int,
) *phpsyntax.Node {
	array = directCompiledArray(array)
	if array == nil || wanted < 0 {
		return nil
	}
	position := 0
	for _, item := range phpquery.ArrayItems(array) {
		itemIndex := position
		if key := phpquery.ArrayItemKey(item); key != nil {
			parsed, err := strconv.Atoi(strings.TrimSpace(key.Text()))
			if err != nil {
				position++
				continue
			}
			itemIndex = parsed
		}
		if itemIndex == wanted {
			return phpquery.ArrayItemValue(item)
		}
		position = itemIndex + 1
	}
	return nil
}

func directCompiledArray(node *phpsyntax.Node) *phpsyntax.Node {
	if node == nil {
		return nil
	}
	if node.Kind() == phpsyntax.PhpArray {
		return node
	}
	for child := range node.ChildNodes() {
		if child.Kind() == phpsyntax.PhpArray {
			return child
		}
	}
	return nil
}

func compiledPHPString(node *phpsyntax.Node) string {
	value := phpquery.StringValue(node)
	value = strings.ReplaceAll(value, `\\`, `\`)
	value = strings.ReplaceAll(value, `\'`, `'`)
	return value
}
