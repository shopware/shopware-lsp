package twig

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// TemplateVariable is one value read from a Twig template's input context.
// Range points at the first source-level occurrence in FilePath.
type TemplateVariable struct {
	Name     string
	FilePath string
	Range    cst.TextRange
}

// TemplateDependency describes a template whose input context can flow into
// another template. Provided contains names explicitly supplied by the caller
// and therefore omitted from the inherited contract.
type TemplateDependency struct {
	Template  string
	Provided  []string
	Propagate bool
}

// TemplateVariableCatalog is the persistent input contract for one addressable
// Twig template.
type TemplateVariableCatalog struct {
	Template     string
	FilePath     string
	Variables    []TemplateVariable
	Dependencies []TemplateDependency
}

type IncludeParameterKind uint8

const (
	IncludeFunctionParameter IncludeParameterKind = iota
	IncludeTagParameter
	EmbedTagParameter
)

// IncludeParameterContext describes the direct parameter hash of a static
// include or embed expression.
type IncludeParameterContext struct {
	Template string
	Kind     IncludeParameterKind
	Hash     cst.TextRange
	Existing []string
}

// IncludeParameter is one key inside an include/embed parameter hash.
type IncludeParameter struct {
	Template string
	Name     string
	Kind     IncludeParameterKind
	Range    cst.TextRange
}

var builtinTemplateVariables = map[string]struct{}{
	"_context":   {},
	"_self":      {},
	"app":        {},
	"attributes": {},
	"computed":   {},
	"loop":       {},
	"this":       {},
}

// TemplateInputVariablesInDocument extracts the external root variables read
// by a template. Locals declared through set, loops, macros, and imports are
// excluded; Twig component props are retained because they form an explicit
// input contract.
func TemplateInputVariablesInDocument(
	filePath string,
	root *twigsyntax.Node,
) []TemplateVariable {
	if root == nil {
		return nil
	}

	variables := make(map[string]TemplateVariable)
	propRanges := make(map[string]cst.TextRange)
	for _, declaration := range twigquery.Nodes(
		root,
		twigsyntax.TwigPropDeclaration,
	) {
		name := firstLiteralName(declaration)
		if name == nil {
			continue
		}
		text, rng := literalName(name)
		if text == "" {
			continue
		}
		propRanges[text] = rng
		variables[text] = TemplateVariable{
			Name:     text,
			FilePath: filePath,
			Range:    rng,
		}
	}

	globalLocals := collectGlobalTemplateLocals(filePath, root)
	scopedLocals := collectScopedTemplateLocals(root)
	for _, nameNode := range twigquery.Nodes(
		root,
		twigsyntax.TwigLiteralName,
	) {
		name, rng := literalName(nameNode)
		if name == "" {
			continue
		}
		if _, builtin := builtinTemplateVariables[name]; builtin {
			continue
		}
		if _, local := globalLocals[name]; local {
			continue
		}
		if isTemplateDeclaration(nameNode) ||
			isFunctionOrFilterName(nameNode) ||
			twigquery.ClosestNodeOfKind(
				nameNode,
				twigsyntax.TwigLiteralHashKey,
			) != nil ||
			isAccessorMember(nameNode) ||
			isComponentInvocationName(nameNode) ||
			isScopedTemplateLocal(name, nameNode, scopedLocals) {
			continue
		}
		if _, declaredProp := propRanges[name]; declaredProp {
			continue
		}
		if _, exists := variables[name]; !exists {
			variables[name] = TemplateVariable{
				Name:     name,
				FilePath: filePath,
				Range:    rng,
			}
		}
	}

	result := make([]TemplateVariable, 0, len(variables))
	for _, variable := range variables {
		result = append(result, variable)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name == result[right].Name {
			return result[left].Range.Start < result[right].Range.Start
		}
		return compareFold(
			result[left].Name,
			result[right].Name,
		) < 0
	})
	return result
}

