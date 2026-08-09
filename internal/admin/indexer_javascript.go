package admin

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func parseComponentRegistrations(root *jssyntax.Node, content []byte, filePath string) []VueComponent {
	return parseComponentRegistrationsWithLineIndex(
		root,
		filePath,
		jssyntax.NewLineIndex(string(content)),
	)
}

func parseComponentRegistrationsWithLineIndex(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponent {
	callNames := []string{
		"Shopware.Component.register",
		"Shopware.Component.extend",
		"Shopware.Component.override",
		"Component.register",
		"Component.extend",
		"Component.override",
	}
	var components []VueComponent
	for _, call := range jsquery.Calls(root, callNames...) {
		name := jsquery.CallName(call)
		line, _ := lineIndex.Position(call.RangeTrimmedTrivia().Start)
		component := VueComponent{FilePath: filePath, Line: int(line) + 1, Kind: ComponentRegister}
		component.Deprecated = componentRegistrationDeprecation(call)
		component.Name = jsquery.StringValue(jsquery.StringArgument(call, 0))
		if component.Name == "" {
			continue
		}

		definitionIndex := 1
		if strings.HasSuffix(name, ".extend") {
			component.Kind = ComponentExtend
			component.ExtendsComponent = jsquery.StringValue(jsquery.StringArgument(call, 1))
			component.TargetComponent = component.ExtendsComponent
			definitionIndex = 2
		} else if strings.HasSuffix(name, ".override") {
			component.Kind = ComponentOverride
			component.TargetComponent = component.Name
		}
		if definition := jsquery.ArgumentExpression(call, definitionIndex); definition != nil {
			switch definition.Kind() {
			case jssyntax.JsObject:
				component.InlineDefinition = parseInlineDefinition(
					root, definition, filePath, lineIndex,
				)
				component.DefinitionPath = filePath
			case jssyntax.JsArrowFunction, jssyntax.JsFunction:
				if importPath := jsquery.DynamicImportPath(definition); importPath != "" {
					component.ImportPath = importPath
					component.DefinitionPath = resolveImportPath(filePath, importPath)
				}
			case jssyntax.JsCallExpression:
				if object := componentDefinitionObject(definition); object != nil {
					component.InlineDefinition = parseInlineDefinition(
						root, object, filePath, lineIndex,
					)
					component.DefinitionPath = filePath
				}
			}
		}
		components = append(components, component)
	}
	for _, component := range parseVueApplicationComponentCollections(
		root, filePath, lineIndex,
	) {
		duplicate := false
		for _, existing := range components {
			if existing.Name == component.Name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			components = append(components, component)
		}
	}
	return components
}

func componentRegistrationDeprecation(call *jssyntax.Node) string {
	for current := call; current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsProgram {
			break
		}
		if deprecation := JavaScriptDeprecation(current); deprecation != "" {
			return deprecation
		}
	}
	return ""
}

