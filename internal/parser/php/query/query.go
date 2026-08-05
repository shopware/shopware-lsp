package query

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func Nodes(root *syntax.Node, kinds ...syntax.Kind) []*syntax.Node {
	if root == nil {
		return nil
	}
	var result []*syntax.Node
	appendNodes(&result, root, kinds)
	return result
}

// Visit walks matching nodes in source order without materializing a result
// slice. Returning false from visit stops the traversal; Visit reports whether
// the complete tree was visited.
func Visit(
	root *syntax.Node,
	visit func(*syntax.Node) bool,
	kinds ...syntax.Kind,
) bool {
	if root == nil || visit == nil {
		return true
	}
	return visitNodes(root, kinds, visit)
}

func visitNodes(
	node *syntax.Node,
	kinds []syntax.Kind,
	visit func(*syntax.Node) bool,
) bool {
	if hasKind(node.Kind(), kinds) && !visit(node) {
		return false
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if ok && !visitNodes(child, kinds, visit) {
			return false
		}
	}
	return true
}

func appendNodes(
	result *[]*syntax.Node,
	node *syntax.Node,
	kinds []syntax.Kind,
) {
	if hasKind(node.Kind(), kinds) {
		*result = append(*result, node)
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if ok {
			appendNodes(result, child, kinds)
		}
	}
}

func Namespace(root *syntax.Node) string {
	var result string
	Visit(
		root,
		func(namespace *syntax.Node) bool {
			result = NameValue(directChild(namespace, syntax.PhpName))
			return false
		},
		syntax.PhpNamespace,
	)
	return result
}

func UseDeclarations(root *syntax.Node) []*syntax.Node {
	return Nodes(root, syntax.PhpUseDeclaration)
}

func Classes(root *syntax.Node) []*syntax.Node {
	return Nodes(root,
		syntax.PhpClassDeclaration,
		syntax.PhpInterfaceDeclaration,
		syntax.PhpTraitDeclaration,
		syntax.PhpEnumDeclaration,
	)
}

func ClassAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node,
		syntax.PhpClassDeclaration,
		syntax.PhpInterfaceDeclaration,
		syntax.PhpTraitDeclaration,
		syntax.PhpEnumDeclaration,
	)
}

func ClassName(node *syntax.Node) string {
	class := ClassAt(node)
	if class == nil {
		return ""
	}
	return NameValue(directChild(class, syntax.PhpName))
}

func ClassBody(node *syntax.Node) *syntax.Node {
	return directChild(ClassAt(node), syntax.PhpClassBody)
}

func ClassExtends(node *syntax.Node) []string {
	return clauseNames(ClassAt(node), syntax.PhpExtendsClause)
}

func ClassImplements(node *syntax.Node) []string {
	return clauseNames(ClassAt(node), syntax.PhpImplementsClause)
}

func IsInterface(node *syntax.Node) bool {
	class := ClassAt(node)
	return class != nil && class.Kind() == syntax.PhpInterfaceDeclaration
}

func IsTrait(node *syntax.Node) bool {
	class := ClassAt(node)
	return class != nil && class.Kind() == syntax.PhpTraitDeclaration
}

func IsEnum(node *syntax.Node) bool {
	class := ClassAt(node)
	return class != nil && class.Kind() == syntax.PhpEnumDeclaration
}

func IsAbstract(node *syntax.Node) bool {
	class := ClassAt(node)
	if class == nil || class.Kind() != syntax.PhpClassDeclaration {
		return false
	}
	for index := 0; index < class.ChildCount(); index++ {
		token := class.Child(index)
		if value, ok := token.(*syntax.Token); ok && strings.EqualFold(value.Text(), "abstract") {
			return true
		}
	}
	return false
}

func Methods(node *syntax.Node) []*syntax.Node {
	body := ClassBody(node)
	if body == nil {
		return nil
	}
	var methods []*syntax.Node
	for index := 0; index < body.ChildCount(); index++ {
		child, ok := body.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.PhpMethodDeclaration {
			methods = append(methods, child)
		}
	}
	return methods
}

func MethodAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.PhpMethodDeclaration)
}

func FunctionAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.PhpFunctionDeclaration)
}

func FunctionLikeAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(
		node,
		syntax.PhpMethodDeclaration,
		syntax.PhpFunctionDeclaration,
		syntax.PhpClosure,
		syntax.PhpArrowFunction,
	)
}

func Functions(root *syntax.Node) []*syntax.Node {
	return Nodes(root, syntax.PhpFunctionDeclaration)
}

func FunctionName(node *syntax.Node) string {
	function := FunctionAt(node)
	if function == nil {
		return ""
	}
	return NameValue(directChild(function, syntax.PhpName))
}

func MethodName(node *syntax.Node) string {
	method := MethodAt(node)
	if method == nil {
		return ""
	}
	return NameValue(directChild(method, syntax.PhpName))
}

func Properties(node *syntax.Node) []*syntax.Node {
	body := ClassBody(node)
	if body == nil {
		return nil
	}
	var properties []*syntax.Node
	for index := 0; index < body.ChildCount(); index++ {
		child, ok := body.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.PhpPropertyDeclaration {
			properties = append(properties, child)
		}
	}
	return properties
}

func Parameters(method *syntax.Node) []*syntax.Node {
	iterator := IterateParameters(method)
	count := iterator.Len()
	if count == 0 {
		return nil
	}
	result := make([]*syntax.Node, 0, count)
	for iterator.Next() {
		result = append(result, iterator.Node())
	}
	return result
}

type ParameterIterator struct {
	list    *syntax.Node
	index   int
	current *syntax.Node
}

func IterateParameters(method *syntax.Node) ParameterIterator {
	method = FunctionLikeAt(method)
	return ParameterIterator{
		list: directChild(method, syntax.PhpParameterList),
	}
}

func (iterator ParameterIterator) Len() int {
	if iterator.list == nil {
		return 0
	}
	count := 0
	for index := 0; index < iterator.list.ChildCount(); index++ {
		child, ok := iterator.list.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.PhpParameter {
			count++
		}
	}
	return count
}

func (iterator *ParameterIterator) Next() bool {
	if iterator.list == nil {
		return false
	}
	for iterator.index < iterator.list.ChildCount() {
		child, ok := iterator.list.Child(iterator.index).(*syntax.Node)
		iterator.index++
		if ok && child.Kind() == syntax.PhpParameter {
			iterator.current = child
			return true
		}
	}
	iterator.current = nil
	return false
}

func (iterator *ParameterIterator) Node() *syntax.Node {
	return iterator.current
}

func ParameterName(node *syntax.Node) string {
	if node == nil || node.Kind() != syntax.PhpParameter {
		return ""
	}
	if token := descendantToken(node, syntax.TkVariable); token != nil {
		return token.Text()
	}
	return ""
}

func ParameterType(node *syntax.Node) string {
	if node == nil || node.Kind() != syntax.PhpParameter {
		return ""
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if ok && isTypeNode(child.Kind()) {
			return compactExpression(child.Text())
		}
	}
	return ""
}

func ParameterOptional(node *syntax.Node) bool {
	return hasToken(node, syntax.TkEquals)
}

// ParameterDefault returns the direct default expression after =.
func ParameterDefault(node *syntax.Node) *syntax.Node {
	if node == nil || node.Kind() != syntax.PhpParameter {
		return nil
	}
	foundEquals := false
	for index := 0; index < node.ChildCount(); index++ {
		switch child := node.Child(index).(type) {
		case *syntax.Token:
			if child.Kind() == syntax.TkEquals {
				foundEquals = true
			}
		case *syntax.Node:
			if foundEquals {
				return child
			}
		}
	}
	return nil
}