// TemplateDependenciesInDocument extracts inheritance and context-preserving
// include edges used when resolving a template's effective input contract.
func TemplateDependenciesInDocument(
	root *twigsyntax.Node,
) []TemplateDependency {
	if root == nil {
		return nil
	}
	var result []TemplateDependency
	for _, kind := range []twigsyntax.Kind{
		twigsyntax.TwigExtends,
		twigsyntax.ShopwareTwigSwExtends,
		twigsyntax.TwigInclude,
		twigsyntax.ShopwareTwigSwInclude,
		twigsyntax.TwigEmbedStartingBlock,
	} {
		for _, node := range twigquery.Nodes(root, kind) {
			template := directTemplateString(node)
			if template == "" {
				continue
			}
			dependency := TemplateDependency{
				Template:  template,
				Propagate: !hasToken(node, twigsyntax.TkOnly),
			}
			if kind == twigsyntax.TwigExtends ||
				kind == twigsyntax.ShopwareTwigSwExtends {
				dependency.Propagate = true
			} else if hash := directWithHash(node); hash != nil {
				dependency.Provided = directHashKeys(hash)
			}
			result = append(result, dependency)
		}
	}
	for _, call := range twigquery.Nodes(
		root,
		twigsyntax.TwigFunctionCall,
	) {
		if !strings.EqualFold(twigquery.FunctionName(call), "include") {
			continue
		}
		templateNode := twigquery.StringArgument(call, 0)
		if templateNode == nil ||
			!twigquery.StringIsStatic(templateNode) {
			continue
		}
		dependency := TemplateDependency{
			Template:  twigquery.StringValue(templateNode),
			Propagate: includeFunctionPropagatesContext(call),
		}
		if hash := directFunctionHash(call); hash != nil {
			dependency.Provided = directHashKeys(hash)
		}
		result = append(result, dependency)
	}
	return uniqueTemplateDependencies(result)
}

// IncludeParameterContextAt recognizes the direct parameter hash under the
// cursor. Nested hashes and expression values are deliberately rejected.
func IncludeParameterContextAt(
	root,
	node *twigsyntax.Node,
	offset uint32,
) (IncludeParameterContext, bool) {
	hash := hashAtOffset(root, node, offset)
	if hash == nil {
		return IncludeParameterContext{}, false
	}
	context, ok := includeContextForHash(hash)
	if !ok {
		return IncludeParameterContext{}, false
	}
	context.Hash = hash.RangeTrimmedTrivia()
	context.Existing = directHashKeys(hash)
	return context, true
}

// IncludeParameterAt recognizes a parsed key under the cursor.
func IncludeParameterAt(
	root,
	node *twigsyntax.Node,
	offset uint32,
) (IncludeParameter, bool) {
	context, ok := IncludeParameterContextAt(root, node, offset)
	if !ok {
		return IncludeParameter{}, false
	}
	hash := hashAtOffset(root, node, offset)
	for _, key := range twigquery.Nodes(
		hash,
		twigsyntax.TwigLiteralHashKey,
	) {
		if twigquery.HashAt(key) != hash {
			continue
		}
		name, rng := hashKeyName(key)
		if name == "" || !rangeContainsOffset(rng, offset) {
			continue
		}
		return IncludeParameter{
			Template: context.Template,
			Name:     name,
			Kind:     context.Kind,
			Range:    rng,
		}, true
	}
	return IncludeParameter{}, false
}

type templateLocalScope struct {
	scope *twigsyntax.Node
	names map[string]struct{}
}

func collectGlobalTemplateLocals(
	filePath string,
	root *twigsyntax.Node,
) map[string]struct{} {
	result := make(map[string]struct{})
	for _, assignment := range twigquery.Nodes(
		root,
		twigsyntax.TwigAssignment,
	) {
		equalOffset, hasEqual := firstTokenOffset(
			assignment,
			twigsyntax.TkEqual,
		)
		for child := range assignment.ChildNodes() {
			if child.Kind() != twigsyntax.TwigLiteralName {
				continue
			}
			if hasEqual && child.Range().Start > equalOffset {
				continue
			}
			name, _ := literalName(child)
			if name != "" {
				result[name] = struct{}{}
			}
		}
	}
	imports := collectMacroImports(filePath, root)
	for name := range imports.namespaces {
		result[name] = struct{}{}
	}
	for name := range imports.direct {
		result[name] = struct{}{}
	}
	return result
}

