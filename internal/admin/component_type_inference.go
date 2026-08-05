package admin

import (
	"regexp"
	"strconv"
	"strings"

	jsparser "github.com/shopware/shopware-lsp/internal/parser/javascript/parser"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

var vueRepositoryCreateExpressionPattern = regexp.MustCompile(
	`^this\.repositoryFactory\.create(?:\?\.)?\(['"]([^'"]+)['"]`,
)

type vueStaticExpressionSegment struct {
	Name            string
	Called          bool
	Optional        bool
	Indexed         bool
	Arguments       string
	IndexExpression string
}

type resolvedVueExpressionType struct {
	Type             string
	ContextPath      string
	OpenRuntimeShape bool
}

// enrichEffectiveComponentMemberTypes resolves legacy JavaScript return
// expressions after inheritance and mixins have been composed. A small fixed
// point is enough for computed-to-computed forwarding while cycles simply
// remain unknown.
func (idx *AdminComponentIndexer) enrichEffectiveComponentMemberTypes(
	component *VueComponent,
) error {
	if idx == nil || component == nil || len(component.Members) == 0 {
		return nil
	}
	for pass := 0; pass < len(component.Members); pass++ {
		changed := false
		for memberIndex := range component.Members {
			member := &component.Members[memberIndex]
			if member.SourceExpression == "" ||
				!componentMemberNeedsInferredType(*member) {
				continue
			}
			resolved, found, err := idx.resolveComponentExpressionType(
				*component,
				member.SourceExpression,
				componentMemberTypeContext(*member, *component),
			)
			if err != nil {
				return err
			}
			if !found || resolved.Type == "" {
				continue
			}
			inferredType := resolved.Type
			if member.Kind == ComponentMemberMethod {
				inferredType = vueCallableWithReturnType(member.Type, resolved.Type)
			}
			contextChanged := resolved.ContextPath != "" &&
				member.TypeContextPath != resolved.ContextPath
			if member.Type == inferredType && !contextChanged {
				continue
			}
			member.Type = inferredType
			member.OpenRuntimeShape = member.OpenRuntimeShape ||
				resolved.OpenRuntimeShape
			if resolved.ContextPath != "" {
				member.TypeContextPath = resolved.ContextPath
			}
			changed = true
		}
		flowChanged, err := idx.enrichEffectiveComponentAssignmentTypes(component)
		if err != nil {
			return err
		}
		changed = changed || flowChanged
		if !changed {
			break
		}
	}
	return nil
}

func (idx *AdminComponentIndexer) enrichEffectiveComponentAssignmentTypes(
	component *VueComponent,
) (bool, error) {
	if component == nil || len(component.Assignments) == 0 {
		return false, nil
	}
	assignments := make(map[string][]VueComponentAssignment)
	for _, assignment := range component.Assignments {
		if assignment.Target != "" && assignment.Expression != "" {
			assignments[assignment.Target] = append(
				assignments[assignment.Target], assignment,
			)
		}
	}
	changed := false
	for memberIndex := range component.Members {
		member := &component.Members[memberIndex]
		writes := assignments[member.Name]
		if member.Kind != ComponentMemberData || len(writes) == 0 {
			continue
		}

		initialType := ""
		if member.SourceExpression != "" {
			resolved, found, err := idx.resolveComponentExpressionType(
				*component,
				member.SourceExpression,
				componentMemberTypeContext(*member, *component),
			)
			if err != nil {
				return changed, err
			}
			if found {
				initialType = resolved.Type
			}
		}
		if initialType == "" && isWeakVueFlowSeed(member.Type) {
			// The CST-based initializer understands literal null/undefined more
			// precisely than the late text resolver. Preserve that indexed seed
			// so a later concrete write widens rather than erases nullability.
			initialType = member.Type
		}
		// A concrete annotation or concrete initializer remains authoritative.
		// Flow evidence only refines weak Options API seeds such as [] and null.
		if member.Type != "" && !isWeakVueFlowSeed(initialType) &&
			!isWeakVueFlowSeed(member.Type) {
			continue
		}

		assignmentTypes := make([]string, 0, len(writes))
		contextPath := ""
		openRuntimeShape := false
		for _, assignment := range writes {
			assignmentContext := assignment.FilePath
			if assignmentContext == "" {
				assignmentContext = componentMemberTypeContext(*member, *component)
			}
			resolved, found, err := idx.resolveComponentExpressionType(
				*component, assignment.Expression, assignmentContext,
			)
			if err != nil {
				return changed, err
			}
			if !found || resolved.Type == "" {
				continue
			}
			assignmentTypes = append(assignmentTypes, resolved.Type)
			openRuntimeShape = openRuntimeShape ||
				vueRuntimeObjectLiteralExpression(assignment.Expression)
			if contextPath == "" {
				contextPath = resolved.ContextPath
			}
		}
		flowType := mergeVueAssignmentTypes(initialType, assignmentTypes)
		if openRuntimeShape && !member.OpenRuntimeShape {
			member.OpenRuntimeShape = true
			changed = true
		}
		if flowType == "" || flowType == member.Type {
			continue
		}
		member.Type = flowType
		if contextPath != "" {
			member.TypeContextPath = contextPath
		}
		changed = true
	}
	return changed, nil
}

func isWeakVueFlowSeed(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	for _, entry := range splitAdminTypeTopLevel(value, '|') {
		switch strings.TrimSpace(entry) {
		case "", "any", "unknown", "null", "undefined", "Array", "Object",
			"Array<any>", "Array<unknown>",
			"ReadonlyArray<any>", "ReadonlyArray<unknown>":
		default:
			return false
		}
	}
	return true
}

func mergeVueAssignmentTypes(initialType string, assignments []string) string {
	var alternatives []string
	seen := make(map[string]bool)
	informative := false
	appendType := func(value string) {
		for _, entry := range splitAdminTypeTopLevel(value, '|') {
			entry = strings.TrimSpace(entry)
			if entry == "" || seen[entry] {
				continue
			}
			seen[entry] = true
			alternatives = append(alternatives, entry)
			switch entry {
			case "any", "unknown", "null", "undefined", "Array", "Object",
				"Array<any>", "Array<unknown>",
				"ReadonlyArray<any>", "ReadonlyArray<unknown>":
			default:
				informative = true
			}
		}
	}
	for _, assignment := range assignments {
		appendType(assignment)
	}
	appendType(initialType)
	if !informative {
		return ""
	}
	filtered := alternatives[:0]
	for _, alternative := range alternatives {
		switch alternative {
		case "any", "unknown", "Array", "Object",
			"Array<any>", "Array<unknown>",
			"ReadonlyArray<any>", "ReadonlyArray<unknown>":
			continue
		default:
			filtered = append(filtered, alternative)
		}
	}
	return strings.Join(filtered, " | ")
}

func componentMemberNeedsInferredType(member VueComponentMember) bool {
	if member.Kind != ComponentMemberMethod {
		return strings.TrimSpace(member.Type) == "" ||
			member.SourceExpression != "" && isWeakVueFlowSeed(member.Type)
	}
	return strings.TrimSpace(member.Type) == "" ||
		strings.TrimSpace(VueCallableReturnType(member.Type)) == "unknown"
}

func componentMemberTypeContext(
	member VueComponentMember,
	component VueComponent,
) string {
	for _, candidate := range []string{
		member.TypeContextPath,
		member.FilePath,
		component.DefinitionPath,
		component.FilePath,
	} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func vueCallableWithReturnType(signature, returnType string) string {
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return "() => " + returnType
	}
	if signature[0] != '(' {
		return signature
	}
	close := matchingSlotDelimiter(signature, 0, '(', ')')
	if close < 0 {
		return signature
	}
	return strings.TrimSpace(signature[:close+1]) + " => " + returnType
}

func (idx *AdminComponentIndexer) resolveComponentExpressionType(
	component VueComponent,
	expression,
	contextPath string,
) (resolvedVueExpressionType, bool, error) {
	expression = trimVueSourceExpression(expression)
	if expression == "" {
		return resolvedVueExpressionType{}, false, nil
	}
	if left, right, split := splitVueTopLevelOperator(expression, "??"); split {
		leftResolved, leftFound, err := idx.resolveComponentExpressionType(
			component, left, contextPath,
		)
		if err != nil {
			return resolvedVueExpressionType{}, false, err
		}
		rightResolved, rightFound, err := idx.resolveComponentExpressionType(
			component, right, contextPath,
		)
		if err != nil {
			return resolvedVueExpressionType{}, false, err
		}
		result := leftResolved
		result.Type = mergeVueNullishTypes(
			leftResolved.Type, rightResolved.Type,
		)
		if result.ContextPath == "" {
			result.ContextPath = rightResolved.ContextPath
		}
		return result, (leftFound || rightFound) && result.Type != "", nil
	}
	if asserted := vueTypeAssertion(expression); asserted != "" {
		return resolvedVueExpressionType{
			Type: asserted, ContextPath: contextPath,
		}, true, nil
	}
	awaited := false
	for strings.HasPrefix(expression, "await ") {
		awaited = true
		expression = strings.TrimSpace(strings.TrimPrefix(expression, "await "))
	}
	if resolved, found, err := idx.resolveVueObjectTransformExpressionType(
		expression,
		contextPath,
		func(argument string) (resolvedVueExpressionType, bool, error) {
			return idx.resolveComponentExpressionType(
				component, argument, contextPath,
			)
		},
	); err != nil || found {
		if found && awaited {
			resolved.Type = vueAwaitedType(resolved.Type)
		}
		return resolved, found, err
	}
	if repositoryType := vueRepositoryExpressionType(expression); repositoryType != "" {
		return resolvedVueExpressionType{
			Type: repositoryType, ContextPath: contextPath,
		}, true, nil
	}
	if resolved, found, err := idx.resolveShopwareContextExpressionType(
		expression, contextPath,
	); err != nil || found {
		if found && awaited {
			resolved.Type = vueAwaitedType(resolved.Type)
		}
		return resolved, found, err
	}
	if resolved, found, err := idx.resolveShopwareUtilsExpressionType(
		expression, contextPath,
	); err != nil || found {
		if found && awaited {
			resolved.Type = vueAwaitedType(resolved.Type)
		}
		return resolved, found, err
	}
	if storeName, segments, matched := vueStaticStoreExpression(expression); matched {
		resolved, found, err := idx.resolveVueStaticTypeChain(
			"AdminStore<'"+storeName+"'>", contextPath, segments,
		)
		if err != nil || !found {
			return resolved, found, err
		}
		if awaited {
			resolved.Type = vueAwaitedType(resolved.Type)
		}
		resolved.OpenRuntimeShape = idx.storeExpressionHasOpenRuntimeShape(
			storeName, segments,
		)
		return resolved, resolved.Type != "", nil
	}
	if segments, matched := vueStaticThisExpression(expression); matched &&
		len(segments) > 0 {
		root, found := component.TemplateMember(segments[0].Name)
		if !found {
			root, found = VueBuiltinMember(segments[0].Name)
		}
		if !found || root.Type == "" {
			return resolvedVueExpressionType{}, false, nil
		}
		rootContext := componentMemberTypeContext(root, component)
		rootType, callable := twigVueReceiverType(
			root.Type, segments[0].Called,
		)
		if !callable {
			return resolvedVueExpressionType{}, false, nil
		}
		resolved, found, err := idx.resolveVueStaticTypeChainWithOptional(
			rootType, rootContext, segments[1:], segments[0].Optional,
		)
		if err != nil || !found {
			return resolved, found, err
		}
		if awaited {
			resolved.Type = vueAwaitedType(resolved.Type)
		}
		resolved.OpenRuntimeShape = root.OpenRuntimeShape
		return resolved, resolved.Type != "", nil
	}
	if inferred := vueStaticLiteralType(expression); inferred != "" {
		return resolvedVueExpressionType{
			Type: inferred, ContextPath: contextPath,
		}, true, nil
	}
	if inferred := vueExpressionTextType(expression, nil); inferred != "" {
		return resolvedVueExpressionType{
			Type: inferred, ContextPath: contextPath,
		}, true, nil
	}
	return resolvedVueExpressionType{}, false, nil
}

// ResolveJavaScriptExpressionType conservatively resolves a standalone
// Administration JavaScript/TypeScript expression. It intentionally has no
// component-instance scope: literals, structural expressions, Shopware
// globals, stores, and indexed static chains may resolve, while arbitrary
// locals remain unknown rather than being guessed.
func (idx *AdminComponentIndexer) ResolveJavaScriptExpressionType(
	expression,
	contextPath string,
) (string, bool, error) {
	resolved, found, err := idx.resolveComponentExpressionType(
		VueComponent{}, expression, contextPath,
	)
	return resolved.Type, found && resolved.Type != "", err
}

func (idx *AdminComponentIndexer) storeExpressionHasOpenRuntimeShape(
	storeName string,
	segments []vueStaticExpressionSegment,
) bool {
	if idx == nil || storeName == "" || len(segments) == 0 ||
		segments[0].Called || segments[0].Indexed {
		return false
	}
	stores, err := idx.GetStore(storeName)
	if err != nil {
		return false
	}
	for _, store := range stores {
		member, found := store.Member(segments[0].Name)
		if found && member.OpenRuntimeShape {
			return true
		}
	}
	return false
}

func (idx *AdminComponentIndexer) resolveVueStaticTypeChain(
	rootType,
	contextPath string,
	segments []vueStaticExpressionSegment,
	liveFiles ...AdminTypeFile,
) (resolvedVueExpressionType, bool, error) {
	return idx.resolveVueStaticTypeChainWithOptional(
		rootType, contextPath, segments, false, liveFiles...,
	)
}

func (idx *AdminComponentIndexer) resolveVueStaticTypeChainWithOptional(
	rootType,
	contextPath string,
	segments []vueStaticExpressionSegment,
	optional bool,
	liveFiles ...AdminTypeFile,
) (resolvedVueExpressionType, bool, error) {
	current := resolvedVueExpressionType{
		Type: rootType, ContextPath: contextPath,
	}
	for _, segment := range segments {
		receiverType := current.Type
		if optional || segment.Optional {
			receiverType = withoutVueNullishType(receiverType)
		}
		if segment.Indexed {
			indexedType, indexedContext, found, err := idx.resolveVueIndexedAccessType(
				receiverType, segment.IndexExpression, current.ContextPath,
				liveFiles...,
			)
			if err != nil {
				return current, false, err
			}
			if !found {
				return current, false, nil
			}
			current.Type = indexedType
			if indexedContext != "" {
				current.ContextPath = indexedContext
			}
			optional = optional || segment.Optional
			continue
		}
		shape, err := idx.ResolveVueType(
			receiverType, current.ContextPath, liveFiles...,
		)
		if err != nil {
			return current, false, err
		}
		member, found := twigVueMemberNamed(shape.Members, segment.Name)
		if !found || member.Type == "" {
			return current, false, nil
		}
		current.Type = member.Type
		if member.DefinitionPath != "" {
			current.ContextPath = member.DefinitionPath
		}
		if segment.Called {
			calledType, found, callErr := idx.resolveVueStaticCallType(
				receiverType, member, segment, current.ContextPath,
			)
			if callErr != nil {
				return current, false, callErr
			}
			if !found {
				return current, false, nil
			}
			current.Type = calledType
		}
		optional = optional || segment.Optional
	}
	if optional && current.Type != "" {
		current.Type = mergeVueTypes(current.Type, "undefined")
	}
	return current, current.Type != "", nil
}

func (idx *AdminComponentIndexer) resolveVueIndexedAccessType(
	receiverType,
	indexExpression,
	contextPath string,
	liveFiles ...AdminTypeFile,
) (string, string, bool, error) {
	return idx.resolveVueIndexedAccessTypeWithIndexType(
		receiverType,
		indexExpression,
		vueStaticIndexExpressionType(indexExpression),
		contextPath,
		liveFiles...,
	)
}

func (idx *AdminComponentIndexer) resolveVueIndexedAccessTypeWithIndexType(
	receiverType,
	indexExpression,
	indexType,
	contextPath string,
	liveFiles ...AdminTypeFile,
) (string, string, bool, error) {
	receiverType = strings.TrimSpace(receiverType)
	indexExpression = strings.TrimSpace(indexExpression)
	indexType = strings.TrimSpace(indexType)
	if receiverType == "" || indexExpression == "" {
		return "", contextPath, false, nil
	}
	if union := splitAdminTypeTopLevel(receiverType, '|'); len(union) > 1 {
		var resultType, resultContext string
		for _, branch := range union {
			branch = strings.TrimSpace(branch)
			if branch == "null" || branch == "undefined" || branch == "never" {
				continue
			}
			branchType, branchContext, found, err :=
				idx.resolveVueIndexedAccessTypeWithIndexType(
					branch, indexExpression, indexType, contextPath,
					liveFiles...,
				)
			if err != nil {
				return "", contextPath, false, err
			}
			if !found {
				return "", contextPath, false, nil
			}
			resultType = mergeVueTypes(resultType, branchType)
			if resultContext == "" {
				resultContext = branchContext
			} else if branchContext != resultContext {
				resultContext = contextPath
			}
		}
		return resultType, resultContext, resultType != "", nil
	}

	value := normalizeVueIterableType(receiverType)
	if name, arguments := parseAdminNamedType(value); len(arguments) == 2 {
		shortName := name
		if separator := strings.LastIndexByte(shortName, '.'); separator >= 0 {
			shortName = shortName[separator+1:]
		}
		if shortName == "Record" {
			return strings.TrimSpace(arguments[1]), contextPath, true, nil
		}
	}
	if len(value) >= 2 && value[0] == '[' &&
		matchingSlotDelimiter(value, 0, '[', ']') == len(value)-1 {
		position, err := strconv.Atoi(indexExpression)
		if err != nil || position < 0 {
			return "", contextPath, false, nil
		}
		items := splitSlotTopLevel(value[1:len(value)-1], ',')
		if position >= len(items) {
			return "", contextPath, false, nil
		}
		return strings.TrimSpace(items[position]), contextPath, true, nil
	}
	if _, err := strconv.Atoi(indexExpression); err == nil ||
		withoutVueNullishType(indexType) == "number" {
		if value == "string" {
			return "string", contextPath, true, nil
		}
		if elementType := VueIterableElementType(value); elementType != "" {
			return elementType, contextPath, true, nil
		}
	}
	key := adminTypeStringLiteral(indexExpression)
	if key == "" {
		return "", contextPath, false, nil
	}
	shape, err := idx.ResolveVueType(value, contextPath, liveFiles...)
	if err != nil {
		return "", contextPath, false, err
	}
	member, found := twigVueMemberNamed(shape.Members, key)
	if !found || member.Type == "" {
		return "", contextPath, false, nil
	}
	if member.DefinitionPath != "" {
		contextPath = member.DefinitionPath
	}
	return member.Type, contextPath, true, nil
}

func vueStaticIndexExpressionType(expression string) string {
	expression = unwrapVueExpressionParentheses(strings.TrimSpace(expression))
	if expression == "" {
		return ""
	}
	if _, err := strconv.Atoi(expression); err == nil {
		return "number"
	}
	if adminTypeStringLiteral(expression) != "" {
		return "string"
	}
	if strings.HasPrefix(expression, "+") || strings.HasPrefix(expression, "-") {
		return "number"
	}
	for _, operator := range []string{"-", "*", "/", "%", "<<", ">>", ">>>"} {
		if _, _, split := splitVueTopLevelOperator(expression, operator); split {
			return "number"
		}
	}
	return ""
}

func (idx *AdminComponentIndexer) resolveVueObjectTransformExpressionType(
	expression,
	contextPath string,
	resolveArgument func(string) (resolvedVueExpressionType, bool, error),
) (resolvedVueExpressionType, bool, error) {
	method, argument, segments, matched := vueStaticObjectTransformExpression(
		expression,
	)
	if !matched {
		return resolvedVueExpressionType{}, false, nil
	}
	argumentType := ""
	if resolveArgument != nil {
		resolved, found, err := resolveArgument(argument)
		if err != nil {
			return resolvedVueExpressionType{}, false, err
		}
		if found {
			argumentType = resolved.Type
			if resolved.ContextPath != "" {
				contextPath = resolved.ContextPath
			}
		}
	}
	resultType, err := idx.vueObjectTransformResultType(
		method, argumentType, contextPath,
	)
	if err != nil || resultType == "" {
		return resolvedVueExpressionType{}, false, err
	}
	if len(segments) == 0 {
		return resolvedVueExpressionType{
			Type: resultType, ContextPath: contextPath,
		}, true, nil
	}
	return idx.resolveVueStaticTypeChain(resultType, contextPath, segments)
}

func (idx *AdminComponentIndexer) vueObjectTransformResultType(
	method,
	argumentType,
	contextPath string,
) (string, error) {
	switch method {
	case "keys":
		return "Array<string>", nil
	case "values", "entries":
		valueType := VueIterableElementType(argumentType)
		if valueType == "" && argumentType != "" {
			shape, err := idx.ResolveVueType(argumentType, contextPath)
			if err != nil {
				return "", err
			}
			for _, member := range shape.Members {
				valueType = mergeVueTypes(valueType, member.Type)
			}
		}
		if valueType == "" {
			valueType = "unknown"
		}
		if method == "values" {
			return "Array<" + valueType + ">", nil
		}
		return "Array<[string, " + valueType + "]>", nil
	case "fromEntries":
		keyType := "string | number | symbol"
		valueType := "unknown"
		entryType := normalizeVueIterableType(
			VueIterableElementType(argumentType),
		)
		if len(entryType) >= 2 && entryType[0] == '[' &&
			matchingSlotDelimiter(entryType, 0, '[', ']') == len(entryType)-1 {
			items := splitSlotTopLevel(entryType[1:len(entryType)-1], ',')
			if len(items) == 2 {
				keyType = strings.TrimSpace(items[0])
				valueType = strings.TrimSpace(items[1])
			}
		}
		return "Record<" + keyType + ", " + valueType + ">", nil
	default:
		return "", nil
	}
}

func vueStaticObjectTransformExpression(
	expression string,
) (string, string, []vueStaticExpressionSegment, bool) {
	expression = unwrapVueExpressionParentheses(expression)
	for _, method := range []string{"values", "keys", "entries", "fromEntries"} {
		prefix := "Object." + method
		if !strings.HasPrefix(expression, prefix) {
			continue
		}
		cursor := len(prefix)
		for cursor < len(expression) && isJavaScriptSpace(expression[cursor]) {
			cursor++
		}
		if cursor < len(expression) && expression[cursor] == '<' {
			close := matchingSlotDelimiter(expression, cursor, '<', '>')
			if close < 0 {
				return "", "", nil, false
			}
			cursor = close + 1
			for cursor < len(expression) && isJavaScriptSpace(expression[cursor]) {
				cursor++
			}
		}
		if cursor >= len(expression) || expression[cursor] != '(' {
			return "", "", nil, false
		}
		close := matchingSlotDelimiter(expression, cursor, '(', ')')
		if close < 0 {
			return "", "", nil, false
		}
		arguments := splitSlotTopLevel(expression[cursor+1:close], ',')
		if len(arguments) != 1 || strings.TrimSpace(arguments[0]) == "" {
			return "", "", nil, false
		}
		segments, end, ok := vueStaticExpressionSegments(expression, close+1)
		if !ok || strings.TrimSpace(expression[end:]) != "" {
			return "", "", nil, false
		}
		return method, strings.TrimSpace(arguments[0]), segments, true
	}
	return "", "", nil, false
}

// resolveVueStaticCallType specializes statically known collection callbacks
// before falling back to the indexed method signature. This keeps the type
// index generic while allowing a concrete map callback to substitute its
// element and result types at the call site.
func (idx *AdminComponentIndexer) resolveVueStaticCallType(
	receiverType string,
	member TwigVueMember,
	segment vueStaticExpressionSegment,
	contextPath string,
) (string, bool, error) {
	if segment.Name == "map" {
		elementType := VueIterableElementType(receiverType)
		if elementType != "" {
			resultType, found, err := idx.resolveVueCollectionCallbackType(
				segment.Arguments, elementType, contextPath,
			)
			if err != nil {
				return "", false, err
			}
			if found && resultType != "" {
				return "Array<" + resultType + ">", true, nil
			}
		}
	}
	resultType := VueCallableReturnType(member.Type)
	return resultType, resultType != "", nil
}

func (idx *AdminComponentIndexer) resolveVueCollectionCallbackType(
	arguments,
	elementType,
	contextPath string,
) (string, bool, error) {
	parts := splitSlotTopLevel(arguments, ',')
	if len(parts) == 0 {
		return "", false, nil
	}
	parameter, body, declaredReturn, found := vueStaticArrowCallback(parts[0])
	if !found || parameter == "" || body == "" {
		return "", false, nil
	}
	resultType, resolved, err := idx.resolveVueBoundExpressionType(
		body, parameter, elementType, contextPath,
	)
	if err != nil {
		return "", false, err
	}
	if resolved && resultType != "" {
		return resultType, true, nil
	}
	if declaredReturn != "" {
		return declaredReturn, true, nil
	}
	return "", false, nil
}

func vueStaticArrowCallback(
	value string,
) (parameter, body, declaredReturn string, found bool) {
	value = strings.TrimSpace(value)
	arrow := indexVueTopLevelOperator(value, "=>")
	if arrow < 0 {
		return "", "", "", false
	}
	header := strings.TrimSpace(value[:arrow])
	if strings.HasPrefix(header, "async ") {
		header = strings.TrimSpace(strings.TrimPrefix(header, "async "))
	}
	if header == "" {
		return "", "", "", false
	}
	parameters := header
	if header[0] == '(' {
		close := matchingSlotDelimiter(header, 0, '(', ')')
		if close < 0 {
			return "", "", "", false
		}
		parameters = header[1:close]
		suffix := strings.TrimSpace(header[close+1:])
		if strings.HasPrefix(suffix, ":") {
			declaredReturn = strings.TrimSpace(strings.TrimPrefix(suffix, ":"))
		} else if suffix != "" {
			return "", "", "", false
		}
	}
	parameterParts := splitSlotTopLevel(parameters, ',')
	if len(parameterParts) == 0 {
		return "", "", "", false
	}
	parameter = strings.TrimSpace(parameterParts[0])
	if equals := indexSlotTopLevel(parameter, '='); equals >= 0 {
		parameter = strings.TrimSpace(parameter[:equals])
	}
	if colon := indexSlotTopLevel(parameter, ':'); colon >= 0 {
		parameter = strings.TrimSpace(parameter[:colon])
	}
	parameter = strings.TrimSuffix(parameter, "?")
	if !isSlotIdentifier(parameter) {
		return "", "", "", false
	}
	body = trimVueSourceExpression(value[arrow+2:])
	if strings.HasPrefix(body, "{") {
		parsed := jsparser.Parse("const __callback = " + value + ";")
		if parsed.Tree == nil || parsed.Tree.Root == nil {
			return "", "", "", false
		}
		arrows := jsquery.Nodes(parsed.Tree.Root, jssyntax.JsArrowFunction)
		if len(arrows) != 1 {
			return "", "", "", false
		}
		body = vueMethodReturnExpression(arrows[0])
	}
	return parameter, body, declaredReturn, body != ""
}

func (idx *AdminComponentIndexer) resolveVueBoundExpressionType(
	expression,
	parameter,
	parameterType,
	contextPath string,
) (string, bool, error) {
	expression = unwrapVueExpressionParentheses(expression)
	if expression == "" {
		return "", false, nil
	}
	if asserted := vueTypeAssertion(expression); asserted != "" {
		return asserted, true, nil
	}
	if left, right, split := splitVueTopLevelOperator(expression, "??"); split {
		leftType, leftFound, err := idx.resolveVueBoundExpressionType(
			left, parameter, parameterType, contextPath,
		)
		if err != nil {
			return "", false, err
		}
		rightType, rightFound, err := idx.resolveVueBoundExpressionType(
			right, parameter, parameterType, contextPath,
		)
		if err != nil {
			return "", false, err
		}
		return mergeVueNullishTypes(leftType, rightType),
			leftFound || rightFound, nil
	}
	if left, right, split := splitVueTopLevelOperator(expression, "||"); split {
		leftType, leftFound, err := idx.resolveVueBoundExpressionType(
			left, parameter, parameterType, contextPath,
		)
		if err != nil {
			return "", false, err
		}
		rightType, rightFound, err := idx.resolveVueBoundExpressionType(
			right, parameter, parameterType, contextPath,
		)
		if err != nil {
			return "", false, err
		}
		return mergeVueTypes(leftType, rightType), leftFound || rightFound, nil
	}
	if expression[0] == '{' {
		return idx.resolveVueProjectionObjectType(
			expression, parameter, parameterType, contextPath,
		)
	}
	if expression[0] == '[' {
		return idx.resolveVueProjectionArrayType(
			expression, parameter, parameterType, contextPath,
		)
	}
	if strings.HasPrefix(expression, parameter) &&
		(len(expression) == len(parameter) ||
			!isVueIdentifierPart(expression[len(parameter)])) {
		if len(expression) == len(parameter) {
			return parameterType, true, nil
		}
		segments, end, matched := vueStaticExpressionSegments(
			expression, len(parameter),
		)
		if matched && strings.TrimSpace(expression[end:]) == "" {
			resolved, found, err := idx.resolveVueStaticTypeChain(
				parameterType, contextPath, segments,
			)
			return resolved.Type, found, err
		}
	}
	if inferred := vueExpressionTextType(expression, nil); inferred != "" {
		return inferred, true, nil
	}
	return "", false, nil
}

func (idx *AdminComponentIndexer) resolveVueProjectionObjectType(
	expression,
	parameter,
	parameterType,
	contextPath string,
) (string, bool, error) {
	close := matchingSlotDelimiter(expression, 0, '{', '}')
	if close != len(expression)-1 {
		return "", false, nil
	}
	var fields []string
	var spreads []string
	for _, raw := range splitSlotTopLevel(expression[1:close], ',') {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "...") {
			spreadType, found, err := idx.resolveVueBoundExpressionType(
				strings.TrimSpace(raw[3:]), parameter, parameterType, contextPath,
			)
			if err != nil {
				return "", false, err
			}
			if found && spreadType != "" {
				spreads = append(spreads, spreadType)
			}
			continue
		}
		colon := indexSlotTopLevel(raw, ':')
		if colon < 0 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(raw[:colon]), "'\"")
		if name == "" || strings.HasPrefix(name, "[") {
			continue
		}
		fieldType, found, err := idx.resolveVueBoundExpressionType(
			strings.TrimSpace(raw[colon+1:]), parameter, parameterType, contextPath,
		)
		if err != nil {
			return "", false, err
		}
		if !found || fieldType == "" {
			fieldType = "unknown"
		}
		fields = append(fields, name+": "+fieldType)
	}
	if len(fields) == 0 && len(spreads) == 0 {
		return "Object", true, nil
	}
	if len(fields) > 0 {
		spreads = append(spreads, "{ "+strings.Join(fields, "; ")+" }")
	}
	return strings.Join(spreads, " & "), true, nil
}