// parseVueApplicationComponentCollections recognizes static component maps
// that are deliberately registered on the Vue application after normalizing
// their object keys to kebab-case. Shopware uses this for eager and lazy
// Meteor components, including compound exports that do not have standalone
// declaration files.
func parseVueApplicationComponentCollections(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponent {
	if root == nil {
		return nil
	}
	var result []VueComponent
	for _, declaration := range jsquery.Nodes(
		root, jssyntax.JsVariableDeclaration,
	) {
		nameNode := firstDirectIdentifier(declaration)
		object := firstObject(declaration)
		if nameNode == nil || object == nil {
			continue
		}
		collectionName := jsquery.IdentifierText(nameNode)
		if collectionName == "" ||
			!isVueApplicationComponentCollection(root, collectionName) {
			continue
		}
		for _, property := range jsquery.Properties(object) {
			propertyName := strings.TrimSpace(jsquery.PropertyName(property))
			if propertyName == "" || !isStaticVueIdentifier(propertyName) {
				continue
			}
			value := jsquery.PropertyValue(property)
			importPath := ""
			if value == nil {
				// JavaScript shorthand: { MtButton }.
				importPath = jsquery.ImportPath(root, propertyName)
			} else {
				switch value.Kind() {
				case jssyntax.JsIdentifier:
					importPath = jsquery.ImportPath(
						root, jsquery.IdentifierText(value),
					)
				case jssyntax.JsArrowFunction, jssyntax.JsFunction:
					importPath = jsquery.DynamicImportPath(value)
				default:
					continue
				}
			}
			line := 0
			if lineIndex != nil {
				lineNumber, _ := lineIndex.Position(
					property.RangeTrimmedTrivia().Start,
				)
				line = int(lineNumber) + 1
			}
			result = append(result, VueComponent{
				Name:           CamelToKebab(propertyName),
				FilePath:       filePath,
				DefinitionPath: filePath,
				ImportPath:     importPath,
				Line:           line,
				Kind:           ComponentRegister,
			})
		}
	}
	for _, component := range parseRawVueApplicationComponentCollections(
		root, filePath, lineIndex,
	) {
		duplicate := false
		for _, existing := range result {
			if existing.Name == component.Name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, component)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Line == result[right].Line {
			return result[left].Name < result[right].Name
		}
		return result[left].Line < result[right].Line
	})
	return result
}

