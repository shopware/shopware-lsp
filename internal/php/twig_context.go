package php

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/textutil"
)

const maxTwigContextDepth = 2

var (
	templateAnnotationPattern = regexp.MustCompile(
		`(?i)@Template\s*\(\s*(?:template\s*=\s*)?["']([^"']+\.twig)["']`,
	)
	twigContextCandidateMatcher = textutil.NewFoldASCIIMatcher(
		"render",
		"template",
		"stream",
	)
)

// TwigTemplateVariable describes one PHP value exposed to a Twig template.
// Type is persisted in canonical PHP type syntax instead of as types.Type,
// whose immutable representation intentionally has no exported fields.
type TwigTemplateVariable struct {
	Template  string
	Name      string
	Type      string
	FormTypes []string
	File      string
	Range     cst.TextRange
}

type TwigTemplateContext struct {
	Template  string
	Variables []TwigTemplateVariable
}

func extractTwigTemplateContexts(
	path string,
	root *phpsyntax.Node,
	document *semantic.Document,
) []TwigTemplateContext {
	if root == nil || document == nil {
		return nil
	}
	source := root.Text()
	if !twigContextCandidateMatcher.ContainsString(source) {
		return nil
	}
	contexts := make(map[string]*twigContextCollector)
	contextFor := func(template string, scope *phpsyntax.Node) *twigContextCollector {
		template = normalizeTwigTemplateName(template)
		if template == "" {
			return nil
		}
		collector := contexts[template]
		if collector == nil {
			collector = &twigContextCollector{
				template:  template,
				path:      path,
				root:      root,
				document:  document,
				scope:     scope,
				variables: make(map[string]TwigTemplateVariable),
				visited:   make(map[string]struct{}),
			}
			contexts[template] = collector
		}
		return collector
	}

	phpquery.Visit(
		root,
		func(call *phpsyntax.Node) bool {
			template, context := twigRenderArguments(call)
			if template == "" || context == nil {
				return true
			}
			scope := phpquery.FunctionLikeAt(call)
			if collector := contextFor(template, scope); collector != nil {
				collector.scope = scope
				collector.collect(context, 0)
			}
			return true
		},
		phpsyntax.PhpMemberCall,
		phpsyntax.PhpFunctionCall,
	)

	phpquery.Visit(
		root,
		func(method *phpsyntax.Node) bool {
			for _, template := range methodTemplateNames(root, method) {
				collector := contextFor(template, method)
				if collector == nil {
					continue
				}
				collector.scope = method
				phpquery.Visit(
					method,
					func(statement *phpsyntax.Node) bool {
						if phpquery.FunctionLikeAt(statement) == method {
							collector.collect(
								firstDirectNode(statement),
								0,
							)
						}
						return true
					},
					phpsyntax.PhpReturnStatement,
				)
			}
			return true
		},
		phpsyntax.PhpMethodDeclaration,
	)

	result := make([]TwigTemplateContext, 0, len(contexts))
	for _, collector := range contexts {
		variables := make([]TwigTemplateVariable, 0, len(collector.variables))
		for _, variable := range collector.variables {
			variables = append(variables, variable)
		}
		sort.Slice(variables, func(left, right int) bool {
			return strings.ToLower(variables[left].Name) <
				strings.ToLower(variables[right].Name)
		})
		result = append(result, TwigTemplateContext{
			Template:  collector.template,
			Variables: variables,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Template < result[right].Template
	})
	return result
}

func twigRenderArguments(call *phpsyntax.Node) (string, *phpsyntax.Node) {
	name := strings.ToLower(phpquery.CallMethodName(call))
	contextIndex := 1
	switch name {
	case "render", "renderview", "renderstorefront", "stream",
		"htmltemplate", "texttemplate":
	case "renderblock", "renderblockview":
		contextIndex = 2
	default:
		return "", nil
	}

	templateNode := argumentExpression(
		call,
		[]string{"view", "name", "template"},
		0,
	)
	if templateNode == nil || templateNode.Kind() != phpsyntax.PhpString {
		return "", nil
	}
	template := phpquery.StringValue(templateNode)
	if !strings.HasSuffix(strings.ToLower(template), ".twig") {
		return "", nil
	}
	return template, argumentExpression(
		call,
		[]string{"parameters", "context"},
		contextIndex,
	)
}

func argumentExpression(
	call *phpsyntax.Node,
	names []string,
	fallback int,
) *phpsyntax.Node {
	arguments := phpquery.IterateArguments(call)
	for index := 0; arguments.Next(); index++ {
		argument := arguments.Node()
		name := strings.ToLower(phpquery.ArgumentName(argument))
		for _, candidate := range names {
			if name == candidate {
				return phpquery.ArgumentExpression(call, index)
			}
		}
	}
	argument := phpquery.Argument(call, fallback)
	if argument == nil || phpquery.ArgumentName(argument) != "" {
		return nil
	}
	return phpquery.ArgumentExpression(call, fallback)
}

func methodTemplateNames(
	root,
	method *phpsyntax.Node,
) []string {
	var templates []string
	explicitAttribute := false
	for _, attribute := range phpquery.Attributes(method) {
		name := phpquery.NameValue(
			phpquery.DirectChild(attribute, phpsyntax.PhpName),
		)
		if !strings.EqualFold(filepath.Base(strings.ReplaceAll(name, `\`, "/")), "Template") {
			continue
		}
		explicitAttribute = true
		templateNode := argumentExpression(attribute, []string{"template"}, 0)
		if templateNode != nil && templateNode.Kind() == phpsyntax.PhpString {
			templates = append(templates, phpquery.StringValue(templateNode))
		} else if guessed := GuessedControllerTemplate(root, method); guessed != "" {
			templates = append(templates, guessed)
		}
	}

	methodText := method.Text()
	annotationMatches := templateAnnotationPattern.FindAllStringSubmatch(
		methodText,
		-1,
	)
	for _, match := range annotationMatches {
		templates = append(templates, match[1])
	}
	if !explicitAttribute &&
		len(annotationMatches) == 0 &&
		containsFoldASCIIString(methodText, "@template") {
		if guessed := GuessedControllerTemplate(root, method); guessed != "" {
			templates = append(templates, guessed)
		}
	}

	unique := make(map[string]struct{}, len(templates))
	result := make([]string, 0, len(templates))
	for _, template := range templates {
		template = normalizeTwigTemplateName(template)
		if template == "" {
			continue
		}
		if _, exists := unique[template]; exists {
			continue
		}
		unique[template] = struct{}{}
		result = append(result, template)
	}
	return result
}

func containsFoldASCIIString(source, needle string) bool {
	if needle == "" {
		return true
	}
	if len(source) < len(needle) {
		return false
	}
	maxStart := len(source) - len(needle)
	for offset := 0; offset <= maxStart; {
		index := indexFoldASCIIStringByte(
			source[offset:maxStart+1],
			lowerASCIIByte(needle[0]),
		)
		if index < 0 {
			return false
		}
		start := offset + index
		if strings.EqualFold(source[start:start+len(needle)], needle) {
			return true
		}
		offset = start + 1
	}
	return false
}

func indexFoldASCIIStringByte(source string, lower byte) int {
	lowerIndex := strings.IndexByte(source, lower)
	if lower < 'a' || lower > 'z' {
		return lowerIndex
	}
	upperIndex := strings.IndexByte(source, lower-'a'+'A')
	if lowerIndex < 0 {
		return upperIndex
	}
	if upperIndex >= 0 && upperIndex < lowerIndex {
		return upperIndex
	}
	return lowerIndex
}

func lowerASCIIByte(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

// GuessedControllerTemplate returns Symfony's conventional template name for
// a controller method with an empty #[Template] / @Template declaration.
func GuessedControllerTemplate(
	root,
	method *phpsyntax.Node,
) string {
	class := phpquery.ClassAt(method)
	if class == nil {
		return ""
	}
	className := strings.TrimSuffix(phpquery.ClassName(class), "Controller")
	if className == "" {
		return ""
	}
	namespace := phpquery.Namespace(root)
	if marker := strings.Index(namespace, `Controller\`); marker >= 0 {
		className = namespace[marker+len(`Controller\`):] + `\` + className
	}
	className = strings.Trim(className, `\`)
	methodName := strings.TrimSuffix(phpquery.MethodName(method), "Action")
	if methodName == "__invoke" {
		return snakePath(className) + ".html.twig"
	}
	return snakePath(className) + "/" + snakeCase(methodName) + ".html.twig"
}

func snakePath(value string) string {
	parts := strings.Split(value, `\`)
	for index := range parts {
		parts[index] = snakeCase(parts[index])
	}
	return strings.Join(parts, "/")
}

func snakeCase(value string) string {
	var result strings.Builder
	for index, current := range value {
		if unicode.IsUpper(current) && index > 0 {
			result.WriteByte('_')
		}
		result.WriteRune(unicode.ToLower(current))
	}
	return result.String()
}

func normalizeTwigTemplateName(template string) string {
	template = filepath.ToSlash(strings.TrimSpace(template))
	return strings.TrimPrefix(template, "/")
}

type twigContextCollector struct {
	template  string
	path      string
	root      *phpsyntax.Node
	document  *semantic.Document
	scope     *phpsyntax.Node
	variables map[string]TwigTemplateVariable
	visited   map[string]struct{}
}

func (collector *twigContextCollector) collect(
	node *phpsyntax.Node,
	depth int,
) {
	if node == nil || depth > maxTwigContextDepth {
		return
	}
	switch node.Kind() {
	case phpsyntax.PhpArray:
		collector.collectArray(node, depth)
	case phpsyntax.PhpVariable:
		collector.collectVariable(
			phpquery.VariableName(node),
			node.Range().Start,
			depth,
		)
	case phpsyntax.PhpFunctionCall:
		if isTwigArrayContextFunction(phpquery.CallName(node)) {
			arguments := phpquery.IterateArguments(node)
			for index := 0; arguments.Next(); index++ {
				collector.collect(
					phpquery.ArgumentExpression(node, index),
					depth,
				)
			}
		}
	case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
		collector.collectLocalMethod(node, depth)
	case phpsyntax.PhpBinaryExpression, phpsyntax.PhpParenthesized,
		phpsyntax.PhpAssignmentExpression:
		for _, child := range directNodesForTwigContext(node) {
			collector.collect(child, depth)
		}
	}
}

func (collector *twigContextCollector) collectArray(
	array *phpsyntax.Node,
	depth int,
) {
	spreadNext := false
	for _, item := range phpquery.ArrayItems(array) {
		text := strings.TrimSpace(item.Text())
		if strings.HasPrefix(text, "...") {
			value := phpquery.ArrayItemValue(item)
			if value != nil && value.Kind() != phpsyntax.Error {
				collector.collect(value, depth)
			} else {
				spreadNext = true
			}
			continue
		}
		if spreadNext {
			collector.collect(phpquery.ArrayItemValue(item), depth)
			spreadNext = false
			continue
		}
		key := phpquery.ArrayItemKey(item)
		if key == nil || key.Kind() != phpsyntax.PhpString {
			continue
		}
		value := phpquery.ArrayItemValue(item)
		collector.add(phpquery.StringValue(key), value, key.RangeTrimmedTrivia())
	}
}

func (collector *twigContextCollector) collectVariable(
	name string,
	before uint32,
	depth int,
) {
	if name == "" || collector.scope == nil {
		return
	}
	visitKey := fmt.Sprintf("%d:%s:%d", collector.scope.Range().Start, name, depth)
	if _, exists := collector.visited[visitKey]; exists {
		return
	}
	collector.visited[visitKey] = struct{}{}

	phpquery.Visit(
		collector.scope,
		func(assignment *phpsyntax.Node) bool {
			if assignment.Range().Start >= before ||
				phpquery.FunctionLikeAt(assignment) != collector.scope {
				return true
			}
			nodes := directNodesForTwigContext(assignment)
			if len(nodes) < 2 {
				return true
			}
			left, right := nodes[0], nodes[len(nodes)-1]
			switch left.Kind() {
			case phpsyntax.PhpVariable:
				if phpquery.VariableName(left) == name {
					collector.collect(right, depth)
				}
			case phpsyntax.PhpArrayAccess:
				base, key := arrayAccessParts(left)
				if base == name && key != nil {
					collector.add(
						phpquery.StringValue(key),
						right,
						key.RangeTrimmedTrivia(),
					)
				}
			}
			return true
		},
		phpsyntax.PhpAssignmentExpression,
	)
}

func (collector *twigContextCollector) collectLocalMethod(
	call *phpsyntax.Node,
	depth int,
) {
	if depth >= maxTwigContextDepth || collector.scope == nil {
		return
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil ||
		(receiver.Kind() == phpsyntax.PhpVariable &&
			phpquery.VariableName(receiver) != "this") {
		return
	}
	name := phpquery.CallMethodName(call)
	class := phpquery.ClassAt(collector.scope)
	if name == "" || class == nil {
		return
	}
	for _, method := range phpquery.Methods(class) {
		if !strings.EqualFold(phpquery.MethodName(method), name) {
			continue
		}
		visitKey := fmt.Sprintf("method:%d", method.Range().Start)
		if _, exists := collector.visited[visitKey]; exists {
			return
		}
		collector.visited[visitKey] = struct{}{}
		previousScope := collector.scope
		collector.scope = method
		phpquery.Visit(
			method,
			func(statement *phpsyntax.Node) bool {
				if phpquery.FunctionLikeAt(statement) == method {
					collector.collect(
						firstDirectNode(statement),
						depth+1,
					)
				}
				return true
			},
			phpsyntax.PhpReturnStatement,
		)
		collector.scope = previousScope
		return
	}
}

func (collector *twigContextCollector) add(
	name string,
	value *phpsyntax.Node,
	rng cst.TextRange,
) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	typeName := "unknown"
	if value != nil {
		if inferred := collector.typeForTwigValue(
			value,
			make(map[string]struct{}),
			0,
		); !inferred.IsUnknown() {
			typeName = inferred.String()
		}
	}
	formTypes := collector.formTypesForTwigValue(
		value,
		make(map[string]struct{}),
		0,
	)
	existing, exists := collector.variables[name]
	if exists && existing.Type != "unknown" && typeName == "unknown" {
		existing.FormTypes = appendUniqueTwigFormTypes(
			existing.FormTypes,
			formTypes...,
		)
		collector.variables[name] = existing
		return
	}
	if exists {
		formTypes = appendUniqueTwigFormTypes(
			existing.FormTypes,
			formTypes...,
		)
	}
	collector.variables[name] = TwigTemplateVariable{
		Template:  collector.template,
		Name:      name,
		Type:      typeName,
		FormTypes: formTypes,
		File:      collector.path,
		Range:     rng,
	}
}

func (collector *twigContextCollector) typeForTwigValue(
	value *phpsyntax.Node,
	visited map[string]struct{},
	depth int,
) types.Type {
	if value == nil || collector.document == nil || depth > 8 {
		return types.Unknown()
	}
	if inferred := collector.document.TypeOf(value).Type; !inferred.IsUnknown() {
		return inferred
	}
	switch value.Kind() {
	case phpsyntax.PhpVariable:
		return collector.typeForAssignedVariable(
			phpquery.VariableName(value),
			value.Range().Start,
			visited,
			depth+1,
		)
	case phpsyntax.PhpParenthesized:
		for child := range value.ChildNodes() {
			if inferred := collector.typeForTwigValue(
				child,
				visited,
				depth+1,
			); !inferred.IsUnknown() {
				return inferred
			}
		}
	}
	return types.Unknown()
}

func (collector *twigContextCollector) typeForAssignedVariable(
	name string,
	before uint32,
	visited map[string]struct{},
	depth int,
) types.Type {
	if name == "" || collector.scope == nil || depth > 8 {
		return types.Unknown()
	}
	key := fmt.Sprintf(
		"type:%d:%s:%d",
		collector.scope.Range().Start,
		name,
		before,
	)
	if _, exists := visited[key]; exists {
		return types.Unknown()
	}
	visited[key] = struct{}{}
	var best *phpsyntax.Node
	var bestStart uint32
	phpquery.Visit(
		collector.scope,
		func(assignment *phpsyntax.Node) bool {
			if assignment.Range().Start >= before ||
				phpquery.FunctionLikeAt(assignment) != collector.scope {
				return true
			}
			nodes := directNodesForTwigContext(assignment)
			if len(nodes) < 2 ||
				nodes[0].Kind() != phpsyntax.PhpVariable ||
				phpquery.VariableName(nodes[0]) != name {
				return true
			}
			if best == nil || assignment.Range().Start > bestStart {
				best = nodes[len(nodes)-1]
				bestStart = assignment.Range().Start
			}
			return true
		},
		phpsyntax.PhpAssignmentExpression,
	)
	if best == nil {
		return types.Unknown()
	}
	return collector.typeForTwigValue(best, visited, depth+1)
}

func (collector *twigContextCollector) formTypesForTwigValue(
	value *phpsyntax.Node,
	visited map[string]struct{},
	depth int,
) []string {
	if value == nil || depth > 8 {
		return nil
	}
	switch value.Kind() {
	case phpsyntax.PhpVariable:
		return collector.formTypesForAssignedVariable(
			phpquery.VariableName(value),
			value.Range().Start,
			visited,
			depth+1,
		)
	case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
		if strings.EqualFold(
			phpquery.CallMethodName(value),
			"createView",
		) {
			return collector.formTypesForFormExpression(
				phpquery.CallReceiver(value),
				value.Range().Start,
				visited,
				depth+1,
			)
		}
	case phpsyntax.PhpParenthesized:
		for child := range value.ChildNodes() {
			if result := collector.formTypesForTwigValue(
				child,
				visited,
				depth+1,
			); len(result) != 0 {
				return result
			}
		}
	}
	return nil
}

func (collector *twigContextCollector) formTypesForFormExpression(
	value *phpsyntax.Node,
	before uint32,
	visited map[string]struct{},
	depth int,
) []string {
	if value == nil || depth > 8 {
		return nil
	}
	if value.Kind() == phpsyntax.PhpVariable {
		return collector.formTypesForAssignedVariable(
			phpquery.VariableName(value),
			before,
			visited,
			depth+1,
		)
	}
	switch value.Kind() {
	case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall,
		phpsyntax.PhpFunctionCall:
		method := strings.ToLower(phpquery.CallMethodName(value))
		typeIndex := -1
		switch method {
		case "createform", "create", "createbuilder":
			typeIndex = 0
		case "createnamed", "createnamedbuilder":
			typeIndex = 1
		case "getform":
			return collector.formTypesForFormExpression(
				phpquery.CallReceiver(value),
				value.Range().Start,
				visited,
				depth+1,
			)
		}
		if typeIndex >= 0 {
			typeNode := argumentExpression(
				value,
				[]string{"type"},
				typeIndex,
			)
			if typeName := twigFormTypeExpression(
				collector.root,
				typeNode,
			); typeName != "" {
				return []string{typeName}
			}
		}
	case phpsyntax.PhpParenthesized:
		for child := range value.ChildNodes() {
			if result := collector.formTypesForFormExpression(
				child,
				before,
				visited,
				depth+1,
			); len(result) != 0 {
				return result
			}
		}
	}
	return nil
}

func (collector *twigContextCollector) formTypesForAssignedVariable(
	name string,
	before uint32,
	visited map[string]struct{},
	depth int,
) []string {
	if name == "" || collector.scope == nil || depth > 8 {
		return nil
	}
	key := fmt.Sprintf(
		"%d:%s:%d",
		collector.scope.Range().Start,
		name,
		before,
	)
	if _, exists := visited[key]; exists {
		return nil
	}
	visited[key] = struct{}{}
	var result []string
	phpquery.Visit(
		collector.scope,
		func(assignment *phpsyntax.Node) bool {
			if assignment.Range().Start >= before ||
				phpquery.FunctionLikeAt(assignment) != collector.scope {
				return true
			}
			nodes := directNodesForTwigContext(assignment)
			if len(nodes) < 2 ||
				nodes[0].Kind() != phpsyntax.PhpVariable ||
				phpquery.VariableName(nodes[0]) != name {
				return true
			}
			right := nodes[len(nodes)-1]
			current := collector.formTypesForFormExpression(
				right,
				assignment.Range().Start,
				visited,
				depth+1,
			)
			if len(current) == 0 {
				current = collector.formTypesForTwigValue(
					right,
					visited,
					depth+1,
				)
			}
			result = appendUniqueTwigFormTypes(result, current...)
			return true
		},
		phpsyntax.PhpAssignmentExpression,
	)
	return result
}

func twigFormTypeExpression(
	root,
	node *phpsyntax.Node,
) string {
	if root == nil || node == nil {
		return ""
	}
	resolver := NewNameResolver(root)
	if raw := phpquery.ClassConstantName(node); raw != "" {
		return strings.Trim(resolver.Resolve(raw), `\`)
	}
	if node.Kind() == phpsyntax.PhpString {
		value := phpquery.StringValue(node)
		if !strings.Contains(value, `\`) {
			return value
		}
		return strings.Trim(resolver.Resolve(value), `\`)
	}
	return ""
}

func appendUniqueTwigFormTypes(
	target []string,
	values ...string,
) []string {
	seen := make(map[string]struct{}, len(target)+len(values))
	for _, value := range target {
		seen[strings.ToLower(strings.Trim(value, `\`))] = struct{}{}
	}
	for _, value := range values {
		value = strings.Trim(strings.TrimSpace(value), `\`)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		target = append(target, value)
	}
	sort.SliceStable(target, func(left, right int) bool {
		return strings.ToLower(target[left]) <
			strings.ToLower(target[right])
	})
	return target
}

func isTwigArrayContextFunction(name string) bool {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), `\`))
	switch name {
	case "array_merge", "array_merge_recursive", "array_replace":
		return true
	default:
		return false
	}
}

func arrayAccessParts(node *phpsyntax.Node) (string, *phpsyntax.Node) {
	nodes := directNodesForTwigContext(node)
	if len(nodes) < 2 || nodes[0].Kind() != phpsyntax.PhpVariable {
		return "", nil
	}
	var key *phpsyntax.Node
	for _, candidate := range nodes[1:] {
		if candidate.Kind() == phpsyntax.PhpString {
			key = candidate
			break
		}
	}
	return phpquery.VariableName(nodes[0]), key
}

func firstDirectNode(node *phpsyntax.Node) *phpsyntax.Node {
	for child := range node.ChildNodes() {
		return child
	}
	return nil
}

func directNodesForTwigContext(node *phpsyntax.Node) []*phpsyntax.Node {
	if node == nil {
		return nil
	}
	var result []*phpsyntax.Node
	for child := range node.ChildNodes() {
		result = append(result, child)
	}
	return result
}

// TwigTemplateVariables returns the merged PHP context for every portable
// name of one Twig file. A concrete inferred type wins over an unknown value.
func (idx *PHPIndex) TwigTemplateVariables(
	templateNames ...string,
) ([]TwigTemplateVariable, error) {
	if idx == nil || idx.twigContextIndexer == nil {
		return nil, nil
	}
	merged := make(map[string]TwigTemplateVariable)
	for _, template := range templateNames {
		template = normalizeTwigTemplateName(template)
		if template == "" {
			continue
		}
		variables, err := idx.twigVariablesForTemplate(template)
		if err != nil {
			return nil, err
		}
		for _, variable := range variables {
			key := strings.ToLower(variable.Name)
			existing, exists := merged[key]
			if !exists ||
				(existing.Type == "unknown" && variable.Type != "unknown") {
				variable.FormTypes = appendUniqueTwigFormTypes(
					variable.FormTypes,
					existing.FormTypes...,
				)
				merged[key] = variable
			} else {
				existing.FormTypes = appendUniqueTwigFormTypes(
					existing.FormTypes,
					variable.FormTypes...,
				)
				merged[key] = existing
			}
		}
	}
	result := make([]TwigTemplateVariable, 0, len(merged))
	for _, variable := range merged {
		result = append(result, variable)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (idx *PHPIndex) twigVariablesForTemplate(
	template string,
) ([]TwigTemplateVariable, error) {
	template = normalizeTwigTemplateName(template)
	if idx == nil || idx.twigContextIndexer == nil || template == "" {
		return nil, nil
	}
	revision := idx.SemanticSnapshot().Revision
	idx.twigContextCacheMu.Lock()
	if idx.twigContextCacheAt != revision {
		idx.twigContextCacheAt = revision
		idx.twigContextCache = make(
			map[string][]TwigTemplateVariable,
		)
	}
	if cached, exists := idx.twigContextCache[template]; exists {
		result := cloneTwigTemplateVariables(cached)
		idx.twigContextCacheMu.Unlock()
		return result, nil
	}
	idx.twigContextCacheMu.Unlock()

	contexts, err := idx.twigContextIndexer.GetValues(template)
	if err != nil {
		return nil, err
	}
	var result []TwigTemplateVariable
	for _, context := range contexts {
		result = append(result, context.Variables...)
	}
	result = idx.enrichUnknownTwigVariables(template, result)

	idx.twigContextCacheMu.Lock()
	if idx.twigContextCacheAt == revision {
		idx.twigContextCache[template] = cloneTwigTemplateVariables(result)
	}
	idx.twigContextCacheMu.Unlock()
	return result, nil
}

func (idx *PHPIndex) enrichUnknownTwigVariables(
	template string,
	variables []TwigTemplateVariable,
) []TwigTemplateVariable {
	paths := make(map[string]struct{})
	for _, variable := range variables {
		if variable.Type == "unknown" && variable.File != "" {
			paths[variable.File] = struct{}{}
		}
	}
	if len(paths) == 0 {
		return variables
	}
	type sourceVariableKey struct {
		path string
		name string
	}
	fresh := make(map[sourceVariableKey]TwigTemplateVariable)
	for path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		root := phpparser.ParseBytes(source).Tree.Root
		document := idx.AnalyzeDocument(path, 0, root)
		for _, context := range extractTwigTemplateContexts(
			path,
			root,
			document,
		) {
			if normalizeTwigTemplateName(context.Template) != template {
				continue
			}
			for _, variable := range context.Variables {
				if variable.Type == "unknown" {
					continue
				}
				fresh[sourceVariableKey{
					path: path,
					name: strings.ToLower(variable.Name),
				}] = variable
			}
		}
	}
	if len(fresh) == 0 {
		return variables
	}
	result := cloneTwigTemplateVariables(variables)
	for position, variable := range result {
		if variable.Type != "unknown" {
			continue
		}
		replacement, found := fresh[sourceVariableKey{
			path: variable.File,
			name: strings.ToLower(variable.Name),
		}]
		if !found {
			continue
		}
		variable.Type = replacement.Type
		variable.FormTypes = appendUniqueTwigFormTypes(
			variable.FormTypes,
			replacement.FormTypes...,
		)
		result[position] = variable
	}
	return result
}

func cloneTwigTemplateVariables(
	variables []TwigTemplateVariable,
) []TwigTemplateVariable {
	result := make([]TwigTemplateVariable, len(variables))
	for position, variable := range variables {
		result[position] = variable
		result[position].FormTypes = append(
			[]string(nil),
			variable.FormTypes...,
		)
	}
	return result
}

// TwigTemplateVariableSources returns every indexed controller source for one
// variable name instead of merging duplicate providers.
func (idx *PHPIndex) TwigTemplateVariableSources(
	name string,
	templateNames ...string,
) ([]TwigTemplateVariable, error) {
	if idx == nil || idx.twigContextIndexer == nil || name == "" {
		return nil, nil
	}
	var result []TwigTemplateVariable
	for _, template := range templateNames {
		template = normalizeTwigTemplateName(template)
		if template == "" {
			continue
		}
		variables, err := idx.twigVariablesForTemplate(template)
		if err != nil {
			return nil, err
		}
		for _, variable := range variables {
			if variable.Name == name {
				result = append(result, variable)
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File == result[right].File {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].File < result[right].File
	})
	return result, nil
}
