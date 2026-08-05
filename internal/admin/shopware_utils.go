package admin

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

// JavaScriptShopwareUtilsMember reports a statically named member rooted at
// Shopware.Utils. receiver contains the path between Utils and the selected
// member, so Shopware.Utils.format.date yields receiver=["format"] and
// member="date".
func JavaScriptShopwareUtilsMember(
	node *jssyntax.Node,
) (receiver []string, memberName string, matched bool) {
	return javaScriptShopwareUtilsMember(node, nil)
}

func javaScriptShopwareUtilsMember(
	node *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) (receiver []string, memberName string, matched bool) {
	if receiver, memberName, matched = javaScriptStaticMember(
		node, "Shopware.Utils",
	); matched {
		return receiver, memberName, true
	}
	member := jsquery.MemberExpressionAt(node)
	if member != nil {
		return javaScriptShopwareUtilsAliasMember(node, member, analysis)
	}
	if node == nil || node.Kind() != jssyntax.JsIdentifier {
		return nil, "", false
	}
	path, found := visibleJavaScriptShopwareUtilsBinding(
		node, jsquery.IdentifierText(node), javaScriptRoot(node), analysis,
	)
	if !found || len(path) == 0 {
		return nil, "", false
	}
	return path[:len(path)-1], path[len(path)-1], true
}

func JavaScriptShopwareUtilsMemberNameNode(
	node *jssyntax.Node,
) *jssyntax.Node {
	return javaScriptShopwareUtilsMemberNameNode(node, nil)
}

func javaScriptShopwareUtilsMemberNameNode(
	node *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) *jssyntax.Node {
	receiver, name, matched := javaScriptShopwareUtilsMember(node, analysis)
	if !matched || name == "" {
		return nil
	}
	_ = receiver
	member := jsquery.MemberExpressionAt(node)
	if member == nil {
		if node != nil && node.Kind() == jssyntax.JsIdentifier {
			return node
		}
		return nil
	}
	var result *jssyntax.Node
	cursor := member.ChildNodeCursor()
	if !cursor.Next() {
		return nil
	}
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == jssyntax.JsIdentifier &&
			strings.TrimSpace(javaScriptTrimmedNodeText(child)) == name {
			result = child
		}
	}
	return result
}

// JavaScriptShopwareEventBusEventAt recognizes the event-name argument of the
// public EventBus on/off/emit methods. The same lexical alias rules as ordinary
// Shopware.Utils member intelligence apply, including destructured EventBus
// bindings and direct aliases of Shopware.Utils.EventBus.
func JavaScriptShopwareEventBusEventAt(
	node *jssyntax.Node,
) (operation, eventName string, matched bool) {
	return javaScriptShopwareEventBusEventAt(node, nil)
}

func javaScriptShopwareEventBusEventAt(
	node *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) (operation, eventName string, matched bool) {
	literal := jsquery.StringAt(node)
	if literal == nil || jsquery.StringArgumentIndex(literal) != 0 {
		return "", "", false
	}
	argument := jsquery.Argument(literal, 0)
	expression, single := singleJavaScriptExpression(argument)
	for single && expression != nil &&
		expression.Kind() == jssyntax.JsParenthesized {
		expression, single = singleJavaScriptExpression(expression)
	}
	if !single || expression != literal {
		return "", "", false
	}
	callee := jsquery.CallCallee(literal)
	receiver, memberName, found := javaScriptShopwareUtilsMember(callee, analysis)
	if !found || len(receiver) != 1 || receiver[0] != "EventBus" {
		return "", "", false
	}
	switch memberName {
	case "on", "off", "emit":
		eventName := jsquery.StringValue(literal)
		if strings.Contains(eventName, "${") {
			return "", "", false
		}
		return memberName, eventName, true
	default:
		return "", "", false
	}
}

func singleJavaScriptExpression(node *jssyntax.Node) (*jssyntax.Node, bool) {
	if node == nil {
		return nil, false
	}
	var expression *jssyntax.Node
	for child := range node.ChildNodes() {
		if expression != nil {
			return nil, false
		}
		expression = child
	}
	return expression, expression != nil
}