func collectScopedTemplateLocals(
	root *twigsyntax.Node,
) []templateLocalScope {
	var result []templateLocalScope
	for _, loop := range twigquery.Nodes(root, twigsyntax.TwigFor) {
		start := directChild(loop, twigsyntax.TwigForBlock)
		if start == nil {
			continue
		}
		inOffset, found := firstTokenOffset(start, twigsyntax.TkIn)
		if !found {
			continue
		}
		names := make(map[string]struct{})
		for child := range start.ChildNodes() {
			if child.Kind() != twigsyntax.TwigLiteralName ||
				child.Range().Start > inOffset {
				continue
			}
			name, _ := literalName(child)
			names[name] = struct{}{}
		}
		result = append(result, templateLocalScope{
			scope: loop,
			names: names,
		})
	}
	for _, macro := range twigquery.Nodes(root, twigsyntax.TwigMacro) {
		start := directChild(macro, twigsyntax.TwigMacroStartingBlock)
		arguments := directChild(start, twigsyntax.TwigArguments)
		if arguments == nil {
			continue
		}
		names := make(map[string]struct{})
		for child := range arguments.ChildNodes() {
			name := firstLiteralName(child)
			if name == nil {
				continue
			}
			text, _ := literalName(name)
			names[text] = struct{}{}
		}
		result = append(result, templateLocalScope{
			scope: macro,
			names: names,
		})
	}
	return result
}

func isScopedTemplateLocal(
	name string,
	node *twigsyntax.Node,
	scopes []templateLocalScope,
) bool {
	for _, scope := range scopes {
		if _, exists := scope.names[name]; exists &&
			isDescendantNode(node, scope.scope) {
			return true
		}
	}
	return false
}

func isTemplateDeclaration(node *twigsyntax.Node) bool {
	for current := node.Parent(); current != nil; current = current.Parent() {
		switch current.Kind() {
		case twigsyntax.TwigPropDeclaration:
			return true
		case twigsyntax.TwigImport, twigsyntax.TwigFrom:
			return true
		case twigsyntax.TwigAssignment:
			equal, found := firstTokenOffset(current, twigsyntax.TkEqual)
			return !found || node.Range().Start < equal
		case twigsyntax.TwigForBlock:
			in, found := firstTokenOffset(current, twigsyntax.TkIn)
			return found && node.Range().Start < in
		case twigsyntax.TwigArguments:
			if twigquery.ClosestNodeOfKind(
				current,
				twigsyntax.TwigMacroStartingBlock,
			) != nil {
				return true
			}
		}
	}
	return false
}

func isFunctionOrFilterName(node *twigsyntax.Node) bool {
	if call := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigFunctionCall,
	); call != nil {
		typed, _ := twigast.CastTwigFunctionCall(call)
		if operand, ok := typed.NameOperand(); ok &&
			isDescendantNode(node, operand.Syntax()) {
			return true
		}
	}
	if filterNode := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigFilter,
	); filterNode != nil {
		typed, _ := twigast.CastTwigFilter(filterNode)
		if operand, ok := typed.Filter(); ok &&
			isDescendantNode(node, operand.Syntax()) {
			return true
		}
	}
	return false
}

func isAccessorMember(node *twigsyntax.Node) bool {
	accessor := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigAccessor,
	)
	if accessor == nil {
		return false
	}
	names := twigquery.Nodes(accessor, twigsyntax.TwigLiteralName)
	return len(names) > 0 && names[0] != node
}

