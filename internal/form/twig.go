package form

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
)

// TwigFieldReference describes a direct child access on a controller-backed
// FormView variable, for example checkout.email. FormTypes preserves every
// statically known createForm(...)->createView() origin for merged templates.
type TwigFieldReference struct {
	Variable  string
	Name      string
	FormTypes []string
	Node      *twigsyntax.Node
	Accessor  *twigsyntax.Node
}

// TwigViewVarReference describes a key access through FormView.vars, for
// example checkout.vars.compound.
type TwigViewVarReference struct {
	Variable  string
	Name      string
	FormTypes []string
	Node      *twigsyntax.Node
	Accessor  *twigsyntax.Node
}

// TwigFormFunctionReference connects a form-rendering Twig function to the
// controller-derived FormType provenance of its first FormView argument.
type TwigFormFunctionReference struct {
	Function  string
	Variable  string
	FormTypes []string
	Range     cst.TextRange
}

// TwigFormVariables returns controller variables with statically known
// createForm(...)->createView() provenance for every logical name of a
// template.
func TwigFormVariables(
	index *php.PHPIndex,
	templatePath string,
) ([]php.TwigTemplateVariable, error) {
	if index == nil || templatePath == "" {
		return nil, nil
	}
	variables, err := index.TwigTemplateVariables(
		twig.TemplateNames(templatePath)...,
	)
	if err != nil {
		return nil, err
	}
	result := make([]php.TwigTemplateVariable, 0, len(variables))
	for _, variable := range variables {
		if len(variable.FormTypes) != 0 {
			result = append(result, variable)
		}
	}
	return result, nil
}

// TwigFieldContextAt recognizes completion and navigation positions on a
// direct FormView child access. Nested access such as checkout.email.vars only
// resolves checkout.email as a form field; the outer accessor remains
// available to ordinary PHP/Twig member inference.
func TwigFieldContextAt(
	node *twigsyntax.Node,
	offset uint32,
	variables []php.TwigTemplateVariable,
) (TwigFieldReference, bool) {
	for accessor := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigAccessor,
	); accessor != nil; accessor = nextTwigAccessor(accessor.Parent()) {
		reference, ok := twigFieldReference(accessor, variables)
		if !ok || isFormViewBuiltin(reference.Name) ||
			offset > accessor.Range().End ||
			offset <= twigAccessorReceiverEnd(accessor) ||
			!twigAccessorHasDotBefore(accessor, offset) {
			continue
		}
		return reference, true
	}
	return TwigFieldReference{}, false
}

// TwigFieldReferences returns every parsed direct FormView child access in a
// template. Incomplete accessors without a member name are intentionally
// omitted.
func TwigFieldReferences(
	root *twigsyntax.Node,
	variables []php.TwigTemplateVariable,
) []TwigFieldReference {
	if root == nil || len(variables) == 0 {
		return nil
	}
	var result []TwigFieldReference
	for _, accessor := range twigquery.Nodes(root, twigsyntax.TwigAccessor) {
		reference, ok := twigFieldReference(accessor, variables)
		if !ok || reference.Node == nil || reference.Name == "" {
			continue
		}
		if isFormViewBuiltin(reference.Name) {
			continue
		}
		result = append(result, reference)
	}
	return result
}

func TwigViewVarContextAt(
	node *twigsyntax.Node,
	offset uint32,
	variables []php.TwigTemplateVariable,
) (TwigViewVarReference, bool) {
	for accessor := twigquery.ClosestNodeOfKind(
		node,
		twigsyntax.TwigAccessor,
	); accessor != nil; accessor = nextTwigAccessor(accessor.Parent()) {
		reference, ok := twigViewVarReference(accessor, variables)
		if !ok || offset > accessor.Range().End ||
			offset <= twigAccessorReceiverEnd(accessor) ||
			!twigAccessorHasDotBefore(accessor, offset) {
			continue
		}
		return reference, true
	}
	return TwigViewVarReference{}, false
}

func TwigViewVarReferences(
	root *twigsyntax.Node,
	variables []php.TwigTemplateVariable,
) []TwigViewVarReference {
	if root == nil || len(variables) == 0 {
		return nil
	}
	var result []TwigViewVarReference
	for _, accessor := range twigquery.Nodes(root, twigsyntax.TwigAccessor) {
		reference, ok := twigViewVarReference(accessor, variables)
		if !ok || reference.Node == nil || reference.Name == "" {
			continue
		}
		result = append(result, reference)
	}
	return result
}

// TwigFormFunctionReferences returns form_start(), form(), form_end(), and
// form_rest() calls whose first argument originates from an indexed
// createForm(...)->createView() controller variable.
func TwigFormFunctionReferences(
	root *twigsyntax.Node,
	variables []php.TwigTemplateVariable,
) []TwigFormFunctionReference {
	if root == nil || len(variables) == 0 {
		return nil
	}
	typesByVariable := make(map[string][]string, len(variables))
	for _, variable := range variables {
		if variable.Name == "" || len(variable.FormTypes) == 0 {
			continue
		}
		typesByVariable[variable.Name] = appendUniqueFormTypes(
			typesByVariable[variable.Name],
			variable.FormTypes...,
		)
	}
	var result []TwigFormFunctionReference
	for _, call := range twigquery.Nodes(
		root,
		twigsyntax.TwigFunctionCall,
	) {
		function := strings.ToLower(twigquery.FunctionName(call))
		switch function {
		case "form_start", "form", "form_end", "form_rest":
		default:
			continue
		}
		argument := twigquery.FunctionArgument(call, 0)
		variableNode := firstTwigDescendant(
			argument,
			twigsyntax.TwigLiteralName,
		)
		if variableNode == nil {
			continue
		}
		variable := strings.TrimSpace(variableNode.Text())
		formTypes := typesByVariable[variable]
		if len(formTypes) == 0 {
			continue
		}
		nameNode := firstTwigDescendant(
			directTwigNode(call, twigsyntax.TwigOperand),
			twigsyntax.TwigLiteralName,
		)
		rng := call.RangeTrimmedTrivia()
		if nameNode != nil {
			rng = nameNode.RangeTrimmedTrivia()
		}
		result = append(result, TwigFormFunctionReference{
			Function:  function,
			Variable:  variable,
			FormTypes: append([]string(nil), formTypes...),
			Range:     rng,
		})
	}
	return result
}