func javaScriptShopwareUtilsAliasMember(
	node,
	member *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) ([]string, string, bool) {
	cursor := member.ChildNodeCursor()
	if !cursor.Next() {
		return nil, "", false
	}
	directReceiver := cursor.Node()
	if node != member && javaScriptNodeWithin(node, directReceiver) {
		return nil, "", false
	}
	alias := leadingJavaScriptIdentifier(javaScriptTrimmedNodeText(member))
	if alias == "" {
		return nil, "", false
	}
	basePath, found := visibleJavaScriptShopwareUtilsBinding(
		member, alias, javaScriptRoot(member), analysis,
	)
	if !found {
		return nil, "", false
	}
	segments, incomplete, parsed := javaScriptStaticExpression(
		javaScriptTrimmedNodeText(member), alias,
	)
	if !parsed || len(segments) == 0 && !incomplete {
		return nil, "", false
	}
	names := staticExpressionSegmentNames(segments)
	if len(names) != len(segments) {
		return nil, "", false
	}
	path := append(append([]string(nil), basePath...), names...)
	if incomplete {
		return path, "", true
	}
	if len(path) == 0 {
		return nil, "", false
	}
	return path[:len(path)-1], path[len(path)-1], true
}

type javaScriptVariableBinding struct {
	localName   string
	sourceName  string
	initializer string
	constant    bool
}

func visibleJavaScriptShopwareUtilsBinding(
	use *jssyntax.Node,
	identifier string,
	root *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) ([]string, bool) {
	if use == nil || root == nil || !isStaticVueIdentifier(identifier) {
		return nil, false
	}
	useStart := use.RangeTrimmedTrivia().Start
	bestDistance := int(^uint(0) >> 1)
	bestStart := uint32(0)
	var best javaScriptVariableBinding
	found := false
	var declarations []*jssyntax.Node
	if analysis != nil {
		declarations = analysis.Nodes(jssyntax.JsVariableDeclaration)
	} else {
		declarations = jsquery.Nodes(root, jssyntax.JsVariableDeclaration)
	}
	for _, declaration := range declarations {
		if javaScriptNodeWithin(use, declaration) ||
			declaration.RangeTrimmedTrivia().Start >= useStart {
			continue
		}
		var binding javaScriptVariableBinding
		var binds bool
		if analysis != nil {
			binding, binds = analysis.variableBinding(declaration, identifier)
		} else {
			binding, binds = javaScriptVariableBindingNamed(
				declaration.Text(), identifier,
			)
		}
		if !binds {
			continue
		}
		distance, visible := javaScriptBindingDistance(use, declaration)
		if !visible {
			continue
		}
		start := declaration.RangeTrimmedTrivia().Start
		if !found || distance < bestDistance ||
			distance == bestDistance && start > bestStart {
			best = binding
			bestDistance = distance
			bestStart = start
			found = true
		}
	}
	if !found || !best.constant ||
		javaScriptParameterShadowsBinding(use, identifier, bestDistance) {
		return nil, false
	}
	segments, incomplete, matched := javaScriptStaticExpression(
		best.initializer, "Shopware.Utils",
	)
	if !matched || incomplete {
		return nil, false
	}
	path := staticExpressionSegmentNames(segments)
	if len(path) != len(segments) {
		return nil, false
	}
	if best.sourceName != "" {
		path = append(path, best.sourceName)
	}
	return path, true
}

func javaScriptVariableBindingNamed(
	value,
	identifier string,
) (javaScriptVariableBinding, bool) {
	for _, binding := range javaScriptVariableBindings(value) {
		if binding.localName == identifier {
			return binding, true
		}
	}
	return javaScriptVariableBinding{}, false
}