func (idx *AdminComponentIndexer) resolveVueProjectionArrayType(
	expression,
	parameter,
	parameterType,
	contextPath string,
) (string, bool, error) {
	close := matchingSlotDelimiter(expression, 0, '[', ']')
	if close != len(expression)-1 {
		return "", false, nil
	}
	elementType := ""
	for _, raw := range splitSlotTopLevel(expression[1:close], ',') {
		itemType, found, err := idx.resolveVueBoundExpressionType(
			strings.TrimSpace(raw), parameter, parameterType, contextPath,
		)
		if err != nil {
			return "", false, err
		}
		if found {
			elementType = mergeVueTypes(elementType, itemType)
		}
	}
	if elementType == "" {
		return "Array", true, nil
	}
	return "Array<" + elementType + ">", true, nil
}

func unwrapVueExpressionParentheses(value string) string {
	value = strings.TrimSpace(value)
	for len(value) >= 2 && value[0] == '(' &&
		matchingSlotDelimiter(value, 0, '(', ')') == len(value)-1 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

func vueStaticLiteralType(expression string) string {
	expression = unwrapVueExpressionParentheses(expression)
	if expression == "" || !strings.Contains("[{\"'`-0123456789", expression[:1]) {
		return ""
	}
	parsed := jsparser.Parse("__shopwareLSPType(" + expression + ");")
	if parsed.Tree == nil || parsed.Tree.Root == nil || len(parsed.Errors) > 0 {
		return ""
	}
	calls := jsquery.Calls(parsed.Tree.Root, "__shopwareLSPType")
	if len(calls) != 1 {
		return ""
	}
	return vueExpressionType(jsquery.ArgumentExpression(calls[0], 0), nil)
}

func indexVueTopLevelOperator(value, operator string) int {
	state := slotScanState{}
	for index := 0; index+len(operator) <= len(value); index++ {
		if state.topLevel() && strings.HasPrefix(value[index:], operator) {
			return index
		}
		state.consume(value[index])
	}
	return -1
}

func splitVueTopLevelOperator(
	value,
	operator string,
) (string, string, bool) {
	index := indexVueTopLevelOperator(value, operator)
	if index < 0 {
		return "", "", false
	}
	left := strings.TrimSpace(value[:index])
	right := strings.TrimSpace(value[index+len(operator):])
	return left, right, left != "" && right != ""
}

func withoutVueNullishType(value string) string {
	var result string
	for _, branch := range splitAdminTypeTopLevel(value, '|') {
		branch = strings.TrimSpace(branch)
		if branch == "" || branch == "null" || branch == "undefined" {
			continue
		}
		result = mergeVueTypes(result, branch)
	}
	return result
}

func mergeVueNullishTypes(left, right string) string {
	left = withoutVueNullishType(left)
	if left == "" {
		return right
	}
	if right == "" || isWeakVueFlowSeed(right) && !isWeakVueFlowSeed(left) {
		return left
	}
	return mergeVueTypes(left, right)
}

func mergeVueTypes(values ...string) string {
	var result []string
	seen := make(map[string]bool)
	for _, value := range values {
		for _, branch := range splitAdminTypeTopLevel(value, '|') {
			branch = strings.TrimSpace(branch)
			if branch == "" || seen[branch] {
				continue
			}
			seen[branch] = true
			result = append(result, branch)
		}
	}
	return strings.Join(result, " | ")
}

func vueRepositoryExpressionType(expression string) string {
	compact := compactVueExpression(expression)
	match := vueRepositoryCreateExpressionPattern.FindStringSubmatch(compact)
	if len(match) != 2 {
		return ""
	}
	// The anchored pattern may match a prefix of a longer chain. A repository
	// result is retained only when create(...) is the complete expression.
	open := strings.IndexByte(compact, '(')
	if open < 0 {
		return ""
	}
	close := matchingSlotDelimiter(compact, open, '(', ')')
	if close != len(compact)-1 {
		return ""
	}
	return "Repository<'" + match[1] + "'>"
}

func vueStaticThisExpression(
	expression string,
) ([]vueStaticExpressionSegment, bool) {
	expression = strings.TrimSpace(expression)
	if !strings.HasPrefix(expression, "this") ||
		(len(expression) > len("this") && isVueIdentifierPart(expression[len("this")])) {
		return nil, false
	}
	segments, end, ok := vueStaticExpressionSegments(expression, len("this"))
	return segments, ok && strings.TrimSpace(expression[end:]) == ""
}

func vueStaticStoreExpression(
	expression string,
) (string, []vueStaticExpressionSegment, bool) {
	expression = strings.TrimSpace(expression)
	prefixes := []string{"Shopware.Store.get", "Store.get"}
	start := -1
	for _, prefix := range prefixes {
		if strings.HasPrefix(expression, prefix) {
			start = len(prefix)
			break
		}
	}
	if start < 0 {
		return "", nil, false
	}
	for start < len(expression) && isJavaScriptSpace(expression[start]) {
		start++
	}
	if start >= len(expression) || expression[start] != '(' {
		return "", nil, false
	}
	close := matchingSlotDelimiter(expression, start, '(', ')')
	if close < 0 {
		return "", nil, false
	}
	argument := strings.TrimSpace(expression[start+1 : close])
	storeName := adminTypeStringLiteral(argument)
	if storeName == "" {
		return "", nil, false
	}
	segments, end, ok := vueStaticExpressionSegments(expression, close+1)
	if !ok || strings.TrimSpace(expression[end:]) != "" {
		return "", nil, false
	}
	return storeName, segments, true
}

func vueStaticExpressionSegments(
	value string,
	cursor int,
) ([]vueStaticExpressionSegment, int, bool) {
	var result []vueStaticExpressionSegment
	for {
		for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
			cursor++
		}
		if cursor >= len(value) {
			return result, cursor, true
		}
		optional := false
		if strings.HasPrefix(value[cursor:], "?.[") {
			optional = true
			cursor += 2
		}
		if cursor < len(value) && value[cursor] == '[' {
			close := matchingSlotDelimiter(value, cursor, '[', ']')
			if close < 0 {
				return nil, cursor, false
			}
			result = append(result, vueStaticExpressionSegment{
				Optional: optional, Indexed: true,
				IndexExpression: strings.TrimSpace(value[cursor+1 : close]),
			})
			cursor = close + 1
			continue
		}
		switch {
		case strings.HasPrefix(value[cursor:], "?."):
			optional = true
			cursor += 2
		case value[cursor] == '.':
			cursor++
		default:
			return result, cursor, len(result) > 0 || cursor == len(value)
		}
		for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
			cursor++
		}
		start := cursor
		if start >= len(value) || !isVueIdentifierStart(value[start]) {
			return nil, cursor, false
		}
		cursor++
		for cursor < len(value) && isVueIdentifierPart(value[cursor]) {
			cursor++
		}
		segment := vueStaticExpressionSegment{
			Name: value[start:cursor], Optional: optional,
		}
		for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
			cursor++
		}
		if cursor < len(value) && value[cursor] == '!' {
			cursor++
		}
		for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
			cursor++
		}
		callOpen := -1
		switch {
		case strings.HasPrefix(value[cursor:], "?.("):
			segment.Optional = true
			callOpen = cursor + 2
		case cursor < len(value) && value[cursor] == '(':
			callOpen = cursor
		}
		if callOpen >= 0 {
			close := matchingSlotDelimiter(value, callOpen, '(', ')')
			if close < 0 {
				return nil, cursor, false
			}
			segment.Called = true
			segment.Arguments = value[callOpen+1 : close]
			cursor = close + 1
		}
		result = append(result, segment)
	}
}

func isVueIdentifierStart(value byte) bool {
	return value == '_' || value == '$' || value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
}

func isVueIdentifierPart(value byte) bool {
	return isVueIdentifierStart(value) || value >= '0' && value <= '9'
}

func vueAwaitedType(value string) string {
	value = strings.TrimSpace(value)
	name, arguments := parseAdminNamedType(value)
	if (name == "Promise" || name == "PromiseLike") && len(arguments) == 1 {
		return strings.TrimSpace(arguments[0])
	}
	return value
}