func isComponentInvocationName(node *twigsyntax.Node) bool {
	start := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigComponentStartingBlock,
	)
	return start != nil &&
		twigquery.ClosestNodeOfKind(
			node,
			twigsyntax.TwigLiteralHash,
		) == nil
}

func includeContextForHash(
	hash *twigsyntax.Node,
) (IncludeParameterContext, bool) {
	if hash == nil {
		return IncludeParameterContext{}, false
	}
	if call := twigquery.ClosestNodeOfKind(
		hash,
		twigsyntax.TwigFunctionCall,
	); call != nil &&
		twigquery.FunctionArgumentIndex(hash) == 1 &&
		strings.EqualFold(twigquery.FunctionName(call), "include") {
		template := twigquery.StringArgument(call, 0)
		if template != nil && twigquery.StringIsStatic(template) {
			return IncludeParameterContext{
				Template: twigquery.StringValue(template),
				Kind:     IncludeFunctionParameter,
			}, true
		}
	}
	with := twigquery.ClosestNodeOfKind(
		hash,
		twigsyntax.TwigIncludeWith,
	)
	if with == nil || directExpressionHash(with) != hash {
		return IncludeParameterContext{}, false
	}
	container := twigquery.ClosestNodeOfKind(
		with,
		twigsyntax.TwigInclude,
		twigsyntax.ShopwareTwigSwInclude,
		twigsyntax.TwigEmbedStartingBlock,
	)
	if container == nil {
		return IncludeParameterContext{}, false
	}
	template := directTemplateString(container)
	if template == "" {
		return IncludeParameterContext{}, false
	}
	kind := IncludeTagParameter
	if container.Kind() == twigsyntax.TwigEmbedStartingBlock {
		kind = EmbedTagParameter
	}
	return IncludeParameterContext{
		Template: template,
		Kind:     kind,
	}, true
}

func hashAtOffset(
	root,
	node *twigsyntax.Node,
	offset uint32,
) *twigsyntax.Node {
	if node != nil {
		if hash := twigquery.ClosestNodeOfKind(
			node,
			twigsyntax.TwigLiteralHash,
		); hash != nil && rangeContainsOffset(hash.Range(), offset) {
			return hash
		}
	}
	var result *twigsyntax.Node
	for _, hash := range twigquery.Nodes(root, twigsyntax.TwigLiteralHash) {
		if !rangeContainsOffset(hash.Range(), offset) {
			continue
		}
		if result == nil || hash.Range().Len() < result.Range().Len() {
			result = hash
		}
	}
	return result
}

func directTemplateString(node *twigsyntax.Node) string {
	for _, literal := range twigquery.Nodes(
		node,
		twigsyntax.TwigLiteralString,
	) {
		for current := literal.Parent(); current != nil && current != node; current = current.Parent() {
			switch current.Kind() {
			case twigsyntax.TwigLiteralHash,
				twigsyntax.TwigLiteralArray,
				twigsyntax.TwigFunctionCall:
				goto next
			}
		}
		if twigquery.StringIsStatic(literal) {
			return twigquery.StringValue(literal)
		}
	next:
	}
	return ""
}

func directWithHash(node *twigsyntax.Node) *twigsyntax.Node {
	with := directChild(node, twigsyntax.TwigIncludeWith)
	if with == nil {
		return nil
	}
	return directExpressionHash(with)
}

func directExpressionHash(container *twigsyntax.Node) *twigsyntax.Node {
	for _, hash := range twigquery.Nodes(
		container,
		twigsyntax.TwigLiteralHash,
	) {
		direct := true
		for current := hash.Parent(); current != nil && current != container; current = current.Parent() {
			switch current.Kind() {
			case twigsyntax.TwigLiteralHash,
				twigsyntax.TwigLiteralArray,
				twigsyntax.TwigFunctionCall:
				direct = false
			}
		}
		if direct {
			return hash
		}
	}
	return nil
}