func ParameterVariadic(node *syntax.Node) bool {
	if node == nil || node.Kind() != syntax.PhpParameter {
		return false
	}
	return descendantToken(node, syntax.TkEllipsis) != nil
}

func PropertyVariables(node *syntax.Node) []*syntax.Node {
	if node == nil || node.Kind() != syntax.PhpPropertyDeclaration {
		return nil
	}
	var result []*syntax.Node
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.PhpVariable {
			result = append(result, child)
		}
	}
	return result
}

func VariableName(node *syntax.Node) string {
	variable := ancestorOrSelf(node, syntax.PhpVariable)
	if variable == nil {
		return ""
	}
	if token := descendantToken(variable, syntax.TkVariable); token != nil {
		return strings.TrimPrefix(token.Text(), "$")
	}
	return ""
}

// VariableKey returns the complete variable token, including its leading "$".
// Unlike rebuilding "$"+VariableName(node), the returned source slice does not
// allocate and is suitable for short-lived binder and inference lookup keys.
func VariableKey(node *syntax.Node) string {
	variable := ancestorOrSelf(node, syntax.PhpVariable)
	if variable == nil {
		return ""
	}
	if token := descendantToken(variable, syntax.TkVariable); token != nil {
		return token.Text()
	}
	return ""
}

func DeclarationVisibility(node *syntax.Node) string {
	if node == nil {
		return ""
	}
	// Modifiers are direct declaration tokens. Restricting the scan to direct
	// tokens avoids stopping at parentheses or variables inside attributes,
	// parameter lists, property hooks, and method bodies.
	cursor := node.ChildTokenCursor()
	for cursor.Next() {
		token := cursor.Token()
		switch strings.ToLower(token.Text()) {
		case "public", "protected", "private":
			return strings.ToLower(token.Text())
		}
		if strings.EqualFold(token.Text(), "function") {
			break
		}
	}
	return ""
}

func PropertyType(node *syntax.Node) string {
	if node == nil || node.Kind() != syntax.PhpPropertyDeclaration {
		return ""
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if ok && isTypeNode(child.Kind()) {
			return compactExpression(child.Text())
		}
	}
	return ""
}

func MethodReturnType(node *syntax.Node) string {
	method := FunctionLikeAt(node)
	if method == nil {
		return ""
	}
	for index := 0; index < method.ChildCount(); index++ {
		child, ok := method.Child(index).(*syntax.Node)
		if ok && isTypeNode(child.Kind()) {
			return compactExpression(child.Text())
		}
	}
	return ""
}

func AttributeGroups(node *syntax.Node) []*syntax.Node {
	if node == nil {
		return nil
	}
	var result []*syntax.Node
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.PhpAttributeGroup {
			result = append(result, child)
		}
	}
	return result
}

func Attributes(node *syntax.Node) []*syntax.Node {
	var result []*syntax.Node
	for _, group := range AttributeGroups(node) {
		for index := 0; index < group.ChildCount(); index++ {
			child, ok := group.Child(index).(*syntax.Node)
			if ok && child.Kind() == syntax.PhpAttribute {
				result = append(result, child)
			}
		}
	}
	return result
}

func AttributeName(node *syntax.Node) string {
	attribute := ancestorOrSelf(node, syntax.PhpAttribute)
	return NameValue(directChild(attribute, syntax.PhpName))
}

func AttributeAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.PhpAttribute)
}

func Calls(root *syntax.Node, names ...string) []*syntax.Node {
	var result []*syntax.Node
	for _, call := range Nodes(root, syntax.PhpMemberCall, syntax.PhpScopedCall, syntax.PhpFunctionCall) {
		if len(names) == 0 {
			result = append(result, call)
		} else if hasString(CallName(call), names) {
			result = append(result, call)
		}
	}
	return result
}

func ExpressionStatements(root *syntax.Node) []*syntax.Node {
	return Nodes(root, syntax.PhpExpressionStatement)
}