func javaScriptVariableBindings(value string) []javaScriptVariableBinding {
	value = strings.TrimSpace(value)
	keyword := ""
	for _, candidate := range []string{"const", "let", "var"} {
		if strings.HasPrefix(value, candidate) && len(value) > len(candidate) &&
			isJavaScriptSpace(value[len(candidate)]) {
			keyword = candidate
			value = strings.TrimSpace(value[len(candidate):])
			break
		}
	}
	if keyword == "" {
		return nil
	}
	equals := indexSlotTopLevel(value, '=')
	if equals < 0 {
		return nil
	}
	left := strings.TrimSpace(value[:equals])
	right := trimVueSourceExpression(value[equals+1:])
	if right == "" || indexSlotTopLevel(right, ',') >= 0 {
		return nil
	}
	constant := keyword == "const"
	if isStaticVueIdentifier(left) {
		return []javaScriptVariableBinding{{
			localName: left, initializer: right, constant: constant,
		}}
	}
	if len(left) < 2 || left[0] != '{' ||
		matchingSlotDelimiter(left, 0, '{', '}') != len(left)-1 {
		return nil
	}
	var bindings []javaScriptVariableBinding
	for _, raw := range splitSlotTopLevel(left[1:len(left)-1], ',') {
		entry := strings.TrimSpace(raw)
		if entry == "" || strings.HasPrefix(entry, "...") {
			continue
		}
		colon := indexSlotTopLevel(entry, ':')
		sourceName := strings.TrimSpace(entry)
		localName := sourceName
		if colon >= 0 {
			sourceName = strings.TrimSpace(entry[:colon])
			localName = strings.TrimSpace(entry[colon+1:])
		}
		if defaultAt := indexSlotTopLevel(localName, '='); defaultAt >= 0 {
			localName = strings.TrimSpace(localName[:defaultAt])
		}
		if !isStaticVueIdentifier(sourceName) ||
			!isStaticVueIdentifier(localName) {
			continue
		}
		bindings = append(bindings, javaScriptVariableBinding{
			localName: localName, sourceName: sourceName,
			initializer: right, constant: constant,
		})
	}
	return bindings
}

func javaScriptBindingDistance(
	use,
	declaration *jssyntax.Node,
) (int, bool) {
	declarationFunction := closestJavaScriptFunctionScope(declaration)
	if declarationFunction != nil &&
		!javaScriptNodeWithin(use, declarationFunction) {
		return 0, false
	}
	declarationBlock := closestJavaScriptBlockScope(
		declaration, declarationFunction,
	)
	if declarationBlock != nil && !javaScriptNodeWithin(use, declarationBlock) {
		return 0, false
	}
	scope := declarationBlock
	if scope == nil {
		scope = declarationFunction
	}
	if scope == nil {
		scope = javaScriptRoot(declaration)
	}
	distance := 0
	for current := use; current != nil; current = current.Parent() {
		if current == scope {
			return distance, true
		}
		distance++
	}
	return 0, false
}

func javaScriptParameterShadowsBinding(
	use *jssyntax.Node,
	identifier string,
	bindingDistance int,
) bool {
	distance := 0
	for current := use; current != nil && distance < bindingDistance; current = current.Parent() {
		switch current.Kind() {
		case jssyntax.JsMethod, jssyntax.JsFunction, jssyntax.JsArrowFunction:
			if javaScriptFunctionBindsParameter(current, identifier) {
				return true
			}
		}
		distance++
	}
	return false
}

func javaScriptFunctionBindsParameter(
	function *jssyntax.Node,
	identifier string,
) bool {
	if function == nil || !isStaticVueIdentifier(identifier) {
		return false
	}
	text := strings.TrimSpace(function.Text())
	parameters := ""
	if function.Kind() == jssyntax.JsArrowFunction {
		arrow := strings.Index(text, "=>")
		if arrow < 0 {
			return false
		}
		parameters = strings.TrimSpace(text[:arrow])
		parameters = strings.TrimSpace(strings.TrimPrefix(parameters, "async "))
		if strings.HasPrefix(parameters, "(") &&
			matchingSlotDelimiter(parameters, 0, '(', ')') == len(parameters)-1 {
			parameters = parameters[1 : len(parameters)-1]
		}
	} else if parsed, _, found := vueMethodHeader(text); found {
		parameters = parsed
	} else {
		return false
	}
	for _, parameter := range splitSlotTopLevel(parameters, ',') {
		parameter = strings.TrimSpace(parameter)
		parameter = strings.TrimSpace(strings.TrimPrefix(parameter, "..."))
		if isStaticVueIdentifier(parameter) && parameter == identifier {
			return true
		}
		if len(parameter) >= 2 && parameter[0] == '{' {
			close := matchingSlotDelimiter(parameter, 0, '{', '}')
			if close > 0 {
				for _, entry := range splitSlotTopLevel(parameter[1:close], ',') {
					entry = strings.TrimSpace(entry)
					if colon := indexSlotTopLevel(entry, ':'); colon >= 0 {
						entry = strings.TrimSpace(entry[colon+1:])
					}
					if defaultAt := indexSlotTopLevel(entry, '='); defaultAt >= 0 {
						entry = strings.TrimSpace(entry[:defaultAt])
					}
					if entry == identifier {
						return true
					}
				}
			}
		}
	}
	return false
}

func leadingJavaScriptIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !isVueIdentifierStart(value[0]) {
		return ""
	}
	cursor := 1
	for cursor < len(value) && isVueIdentifierPart(value[cursor]) {
		cursor++
	}
	return value[:cursor]
}

func javaScriptRoot(node *jssyntax.Node) *jssyntax.Node {
	if node == nil {
		return nil
	}
	for node.Parent() != nil {
		node = node.Parent()
	}
	return node
}

// ResolveShopwareUtils resolves one receiver against the indexed default
// export of core/service/util.service. Local declarations, relative/default
// imports, and callable aliases are followed lazily so source edits update the
// public utility contract without a duplicated catalog in the LSP.
func (idx *AdminComponentIndexer) ResolveShopwareUtils(
	receiver,
	contextPath string,
) (VueTypeShape, error) {
	segments, matched := staticExpressionPath(receiver)
	if !matched {
		return VueTypeShape{}, nil
	}
	names := staticExpressionSegmentNames(segments)
	if len(names) != len(segments) {
		return VueTypeShape{}, nil
	}
	liveFiles := idx.liveTypeFileOverlays(nil)
	declaration, declarationContext, found, err :=
		idx.resolveAdminTypeDeclaration(
			contextPath, shopwareUtilsType, liveFiles,
		)
	if err != nil || !found {
		return VueTypeShape{Type: shopwareUtilsType}, err
	}
	shape, currentContext, _, err := idx.resolveShopwareUtilityDeclaration(
		declaration, declarationContext, make(map[string]bool), liveFiles,
	)
	if err != nil {
		return shape, err
	}
	for _, name := range names {
		shape, err = idx.materializeShopwareUtilityShape(
			shape, currentContext, liveFiles,
		)
		if err != nil {
			return shape, err
		}
		member, memberFound := twigVueMemberNamed(shape.Members, name)
		if !memberFound {
			return VueTypeShape{}, nil
		}
		if len(member.NestedMembers) > 0 || member.NestedComplete {
			shape = VueTypeShape{
				Type: member.Type, Members: member.NestedMembers,
				Complete: member.NestedComplete,
			}
			continue
		}
		var resolved bool
		shape, currentContext, resolved, err = idx.resolveShopwareUtilityValue(
			member.Type, currentContext, make(map[string]bool), liveFiles,
		)
		if err != nil {
			return shape, err
		}
		if !resolved {
			return VueTypeShape{}, nil
		}
	}
	return idx.materializeShopwareUtilityShape(
		shape, currentContext, liveFiles,
	)
}

// ResolveShopwareEventBusEvents returns the explicitly declared Events map
// behind Shopware.Utils.EventBus. The contract remains open when Events extends
// Record, so callers may offer the known names without diagnosing extension or
// legacy event names that are not part of the core declaration.
func (idx *AdminComponentIndexer) ResolveShopwareEventBusEvents(
	contextPath string,
) (VueTypeShape, error) {
	if idx == nil || idx.typeIndex == nil {
		return VueTypeShape{}, nil
	}
	liveFiles := idx.liveTypeFileOverlays(nil)
	declaration, declarationContext, found, err :=
		idx.resolveAdminTypeDeclaration(
			contextPath, shopwareUtilsType, liveFiles,
		)
	if err != nil || !found {
		return VueTypeShape{}, err
	}
	rootShape, rootContext, resolved, err :=
		idx.resolveShopwareUtilityDeclaration(
			declaration, declarationContext, make(map[string]bool), liveFiles,
		)
	if err != nil || !resolved {
		return VueTypeShape{}, err
	}
	eventBus, found := twigVueMemberNamed(rootShape.Members, "EventBus")
	if !found || eventBus.Type == "" {
		return VueTypeShape{}, nil
	}
	eventBusShape, eventBusContext, resolved, err :=
		idx.resolveShopwareUtilityValue(
			eventBus.Type, rootContext, make(map[string]bool), liveFiles,
		)
	if err != nil || !resolved {
		return VueTypeShape{}, err
	}
	typeName, arguments := parseAdminNamedType(eventBusShape.Type)
	if typeName != "Emitter" || len(arguments) != 1 {
		return VueTypeShape{}, nil
	}
	return idx.resolveVueType(
		arguments[0], eventBusContext, make(map[string]bool), nil, liveFiles,
	)
}

