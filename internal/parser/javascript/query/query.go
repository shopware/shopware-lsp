package query

import (
	"iter"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func Nodes(root *syntax.Node, kinds ...syntax.Kind) []*syntax.Node {
	if root == nil {
		return nil
	}
	accepted := make(map[syntax.Kind]struct{}, len(kinds))
	for _, kind := range kinds {
		accepted[kind] = struct{}{}
	}
	var result []*syntax.Node
	appendNodes(&result, root, accepted)
	return result
}

func appendNodes(
	result *[]*syntax.Node,
	node *syntax.Node,
	accepted map[syntax.Kind]struct{},
) {
	if node == nil {
		return
	}
	if _, ok := accepted[node.Kind()]; ok {
		*result = append(*result, node)
	}
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		appendNodes(result, cursor.Node(), accepted)
	}
}

// NodeIndex records a JavaScript tree's nodes once for analyses that issue
// several whole-document queries. The returned slices are immutable views and
// remain valid for the lifetime of the parsed tree.
type NodeIndex struct {
	root   *syntax.Node
	byKind map[syntax.Kind][]*syntax.Node
}

func NewNodeIndex(root *syntax.Node) *NodeIndex {
	index := &NodeIndex{
		root:   root,
		byKind: make(map[syntax.Kind][]*syntax.Node),
	}
	index.append(root)
	return index
}

func (index *NodeIndex) append(node *syntax.Node) {
	if node == nil {
		return
	}
	kind := node.Kind()
	index.byKind[kind] = append(index.byKind[kind], node)
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		index.append(cursor.Node())
	}
}

func (index *NodeIndex) Nodes(kinds ...syntax.Kind) []*syntax.Node {
	if index == nil || len(kinds) == 0 {
		return nil
	}
	if len(kinds) == 1 {
		return index.byKind[kinds[0]]
	}
	return Nodes(index.root, kinds...)
}

func (index *NodeIndex) Calls(names ...string) []*syntax.Node {
	if index == nil {
		return nil
	}
	accepted := make(map[string]struct{}, len(names))
	for _, name := range names {
		accepted[name] = struct{}{}
	}
	var result []*syntax.Node
	for _, call := range index.Nodes(syntax.JsCallExpression) {
		if len(accepted) == 0 {
			result = append(result, call)
			continue
		}
		if _, found := accepted[CallName(call)]; found {
			result = append(result, call)
		}
	}
	return result
}

func (index *NodeIndex) IterateCalls(names ...string) iter.Seq[*syntax.Node] {
	return func(yield func(*syntax.Node) bool) {
		if index == nil {
			return
		}
		for _, call := range index.Nodes(syntax.JsCallExpression) {
			if len(names) != 0 && !slices.Contains(names, CallName(call)) {
				continue
			}
			if !yield(call) {
				return
			}
		}
	}
}

func Calls(root *syntax.Node, names ...string) []*syntax.Node {
	accepted := make(map[string]struct{}, len(names))
	for _, name := range names {
		accepted[name] = struct{}{}
	}
	var result []*syntax.Node
	appendCalls(&result, root, accepted)
	return result
}

func IterateCalls(
	root *syntax.Node,
	names ...string,
) iter.Seq[*syntax.Node] {
	return func(yield func(*syntax.Node) bool) {
		iterateCalls(root, names, yield)
	}
}

func iterateCalls(
	node *syntax.Node,
	names []string,
	yield func(*syntax.Node) bool,
) bool {
	if node == nil {
		return true
	}
	if node.Kind() == syntax.JsCallExpression &&
		(len(names) == 0 || slices.Contains(names, CallName(node))) &&
		!yield(node) {
		return false
	}
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		if !iterateCalls(cursor.Node(), names, yield) {
			return false
		}
	}
	return true
}

func appendCalls(
	result *[]*syntax.Node,
	node *syntax.Node,
	accepted map[string]struct{},
) {
	if node == nil {
		return
	}
	if node.Kind() == syntax.JsCallExpression {
		if len(accepted) == 0 {
			*result = append(*result, node)
		} else if _, found := accepted[CallName(node)]; found {
			*result = append(*result, node)
		}
	}
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		appendCalls(result, cursor.Node(), accepted)
	}
}

func CallAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.JsCallExpression)
}