// AssignedVariable returns the variable on the left side of a simple
// assignment expression such as "$services = $container->services()".
func AssignedVariable(node *syntax.Node) string {
	statement := ancestorOrSelf(node, syntax.PhpExpressionStatement)
	if statement == nil {
		return ""
	}
	assignments := Nodes(statement, syntax.PhpAssignmentExpression)
	if len(assignments) > 0 {
		for index := 0; index < assignments[0].ChildCount(); index++ {
			child, ok := assignments[0].Child(index).(*syntax.Node)
			if !ok {
				continue
			}
			if child.Kind() == syntax.PhpVariable {
				name := VariableName(child)
				if name != "" {
					return "$" + name
				}
			}
			// Only inspect the assignment's left operand.
			break
		}
	}
	var variable string
	for index := 0; index < statement.ChildCount(); index++ {
		child := statement.Child(index)
		switch element := child.(type) {
		case *syntax.Token:
			if element.Kind() == syntax.TkEquals {
				return variable
			}
		case *syntax.Node:
			if variable == "" && element.Kind() == syntax.PhpVariable {
				name := VariableName(element)
				if name != "" {
					variable = "$" + name
				}
			}
		}
	}
	return ""
}

func CallAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.PhpMemberCall, syntax.PhpScopedCall, syntax.PhpFunctionCall)
}

func CallName(node *syntax.Node) string {
	call := CallAt(node)
	if call == nil {
		return ""
	}
	list := directChild(call, syntax.PhpArgumentList)
	if list == nil {
		return ""
	}
	prefixLength := int(list.Range().Start - call.Range().Start)
	if prefixLength < 0 || prefixLength > len(call.Text()) {
		return ""
	}
	return compactExpression(call.Text()[:prefixLength])
}

func CallMethodName(node *syntax.Node) string {
	call := CallAt(node)
	if call == nil {
		return ""
	}
	// Calls are represented as receiver/name nodes followed by the argument
	// list. Reading the last node before the arguments avoids rendering and
	// compacting an arbitrarily long receiver chain just to recover its final
	// method name.
	var target *syntax.Node
	for index := 0; index < call.ChildCount(); index++ {
		element := call.Child(index)
		child, ok := element.(*syntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == syntax.PhpArgumentList {
			if target != nil {
				return compactExpression(target.Text())
			}
			return ""
		}
		target = child
	}
	return ""
}

// CallReceiver returns the direct receiver expression of a member or scoped
// call. Function calls do not have receivers.
func CallReceiver(node *syntax.Node) *syntax.Node {
	call := CallAt(node)
	if call == nil ||
		(call.Kind() != syntax.PhpMemberCall && call.Kind() != syntax.PhpScopedCall) {
		return nil
	}
	for index := 0; index < call.ChildCount(); index++ {
		child, ok := call.Child(index).(*syntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == syntax.PhpArgumentList {
			return nil
		}
		return child
	}
	return nil
}

type ArgumentIterator struct {
	list    *syntax.Node
	index   int
	current *syntax.Node
}

func IterateArguments(node *syntax.Node) ArgumentIterator {
	return ArgumentIterator{list: argumentList(node)}
}

func (iterator ArgumentIterator) Len() int {
	if iterator.list == nil {
		return 0
	}
	count := 0
	for index := 0; index < iterator.list.ChildCount(); index++ {
		child, ok := iterator.list.Child(index).(*syntax.Node)
		if ok && isArgument(child) {
			count++
		}
	}
	return count
}

func (iterator *ArgumentIterator) Next() bool {
	if iterator.list == nil {
		return false
	}
	for iterator.index < iterator.list.ChildCount() {
		child, ok := iterator.list.Child(iterator.index).(*syntax.Node)
		iterator.index++
		if ok && isArgument(child) {
			iterator.current = child
			return true
		}
	}
	iterator.current = nil
	return false
}

func (iterator *ArgumentIterator) Node() *syntax.Node {
	return iterator.current
}

func argumentList(node *syntax.Node) *syntax.Node {
	var call *syntax.Node
	if node != nil {
		switch node.Kind() {
		case syntax.PhpMemberCall, syntax.PhpScopedCall, syntax.PhpFunctionCall,
			syntax.PhpAttribute, syntax.PhpObjectCreation:
			call = node
		}
	}
	if call == nil {
		call = CallAt(node)
	}
	return directChild(call, syntax.PhpArgumentList)
}

func isArgument(node *syntax.Node) bool {
	return node != nil && (node.Kind() == syntax.PhpArgument ||
		node.Kind() == syntax.PhpNamedArgument)
}

func Arguments(node *syntax.Node) []*syntax.Node {
	iterator := IterateArguments(node)
	count := iterator.Len()
	if count == 0 {
		return nil
	}
	result := make([]*syntax.Node, 0, count)
	for iterator.Next() {
		result = append(result, iterator.Node())
	}
	return result
}

func Argument(node *syntax.Node, index int) *syntax.Node {
	if index < 0 {
		return nil
	}
	iterator := IterateArguments(node)
	for iterator.Next() {
		if index == 0 {
			return iterator.Node()
		}
		index--
	}
	return nil
}

func ArgumentIndex(container, node *syntax.Node) int {
	iterator := IterateArguments(container)
	index := 0
	for iterator.Next() {
		if contains(iterator.Node(), node) {
			return index
		}
		index++
	}
	return -1
}

func ArgumentName(node *syntax.Node) string {
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case syntax.PhpNamedArgument:
			return NameValue(directChild(current, syntax.PhpName))
		case syntax.PhpArgument:
			// A positional argument can itself be nested beneath an outer
			// named argument. Stop at its own boundary so the outer name does
			// not leak into the inner call.
			return ""
		}
	}
	return ""
}