// TypeScript recovery can retain a class method body losslessly even when a
// construct such as `as const` prevents it from producing nested expression
// nodes. Scan only the same strongly identified registration shape so these
// valid declarations remain indexable without broad text heuristics.
func parseRawVueApplicationComponentCollections(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponent {
	if root == nil {
		return nil
	}
	source := root.Text()
	collections := rawVueApplicationComponentCollections(source)
	var result []VueComponent
	for collectionName, loopOffset := range collections {
		open := staticVueCollectionObjectStart(
			source[:loopOffset], collectionName,
		)
		if open < 0 {
			continue
		}
		close := balancedBraceEnd(source, open)
		if close <= open || close > loopOffset {
			continue
		}
		for _, segment := range splitTopLevelDeclarations(
			source[open+1:close], open+1,
		) {
			entry, found := rawVueComponentCollectionEntry(segment)
			if !found {
				continue
			}
			line := 0
			if lineIndex != nil {
				lineNumber, _ := lineIndex.Position(uint32(entry.offset))
				line = int(lineNumber) + 1
			}
			importPath := jsquery.ImportPath(root, entry.symbol)
			if importPath == "" {
				importPath = rawDynamicImportPath(entry.expression)
			}
			result = append(result, VueComponent{
				Name:           CamelToKebab(entry.name),
				FilePath:       filePath,
				DefinitionPath: filePath,
				ImportPath:     importPath,
				Line:           line,
				Kind:           ComponentRegister,
			})
		}
	}
	return result
}

func rawVueApplicationComponentCollections(source string) map[string]int {
	result := make(map[string]int)
	const marker = "Object.entries"
	for searchOffset := 0; searchOffset < len(source); {
		relative := strings.Index(source[searchOffset:], marker)
		if relative < 0 {
			break
		}
		position := searchOffset + relative
		searchOffset = position + len(marker)
		if !adminTypeCodePosition(source, position) {
			continue
		}
		cursor := skipJavaScriptSpaces(source, searchOffset)
		if cursor >= len(source) || source[cursor] != '(' {
			continue
		}
		close := matchingSlotDelimiter(source, cursor, '(', ')')
		if close < 0 {
			continue
		}
		collectionName := strings.TrimSpace(source[cursor+1 : close])
		if !isStaticVueIdentifier(collectionName) {
			continue
		}
		cursor = skipJavaScriptSpaces(source, close+1)
		if cursor >= len(source) || source[cursor] != '.' {
			continue
		}
		cursor = skipJavaScriptSpaces(source, cursor+1)
		if !strings.HasPrefix(source[cursor:], "forEach") {
			continue
		}
		cursor = skipJavaScriptSpaces(source, cursor+len("forEach"))
		if cursor >= len(source) || source[cursor] != '(' {
			continue
		}
		callbackClose := matchingSlotDelimiter(source, cursor, '(', ')')
		if callbackClose < 0 {
			continue
		}
		callback := compactJavaScriptText(source[cursor+1 : callbackClose])
		if !strings.Contains(callback, "kebabCase(") ||
			(!strings.Contains(callback, ".component(") &&
				!strings.Contains(callback, "registerAsyncComponent(")) {
			continue
		}
		result[collectionName] = position
	}
	return result
}

func staticVueCollectionObjectStart(source, collectionName string) int {
	for searchEnd := len(source); searchEnd > 0; {
		position := strings.LastIndex(source[:searchEnd], collectionName)
		if position < 0 {
			return -1
		}
		searchEnd = position
		if !adminTypeCodePosition(source, position) ||
			position > 0 && isVueIdentifierPart(source[position-1]) ||
			position+len(collectionName) < len(source) &&
				isVueIdentifierPart(source[position+len(collectionName)]) {
			continue
		}
		before := position
		for before > 0 && isJavaScriptSpace(source[before-1]) {
			before--
		}
		keywordStart := before
		for keywordStart > 0 && isVueIdentifierPart(source[keywordStart-1]) {
			keywordStart--
		}
		if source[keywordStart:before] != "const" &&
			source[keywordStart:before] != "let" {
			continue
		}
		cursor := skipJavaScriptSpaces(source, position+len(collectionName))
		if cursor >= len(source) || source[cursor] != '=' {
			continue
		}
		cursor = skipJavaScriptSpaces(source, cursor+1)
		if cursor < len(source) && source[cursor] == '{' {
			return cursor
		}
	}
	return -1
}

type rawVueComponentEntry struct {
	name       string
	symbol     string
	expression string
	offset     int
}

func rawVueComponentCollectionEntry(
	segment declarationSegment,
) (rawVueComponentEntry, bool) {
	trimmed, skipped := trimDeclarationPrefix(segment.text)
	if trimmed == "" || !isVueIdentifierStart(trimmed[0]) {
		return rawVueComponentEntry{}, false
	}
	end := 1
	for end < len(trimmed) && isVueIdentifierPart(trimmed[end]) {
		end++
	}
	name := trimmed[:end]
	rest := strings.TrimSpace(trimmed[end:])
	entry := rawVueComponentEntry{
		name: name, symbol: name,
		offset: segment.offset + skipped,
	}
	if rest == "" {
		return entry, true
	}
	if rest[0] != ':' {
		return rawVueComponentEntry{}, false
	}
	entry.expression = strings.TrimSpace(rest[1:])
	if entry.expression == "" {
		return rawVueComponentEntry{}, false
	}
	if isStaticVueIdentifier(entry.expression) {
		entry.symbol = entry.expression
	} else {
		entry.symbol = ""
	}
	return entry, true
}

func rawDynamicImportPath(expression string) string {
	position := strings.Index(expression, "import")
	if position < 0 {
		return ""
	}
	cursor := skipJavaScriptSpaces(expression, position+len("import"))
	if cursor >= len(expression) || expression[cursor] != '(' {
		return ""
	}
	cursor = skipJavaScriptSpaces(expression, cursor+1)
	if cursor >= len(expression) ||
		(expression[cursor] != '\'' && expression[cursor] != '"') {
		return ""
	}
	quote := expression[cursor]
	start := cursor + 1
	for cursor = start; cursor < len(expression); cursor++ {
		if expression[cursor] == '\\' {
			cursor++
			continue
		}
		if expression[cursor] == quote {
			return expression[start:cursor]
		}
	}
	return ""
}

func skipJavaScriptSpaces(value string, cursor int) int {
	for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
		cursor++
	}
	return cursor
}