func twigViewVarReference(
	accessor *twigsyntax.Node,
	variables []php.TwigTemplateVariable,
) (TwigViewVarReference, bool) {
	operands := directTwigNodes(accessor, twigsyntax.TwigOperand)
	if len(operands) == 0 {
		return TwigViewVarReference{}, false
	}
	receiver := unwrapTwigAccessor(firstTwigNode(operands[0]))
	if receiver == nil {
		return TwigViewVarReference{}, false
	}
	base, ok := twigFieldReference(receiver, variables)
	if !ok || !strings.EqualFold(base.Name, "vars") {
		return TwigViewVarReference{}, false
	}
	reference := TwigViewVarReference{
		Variable:  base.Variable,
		FormTypes: base.FormTypes,
		Accessor:  accessor,
	}
	if len(operands) >= 2 {
		for _, child := range directTwigNodes(
			operands[1],
			twigsyntax.TwigLiteralName,
		) {
			reference.Name = strings.TrimSpace(child.Text())
			reference.Node = child
			break
		}
	}
	return reference, true
}

func twigFieldReference(
	accessor *twigsyntax.Node,
	variables []php.TwigTemplateVariable,
) (TwigFieldReference, bool) {
	operands := directTwigNodes(accessor, twigsyntax.TwigOperand)
	if len(operands) < 1 {
		return TwigFieldReference{}, false
	}
	rootName := unwrapTwigName(firstTwigNode(operands[0]))
	if rootName == nil {
		return TwigFieldReference{}, false
	}
	var formTypes []string
	for _, variable := range variables {
		if variable.Name != strings.TrimSpace(rootName.Text()) {
			continue
		}
		formTypes = appendUniqueFormTypes(
			formTypes,
			variable.FormTypes...,
		)
	}
	if len(formTypes) == 0 {
		return TwigFieldReference{}, false
	}
	reference := TwigFieldReference{
		Variable:  strings.TrimSpace(rootName.Text()),
		FormTypes: formTypes,
		Accessor:  accessor,
	}
	if len(operands) >= 2 {
		for _, child := range directTwigNodes(
			operands[1],
			twigsyntax.TwigLiteralName,
		) {
			reference.Name = strings.TrimSpace(child.Text())
			reference.Node = child
			break
		}
	}
	return reference, true
}

func isFormViewBuiltin(name string) bool {
	switch strings.ToLower(name) {
	case "vars", "parent", "children":
		return true
	default:
		return false
	}
}

func unwrapTwigAccessor(node *twigsyntax.Node) *twigsyntax.Node {
	for node != nil {
		switch node.Kind() {
		case twigsyntax.TwigAccessor:
			return node
		case twigsyntax.TwigExpression,
			twigsyntax.TwigOperand,
			twigsyntax.TwigParenthesesExpression:
			node = firstTwigNode(node)
		default:
			return nil
		}
	}
	return nil
}

func appendUniqueFormTypes(target []string, values ...string) []string {
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

func unwrapTwigName(node *twigsyntax.Node) *twigsyntax.Node {
	for node != nil {
		switch node.Kind() {
		case twigsyntax.TwigLiteralName:
			return node
		case twigsyntax.TwigExpression,
			twigsyntax.TwigOperand,
			twigsyntax.TwigParenthesesExpression:
			node = firstTwigNode(node)
		default:
			return nil
		}
	}
	return nil
}

func directTwigNodes(
	node *twigsyntax.Node,
	kind twigsyntax.Kind,
) []*twigsyntax.Node {
	if node == nil {
		return nil
	}
	var result []*twigsyntax.Node
	for child := range node.ChildNodes() {
		if child.Kind() == kind {
			result = append(result, child)
		}
	}
	return result
}

func firstTwigNode(node *twigsyntax.Node) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		return child
	}
	return nil
}

func firstTwigDescendant(
	node *twigsyntax.Node,
	kind twigsyntax.Kind,
) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for element := range node.Descendants() {
		child, ok := element.(*twigsyntax.Node)
		if ok && child.Kind() == kind {
			return child
		}
	}
	return nil
}

func directTwigNode(
	node *twigsyntax.Node,
	kind twigsyntax.Kind,
) *twigsyntax.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

func nextTwigAccessor(node *twigsyntax.Node) *twigsyntax.Node {
	return twigquery.ClosestNodeOfKind(node, twigsyntax.TwigAccessor)
}

func twigAccessorReceiverEnd(accessor *twigsyntax.Node) uint32 {
	operands := directTwigNodes(accessor, twigsyntax.TwigOperand)
	if len(operands) == 0 {
		return accessor.Range().Start
	}
	return operands[0].Range().End
}

func twigAccessorHasDotBefore(
	accessor *twigsyntax.Node,
	offset uint32,
) bool {
	if accessor == nil || offset <= accessor.Range().Start {
		return false
	}
	end := offset
	if end > accessor.Range().End {
		end = accessor.Range().End
	}
	localEnd := int(end - accessor.Range().Start)
	source := accessor.Text()
	if localEnd > len(source) {
		localEnd = len(source)
	}
	return strings.Contains(source[:localEnd], ".")
}