func ArgumentExpression(node *syntax.Node, index int) *syntax.Node {
	argument := Argument(node, index)
	if argument == nil {
		return nil
	}
	for index := 0; index < argument.ChildCount(); index++ {
		child, ok := argument.Child(index).(*syntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == syntax.PhpName && argument.Kind() == syntax.PhpNamedArgument {
			continue
		}
		return child
	}
	return nil
}

// ArgumentValueText returns the complete expression text for an argument.
// Unlike ArgumentExpression it also preserves parser-recovered composite
// expressions such as Foo::class . '.inner'.
func ArgumentValueText(node *syntax.Node, index int) string {
	argument := Argument(node, index)
	if argument == nil {
		return ""
	}
	text := strings.TrimSpace(argument.Text())
	if argument.Kind() != syntax.PhpNamedArgument {
		return text
	}
	if colon := strings.IndexByte(text, ':'); colon >= 0 {
		return strings.TrimSpace(text[colon+1:])
	}
	return ""
}

func StringArgument(node *syntax.Node, index int) *syntax.Node {
	argument := Argument(node, index)
	if argument == nil {
		return nil
	}
	return descendantNode(argument, syntax.PhpString)
}

func StringAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.PhpString)
}

func StringValue(node *syntax.Node) string {
	stringNode := StringAt(node)
	if stringNode == nil {
		return ""
	}
	if token := descendantToken(stringNode, syntax.TkString); token != nil {
		text := token.Text()
		if len(text) >= 2 {
			quote := text[0]
			if (quote == '\'' || quote == '"' || quote == '`') && text[len(text)-1] == quote {
				return text[1 : len(text)-1]
			}
		}
		return text
	}
	return ""
}

