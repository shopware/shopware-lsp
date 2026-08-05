package admin

import (
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

type AdminServiceRegistrationKind string

const (
	AdminServiceProvider AdminServiceRegistrationKind = "provider"
	AdminServiceFactory  AdminServiceRegistrationKind = "factory"
)

// AdminService is a service registered in the Administration dependency
// container through Application.addServiceProvider or Service().register.
type AdminService struct {
	Name               string
	Kind               AdminServiceRegistrationKind
	FilePath           string
	Line               int
	ImplementationName string
	ImplementationPath string
}

type AdminCMSRegistrationKind string

const (
	AdminCMSElement AdminCMSRegistrationKind = "element"
	AdminCMSBlock   AdminCMSRegistrationKind = "block"
)

// AdminCMSRegistration describes one statically registered Shopping
// Experiences element or block. Component fields link the CMS registry to the
// ordinary Administration component catalog; Slots link blocks to elements.
type AdminCMSRegistration struct {
	Kind             AdminCMSRegistrationKind
	Name             string
	Label            string
	Category         string
	Component        string
	ConfigComponent  string
	PreviewComponent string
	Slots            []AdminCMSReference
	FilePath         string
	Line             int
	NameRange        AdminSourceRange
}

type AdminCMSReference struct {
	Name  string
	Range AdminSourceRange
}

func AdminCMSKey(kind AdminCMSRegistrationKind, name string) string {
	return string(kind) + "\x00" + name
}

var serviceImplementationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\breturn\s+new\s+([A-Za-z_$][A-Za-z0-9_$]*)`),
	regexp.MustCompile(`=>\s*new\s+([A-Za-z_$][A-Za-z0-9_$]*)`),
	regexp.MustCompile(`\breturn\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`),
	regexp.MustCompile(`=>\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`),
	regexp.MustCompile(`\breturn\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*;`),
}

type AdminStoreMemberKind string

const (
	AdminStoreState  AdminStoreMemberKind = "state"
	AdminStoreGetter AdminStoreMemberKind = "getter"
	AdminStoreAction AdminStoreMemberKind = "action"
)

// AdminStore is a Pinia store registered through Shopware.Store.register.
type AdminStore struct {
	Name        string
	FilePath    string
	Line        int
	StateType   string
	FactoryName string
	FactoryPath string
	Members     []AdminStoreMember
}

type AdminStoreMember struct {
	Name             string
	Kind             AdminStoreMemberKind
	Type             string
	OpenRuntimeShape bool
	FilePath         string
	Line             int
}

// AdminStoreFactory is the setup-style function used by a registered Pinia
// store. It is indexed independently from the registration so edits to an
// imported composable immediately update Store.get(...) features.
type AdminStoreFactory struct {
	FilePath string
	Members  []AdminStoreMember
}

func (store AdminStore) Member(name string) (AdminStoreMember, bool) {
	for _, member := range store.Members {
		if member.Name == name {
			return member, true
		}
	}
	return AdminStoreMember{}, false
}

func parseAdminRuntimeRegistries(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) ([]AdminService, []AdminStore) {
	var services []AdminService
	var stores []AdminStore
	for _, call := range jsquery.Calls(root) {
		if kind, matched := serviceRegistrationKind(call); matched {
			nameNode := jsquery.StringArgument(call, 0)
			name := jsquery.StringValue(nameNode)
			if name != "" {
				line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
				service := AdminService{
					Name: name, Kind: kind, FilePath: filePath,
					Line: int(line) + 1,
				}
				service.ImplementationName = serviceImplementationName(call)
				if service.ImplementationName != "" {
					if importPath := jsquery.ImportPath(
						root,
						service.ImplementationName,
					); importPath != "" {
						service.ImplementationPath = resolveImportPath(
							filePath,
							importPath,
						)
					}
				}
				services = append(services, service)
			}
		}

		if store, matched := parseStoreRegistration(
			root,
			call,
			filePath,
			lineIndex,
		); matched {
			stores = append(stores, store)
		}
	}
	return services, stores
}

func parseAdminDirectives(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminDirective {
	if root == nil || lineIndex == nil {
		return nil
	}
	var result []AdminDirective
	for _, call := range jsquery.Calls(
		root,
		"Directive.register",
		"Shopware.Directive.register",
	) {
		nameNode := jsquery.StringArgument(call, 0)
		name := jsquery.StringValue(nameNode)
		if name == "" {
			continue
		}
		line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
		result = append(result, AdminDirective{
			Name: name, FilePath: filePath, Line: int(line) + 1,
		})
	}
	return result
}

func parseAdminFilters(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminFilter {
	if root == nil || lineIndex == nil {
		return nil
	}
	var result []AdminFilter
	for _, call := range jsquery.Calls(
		root,
		"Filter.register",
		"Shopware.Filter.register",
	) {
		nameNode := jsquery.StringArgument(call, 0)
		name := jsquery.StringValue(nameNode)
		if name == "" {
			continue
		}
		line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
		result = append(result, AdminFilter{
			Name: name, FilePath: filePath, Line: int(line) + 1,
			NameRange: componentMemberNameRange(nameNode, lineIndex),
			Signature: vueMethodSignature(
				jsquery.ArgumentExpression(call, 1), nil,
			),
		})
	}
	return result
}

func parseAdminCMSRegistrations(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminCMSRegistration {
	if root == nil || lineIndex == nil {
		return nil
	}
	var result []AdminCMSRegistration
	for _, call := range jsquery.Calls(root) {
		var kind AdminCMSRegistrationKind
		switch jsquery.CallMethodName(call) {
		case "registerCmsElement":
			kind = AdminCMSElement
		case "registerCmsBlock":
			kind = AdminCMSBlock
		default:
			continue
		}
		config := jsquery.ObjectArgument(call, 0)
		nameNode := jsquery.PropertyValue(jsquery.Property(config, "name"))
		name := jsquery.StringValue(nameNode)
		if name == "" || nameNode == nil {
			continue
		}
		line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
		registration := AdminCMSRegistration{
			Kind: kind, Name: name, FilePath: filePath, Line: int(line) + 1,
			NameRange:        componentMemberNameRange(nameNode, lineIndex),
			Label:            stringProperty(config, "label"),
			Category:         stringProperty(config, "category"),
			Component:        stringProperty(config, "component"),
			ConfigComponent:  stringProperty(config, "configComponent"),
			PreviewComponent: stringProperty(config, "previewComponent"),
		}
		if kind == AdminCMSBlock {
			slots := jsquery.PropertyValue(jsquery.Property(config, "slots"))
			if slots != nil && slots.Kind() == jssyntax.JsObject {
				for _, property := range jsquery.Properties(slots) {
					reference := jsquery.PropertyValue(property)
					if reference != nil && reference.Kind() == jssyntax.JsObject {
						reference = jsquery.PropertyValue(
							jsquery.Property(reference, "type"),
						)
					}
					name := jsquery.StringValue(reference)
					if name == "" || reference == nil {
						continue
					}
					registration.Slots = append(
						registration.Slots,
						AdminCMSReference{
							Name:  name,
							Range: componentMemberNameRange(reference, lineIndex),
						},
					)
				}
			}
		}
		result = append(result, registration)
	}
	return result
}

func preferredCMSRegistrations(
	values []AdminCMSRegistration,
) []AdminCMSRegistration {
	return preferRuntimeDefinitions(values, func(value AdminCMSRegistration) string {
		return value.FilePath
	})
}

func serviceImplementationName(call *jssyntax.Node) string {
	arguments := jsquery.Arguments(call)
	if len(arguments) < 2 {
		return ""
	}
	// The intentionally shallow JavaScript parser can recover `() => new X()`
	// as two argument nodes around the `new` keyword. Inspecting the complete
	// factory tail keeps implementation discovery correct for that valid syntax.
	// It also avoids scanning the receiver of a fluent service-provider chain.
	var factoryText strings.Builder
	for _, argument := range arguments[1:] {
		factoryText.WriteString(argument.Text())
	}
	text := factoryText.String()
	for _, pattern := range serviceImplementationPatterns {
		matches := pattern.FindStringSubmatch(text)
		if len(matches) == 2 {
			return matches[1]
		}
	}
	return ""
}

func serviceRegistrationKind(
	call *jssyntax.Node,
) (AdminServiceRegistrationKind, bool) {
	name := jsquery.CallName(call)
	switch jsquery.CallMethodName(call) {
	case "addServiceProvider":
		if strings.HasSuffix(name, ".addServiceProvider") {
			return AdminServiceProvider, true
		}
	case "register":
		if strings.HasSuffix(name, "Service().register") {
			return AdminServiceFactory, true
		}
	}
	return "", false
}

func parseStoreRegistration(
	root *jssyntax.Node,
	call *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) (AdminStore, bool) {
	name := jsquery.CallName(call)
	if name != "Shopware.Store.register" && name != "Store.register" {
		return AdminStore{}, false
	}

	var nameNode, options *jssyntax.Node
	first := jsquery.ArgumentExpression(call, 0)
	if first != nil && first.Kind() == jssyntax.JsString {
		nameNode = first
		options = jsquery.ArgumentExpression(call, 1)
	} else if first != nil && first.Kind() == jssyntax.JsObject {
		options = first
		if id := jsquery.Property(first, "id"); id != nil {
			nameNode = jsquery.PropertyValue(id)
		}
	}
	storeName := jsquery.StringValue(nameNode)
	if storeName == "" {
		return AdminStore{}, false
	}
	line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
	store := AdminStore{
		Name: storeName, FilePath: filePath, Line: int(line) + 1,
	}
	if options != nil && options.Kind() == jssyntax.JsObject {
		if state := jsquery.Property(options, "state"); state != nil {
			store.StateType = vueMethodDeclaredReturnType(state)
		}
		store.Members = parseStoreMembers(options, filePath, lineIndex)
	} else if options != nil && options.Kind() == jssyntax.JsIdentifier {
		store.FactoryName = jsquery.IdentifierText(options)
		if importPath := jsquery.ImportPath(root, store.FactoryName); importPath != "" {
			store.FactoryPath = resolveImportPath(filePath, importPath)
		}
	}
	return store, true
}

func parseAdminStoreFactory(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) *AdminStoreFactory {
	for _, export := range jsquery.ExportDefaults(root) {
		function := jsquery.ExportDefaultExpression(export)
		if function == nil || (function.Kind() != jssyntax.JsFunction &&
			function.Kind() != jssyntax.JsArrowFunction) {
			continue
		}
		returned := returnedSetupObject(function)
		if returned == nil {
			continue
		}
		return &AdminStoreFactory{
			FilePath: filePath,
			Members: parseSetupStoreMembers(
				root,
				returned,
				filePath,
				lineIndex,
			),
		}
	}
	return nil
}

func returnedSetupObject(function *jssyntax.Node) *jssyntax.Node {
	var arrowFallback *jssyntax.Node
	for _, object := range jsquery.Nodes(function, jssyntax.JsObject) {
		if closestJavaScriptFunction(object) != function {
			continue
		}
		start := int(object.RangeTrimmedTrivia().Start - function.Range().Start)
		if start < 0 || start > len(function.Text()) {
			continue
		}
		before := strings.TrimSpace(function.Text()[:start])
		if strings.HasSuffix(before, "return") {
			return object
		}
		if function.Kind() == jssyntax.JsArrowFunction && arrowFallback == nil {
			arrowFallback = object
		}
	}
	return arrowFallback
}

func closestJavaScriptFunction(node *jssyntax.Node) *jssyntax.Node {
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsFunction ||
			current.Kind() == jssyntax.JsArrowFunction ||
			current.Kind() == jssyntax.JsMethod {
			return current
		}
	}
	return nil
}

type setupBinding struct {
	kind      AdminStoreMemberKind
	line      int
	valueType string
	object    *jssyntax.Node
}

func parseSetupStoreMembers(
	root,
	returned *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminStoreMember {
	bindings := setupBindings(root, lineIndex)
	var members []AdminStoreMember
	positions := make(map[string]int)
	add := func(member AdminStoreMember) {
		if member.Name == "" {
			return
		}
		if position, found := positions[member.Name]; found {
			members[position] = member
			return
		}
		positions[member.Name] = len(members)
		members = append(members, member)
	}
	for _, property := range jsquery.Properties(returned) {
		name := jsquery.PropertyName(property)
		if name == "" {
			continue
		}
		if strings.Contains(property.Text(), "...") {
			binding, found := bindings[name]
			if !found || binding.object == nil {
				continue
			}
			for _, stateProperty := range jsquery.Properties(binding.object) {
				stateName := jsquery.PropertyName(stateProperty)
				line, _ := lineIndex.Position(
					stateProperty.RangeTrimmedTrivia().Start,
				)
				add(AdminStoreMember{
					Name: stateName, Kind: AdminStoreState,
					Type:     vuePropertyValueType(stateProperty, nil),
					FilePath: filePath, Line: int(line) + 1,
				})
			}
			continue
		}

		publicName := name
		bindingName := name
		if value := jsquery.PropertyValue(property); value != nil &&
			value.Kind() == jssyntax.JsIdentifier {
			bindingName = jsquery.IdentifierText(value)
		}
		binding, found := bindings[bindingName]
		line, _ := lineIndex.Position(property.RangeTrimmedTrivia().Start)
		member := AdminStoreMember{
			Name: publicName, Kind: AdminStoreState,
			FilePath: filePath, Line: int(line) + 1,
		}
		if property.Kind() == jssyntax.JsMethod {
			member.Kind = AdminStoreAction
		}
		if found {
			member.Kind = binding.kind
			member.Line = binding.line
			member.Type = binding.valueType
		}
		add(member)
	}
	return members
}

func setupBindings(
	root *jssyntax.Node,
	lineIndex *cst.LineIndex,
) map[string]setupBinding {
	bindings := make(map[string]setupBinding)
	for _, function := range jsquery.Nodes(root, jssyntax.JsFunction) {
		nameNode := firstDirectIdentifier(function)
		if nameNode == nil {
			continue
		}
		name := jsquery.IdentifierText(nameNode)
		line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
		bindings[name] = setupBinding{
			kind:      AdminStoreAction,
			line:      int(line) + 1,
			valueType: vueMethodSignature(function, nil),
		}
	}
	for _, declaration := range jsquery.Nodes(root, jssyntax.JsVariableDeclaration) {
		nameNode := firstDirectIdentifier(declaration)
		if nameNode == nil {
			continue
		}
		name := jsquery.IdentifierText(nameNode)
		line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
		kind := AdminStoreState
		text := compactJavaScriptText(declaration.Text())
		if strings.Contains(text, "=computed(") ||
			strings.Contains(text, "=computed<") {
			kind = AdminStoreGetter
		} else if strings.Contains(text, "=>") ||
			strings.Contains(text, "=function") {
			kind = AdminStoreAction
		}
		bindings[name] = setupBinding{
			kind: kind, line: int(line) + 1,
			valueType: setupStoreBindingType(declaration, kind),
			object:    firstObject(declaration),
		}
	}
	return bindings
}

func setupStoreBindingType(
	declaration *jssyntax.Node,
	kind AdminStoreMemberKind,
) string {
	if declaration == nil {
		return ""
	}
	text := strings.TrimSpace(declaration.Text())
	equals := strings.IndexByte(text, '=')
	if equals < 0 {
		return ""
	}
	left := strings.TrimSpace(text[:equals])
	if colon := strings.LastIndexByte(left, ':'); colon >= 0 {
		if declared := strings.TrimSpace(left[colon+1:]); declared != "" {
			return declared
		}
	}
	right := trimVueSourceExpression(text[equals+1:])
	for _, wrapper := range []string{"ref", "shallowRef", "computed", "reactive"} {
		prefix := wrapper + "<"
		compact := strings.TrimSpace(right)
		if !strings.HasPrefix(compact, prefix) {
			continue
		}
		open := len(wrapper)
		close := matchingSlotDelimiter(compact, open, '<', '>')
		if close > open {
			return strings.TrimSpace(compact[open+1 : close])
		}
	}
	if kind == AdminStoreAction {
		parameters, returnType, found := vueMethodHeader(right)
		if found {
			if returnType == "" {
				returnType = "unknown"
			}
			return "(" + parameters + ") => " + returnType
		}
	}
	if open := strings.IndexByte(right, '('); open >= 0 {
		name := strings.TrimSpace(right[:open])
		if name == "ref" || name == "shallowRef" || name == "reactive" {
			close := matchingSlotDelimiter(right, open, '(', ')')
			if close > open {
				return vueExpressionTextType(
					strings.TrimSpace(right[open+1:close]), nil,
				)
			}
		}
		if name == "computed" {
			if arrow := strings.Index(right[open+1:], "=>"); arrow >= 0 {
				body := strings.TrimSpace(right[open+1+arrow+2:])
				body = strings.TrimSuffix(body, ")")
				return vueExpressionTextType(body, nil)
			}
		}
	}
	return vueExpressionTextType(right, nil)
}

func firstDirectIdentifier(node *jssyntax.Node) *jssyntax.Node {
	if node == nil {
		return nil
	}
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		if child := cursor.Node(); child.Kind() == jssyntax.JsIdentifier {
			// Type annotations wrap the declared identifier together with its
			// type (`state: ContextState`). Follow the leading identifier node
			// so the binding remains `state`, not the annotation name.
			for {
				var nested *jssyntax.Node
				nestedCursor := child.ChildNodeCursor()
				for nestedCursor.Next() {
					candidate := nestedCursor.Node()
					if candidate.Kind() == jssyntax.JsIdentifier {
						nested = candidate
						break
					}
				}
				if nested == nil {
					return child
				}
				child = nested
			}
		}
	}
	return nil
}

func compactJavaScriptText(value string) string {
	return strings.Join(strings.Fields(value), "")
}

func mergeStoreMembers(
	base,
	overlay []AdminStoreMember,
) []AdminStoreMember {
	result := append([]AdminStoreMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index, member := range result {
		positions[member.Name] = index
	}
	for _, member := range overlay {
		if index, found := positions[member.Name]; found {
			result[index] = member
		} else {
			positions[member.Name] = len(result)
			result = append(result, member)
		}
	}
	return result
}

func parseStoreMembers(
	options *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminStoreMember {
	sections := []struct {
		name string
		kind AdminStoreMemberKind
	}{
		{"state", AdminStoreState},
		{"getters", AdminStoreGetter},
		{"actions", AdminStoreAction},
	}
	var members []AdminStoreMember
	seen := make(map[string]bool)
	known := make(map[string]string)
	for _, section := range sections {
		property := jsquery.Property(options, section.name)
		object := firstObject(property)
		for _, memberProperty := range jsquery.Properties(object) {
			name := jsquery.PropertyName(memberProperty)
			key := string(section.kind) + "\x00" + name
			if name == "" || seen[key] {
				continue
			}
			seen[key] = true
			memberType := ""
			switch section.kind {
			case AdminStoreState:
				memberType = vuePropertyValueType(memberProperty, known)
			case AdminStoreGetter:
				memberType = vueMethodReturnType(memberProperty, known)
			case AdminStoreAction:
				memberType = vueMethodSignature(memberProperty, known)
			}
			if memberType != "" {
				known[name] = memberType
			}
			line, _ := lineIndex.Position(
				memberProperty.RangeTrimmedTrivia().Start,
			)
			members = append(members, AdminStoreMember{
				Name: name, Kind: section.kind, Type: memberType,
				OpenRuntimeShape: section.kind == AdminStoreState &&
					vuePropertyHasInferredObjectShape(memberProperty),
				FilePath: filePath,
				Line:     int(line) + 1,
			})
		}
	}
	return members
}

func preferredServices(values []AdminService) []AdminService {
	return preferRuntimeDefinitions(values, func(value AdminService) string {
		return value.FilePath
	})
}

func preferredStores(values []AdminStore) []AdminStore {
	return preferRuntimeDefinitions(values, func(value AdminStore) string {
		return value.FilePath
	})
}

func preferRuntimeDefinitions[T any](values []T, filePath func(T) string) []T {
	if len(values) < 2 {
		return values
	}
	hasProduction := false
	for _, value := range values {
		if !isAdminTestPath(filePath(value)) {
			hasProduction = true
			break
		}
	}
	result := make([]T, 0, len(values))
	for _, value := range values {
		if hasProduction && isAdminTestPath(filePath(value)) {
			continue
		}
		result = append(result, value)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return filePath(result[left]) < filePath(result[right])
	})
	return result
}

func isAdminTestPath(filePath string) bool {
	base := strings.ToLower(filePath)
	return strings.Contains(base, ".spec.") || strings.Contains(base, ".test.") ||
		strings.Contains(base, "/__tests__/")
}