func isVueApplicationComponentCollection(
	root *jssyntax.Node,
	collectionName string,
) bool {
	wantedReceiver := "Object.entries(" + collectionName + ")"
	for _, call := range jsquery.Calls(root) {
		receiver, found := staticCallbackCallReceiver(call, "forEach")
		if !found || compactJavaScriptText(receiver) != wantedReceiver {
			continue
		}
		callback := jsquery.ArgumentExpression(call, 0)
		if callback == nil {
			continue
		}
		text := compactJavaScriptText(callback.Text())
		if !strings.Contains(text, "kebabCase(") {
			continue
		}
		if strings.Contains(text, ".component(") ||
			strings.Contains(text, "registerAsyncComponent(") {
			return true
		}
	}
	return false
}

func parseMixinsAndModules(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) ([]AdminMixin, []AdminModule) {
	var mixins []AdminMixin
	for _, call := range jsquery.Calls(
		root,
		"Shopware.Mixin.register",
		"Mixin.register",
	) {
		name := jsquery.StringValue(jsquery.StringArgument(call, 0))
		if name == "" {
			continue
		}
		line, _ := lineIndex.Position(call.RangeTrimmedTrivia().Start)
		mixin := AdminMixin{
			Name: name, FilePath: filePath, Line: int(line) + 1,
		}
		if object := componentDefinitionObject(
			jsquery.ArgumentExpression(call, 1),
		); object != nil {
			definition := parseInlineDefinition(
				root, object, filePath, lineIndex,
			)
			if definition != nil {
				mixin.Definition = *definition
			}
		}
		mixins = append(mixins, mixin)
	}

	var modules []AdminModule
	for _, call := range jsquery.Calls(
		root,
		"Shopware.Module.register",
		"Module.register",
	) {
		name := jsquery.StringValue(jsquery.StringArgument(call, 0))
		config := resolveAdminModuleConfig(root, call)
		if name == "" || config == nil {
			continue
		}
		line, _ := lineIndex.Position(call.RangeTrimmedTrivia().Start)
		module := AdminModule{
			Name: name, FilePath: filePath, Line: int(line) + 1,
			DisplayName: stringProperty(config, "name"),
			Type:        stringProperty(config, "type"),
			Title:       stringProperty(config, "title"),
			Description: stringProperty(config, "description"),
		}
		if routesProperty := jsquery.Property(config, "routes"); routesProperty != nil {
			module.Routes = appendAdminModuleRoutes(
				module.Routes,
				root,
				resolveAdminRouteObject(
					root,
					jsquery.PropertyValue(routesProperty),
				),
				strings.ReplaceAll(name, "-", "."),
				lineIndex,
			)
		}
		module.Routes = appendAdminRouteMiddlewareRoutes(
			module.Routes, root, config, lineIndex,
		)
		modules = append(modules, module)
	}
	return mixins, modules
}

func resolveAdminModuleConfig(
	root,
	call *jssyntax.Node,
) *jssyntax.Node {
	expression := jsquery.ArgumentExpression(call, 1)
	if expression == nil {
		return nil
	}
	if expression.Kind() == jssyntax.JsObject {
		return expression
	}
	identifier := strings.TrimSpace(jsquery.IdentifierText(expression))
	if identifier == "" {
		return nil
	}
	declaration, _, found := visibleJavaScriptConstDeclaration(
		call, identifier, root,
	)
	if !found || declaration == nil {
		return nil
	}
	for child := range declaration.ChildNodes() {
		if child.Kind() == jssyntax.JsObject {
			return child
		}
	}
	return nil
}