// StringContentRange returns the source range inside matching single or double
// quotes. Other string forms retain their full trimmed range.
func StringContentRange(node *syntax.Node) syntax.TextRange {
	if node == nil {
		return syntax.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := strings.TrimSpace(node.Text())
	if len(text) >= 2 &&
		(text[0] == '\'' && text[len(text)-1] == '\'' ||
			text[0] == '"' && text[len(text)-1] == '"') {
		rng.Start++
		rng.End--
	}
	return rng
}

func StringArgumentIndex(node *syntax.Node) int {
	stringNode := StringAt(node)
	if stringNode == nil {
		return -1
	}
	call := CallAt(stringNode)
	if call == nil {
		return -1
	}
	return ArgumentIndex(call, stringNode)
}

func StringInCall(node *syntax.Node, argumentIndex int, names ...string) bool {
	if StringAt(node) == nil || StringArgumentIndex(node) != argumentIndex {
		return false
	}
	callName := CallName(node)
	methodName := CallMethodName(node)
	for _, name := range names {
		if callName == name || methodName == name {
			return true
		}
	}
	return false
}

func ObjectCreations(root *syntax.Node, classNames ...string) []*syntax.Node {
	var result []*syntax.Node
	for _, object := range Nodes(root, syntax.PhpObjectCreation) {
		if len(classNames) == 0 {
			result = append(result, object)
		} else if hasString(ObjectClassName(object), classNames) {
			result = append(result, object)
		}
	}
	return result
}

func ObjectCreationAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.PhpObjectCreation)
}

func ObjectClassName(node *syntax.Node) string {
	object := ancestorOrSelf(node, syntax.PhpObjectCreation)
	return NameValue(directChild(object, syntax.PhpName))
}

// ClassConstantName returns the class-like name from a Foo::class expression.
func ClassConstantName(node *syntax.Node) string {
	if node == nil {
		return ""
	}
	candidates := []*syntax.Node{}
	if member := ancestorOrSelf(node, syntax.PhpScopedAccess, syntax.PhpMemberAccess); member != nil {
		candidates = append(candidates, member)
	}
	candidates = append(candidates, Nodes(node, syntax.PhpScopedAccess, syntax.PhpMemberAccess)...)
	for _, member := range candidates {
		text := compactExpression(member.Text())
		separator := strings.LastIndex(text, "::")
		if separator < 0 || !strings.EqualFold(text[separator+2:], "class") {
			continue
		}
		return strings.TrimSpace(text[:separator])
	}
	return ""
}

func Arrays(root *syntax.Node) []*syntax.Node {
	return Nodes(root, syntax.PhpArray)
}

func ArrayAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.PhpArray)
}

func ArrayItemAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.PhpArrayItem)
}

func ArrayItems(node *syntax.Node) []*syntax.Node {
	array := ArrayAt(node)
	if array == nil {
		return nil
	}
	var result []*syntax.Node
	for index := 0; index < array.ChildCount(); index++ {
		child, ok := array.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.PhpArrayItem {
			result = append(result, child)
		}
	}
	return result
}

// ArrayItemKey returns the expression left of => for an associative array
// entry. List-style entries do not have a key.
func ArrayItemKey(node *syntax.Node) *syntax.Node {
	item := ArrayItemAt(node)
	if item == nil || !hasDirectToken(item, syntax.TkArrow) {
		return nil
	}
	for index := 0; index < item.ChildCount(); index++ {
		if child, ok := item.Child(index).(*syntax.Node); ok {
			return child
		}
	}
	return nil
}

// ArrayItemValue returns the expression on the right of =>, or the sole
// expression for a list-style array item.
func ArrayItemValue(node *syntax.Node) *syntax.Node {
	item := ArrayItemAt(node)
	if item == nil {
		return nil
	}
	keyed := hasDirectToken(item, syntax.TkArrow)
	var first *syntax.Node
	for index := 0; index < item.ChildCount(); index++ {
		child, ok := item.Child(index).(*syntax.Node)
		if !ok {
			continue
		}
		if first == nil {
			first = child
			if !keyed {
				return child
			}
			continue
		}
		return child
	}
	return first
}

func NameValue(node *syntax.Node) string {
	if node == nil || node.Kind() != syntax.PhpName {
		return ""
	}
	text := node.Text()
	rng := node.Range()
	trimmed := node.RangeTrimmedTrivia()
	start := int(trimmed.Start - rng.Start)
	end := int(trimmed.End - rng.Start)
	if start >= 0 && start <= end && end <= len(text) {
		text = text[start:end]
	}
	return compactName(text)
}