func (idx *AdminComponentIndexer) ResolveShopwareEventBusEvent(
	name,
	contextPath string,
) (TwigVueMember, bool, error) {
	shape, err := idx.ResolveShopwareEventBusEvents(contextPath)
	if err != nil {
		return TwigVueMember{}, false, err
	}
	member, found := twigVueMemberNamed(shape.Members, name)
	return member, found, nil
}

// shopwareEventBusEventAtDefinitionRange recognizes the typed key that owns a
// Shopware EventBus event. A same-named property in an unrelated interface is
// rejected by comparing its exact resolved declaration source and range.
func (idx *AdminComponentIndexer) shopwareEventBusEventAtDefinitionRange(
	filePath string,
	rangeValue cst.TextRange,
	lineIndex *cst.LineIndex,
) (TwigVueMember, bool, error) {
	if idx == nil || filePath == "" || lineIndex == nil ||
		rangeValue.End <= rangeValue.Start {
		return TwigVueMember{}, false, nil
	}
	liveFiles := idx.liveTypeFileOverlays(nil)
	typeFile, found, err := idx.adminTypeFileForResolution(filePath, liveFiles)
	if err != nil || !found {
		return TwigVueMember{}, false, err
	}
	for _, declaration := range typeFile.Declarations {
		for _, candidate := range declaration.Members {
			if !adminSourceRangeOverlapsTextRange(
				candidate.DefinitionRange, rangeValue, lineIndex,
			) {
				continue
			}
			event, eventFound, eventErr := idx.ResolveShopwareEventBusEvent(
				candidate.Name, filePath,
			)
			if eventErr != nil {
				return TwigVueMember{}, false, eventErr
			}
			if !eventFound || normalizeDefinitionPath(event.DefinitionPath) !=
				normalizeDefinitionPath(filePath) ||
				event.DefinitionRange != candidate.DefinitionRange {
				continue
			}
			return event, true, nil
		}
	}
	return TwigVueMember{}, false, nil
}

func adminSourceRangeOverlapsTextRange(
	sourceRange AdminSourceRange,
	textRange cst.TextRange,
	lineIndex *cst.LineIndex,
) bool {
	if lineIndex == nil || textRange.End <= textRange.Start {
		return false
	}
	start := lineIndex.OffsetUTF16(
		uint32(sourceRange.StartLine), uint32(sourceRange.StartCharacter),
	)
	end := lineIndex.OffsetUTF16(
		uint32(sourceRange.EndLine), uint32(sourceRange.EndCharacter),
	)
	return start < textRange.End && end > textRange.Start
}

func (idx *AdminComponentIndexer) materializeShopwareUtilityShape(
	shape VueTypeShape,
	contextPath string,
	liveFiles map[string]AdminTypeFile,
) (VueTypeShape, error) {
	result := shape
	result.Members = append([]TwigVueMember(nil), shape.Members...)
	for memberIndex := range result.Members {
		member := &result.Members[memberIndex]
		if member.Type == "" || len(member.NestedMembers) > 0 ||
			member.NestedComplete {
			continue
		}
		resolved, _, found, err := idx.resolveShopwareUtilityValue(
			member.Type, contextPath, make(map[string]bool), liveFiles,
		)
		if err != nil {
			return result, err
		}
		if found && resolved.Type != "" {
			member.Type = resolved.Type
		}
	}
	return result, nil
}