// CallCallee returns the direct expression invoked by a call. Descendants of
// a call are accepted for parity with the other cursor-oriented queries.
func CallCallee(node *syntax.Node) *syntax.Node {
	call := ancestorOrSelf(node, syntax.JsCallExpression)
	if call == nil {
		return nil
	}
	cursor := call.ChildNodeCursor()
	if cursor.Next() {
		return cursor.Node()
	}
	return nil
}

func CallName(node *syntax.Node) string {
	call := ancestorOrSelf(node, syntax.JsCallExpression)
	if call == nil {
		return ""
	}
	for index := 0; index < call.ChildCount(); index++ {
		child, ok := call.Child(index).(*syntax.Node)
		if !ok {
			continue
		}
		switch child.Kind() {
		case syntax.JsMemberExpression, syntax.JsIdentifier:
			return compactNodeExpression(child)
		case syntax.JsCallExpression:
			return CallName(child)
		}
	}
	return ""
}

// CallMethodName returns the terminal identifier of a call's callee. Unlike
// CallName, it remains stable for fluent chains where the receiver contains a
// previous call and its arguments.
//
// For example, both Application.addServiceProvider(...) and
// Application.addServiceProvider(...).addServiceProvider(...) return
// "addServiceProvider".
func CallMethodName(node *syntax.Node) string {
	call := ancestorOrSelf(node, syntax.JsCallExpression)
	if call == nil {
		return ""
	}
	cursor := call.ChildNodeCursor()
	if !cursor.Next() {
		return ""
	}
	callee := cursor.Node()
	if callee.Kind() == syntax.JsIdentifier {
		return strings.TrimSpace(callee.Text())
	}
	if callee.Kind() != syntax.JsMemberExpression {
		return ""
	}
	var method string
	memberCursor := callee.ChildNodeCursor()
	for memberCursor.Next() {
		child := memberCursor.Node()
		if child.Kind() == syntax.JsIdentifier {
			method = strings.TrimSpace(child.Text())
		}
	}
	return method
}

// MemberNameNode returns the terminal identifier of a member expression.
func MemberNameNode(node *syntax.Node) *syntax.Node {
	if node == nil {
		return nil
	}
	if node.Kind() == syntax.JsIdentifier {
		return node
	}
	if node.Kind() != syntax.JsMemberExpression {
		return nil
	}
	var result *syntax.Node
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.JsIdentifier {
			result = child
		}
	}
	return result
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

type ArgumentIterator struct {
	list    *syntax.Node
	index   int
	current *syntax.Node
}

func IterateArguments(node *syntax.Node) ArgumentIterator {
	call := ancestorOrSelf(node, syntax.JsCallExpression)
	return ArgumentIterator{list: directChild(call, syntax.JsArgumentList)}
}