func appendAdminRouteMiddlewareRoutes(
	destination []AdminModuleRoute,
	root,
	config *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []AdminModuleRoute {
	property := jsquery.Property(config, "routeMiddleware")
	middleware := resolveAdminRouteMiddleware(root, property)
	if middleware == nil {
		return destination
	}
	positions := make(map[string]bool, len(destination))
	for _, route := range destination {
		positions[route.Name] = true
	}
	for _, call := range jsquery.Calls(middleware) {
		callName := jsquery.CallName(call)
		if callName != "children.push" &&
			!strings.HasSuffix(callName, ".children.push") {
			continue
		}
		routeConfig := jsquery.ObjectArgument(call, 0)
		if routeConfig == nil {
			continue
		}
		name := adminStaticStringProperty(root, routeConfig, "name")
		if name == "" || positions[name] {
			continue
		}
		positions[name] = true
		line, _ := lineIndex.Position(routeConfig.RangeTrimmedTrivia().Start)
		destination = append(destination, AdminModuleRoute{
			Name: name, LocalName: adminModuleRouteLocalName(name),
			Path:      stringProperty(routeConfig, "path"),
			Component: adminModuleRouteComponent(routeConfig),
			Line:      int(line) + 1,
		})
	}
	return destination
}

func resolveAdminRouteMiddleware(
	root,
	property *jssyntax.Node,
) *jssyntax.Node {
	if property == nil {
		return nil
	}
	if property.Kind() == jssyntax.JsMethod {
		return property
	}
	value := jsquery.PropertyValue(property)
	if value == nil {
		return nil
	}
	switch value.Kind() {
	case jssyntax.JsFunction, jssyntax.JsArrowFunction, jssyntax.JsMethod:
		return value
	}
	identifier := strings.TrimSpace(jsquery.IdentifierText(value))
	if identifier == "" {
		return nil
	}
	for _, function := range jsquery.Nodes(root, jssyntax.JsFunction) {
		if adminJavaScriptFunctionName(function) == identifier {
			return function
		}
	}
	declaration, _, found := visibleJavaScriptConstDeclaration(
		property, identifier, root,
	)
	if !found || declaration == nil {
		return nil
	}
	for child := range declaration.ChildNodes() {
		if child.Kind() == jssyntax.JsFunction ||
			child.Kind() == jssyntax.JsArrowFunction {
			return child
		}
	}
	return nil
}

func adminStaticStringProperty(
	root,
	object *jssyntax.Node,
	name string,
) string {
	property := jsquery.Property(object, name)
	if property == nil {
		return ""
	}
	value := jsquery.PropertyValue(property)
	if direct := jsquery.StringValue(value); direct != "" {
		return direct
	}
	identifier := ""
	if value != nil {
		identifier = strings.TrimSpace(jsquery.IdentifierText(value))
	} else if property.Kind() == jssyntax.JsProperty {
		identifier = jsquery.PropertyName(property)
	}
	if identifier == "" {
		return ""
	}
	expression, found := visibleJavaScriptConstInitializer(
		property, identifier, root,
	)
	if !found {
		return ""
	}
	parsed := javascriptparser.Parse(expression)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return ""
	}
	for _, stringNode := range jsquery.Nodes(
		parsed.Tree.Root, jssyntax.JsString,
	) {
		return jsquery.StringValue(stringNode)
	}
	return ""
}

func adminModuleRouteLocalName(name string) string {
	if position := strings.LastIndexByte(name, '.'); position >= 0 {
		return name[position+1:]
	}
	return name
}

func appendAdminModuleRoutes(
	destination []AdminModuleRoute,
	root *jssyntax.Node,
	routesObject *jssyntax.Node,
	prefix string,
	lineIndex *cst.LineIndex,
) []AdminModuleRoute {
	for _, routeProperty := range jsquery.Properties(routesObject) {
		localName := jsquery.PropertyName(routeProperty)
		routeConfig := jsquery.PropertyValue(routeProperty)
		if localName == "" || routeConfig == nil {
			continue
		}
		name := prefix + "." + localName
		routeLine, _ := lineIndex.Position(
			routeProperty.RangeTrimmedTrivia().Start,
		)
		destination = append(destination, AdminModuleRoute{
			Name:      name,
			LocalName: localName,
			Path:      stringProperty(routeConfig, "path"),
			Component: adminModuleRouteComponent(routeConfig),
			Line:      int(routeLine) + 1,
		})
		children := resolveAdminRouteObject(
			root,
			jsquery.PropertyValue(jsquery.Property(routeConfig, "children")),
		)
		destination = appendAdminModuleRoutes(
			destination,
			root,
			children,
			name,
			lineIndex,
		)
	}
	return destination
}