func (idx *AdminComponentIndexer) resolveShopwareUtilityValue(
	value,
	contextPath string,
	seen map[string]bool,
	liveFiles map[string]AdminTypeFile,
) (VueTypeShape, string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return VueTypeShape{}, contextPath, false, nil
	}
	if _, _, callable := VueCallableSignature(value); callable {
		return VueTypeShape{Type: value}, contextPath, true, nil
	}
	namedType, typeArguments := parseAdminNamedType(value)
	if namedType != "" && len(typeArguments) > 0 {
		if builtin, matched := adminJavaScriptBuiltinShape(
			value, namedType, typeArguments,
		); matched {
			return builtin, contextPath, true, nil
		}
	}
	parts := strings.Split(value, ".")
	if len(parts) == 0 || !isAdminTypeIdentifier(parts[0]) {
		return VueTypeShape{Type: value}, contextPath, true, nil
	}
	key := filepath.Clean(contextPath) + "\x00utility\x00" + value
	if seen[key] {
		return VueTypeShape{Type: value}, contextPath, false, nil
	}
	seen[key] = true
	defer delete(seen, key)
	declaration, declarationContext, found, err :=
		idx.resolveAdminTypeDeclaration(contextPath, parts[0], liveFiles)
	if err != nil {
		return VueTypeShape{}, contextPath, false, err
	}
	if !found {
		if namedType != "" {
			if builtin, matched := adminJavaScriptBuiltinShape(
				value, namedType, typeArguments,
			); matched {
				return builtin, contextPath, true, nil
			}
		}
		if idx.shopwareUtilityImportedValue(
			contextPath, parts[0], liveFiles,
		) {
			return VueTypeShape{Type: "Function"}, contextPath, true, nil
		}
		return VueTypeShape{Type: value}, contextPath, false, nil
	}
	shape, resolvedContext, resolved, err :=
		idx.resolveShopwareUtilityDeclaration(
			declaration, declarationContext, seen, liveFiles,
		)
	if err != nil || !resolved {
		return shape, resolvedContext, resolved, err
	}
	for _, part := range parts[1:] {
		shape, err = idx.materializeShopwareUtilityShape(
			shape, resolvedContext, liveFiles,
		)
		if err != nil {
			return shape, resolvedContext, false, err
		}
		member, memberFound := twigVueMemberNamed(shape.Members, part)
		if !memberFound {
			return VueTypeShape{}, resolvedContext, false, nil
		}
		if len(member.NestedMembers) > 0 || member.NestedComplete {
			shape = VueTypeShape{
				Type: member.Type, Members: member.NestedMembers,
				Complete: member.NestedComplete,
			}
			continue
		}
		shape, resolvedContext, resolved, err =
			idx.resolveShopwareUtilityValue(
				member.Type, resolvedContext, seen, liveFiles,
			)
		if err != nil || !resolved {
			return shape, resolvedContext, resolved, err
		}
	}
	return shape, resolvedContext, true, nil
}

func (idx *AdminComponentIndexer) resolveShopwareUtilityDeclaration(
	declaration AdminTypeDeclaration,
	contextPath string,
	seen map[string]bool,
	liveFiles map[string]AdminTypeFile,
) (VueTypeShape, string, bool, error) {
	if declaration.Alias != "" {
		return idx.resolveShopwareUtilityValue(
			declaration.Alias, contextPath, seen, liveFiles,
		)
	}
	return VueTypeShape{
		Type: declaration.Name, Members: declaration.Members,
		Complete: declaration.Interface && !declaration.Open,
	}, contextPath, true, nil
}

func (idx *AdminComponentIndexer) shopwareUtilityImportedValue(
	contextPath,
	name string,
	liveFiles map[string]AdminTypeFile,
) bool {
	typeFile, found, err := idx.adminTypeFileForResolution(
		contextPath, liveFiles,
	)
	if err != nil || !found {
		return false
	}
	for _, typeImport := range typeFile.Imports {
		if typeImport.LocalName == name {
			return true
		}
	}
	return false
}

func (idx *AdminComponentIndexer) resolveShopwareUtilsExpressionType(
	expression,
	contextPath string,
) (resolvedVueExpressionType, bool, error) {
	segments, incomplete, matched := javaScriptStaticExpression(
		expression, "Shopware.Utils",
	)
	if !matched || incomplete || len(segments) == 0 {
		return resolvedVueExpressionType{}, false, nil
	}
	for _, segment := range segments[:len(segments)-1] {
		if segment.Called || segment.Indexed || segment.Name == "" {
			return resolvedVueExpressionType{}, false, nil
		}
	}
	last := segments[len(segments)-1]
	if last.Indexed || last.Name == "" {
		return resolvedVueExpressionType{}, false, nil
	}
	receiver := make([]string, 0, len(segments)-1)
	for _, segment := range segments[:len(segments)-1] {
		receiver = append(receiver, segment.Name)
	}
	shape, err := idx.ResolveShopwareUtils(
		strings.Join(receiver, "."), contextPath,
	)
	if err != nil {
		return resolvedVueExpressionType{}, false, err
	}
	member, found := twigVueMemberNamed(shape.Members, last.Name)
	if !found || member.Type == "" {
		return resolvedVueExpressionType{}, false, nil
	}
	memberType := member.Type
	if last.Called {
		memberType = VueCallableReturnType(memberType)
		if memberType == "" {
			return resolvedVueExpressionType{}, false, nil
		}
	}
	return resolvedVueExpressionType{
		Type: memberType, ContextPath: member.DefinitionPath,
	}, true, nil
}