func (iterator ArgumentIterator) Len() int {
	if iterator.list == nil {
		return 0
	}
	count := 0
	for index := 0; index < iterator.list.ChildCount(); index++ {
		child, ok := iterator.list.Child(index).(*syntax.Node)
		if ok && child.Kind() == syntax.JsArgument {
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
		if ok && child.Kind() == syntax.JsArgument {
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

func ArgumentExpression(node *syntax.Node, index int) *syntax.Node {
	argument := Argument(node, index)
	if argument == nil {
		return nil
	}
	for index := 0; index < argument.ChildCount(); index++ {
		if child, ok := argument.Child(index).(*syntax.Node); ok {
			return child
		}
	}
	return nil
}

func StringArgument(node *syntax.Node, index int) *syntax.Node {
	argument := Argument(node, index)
	if argument == nil {
		return nil
	}
	for element := range argument.Descendants() {
		if stringNode, ok := element.(*syntax.Node); ok && stringNode.Kind() == syntax.JsString {
			return stringNode
		}
	}
	return nil
}

func ObjectArgument(node *syntax.Node, index int) *syntax.Node {
	argument := Argument(node, index)
	if argument == nil {
		return nil
	}
	for element := range argument.Descendants() {
		if object, ok := element.(*syntax.Node); ok && object.Kind() == syntax.JsObject {
			return object
		}
	}
	return nil
}

func StringAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.JsString)
}

func StringValue(node *syntax.Node) string {
	stringNode := ancestorOrSelf(node, syntax.JsString)
	if stringNode == nil {
		return ""
	}
	text := ""
	for element := range stringNode.Descendants() {
		token, ok := element.(*syntax.Token)
		if ok && (token.Kind() == syntax.TkString || token.Kind() == syntax.TkTemplate) {
			text = token.Text()
			break
		}
	}
	if text == "" {
		return ""
	}
	if len(text) > 0 && (text[0] == '\'' || text[0] == '"' || text[0] == '`') {
		quote := text[0]
		text = text[1:]
		if len(text) > 0 && text[len(text)-1] == quote {
			text = text[:len(text)-1]
		}
	}
	return text
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
	iterator := IterateArguments(call)
	index := 0
	for iterator.Next() {
		if contains(iterator.Node(), stringNode) {
			return index
		}
		index++
	}
	return -1
}

// StringInCall reports whether node is the string at argumentIndex of one of
// the named calls. It accepts any descendant at the cursor position.
func StringInCall(node *syntax.Node, argumentIndex int, names ...string) bool {
	if StringAt(node) == nil || StringArgumentIndex(node) != argumentIndex {
		return false
	}
	callName := CallName(node)
	for _, name := range names {
		if callName == name {
			return true
		}
	}
	return false
}

func ExportDefaults(root *syntax.Node) []*syntax.Node {
	return Nodes(root, syntax.JsExportDefault)
}

func ExportDefaultExpression(node *syntax.Node) *syntax.Node {
	exportNode := ancestorOrSelf(node, syntax.JsExportDefault)
	if exportNode == nil {
		return nil
	}
	cursor := exportNode.ChildNodeCursor()
	if cursor.Next() {
		return cursor.Node()
	}
	return nil
}

func Properties(object *syntax.Node) []*syntax.Node {
	if object == nil || object.Kind() != syntax.JsObject {
		return nil
	}
	var result []*syntax.Node
	cursor := object.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.JsProperty || child.Kind() == syntax.JsMethod {
			result = append(result, child)
		}
	}
	return result
}

func Property(object *syntax.Node, name string) *syntax.Node {
	if object == nil || object.Kind() != syntax.JsObject {
		return nil
	}
	cursor := object.ChildNodeCursor()
	for cursor.Next() {
		property := cursor.Node()
		if property.Kind() != syntax.JsProperty &&
			property.Kind() != syntax.JsMethod {
			continue
		}
		if PropertyName(property) == name {
			return property
		}
	}
	return nil
}

// PropertyAt returns the closest property containing node. It is useful for
// string-valued configuration APIs where the property name determines the
// reference domain.
func PropertyAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.JsProperty)
}

func PropertyName(node *syntax.Node) string {
	name := PropertyNameNode(node)
	if name == nil {
		return ""
	}
	switch name.Kind() {
	case syntax.JsString:
		return StringValue(name)
	case syntax.JsIdentifier:
		return identifierTokenText(name)
	default:
		return strings.TrimSpace(name.Text())
	}
}

func PropertyNameNode(node *syntax.Node) *syntax.Node {
	if node == nil || (node.Kind() != syntax.JsProperty && node.Kind() != syntax.JsMethod) {
		return nil
	}
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		switch child.Kind() {
		case syntax.JsIdentifier, syntax.JsString:
			return child
		}
	}
	return nil
}

func PropertyValue(node *syntax.Node) *syntax.Node {
	if node == nil || node.Kind() != syntax.JsProperty {
		return nil
	}
	seenName := false
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if !seenName {
			seenName = true
			continue
		}
		return child
	}
	return nil
}

func MethodName(node *syntax.Node) string {
	if node == nil || node.Kind() != syntax.JsMethod {
		return ""
	}
	return PropertyName(node)
}

func ArrayItems(array *syntax.Node) []*syntax.Node {
	if array == nil || array.Kind() != syntax.JsArray {
		return nil
	}
	var result []*syntax.Node
	cursor := array.ChildNodeCursor()
	for cursor.Next() {
		result = append(result, cursor.Node())
	}
	return result
}

func ObjectAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.JsObject)
}

// MemberExpressionAt returns the closest member expression containing node.
func MemberExpressionAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.JsMemberExpression)
}

// ThisMember reports a direct Vue instance member expression. Nested member
// access such as this.repository.search only matches while the cursor is on
// repository; search belongs to the repository value rather than the component.
func ThisMember(node *syntax.Node) (string, bool) {
	member := MemberExpressionAt(node)
	if member == nil {
		return "", false
	}
	text := compactNodeExpression(member)
	text = strings.ReplaceAll(text, "?.", ".")
	if !strings.HasPrefix(text, "this.") {
		return "", false
	}
	name := strings.TrimPrefix(text, "this.")
	if strings.ContainsAny(name, ".[()") {
		return "", false
	}
	return name, true
}