func directFunctionHash(call *twigsyntax.Node) *twigsyntax.Node {
	for _, hash := range twigquery.Nodes(call, twigsyntax.TwigLiteralHash) {
		if twigquery.FunctionCallAt(hash) == call &&
			twigquery.FunctionArgumentIndex(hash) == 1 {
			return hash
		}
	}
	return nil
}

func directHashKeys(hash *twigsyntax.Node) []string {
	var result []string
	for _, key := range twigquery.Nodes(
		hash,
		twigsyntax.TwigLiteralHashKey,
	) {
		if twigquery.HashAt(key) != hash {
			continue
		}
		name, _ := hashKeyName(key)
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

func hashKeyName(node *twigsyntax.Node) (string, cst.TextRange) {
	if node == nil {
		return "", cst.TextRange{}
	}
	if literal := firstDescendantOfKind(
		node,
		twigsyntax.TwigLiteralString,
	); literal != nil {
		typed, _ := twigast.CastTwigLiteralString(literal)
		if inner, ok := typed.GetInner(); ok {
			rng := inner.Syntax().RangeTrimmedTrivia()
			return inner.Syntax().Text(), rng
		}
	}
	rng := node.RangeTrimmedTrivia()
	return strings.TrimSpace(node.Text()), rng
}

func firstLiteralName(node *twigsyntax.Node) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for _, candidate := range twigquery.Nodes(
		node,
		twigsyntax.TwigLiteralName,
	) {
		return candidate
	}
	return nil
}

func firstDescendantOfKind(
	node *twigsyntax.Node,
	kind twigsyntax.Kind,
) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for element := range node.Descendants() {
		candidate, ok := element.(*twigsyntax.Node)
		if ok && candidate.Kind() == kind {
			return candidate
		}
	}
	return nil
}

func firstTokenOffset(
	node *twigsyntax.Node,
	kind twigsyntax.Kind,
) (uint32, bool) {
	if node == nil {
		return 0, false
	}
	for element := range node.Descendants() {
		token, ok := element.(*twigsyntax.Token)
		if ok && token.Kind() == kind {
			return token.Range().Start, true
		}
	}
	return 0, false
}

func hasToken(node *twigsyntax.Node, kind twigsyntax.Kind) bool {
	_, found := firstTokenOffset(node, kind)
	return found
}

func isDescendantNode(
	node,
	ancestor *twigsyntax.Node,
) bool {
	if node == nil || ancestor == nil {
		return false
	}
	for current := node; current != nil; current = current.Parent() {
		if current == ancestor {
			return true
		}
	}
	return false
}

func rangeContainsOffset(rng cst.TextRange, offset uint32) bool {
	return offset >= rng.Start && offset <= rng.End
}

func includeFunctionPropagatesContext(call *twigsyntax.Node) bool {
	typed, ok := twigast.CastTwigFunctionCall(call)
	if !ok {
		return true
	}
	arguments, ok := typed.Arguments()
	if !ok {
		return true
	}
	index := 0
	for child := range arguments.Syntax().ChildNodes() {
		text := strings.ReplaceAll(child.Text(), " ", "")
		text = strings.ReplaceAll(text, "\t", "")
		lower := strings.ToLower(text)
		if child.Kind() == twigsyntax.TwigNamedArgument &&
			(strings.HasPrefix(lower, "with_context=") ||
				strings.HasPrefix(lower, "with_context:")) {
			return !strings.HasSuffix(lower, "false")
		}
		if index == 2 {
			return strings.TrimSpace(strings.ToLower(child.Text())) != "false"
		}
		index++
	}
	return true
}

func uniqueTemplateDependencies(
	dependencies []TemplateDependency,
) []TemplateDependency {
	seen := make(map[string]struct{}, len(dependencies))
	result := make([]TemplateDependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		provided := append([]string(nil), dependency.Provided...)
		sort.Strings(provided)
		key := dependency.Template + "\x00" +
			strings.Join(provided, "\x00")
		if dependency.Propagate {
			key += "\x00context"
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		dependency.Provided = provided
		result = append(result, dependency)
	}
	return result
}
