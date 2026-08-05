package admin

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

const shopwareUtilsType = "Shopware.Utils"

func parseShopwareUtilityValueDeclarations(
	filePath,
	source string,
	lineIndex *cst.LineIndex,
) []AdminTypeDeclaration {
	if !isShopwareUtilitySource(filePath) || strings.TrimSpace(source) == "" {
		return nil
	}
	parsed := javascriptparser.Parse(source)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return nil
	}
	root := parsed.Tree.Root
	var result []AdminTypeDeclaration
	seen := make(map[string]bool)
	appendDeclaration := func(declaration AdminTypeDeclaration) {
		key := declaration.Name
		if declaration.Default {
			key = "\x00default"
		}
		if declaration.Name == "" || seen[key] {
			return
		}
		seen[key] = true
		result = append(result, declaration)
	}

	for _, function := range jsquery.Nodes(root, jssyntax.JsFunction) {
		if !topLevelJavaScriptNode(function) {
			continue
		}
		nameNode := firstDirectIdentifier(function)
		if nameNode == nil {
			continue
		}
		name := jsquery.IdentifierText(nameNode)
		if name == "" || !strings.Contains(function.Text(), "function") {
			continue
		}
		appendDeclaration(AdminTypeDeclaration{
			Name: name, FilePath: filePath,
			Line:            utilityNodeLine(nameNode, lineIndex),
			Alias:           vueMethodSignature(function, nil),
			DefinitionRange: componentMemberNameRange(nameNode, lineIndex),
		})
	}

	for _, variable := range jsquery.Nodes(
		root, jssyntax.JsVariableDeclaration,
	) {
		if !topLevelJavaScriptNode(variable) {
			continue
		}
		nameNode := firstDirectIdentifier(variable)
		if nameNode == nil {
			continue
		}
		name := jsquery.IdentifierText(nameNode)
		if name == "" {
			continue
		}
		declaration := AdminTypeDeclaration{
			Name: name, FilePath: filePath,
			Line:            utilityNodeLine(nameNode, lineIndex),
			DefinitionRange: componentMemberNameRange(nameNode, lineIndex),
		}
		if object := firstObject(variable); object != nil {
			declaration.Interface = true
			declaration.Members = shopwareUtilityObjectMembers(
				object, filePath, lineIndex,
			)
		} else {
			declaration.Alias = shopwareUtilityVariableType(variable)
		}
		appendDeclaration(declaration)
	}

	for _, exportNode := range jsquery.ExportDefaults(root) {
		expression := jsquery.ExportDefaultExpression(exportNode)
		if expression == nil {
			continue
		}
		name := "default"
		if isShopwareUtilityService(filePath) {
			name = shopwareUtilsType
		}
		declaration := AdminTypeDeclaration{
			Name: name, FilePath: filePath, Default: true,
			Line: utilityNodeLine(expression, lineIndex),
		}
		switch expression.Kind() {
		case jssyntax.JsObject:
			declaration.Interface = true
			declaration.Members = shopwareUtilityObjectMembers(
				expression, filePath, lineIndex,
			)
		case jssyntax.JsIdentifier:
			declaration.Alias = jsquery.IdentifierText(expression)
		default:
			declaration.Alias = strings.TrimSpace(expression.Text())
		}
		appendDeclaration(declaration)
	}
	return result
}

func shopwareUtilityObjectMembers(
	object *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []TwigVueMember {
	properties := jsquery.Properties(object)
	result := make([]TwigVueMember, 0, len(properties))
	for _, property := range properties {
		name := jsquery.PropertyName(property)
		nameNode := jsquery.PropertyNameNode(property)
		if name == "" || nameNode == nil {
			continue
		}
		member := TwigVueMember{
			Name: name, DefinitionPath: filePath,
			DefinitionLine:  utilityNodeLine(nameNode, lineIndex),
			DefinitionRange: componentMemberNameRange(nameNode, lineIndex),
		}
		if property.Kind() == jssyntax.JsMethod {
			member.Type = vueMethodSignature(property, nil)
			result = append(result, member)
			continue
		}
		value := jsquery.PropertyValue(property)
		if value == nil {
			member.Type = name
			result = append(result, member)
			continue
		}
		switch value.Kind() {
		case jssyntax.JsObject:
			member.Type = "{" + name + "}"
			member.NestedMembers = shopwareUtilityObjectMembers(
				value, filePath, lineIndex,
			)
			member.NestedComplete = true
		case jssyntax.JsFunction, jssyntax.JsArrowFunction:
			member.Type = vueMethodSignature(value, nil)
		default:
			member.Type = compactVueExpression(value.Text())
		}
		result = append(result, member)
	}
	return result
}

func shopwareUtilityVariableType(variable *jssyntax.Node) string {
	if variable == nil {
		return ""
	}
	text := strings.TrimSpace(variable.Text())
	equals := strings.IndexByte(text, '=')
	if equals < 0 {
		return ""
	}
	value := trimVueSourceExpression(text[equals+1:])
	if strings.HasPrefix(value, "mitt<") {
		open := strings.IndexByte(value, '<')
		close := matchingSlotDelimiter(value, open, '<', '>')
		if close > open {
			return "Emitter<" + strings.TrimSpace(value[open+1:close]) + ">"
		}
	}
	if strings.Contains(value, "=>") || strings.HasPrefix(value, "function") {
		return setupStoreBindingType(variable, AdminStoreAction)
	}
	return setupStoreBindingType(variable, AdminStoreState)
}

func isShopwareUtilitySource(filePath string) bool {
	normalized := filepath.ToSlash(filepath.Clean(filePath))
	return strings.Contains(normalized, "/core/service/utils/") ||
		isShopwareUtilityService(normalized)
}

func isShopwareUtilityService(filePath string) bool {
	normalized := filepath.ToSlash(filepath.Clean(filePath))
	return strings.HasSuffix(normalized, "/core/service/util.service.ts") ||
		strings.HasSuffix(normalized, "/core/service/util.service.js")
}

func topLevelJavaScriptNode(node *jssyntax.Node) bool {
	if node == nil || node.Parent() == nil {
		return false
	}
	parent := node.Parent()
	if parent.Kind() == jssyntax.JsExpressionStatement {
		parent = parent.Parent()
	}
	return parent != nil && parent.Kind() == jssyntax.JsProgram
}

func utilityNodeLine(node *jssyntax.Node, lineIndex *cst.LineIndex) int {
	if node == nil || lineIndex == nil {
		return 0
	}
	line, _ := lineIndex.Position(node.RangeTrimmedTrivia().Start)
	return int(line) + 1
}