func ThisMemberNameNode(node *syntax.Node) *syntax.Node {
	member := MemberExpressionAt(node)
	if _, matched := ThisMember(member); !matched || member == nil {
		return nil
	}
	var result *syntax.Node
	cursor := member.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.JsIdentifier {
			result = child
		}
	}
	return result
}

// StoreMember reports a direct member accessed from a Shopware Store lookup.
// Nested access is intentionally conservative: for
// Shopware.Store.get('session').currentUser.id, currentUser matches while id
// does not, because id belongs to the currentUser value rather than the store.
func StoreMember(node *syntax.Node) (storeName, memberName string, matched bool) {
	member := MemberExpressionAt(node)
	if member == nil {
		return "", "", false
	}
	cursor := member.ChildNodeCursor()
	if !cursor.Next() {
		return "", "", false
	}
	receiver := cursor.Node()
	if receiver.Kind() != syntax.JsCallExpression {
		return "", "", false
	}
	callName := CallName(receiver)
	if callName != "Shopware.Store.get" && callName != "Store.get" {
		return "", "", false
	}
	storeName = StringValue(StringArgument(receiver, 0))
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.JsIdentifier {
			memberName = strings.TrimSpace(child.Text())
		}
	}
	return storeName, memberName, storeName != ""
}

func ImportPath(root *syntax.Node, binding string) string {
	for _, statement := range Nodes(root, syntax.JsImportStatement) {
		if binding != "" && !hasIdentifier(statement, binding) {
			continue
		}
		for _, stringNode := range Nodes(statement, syntax.JsString) {
			return StringValue(stringNode)
		}
	}
	return ""
}

func DynamicImportPath(root *syntax.Node) string {
	for _, call := range Calls(root, "import") {
		if path := StringArgument(call, 0); path != nil {
			return StringValue(path)
		}
	}
	return ""
}

func IdentifierText(node *syntax.Node) string {
	identifier := ancestorOrSelf(node, syntax.JsIdentifier)
	if identifier == nil {
		return ""
	}
	return identifierTokenText(identifier)
}

func identifierTokenText(identifier *syntax.Node) string {
	if identifier == nil {
		return ""
	}
	cursor := identifier.ChildTokenCursor()
	for cursor.Next() {
		token := cursor.Token()
		if token.Kind() == syntax.TkIdentifier || token.Kind() == syntax.TkKeyword {
			return token.Text()
		}
	}
	return strings.TrimSpace(identifier.Text())
}

func directChild(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	if node == nil {
		return nil
	}
	cursor := node.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

func ancestorOrSelf(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func contains(parent, child *syntax.Node) bool {
	if parent == nil || child == nil {
		return false
	}
	parentRange := parent.Range()
	childRange := child.Range()
	return parentRange.Start <= childRange.Start && childRange.End <= parentRange.End
}

func hasIdentifier(node *syntax.Node, name string) bool {
	for _, identifier := range Nodes(node, syntax.JsIdentifier) {
		if strings.TrimSpace(identifier.Text()) == name {
			return true
		}
	}
	for element := range node.Descendants() {
		token, ok := element.(*syntax.Token)
		if ok && token.Kind() == syntax.TkIdentifier && token.Text() == name {
			return true
		}
	}
	return false
}

func compactNodeExpression(node *syntax.Node) string {
	if node == nil {
		return ""
	}
	text := strings.TrimSpace(node.Text())
	if !mayContainJavaScriptTrivia(text) {
		return text
	}
	var builder strings.Builder
	builder.Grow(len(text))
	appendCompactNodeExpression(&builder, node)
	return builder.String()
}

func mayContainJavaScriptTrivia(text string) bool {
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case ' ', '\t', '\f', '\r', '\n':
			return true
		case '/':
			if index+1 < len(text) &&
				(text[index+1] == '/' || text[index+1] == '*') {
				return true
			}
		}
	}
	return false
}

func appendCompactNodeExpression(
	builder *strings.Builder,
	node *syntax.Node,
) {
	for index := 0; index < node.ChildCount(); index++ {
		switch child := node.Child(index).(type) {
		case *syntax.Node:
			appendCompactNodeExpression(builder, child)
		case *syntax.Token:
			if !child.Kind().IsTrivia() {
				builder.WriteString(child.Text())
			}
		}
	}
}