// resolveAdminRouteObject follows the small static factory pattern used by
// core Administration modules, for example `children: detailChildren()` with
// a local `function detailChildren() { return { ... }; }` declaration.
func resolveAdminRouteObject(
	root *jssyntax.Node,
	expression *jssyntax.Node,
) *jssyntax.Node {
	if expression == nil || expression.Kind() == jssyntax.JsObject {
		return expression
	}
	if expression.Kind() != jssyntax.JsCallExpression {
		return nil
	}
	name := jsquery.CallName(expression)
	if name == "" || strings.Contains(name, ".") ||
		len(jsquery.Arguments(expression)) != 0 {
		return nil
	}
	for _, function := range jsquery.Nodes(root, jssyntax.JsFunction) {
		if adminJavaScriptFunctionName(function) != name {
			continue
		}
		for _, object := range jsquery.Nodes(function, jssyntax.JsObject) {
			return object
		}
	}
	return nil
}

func adminJavaScriptFunctionName(function *jssyntax.Node) string {
	if function == nil || function.Kind() != jssyntax.JsFunction {
		return ""
	}
	for child := range function.ChildNodes() {
		if child.Kind() == jssyntax.JsIdentifier {
			return strings.TrimSpace(child.Text())
		}
	}
	return ""
}

func adminModuleRouteComponent(routeConfig *jssyntax.Node) string {
	if component := stringProperty(routeConfig, "component"); component != "" {
		return component
	}
	components := jsquery.PropertyValue(jsquery.Property(routeConfig, "components"))
	return stringProperty(components, "default")
}

func stringProperty(object *jssyntax.Node, name string) string {
	property := jsquery.Property(object, name)
	if property == nil {
		return ""
	}
	return jsquery.StringValue(jsquery.PropertyValue(property))
}

func parseInlineDefinition(
	root,
	object *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) *ComponentDefinition {
	definition := ParseComponentObject(object, filePath, lineIndex)
	if definition == nil {
		return nil
	}
	if root == nil {
		root = object
		for root.Parent() != nil {
			root = root.Parent()
		}
	}
	enrichLocalComponentImports(root, definition)
	if templateImport := jsquery.ImportPath(root, "template"); templateImport != "" {
		definition.TemplatePath = ResolveTemplatePath(filePath, templateImport)
		if result, err := ParseTemplateFromFile(definition.TemplatePath); err == nil {
			definition.Slots = result.Slots
			definition.Blocks = result.Blocks
		}
	}
	return definition
}

// resolveImportPath resolves an import path relative to the registration file
func resolveImportPath(registrationFile, importPath string) string {
	if importPath == "" {
		return ""
	}

	var basePath string

	// If it starts with 'src/', it's an absolute path from the administration root
	if strings.HasPrefix(importPath, "src/") {
		// Find the administration root
		adminIdx := strings.Index(registrationFile, "Resources/app/administration/")
		if adminIdx != -1 {
			adminRoot := registrationFile[:adminIdx+len("Resources/app/administration/")]
			basePath = filepath.Join(adminRoot, importPath)
		} else {
			return importPath
		}
	} else if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		// Handle relative paths
		dir := filepath.Dir(registrationFile)
		basePath = filepath.Join(dir, importPath)
	} else {
		return importPath
	}

	// Try to resolve the actual file
	return resolveJSFile(basePath)
}

// resolveJSFile resolves Administration JavaScript, TypeScript, and Vue SFC
// imports. The historical name is retained because callers also use it for
// store and service module paths.
func resolveJSFile(basePath string) string {
	// If already has extension, return as-is
	if strings.HasSuffix(basePath, ".js") || strings.HasSuffix(basePath, ".ts") ||
		strings.HasSuffix(basePath, ".vue") {
		return basePath
	}

	// Try direct file with extensions
	candidates := []string{
		basePath + ".js",
		basePath + ".ts",
		basePath + ".vue",
		filepath.Join(basePath, "index.js"),
		filepath.Join(basePath, "index.ts"),
		filepath.Join(basePath, "index.vue"),
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Fallback: return with /index.js as most common pattern
	return filepath.Join(basePath, "index.js")
}