func DirectChild(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	return directChild(node, kind)
}

func clauseNames(class *syntax.Node, kind syntax.Kind) []string {
	clause := directChild(class, kind)
	if clause == nil {
		return nil
	}
	var result []string
	for index := 0; index < clause.ChildCount(); index++ {
		child, ok := clause.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.PhpName {
			result = append(result, NameValue(child))
		}
	}
	return result
}

func directChild(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		child, ok := element.(*syntax.Node)
		if ok && child.Kind() == kind {
			return child
		}
	}
	return nil
}

func isTypeNode(kind syntax.Kind) bool {
	switch kind {
	case syntax.PhpType, syntax.PhpNullableType,
		syntax.PhpUnionType, syntax.PhpIntersectionType:
		return true
	default:
		return false
	}
}

func ancestorOrSelf(node *syntax.Node, kinds ...syntax.Kind) *syntax.Node {
	if node == nil {
		return nil
	}
	for current := node; current != nil; current = current.Parent() {
		if hasKind(current.Kind(), kinds) {
			return current
		}
	}
	return nil
}

func hasKind(value syntax.Kind, accepted []syntax.Kind) bool {
	for _, candidate := range accepted {
		if value == candidate {
			return true
		}
	}
	return false
}

func hasString(value string, accepted []string) bool {
	for _, candidate := range accepted {
		if value == candidate {
			return true
		}
	}
	return false
}

func contains(parent, child *syntax.Node) bool {
	if parent == nil || child == nil {
		return false
	}
	return parent.Range().Start <= child.Range().Start && child.Range().End <= parent.Range().End
}

func hasToken(node *syntax.Node, kind syntax.Kind) bool {
	return descendantToken(node, kind) != nil
}

func descendantToken(node *syntax.Node, kind syntax.Kind) *syntax.Token {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		switch child := node.Child(index).(type) {
		case *syntax.Token:
			if child.Kind() == kind {
				return child
			}
		case *syntax.Node:
			if token := descendantToken(child, kind); token != nil {
				return token
			}
		}
	}
	return nil
}

func descendantNode(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	if node == nil {
		return nil
	}
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*syntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == kind {
			return child
		}
		if descendant := descendantNode(child, kind); descendant != nil {
			return descendant
		}
	}
	return nil
}

func hasDirectToken(node *syntax.Node, kind syntax.Kind) bool {
	if node == nil {
		return false
	}
	for index := 0; index < node.ChildCount(); index++ {
		element := node.Child(index)
		if token, ok := element.(*syntax.Token); ok && token.Kind() == kind {
			return true
		}
	}
	return false
}

func compactName(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, text)
}

func compactExpression(text string) string {
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case ' ', '\t', '\r', '\n':
			return compactExpressionSlow(text)
		case '/':
			if index+1 < len(text) &&
				(text[index+1] == '/' || text[index+1] == '*') {
				return compactExpressionSlow(text)
			}
		}
	}
	return text
}

func compactExpressionSlow(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	inLineComment := false
	inBlockComment := false
	for index := 0; index < len(text); index++ {
		if inLineComment {
			if text[index] == '\n' || text[index] == '\r' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if index+1 < len(text) && text[index] == '*' && text[index+1] == '/' {
				inBlockComment = false
				index++
			}
			continue
		}
		if index+1 < len(text) && text[index] == '/' && text[index+1] == '/' {
			inLineComment = true
			index++
			continue
		}
		if index+1 < len(text) && text[index] == '/' && text[index+1] == '*' {
			inBlockComment = true
			index++
			continue
		}
		switch text[index] {
		case ' ', '\t', '\r', '\n':
			continue
		}
		builder.WriteByte(text[index])
	}
	return builder.String()
}
