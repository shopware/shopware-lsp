package admin

import (
	"bytes"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// TwigVueBindingKind describes a name introduced by Vue template syntax.
// These bindings are document-local and deliberately remain outside the
// persistent Administration symbol index.
type TwigVueBindingKind string

const (
	TwigVueBindingFor   TwigVueBindingKind = "v-for"
	TwigVueBindingEvent TwigVueBindingKind = "event"
)

// TwigVueBinding is one lexically scoped Vue template variable. DeclarationRange
// is empty for Vue's implicit $event local. DefinitionPath/DefinitionLine are
// populated when an indexed component event provides an external declaration.
type TwigVueBinding struct {
	Name             string
	Kind             TwigVueBindingKind
	Ordinal          int
	DeclarationRange cst.TextRange
	ScopeRange       cst.TextRange
	ExpressionRange  cst.TextRange
	Iterable         string
	ComponentName    string
	EventName        string
	Type             string
	DefinitionPath   string
	DefinitionLine   int
	Members          []TwigVueMember
	MembersComplete  bool
	TypeContextPath  string
}

// TwigVueMember is a direct property observed on one lexical Vue binding.
// Vue templates are frequently backed by untyped JavaScript, so the lexical
// shape remains useful even when no external TypeScript declaration exists.
// Range is the first observed occurrence and is never treated as a declaration.
type TwigVueMember struct {
	Name            string
	Type            string
	Documentation   string
	Optional        bool
	NestedMembers   []TwigVueMember
	NestedComplete  bool
	Range           cst.TextRange
	DefinitionPath  string
	DefinitionLine  int
	DefinitionRange AdminSourceRange
}

// TwigVueMemberAccess is one safe root.member access in an evaluated Vue
// expression. An empty Member is valid at a completion cursor immediately
// after the dot. Receiver segments may include statically inspectable indexed
// access; the type resolver decides whether the receiver contract makes that
// access sound.
type TwigVueMemberAccess struct {
	Root         string
	RootCalled   bool
	Member       string
	MemberCalled bool
	RootRange    cst.TextRange
	MemberRange  cst.TextRange
	Receiver     []TwigVueMemberSegment
}

// TwigVueMemberSegment is one operation between the lexical root and the
// member under the cursor. For row.product.manufacturer.name, resolving name
// yields product and manufacturer as Receiver segments. Indexed segments
// preserve their source expression so typed arrays, tuples, and Records can be
// resolved without treating arbitrary JavaScript objects as dictionaries.
type TwigVueMemberSegment struct {
	Name            string
	Range           cst.TextRange
	Called          bool
	Indexed         bool
	Optional        bool
	IndexExpression string
	IndexRange      cst.TextRange
}

// TwigVueCall identifies the innermost statically named call containing the
// cursor in an evaluated Administration template expression. Vue directive
// values are stored as lossless HTML tokens rather than Twig function nodes,
// so signature help uses this lexical representation for both directive and
// interpolation syntax.
type TwigVueCall struct {
	Name           string
	NameRange      cst.TextRange
	ActiveArgument int
	Filter         bool
}

// TwigVueCallSite is one complete statically named call in an evaluated
// Administration template expression. Argument ranges exclude surrounding
// whitespace and retain the exact source range of nested expressions.
type TwigVueCallSite struct {
	TwigVueCall
	Range     cst.TextRange
	OpenParen uint32
	Arguments []cst.TextRange
}

// ResolvedTwigVueMember joins a safe property chain with its lexical v-for or
// event binding and the structural type reached by its receiver.
type ResolvedTwigVueMember struct {
	Binding         TwigVueBinding
	Access          TwigVueMemberAccess
	Member          TwigVueMember
	MemberFound     bool
	ReceiverFound   bool
	ReceiverType    string
	ReceiverMembers []TwigVueMember
	MembersComplete bool
}

// ResolvedTwigVueInstanceMember joins a property chain rooted in an indexed
// component prop/data/computed value with the structural type reached by its
// receiver. Lexical v-for, event, and scoped-slot locals are excluded by the
// resolver so component scope never leaks through a shadowing declaration.
type ResolvedTwigVueInstanceMember struct {
	Component       VueComponent
	RootMember      VueComponentMember
	Access          TwigVueMemberAccess
	Member          TwigVueMember
	MemberFound     bool
	ReceiverFound   bool
	ReceiverType    string
	ReceiverMembers []TwigVueMember
	MembersComplete bool
}

func (resolved ResolvedTwigVueInstanceMember) QualifiedName() string {
	return resolved.Access.QualifiedName()
}

func (access TwigVueMemberAccess) MemberPath() []string {
	result := make([]string, 0, len(access.Receiver)+1)
	for _, segment := range access.Receiver {
		if segment.Indexed {
			result = append(result, "["+strings.TrimSpace(segment.IndexExpression)+"]")
			continue
		}
		result = append(result, segment.Name)
	}
	if access.Member != "" {
		result = append(result, access.Member)
	}
	return result
}

// QualifiedName renders the safe chain as it appears semantically, retaining
// call markers while MemberPath remains name-only for reference identity.
func (access TwigVueMemberAccess) QualifiedName() string {
	result := access.Root
	if access.RootCalled {
		result += "()"
	}
	for _, segment := range access.Receiver {
		if segment.Indexed {
			if segment.Optional {
				result += "?."
			}
			result += "[" + strings.TrimSpace(segment.IndexExpression) + "]"
			if segment.Called {
				result += "()"
			}
			continue
		}
		name := segment.Name
		if segment.Called {
			name += "()"
		}
		result += "." + name
	}
	if access.Member != "" {
		member := access.Member
		if access.MemberCalled {
			member += "()"
		}
		result += "." + member
	}
	return result
}

// SamePath compares the semantic member chain, including which named
// segments are invoked. Source ranges are intentionally ignored.
func (access TwigVueMemberAccess) SamePath(other TwigVueMemberAccess) bool {
	if access.Root != other.Root || access.RootCalled != other.RootCalled ||
		access.Member != other.Member ||
		access.MemberCalled != other.MemberCalled ||
		len(access.Receiver) != len(other.Receiver) {
		return false
	}
	for index := range access.Receiver {
		if access.Receiver[index].Name != other.Receiver[index].Name ||
			access.Receiver[index].Called != other.Receiver[index].Called ||
			access.Receiver[index].Indexed != other.Receiver[index].Indexed ||
			strings.TrimSpace(access.Receiver[index].IndexExpression) !=
				strings.TrimSpace(other.Receiver[index].IndexExpression) {
			return false
		}
	}
	return true
}

func (binding TwigVueBinding) IsDeclarationOffset(offset uint32) bool {
	return binding.DeclarationRange.Len() > 0 &&
		offset >= binding.DeclarationRange.Start &&
		offset <= binding.DeclarationRange.End
}

func (binding TwigVueBinding) sameIdentity(other TwigVueBinding) bool {
	return binding.Kind == other.Kind && binding.Name == other.Name &&
		binding.DeclarationRange == other.DeclarationRange &&
		binding.ScopeRange == other.ScopeRange
}

// TwigVueBindings returns all v-for declarations and implicit event locals in
// a template. Callers use the scope ranges to resolve shadowing at a cursor.
func TwigVueBindings(
	root *twigsyntax.Node,
	content []byte,
) []TwigVueBinding {
	if root == nil {
		return nil
	}
	var result []TwigVueBinding
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlTag) {
		tag, ok := twigast.CastHtmlTag(node)
		if !ok || tag.Name() == nil {
			continue
		}
		startingTag, ok := tag.StartingTag()
		if !ok {
			continue
		}
		for _, attribute := range startingTag.Attributes() {
			name := twigquery.HTMLAttributeName(attribute.Syntax())
			value, hasValue := attribute.Value()
			if !hasValue {
				continue
			}
			inner, hasInner := value.GetInner()
			if !hasInner {
				continue
			}
			expressionRange := inner.Syntax().RangeTrimmedTrivia()
			if expressionRange.End > uint32(len(content)) {
				continue
			}
			switch {
			case name == "v-for":
				result = append(result, parseTwigVueForBindings(
					inner.Syntax().Text(), expressionRange.Start,
					node.RangeTrimmedTrivia(), expressionRange,
				)...)
			case NormalizeEventName(name) != "":
				result = append(result, TwigVueBinding{
					Name: "$event", Kind: TwigVueBindingEvent,
					ScopeRange: expressionRange, ExpressionRange: expressionRange,
					ComponentName: tag.Name().Text(),
					EventName:     NormalizeEventName(name),
				})
			}
		}
	}
	return result
}

// TwigVueBindingsAtOffset returns the effective lexical Vue bindings at an
// offset. Innermost declarations replace outer declarations of the same name.
// A v-for alias is not visible in its own iterable expression.
func TwigVueBindingsAtOffset(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) []TwigVueBinding {
	return twigVueBindingsAtOffset(TwigVueBindings(root, content), offset)
}

func twigVueBindingsAtOffset(
	bindings []TwigVueBinding,
	offset uint32,
) []TwigVueBinding {
	visible := make([]TwigVueBinding, 0)
	for _, binding := range bindings {
		if offset < binding.ScopeRange.Start || offset > binding.ScopeRange.End {
			continue
		}
		if binding.Kind == TwigVueBindingFor &&
			offset >= binding.ExpressionRange.Start &&
			offset <= binding.ExpressionRange.End &&
			!binding.IsDeclarationOffset(offset) {
			continue
		}
		visible = append(visible, binding)
	}
	// Apply outer scopes first and innermost scopes last. Stable ordering keeps
	// tuple bindings in their source order.
	sort.SliceStable(visible, func(left, right int) bool {
		return visible[left].ScopeRange.Len() > visible[right].ScopeRange.Len()
	})
	positions := make(map[string]int, len(visible))
	result := make([]TwigVueBinding, 0, len(visible))
	for _, binding := range visible {
		if index, exists := positions[binding.Name]; exists {
			result[index] = binding
			continue
		}
		positions[binding.Name] = len(result)
		result = append(result, binding)
	}
	return result
}

// TwigVueBindingAtOffset resolves the Vue lexical variable touching offset.
func TwigVueBindingAtOffset(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (*TwigVueBinding, bool) {
	bindings := TwigVueBindings(root, content)
	for _, binding := range bindings {
		if binding.IsDeclarationOffset(offset) {
			resolved := binding
			return &resolved, true
		}
	}
	name, _, found := TwigVueExpressionRootIdentifierAtOffset(
		root, content, offset,
	)
	if !found {
		return nil, false
	}
	for _, binding := range twigVueBindingsAtOffset(bindings, offset) {
		if binding.Name == name {
			resolved := binding
			return &resolved, true
		}
	}
	return nil, false
}

// TwigVueBindingReferences finds a declaration and all root-identifier usages
// resolving to the same lexical binding. Strings, member property names and
// object literal keys are excluded by the expression-aware resolver.
func TwigVueBindingReferences(
	root *twigsyntax.Node,
	content []byte,
	target TwigVueBinding,
) []cst.TextRange {
	if root == nil || target.Name == "" {
		return nil
	}
	bindings := TwigVueBindings(root, content)
	var result []cst.TextRange
	seen := make(map[cst.TextRange]bool)
	add := func(value cst.TextRange) {
		if value.Len() == 0 || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	add(target.DeclarationRange)
	for start := 0; start < len(content); {
		relative := bytes.Index(content[start:], []byte(target.Name))
		if relative < 0 {
			break
		}
		position := start + relative
		end := position + len(target.Name)
		start = end
		if position > 0 && isSlotIdentifierContinue(content[position-1]) ||
			end < len(content) && isSlotIdentifierContinue(content[end]) {
			continue
		}
		name, rangeValue, found := TwigVueExpressionRootIdentifierAtOffset(
			root, content, uint32(position),
		)
		if !found || name != target.Name {
			continue
		}
		for _, candidate := range twigVueBindingsAtOffset(
			bindings, uint32(position),
		) {
			if candidate.Name == name && candidate.sameIdentity(target) {
				add(rangeValue)
				break
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Start < result[right].Start
	})
	return result
}

// TwigVueExpressionMemberAtOffset resolves a statically inspectable property
// chain rooted in one identifier. Calls and bracket access are retained as
// receiver operations while literal/comment text remains excluded. Resolution
// still rejects computed access unless the indexed receiver has a sound typed
// contract such as an array, tuple, or Record.
func TwigVueExpressionMemberAtOffset(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (TwigVueMemberAccess, bool) {
	if root == nil || offset > uint32(len(content)) {
		return TwigVueMemberAccess{}, false
	}
	nodeOffset := offset
	if nodeOffset == uint32(len(content)) && nodeOffset > 0 {
		nodeOffset--
	}
	node := root.NodeAtOffset(nodeOffset)
	if node == nil || !IsTwigVueExpressionAt(node, offset) {
		return TwigVueMemberAccess{}, false
	}
	expressionRange, found := twigVueExpressionRange(node)
	if !found || offset < expressionRange.Start || offset > expressionRange.End {
		return TwigVueMemberAccess{}, false
	}

	member, memberRange, hasMember := IdentifierAtOffset(content, offset)
	dot := int(offset) - 1
	if hasMember {
		dot = int(memberRange.Start) - 1
	} else {
		memberRange = cst.TextRange{Start: offset, End: offset}
	}
	for dot >= int(expressionRange.Start) && isSlotSpace(content[dot]) {
		dot--
	}
	if dot < int(expressionRange.Start) || content[dot] != '.' {
		return TwigVueMemberAccess{}, false
	}
	receiverEnd := dot
	if receiverEnd > int(expressionRange.Start) && content[receiverEnd-1] == '?' {
		receiverEnd--
	}
	for receiverEnd > int(expressionRange.Start) &&
		isSlotSpace(content[receiverEnd-1]) {
		receiverEnd--
	}
	var reversed []TwigVueMemberSegment
	cursor := receiverEnd
	rootStart := receiverEnd
	for {
		segment, segmentStart, segmentFound := twigVueChainSegmentBefore(
			content, int(expressionRange.Start), cursor,
		)
		if !segmentFound {
			return TwigVueMemberAccess{}, false
		}
		reversed = append(reversed, segment)
		rootStart = segmentStart
		if segment.Indexed {
			if segmentStart <= int(expressionRange.Start) {
				return TwigVueMemberAccess{}, false
			}
			cursor = segmentStart
			continue
		}
		previous := segmentStart - 1
		for previous >= int(expressionRange.Start) && isSlotSpace(content[previous]) {
			previous--
		}
		if previous < int(expressionRange.Start) || content[previous] != '.' {
			break
		}
		cursor = previous
		if cursor > int(expressionRange.Start) && content[cursor-1] == '?' {
			cursor--
		}
		for cursor > int(expressionRange.Start) && isSlotSpace(content[cursor-1]) {
			cursor--
		}
	}
	previous := rootStart - 1
	for previous >= int(expressionRange.Start) && isSlotSpace(content[previous]) {
		previous--
	}
	if previous >= int(expressionRange.Start) &&
		(content[previous] == '.' || content[previous] == ']' ||
			content[previous] == ')') {
		return TwigVueMemberAccess{}, false
	}
	segments := make([]TwigVueMemberSegment, len(reversed))
	for index := range reversed {
		segments[len(reversed)-1-index] = reversed[index]
	}
	if len(segments) == 0 || segments[0].Indexed || segments[0].Name == "" {
		return TwigVueMemberAccess{}, false
	}
	rootRange := segments[0].Range
	if vueExpressionPositionInLiteralOrComment(
		content[expressionRange.Start:rootRange.Start],
	) {
		return TwigVueMemberAccess{}, false
	}
	if hasMember && vueExpressionPositionInLiteralOrComment(
		content[expressionRange.Start:memberRange.Start],
	) {
		return TwigVueMemberAccess{}, false
	}
	return TwigVueMemberAccess{
		Root: segments[0].Name, RootCalled: segments[0].Called,
		Member: member, MemberCalled: twigVueMemberCalledAfter(
			content, int(memberRange.End), int(expressionRange.End),
		),
		RootRange: rootRange, MemberRange: memberRange,
		Receiver: append([]TwigVueMemberSegment(nil), segments[1:]...),
	}, true
}

func twigVueChainSegmentBefore(
	content []byte,
	expressionStart,
	cursor int,
) (TwigVueMemberSegment, int, bool) {
	for cursor > expressionStart && isSlotSpace(content[cursor-1]) {
		cursor--
	}
	if cursor > expressionStart && content[cursor-1] == ']' {
		close := cursor - 1
		open := matchingVueIndexOpen(content, expressionStart, close)
		if open < expressionStart {
			return TwigVueMemberSegment{}, cursor, false
		}
		segmentStart := open
		optional := false
		if open >= expressionStart+2 && content[open-1] == '.' &&
			content[open-2] == '?' {
			segmentStart = open - 2
			optional = true
		}
		return TwigVueMemberSegment{
			Range: cst.TextRange{
				Start: uint32(segmentStart), End: uint32(cursor),
			},
			Indexed: true, Optional: optional,
			IndexExpression: strings.TrimSpace(string(content[open+1 : close])),
			IndexRange: cst.TextRange{
				Start: uint32(open + 1), End: uint32(close),
			},
		}, segmentStart, true
	}
	called := false
	if cursor > expressionStart && content[cursor-1] == ')' {
		open := matchingVueCallOpen(content, expressionStart, cursor-1)
		if open < expressionStart {
			return TwigVueMemberSegment{}, cursor, false
		}
		cursor = open
		for cursor > expressionStart && isSlotSpace(content[cursor-1]) {
			cursor--
		}
		if cursor >= expressionStart+2 && content[cursor-1] == '.' &&
			content[cursor-2] == '?' {
			cursor -= 2
			for cursor > expressionStart && isSlotSpace(content[cursor-1]) {
				cursor--
			}
		}
		called = true
	}
	segmentEnd := cursor
	segmentStart := segmentEnd
	for segmentStart > expressionStart &&
		isSlotIdentifierContinue(content[segmentStart-1]) {
		segmentStart--
	}
	if segmentStart == segmentEnd ||
		!isSlotIdentifierStart(content[segmentStart]) {
		return TwigVueMemberSegment{}, cursor, false
	}
	return TwigVueMemberSegment{
		Name: string(content[segmentStart:segmentEnd]),
		Range: cst.TextRange{
			Start: uint32(segmentStart), End: uint32(segmentEnd),
		},
		Called: called,
	}, segmentStart, true
}

func matchingVueIndexOpen(content []byte, start, close int) int {
	if start < 0 || close <= start || close >= len(content) ||
		content[close] != ']' {
		return -1
	}
	value := string(content[start : close+1])
	for candidate := 0; candidate < len(value); candidate++ {
		if value[candidate] != '[' || vueExpressionPositionInLiteralOrComment(
			content[start:start+candidate],
		) {
			continue
		}
		if matchingSlotDelimiter(value, candidate, '[', ']') == len(value)-1 {
			return start + candidate
		}
	}
	return -1
}

func matchingVueCallOpen(
	content []byte,
	start,
	close int,
) int {
	if start < 0 || close <= start || close >= len(content) ||
		content[close] != ')' {
		return -1
	}
	value := string(content[start : close+1])
	for candidate := 0; candidate < len(value); candidate++ {
		if value[candidate] != '(' || vueExpressionPositionInLiteralOrComment(
			content[start:start+candidate],
		) {
			continue
		}
		if matchingSlotDelimiter(value, candidate, '(', ')') == len(value)-1 {
			return start + candidate
		}
	}
	return -1
}

func twigVueMemberCalledAfter(
	content []byte,
	cursor,
	expressionEnd int,
) bool {
	if cursor < 0 || cursor > len(content) {
		return false
	}
	if expressionEnd > len(content) {
		expressionEnd = len(content)
	}
	for cursor < expressionEnd && isSlotSpace(content[cursor]) {
		cursor++
	}
	if cursor+1 < expressionEnd && content[cursor] == '?' &&
		content[cursor+1] == '.' {
		cursor += 2
		for cursor < expressionEnd && isSlotSpace(content[cursor]) {
			cursor++
		}
	}
	return cursor < expressionEnd && content[cursor] == '('
}

// TwigVueExpressionMemberAccesses returns all complete member accesses in
// evaluated Vue expressions in source order.
func TwigVueExpressionMemberAccesses(
	root *twigsyntax.Node,
	content []byte,
) []TwigVueMemberAccess {
	if root == nil {
		return nil
	}
	var result []TwigVueMemberAccess
	seen := make(map[cst.TextRange]bool)
	for position := 0; position < len(content); position++ {
		if content[position] != '.' {
			continue
		}
		member := position + 1
		for member < len(content) && isSlotSpace(content[member]) {
			member++
		}
		if member >= len(content) || !isSlotIdentifierStart(content[member]) {
			continue
		}
		access, found := TwigVueExpressionMemberAtOffset(
			root, content, uint32(member),
		)
		if !found || access.Member == "" || seen[access.MemberRange] {
			continue
		}
		seen[access.MemberRange] = true
		result = append(result, access)
	}
	return result
}

// TwigVueExpressionRootIdentifiers returns root identifiers from evaluated
// Vue expressions in source order. Member names, object keys, strings, and
// comments are excluded by the same lexical resolver used for hover and
// navigation.
func TwigVueExpressionRootIdentifiers(
	root *twigsyntax.Node,
	content []byte,
) []TwigVueMember {
	if root == nil {
		return nil
	}
	var result []TwigVueMember
	seen := make(map[cst.TextRange]bool)
	for position := 0; position < len(content); {
		if !isSlotIdentifierStart(content[position]) ||
			position > 0 && isSlotIdentifierContinue(content[position-1]) {
			position++
			continue
		}
		end := position + 1
		for end < len(content) && isSlotIdentifierContinue(content[end]) {
			end++
		}
		name, rangeValue, found := TwigVueExpressionRootIdentifierAtOffset(
			root, content, uint32(position),
		)
		if found && rangeValue.Start == uint32(position) && !seen[rangeValue] {
			seen[rangeValue] = true
			result = append(result, TwigVueMember{
				Name: name, Range: rangeValue,
			})
		}
		position = end
	}
	return result
}

// TwigVueBindingMembers returns the direct properties used through target in
// its lexical scope. Nested v-for declarations with the same alias are
// excluded by resolving every receiver back to its binding identity.
func TwigVueBindingMembers(
	root *twigsyntax.Node,
	content []byte,
	target TwigVueBinding,
) []TwigVueMember {
	if root == nil || target.Name == "" {
		return nil
	}
	bindings := TwigVueBindings(root, content)
	seen := make(map[string]bool)
	var result []TwigVueMember
	for start := 0; start < len(content); {
		relative := bytes.Index(content[start:], []byte(target.Name))
		if relative < 0 {
			break
		}
		position := start + relative
		start = position + len(target.Name)
		memberOffset, hasMember := twigVueMemberOffsetAfterRoot(
			content, position+len(target.Name),
		)
		if !hasMember {
			continue
		}
		access, found := TwigVueExpressionMemberAtOffset(
			root, content, memberOffset,
		)
		if !found || access.Root != target.Name || access.Member == "" ||
			access.RootRange.Start != uint32(position) || len(access.Receiver) != 0 {
			continue
		}
		resolved := false
		for _, candidate := range twigVueBindingsAtOffset(
			bindings, access.RootRange.Start,
		) {
			if candidate.Name == target.Name && candidate.sameIdentity(target) {
				resolved = true
				break
			}
		}
		if !resolved || seen[access.Member] {
			continue
		}
		seen[access.Member] = true
		result = append(result, TwigVueMember{
			Name: access.Member, Range: access.MemberRange,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

// TwigVueBindingMemberReferences returns every use of one direct property on
// the same lexical binding. There is no local declaration range for a member.
func TwigVueBindingMemberReferences(
	root *twigsyntax.Node,
	content []byte,
	target TwigVueBinding,
	memberName string,
) []cst.TextRange {
	return TwigVueBindingMemberPathReferences(
		root, content, target, []string{memberName},
	)
}

// TwigVueBindingMemberPathReferences returns every use of the same safe
// property path on one lexical binding, including nested paths.
func TwigVueBindingMemberPathReferences(
	root *twigsyntax.Node,
	content []byte,
	target TwigVueBinding,
	memberPath []string,
) []cst.TextRange {
	if len(memberPath) == 0 || memberPath[len(memberPath)-1] == "" {
		return nil
	}
	bindings := TwigVueBindings(root, content)
	var result []cst.TextRange
	for _, access := range TwigVueExpressionMemberAccesses(root, content) {
		if access.Root != target.Name ||
			!equalTwigVueMemberPath(access.MemberPath(), memberPath) {
			continue
		}
		for _, candidate := range twigVueBindingsAtOffset(
			bindings, access.RootRange.Start,
		) {
			if candidate.Name == target.Name && candidate.sameIdentity(target) {
				result = append(result, access.MemberRange)
				break
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Start < result[right].Start
	})
	return result
}

// TwigVueBindingMemberAccessReferences retains invocation markers when
// finding a member chain, so value.method.name and value.method().name do not
// collapse into the same local reference set.
func TwigVueBindingMemberAccessReferences(
	root *twigsyntax.Node,
	content []byte,
	target TwigVueBinding,
	targetAccess TwigVueMemberAccess,
) []cst.TextRange {
	if targetAccess.Member == "" {
		return nil
	}
	bindings := TwigVueBindings(root, content)
	var result []cst.TextRange
	for _, access := range TwigVueExpressionMemberAccesses(root, content) {
		if !access.SamePath(targetAccess) {
			continue
		}
		for _, candidate := range twigVueBindingsAtOffset(
			bindings, access.RootRange.Start,
		) {
			if candidate.Name == target.Name && candidate.sameIdentity(target) {
				result = append(result, access.MemberRange)
				break
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Start < result[right].Start
	})
	return result
}

func equalTwigVueMemberPath(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func twigVueMemberOffsetAfterRoot(
	content []byte,
	position int,
) (uint32, bool) {
	for position < len(content) && isSlotSpace(content[position]) {
		position++
	}
	if position < len(content) && content[position] == '?' {
		position++
	}
	if position >= len(content) || content[position] != '.' {
		return 0, false
	}
	position++
	for position < len(content) && isSlotSpace(content[position]) {
		position++
	}
	if position >= len(content) || !isSlotIdentifierStart(content[position]) {
		return 0, false
	}
	return uint32(position), true
}

// TwigVueExpressionRootIdentifierAtOffset resolves a root identifier only
// when it is inside an evaluated Vue expression and outside string literals or
// comments. It is the lossless lexical bridge used by local-variable features.
func TwigVueExpressionRootIdentifierAtOffset(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (string, cst.TextRange, bool) {
	if root == nil || offset > uint32(len(content)) {
		return "", cst.TextRange{}, false
	}
	nodeOffset := offset
	if nodeOffset == uint32(len(content)) && nodeOffset > 0 {
		nodeOffset--
	}
	node := root.NodeAtOffset(nodeOffset)
	if node == nil || !IsTwigVueExpressionAt(node, offset) {
		return "", cst.TextRange{}, false
	}
	expressionRange, found := twigVueExpressionRange(node)
	if !found || offset < expressionRange.Start || offset > expressionRange.End {
		return "", cst.TextRange{}, false
	}
	name, rangeValue, found := ExpressionRootIdentifierAtOffset(content, offset)
	if !found || rangeValue.Start < expressionRange.Start {
		return "", cst.TextRange{}, false
	}
	if vueExpressionPositionInLiteralOrComment(
		content[expressionRange.Start:rangeValue.Start],
	) {
		return "", cst.TextRange{}, false
	}
	if vueExpressionReservedIdentifier(name) ||
		vueArrowParameterIsLocal(
			content[expressionRange.Start:expressionRange.End],
			name,
			rangeValue.Start-expressionRange.Start,
		) {
		return "", cst.TextRange{}, false
	}
	return name, rangeValue, true
}

func vueExpressionReservedIdentifier(name string) bool {
	switch name {
	case "await", "break", "case", "catch", "class", "const", "continue",
		"debugger", "default", "delete", "do", "else", "export", "extends",
		"false", "finally", "for", "function", "if", "import", "in",
		"instanceof", "let", "new", "null", "of", "return", "super",
		"switch", "this", "throw", "true", "try", "typeof", "var", "void",
		"while", "with", "yield":
		return true
	default:
		return false
	}
}

func vueArrowParameterIsLocal(
	expression []byte,
	name string,
	target uint32,
) bool {
	if name == "" || !bytes.Contains(expression, []byte("=>")) {
		return false
	}
	const prefix = "const __shopwareLSPTemplateValue = "
	parsed := javascriptparser.Parse(prefix + string(expression) + ";")
	target += uint32(len(prefix))
	for _, arrow := range jsquery.Nodes(
		parsed.Tree.Root, jssyntax.JsArrowFunction,
	) {
		arrowRange := arrow.RangeTrimmedTrivia()
		if target < arrowRange.Start || target > arrowRange.End {
			continue
		}
		arrowStart := uint32(0)
		for token := range arrow.ChildTokens() {
			if token.Kind() == jssyntax.TkArrow {
				arrowStart = token.Range().Start
				break
			}
		}
		if arrowStart == 0 {
			continue
		}
		for _, identifier := range jsquery.Nodes(
			arrow, jssyntax.JsIdentifier,
		) {
			identifierRange := identifier.RangeTrimmedTrivia()
			if identifierRange.End > arrowStart {
				continue
			}
			if strings.TrimSpace(identifier.Text()) == name {
				return true
			}
		}
	}
	return false
}

// TwigVueCallAtOffset returns the innermost open call containing offset. Commas
// in nested calls, arrays, objects, and strings do not advance ActiveArgument.
// Calls in comments and literal text are ignored; template-literal ${...}
// expressions remain executable and are scanned normally.
func TwigVueCallAtOffset(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (TwigVueCall, bool) {
	if root == nil || offset > uint32(len(content)) {
		return TwigVueCall{}, false
	}
	nodeOffset := offset
	if nodeOffset == uint32(len(content)) && nodeOffset > 0 {
		nodeOffset--
	}
	node := root.NodeAtOffset(nodeOffset)
	if node == nil || !IsTwigVueExpressionAt(node, offset) {
		return TwigVueCall{}, false
	}
	expressionRange, found := twigVueExpressionRange(node)
	if !found || offset < expressionRange.Start || offset > expressionRange.End {
		return TwigVueCall{}, false
	}
	type frame struct {
		delimiter byte
		call      TwigVueCall
		callable  bool
	}
	stack := make([]frame, 0, 8)
	limit := int(offset)
	if limit > int(expressionRange.End) {
		limit = int(expressionRange.End)
	}
	expressionStart := int(expressionRange.Start)
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	var templates []int
	for index := expressionStart; index < limit; index++ {
		current := content[index]
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && index+1 < limit && content[index+1] == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if len(templates) > 0 && templates[len(templates)-1] == 0 {
			if current == '\\' {
				index++
				continue
			}
			if current == '`' {
				templates = templates[:len(templates)-1]
				continue
			}
			if current == '$' && index+1 < limit && content[index+1] == '{' {
				templates[len(templates)-1] = 1
				stack = append(stack, frame{delimiter: '{'})
				index++
			}
			continue
		}
		if current == '/' && index+1 < limit {
			switch content[index+1] {
			case '/':
				lineComment = true
				index++
				continue
			case '*':
				blockComment = true
				index++
				continue
			}
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		if current == '`' {
			templates = append(templates, 0)
			continue
		}
		if len(templates) > 0 && current == '{' {
			templates[len(templates)-1]++
		}
		switch current {
		case '(', '[', '{':
			entry := frame{delimiter: current}
			if current == '(' {
				entry.call, entry.callable = twigVueCallBefore(
					content, expressionStart, index,
				)
			}
			stack = append(stack, entry)
		case ')', ']', '}':
			if len(stack) == 0 || !matchingVueCallDelimiter(
				stack[len(stack)-1].delimiter, current,
			) {
				continue
			}
			stack = stack[:len(stack)-1]
		case ',':
			if len(stack) > 0 && stack[len(stack)-1].delimiter == '(' &&
				stack[len(stack)-1].callable {
				stack[len(stack)-1].call.ActiveArgument++
			}
		}
		if len(templates) > 0 && current == '}' {
			templates[len(templates)-1]--
		}
	}
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].callable {
			return stack[index].call, true
		}
	}
	return TwigVueCall{}, false
}

// TwigVueCalls returns complete calls in evaluated Administration template
// expressions in source order. It shares the lexical recognizer used by
// TwigVueCallAtOffset, so grouping parentheses and call-like text inside
// strings or comments are not reported as calls.
func TwigVueCalls(
	root *twigsyntax.Node,
	content []byte,
) []TwigVueCallSite {
	if root == nil || len(content) == 0 {
		return nil
	}
	var result []TwigVueCallSite
	seen := make(map[cst.TextRange]bool)
	for open := 0; open < len(content); open++ {
		if content[open] != '(' {
			continue
		}
		node := root.NodeAtOffset(uint32(open))
		if node == nil || !IsTwigVueExpressionAt(node, uint32(open)) {
			continue
		}
		expressionRange, found := twigVueExpressionRange(node)
		if !found || uint32(open) < expressionRange.Start ||
			uint32(open) >= expressionRange.End {
			continue
		}
		candidate, callable := twigVueCallBefore(
			content, int(expressionRange.Start), open,
		)
		if !callable || seen[candidate.NameRange] {
			continue
		}
		active, activeFound := TwigVueCallAtOffset(
			root, content, uint32(open+1),
		)
		if !activeFound || active.NameRange != candidate.NameRange ||
			active.Filter != candidate.Filter {
			continue
		}
		arguments, end := twigVueCallArgumentRanges(
			content, open, int(expressionRange.End),
		)
		seen[candidate.NameRange] = true
		result = append(result, TwigVueCallSite{
			TwigVueCall: candidate,
			Range: cst.TextRange{
				Start: candidate.NameRange.Start,
				End:   uint32(end),
			},
			OpenParen: uint32(open),
			Arguments: arguments,
		})
	}
	return result
}

func twigVueCallArgumentRanges(
	content []byte,
	open,
	expressionEnd int,
) ([]cst.TextRange, int) {
	if open < 0 || open >= len(content) || content[open] != '(' {
		return nil, open
	}
	if expressionEnd > len(content) {
		expressionEnd = len(content)
	}
	if expressionEnd <= open {
		return nil, open + 1
	}
	stack := []byte{'('}
	argumentStart := open + 1
	var result []cst.TextRange
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	appendArgument := func(end int) {
		start := argumentStart
		for start < end && isSlotSpace(content[start]) {
			start++
		}
		for end > start && isSlotSpace(content[end-1]) {
			end--
		}
		if start < end {
			result = append(result, cst.TextRange{
				Start: uint32(start), End: uint32(end),
			})
		}
	}
	for index := open + 1; index < expressionEnd; index++ {
		current := content[index]
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && index+1 < expressionEnd &&
				content[index+1] == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '/' && index+1 < expressionEnd {
			switch content[index+1] {
			case '/':
				lineComment = true
				index++
				continue
			case '*':
				blockComment = true
				index++
				continue
			}
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		switch current {
		case '(', '[', '{':
			stack = append(stack, current)
		case ')', ']', '}':
			if len(stack) == 0 || !matchingVueCallDelimiter(
				stack[len(stack)-1], current,
			) {
				continue
			}
			if len(stack) == 1 {
				appendArgument(index)
				return result, index + 1
			}
			stack = stack[:len(stack)-1]
		case ',':
			if len(stack) == 1 {
				appendArgument(index)
				argumentStart = index + 1
			}
		}
	}
	appendArgument(expressionEnd)
	return result, expressionEnd
}

func twigVueCallBefore(
	content []byte,
	expressionStart,
	open int,
) (TwigVueCall, bool) {
	cursor := open
	for cursor > expressionStart && isSlotSpace(content[cursor-1]) {
		cursor--
	}
	if cursor >= expressionStart+2 && content[cursor-2] == '?' &&
		content[cursor-1] == '.' {
		cursor -= 2
		for cursor > expressionStart && isSlotSpace(content[cursor-1]) {
			cursor--
		}
	}
	end := cursor
	for cursor > expressionStart && isSlotIdentifierContinue(content[cursor-1]) {
		cursor--
	}
	if cursor == end || !isSlotIdentifierStart(content[cursor]) {
		return TwigVueCall{}, false
	}
	previous := cursor
	for previous > expressionStart && isSlotSpace(content[previous-1]) {
		previous--
	}
	filter := previous > expressionStart && content[previous-1] == '|'
	if filter && previous >= expressionStart+2 && content[previous-2] == '|' {
		filter = false
	}
	return TwigVueCall{
		Name: string(content[cursor:end]),
		NameRange: cst.TextRange{
			Start: uint32(cursor), End: uint32(end),
		},
		Filter: filter,
	}, true
}

func matchingVueCallDelimiter(open, close byte) bool {
	return open == '(' && close == ')' || open == '[' && close == ']' ||
		open == '{' && close == '}'
}

func twigVueExpressionRange(
	node *twigsyntax.Node,
) (cst.TextRange, bool) {
	if attributeNode := twigquery.HTMLAttributeAt(node); attributeNode != nil {
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok {
			return cst.TextRange{}, false
		}
		value, ok := attribute.Value()
		if !ok {
			return cst.TextRange{}, false
		}
		inner, ok := value.GetInner()
		if !ok {
			return cst.TextRange{}, false
		}
		return inner.Syntax().RangeTrimmedTrivia(), true
	}
	if variable := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigVar); variable != nil {
		return variable.RangeTrimmedTrivia(), true
	}
	return cst.TextRange{}, false
}

func vueExpressionPositionInLiteralOrComment(prefix []byte) bool {
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	// Each entry represents one template literal. Zero is template text;
	// positive values are the brace depth of its current ${...} expression.
	var templates []int
	for index := 0; index < len(prefix); index++ {
		current := prefix[index]
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && index+1 < len(prefix) && prefix[index+1] == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if len(templates) > 0 && templates[len(templates)-1] == 0 {
			if current == '\\' {
				index++
				continue
			}
			if current == '`' {
				templates = templates[:len(templates)-1]
				continue
			}
			if current == '$' && index+1 < len(prefix) &&
				prefix[index+1] == '{' {
				templates[len(templates)-1] = 1
				index++
			}
			continue
		}
		if current == '/' && index+1 < len(prefix) {
			switch prefix[index+1] {
			case '/':
				lineComment = true
				index++
				continue
			case '*':
				blockComment = true
				index++
				continue
			}
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		if current == '`' {
			templates = append(templates, 0)
			continue
		}
		if len(templates) > 0 {
			top := len(templates) - 1
			switch current {
			case '{':
				templates[top]++
			case '}':
				templates[top]--
			}
		}
	}
	return quote != 0 || lineComment || blockComment ||
		len(templates) > 0 && templates[len(templates)-1] == 0
}

func parseTwigVueForBindings(
	value string,
	base uint32,
	scopeRange,
	expressionRange cst.TextRange,
) []TwigVueBinding {
	separatorStart, separatorEnd := twigVueForSeparator(value)
	if separatorStart < 0 {
		return nil
	}
	leftStart, leftEnd := trimByteRange(value, 0, separatorStart)
	if leftStart >= leftEnd {
		return nil
	}
	if value[leftStart] == '(' && value[leftEnd-1] == ')' &&
		matchingSlotDelimiter(value, leftStart, '(', ')') == leftEnd-1 {
		leftStart++
		leftEnd--
		leftStart, leftEnd = trimByteRange(value, leftStart, leftEnd)
	}
	iterableStart, iterableEnd := trimByteRange(
		value, separatorEnd, len(value),
	)
	iterable := ""
	if iterableStart < iterableEnd {
		iterable = value[iterableStart:iterableEnd]
	}
	var result []TwigVueBinding
	for ordinal, part := range splitTwigVueTopLevelRanges(
		value, leftStart, leftEnd, ',',
	) {
		start, end := trimByteRange(value, part.Start, part.End)
		if start >= end || !isSlotIdentifier(value[start:end]) {
			continue
		}
		result = append(result, TwigVueBinding{
			Name: value[start:end], Kind: TwigVueBindingFor, Ordinal: ordinal,
			DeclarationRange: cst.TextRange{
				Start: base + uint32(start), End: base + uint32(end),
			},
			ScopeRange: scopeRange, ExpressionRange: expressionRange,
			Iterable: iterable,
		})
	}
	return result
}

func twigVueForSeparator(value string) (int, int) {
	state := slotScanState{}
	for index := 0; index < len(value); index++ {
		if state.topLevel() {
			for _, word := range []string{"in", "of"} {
				end := index + len(word)
				if end > len(value) || value[index:end] != word ||
					index == 0 || !isSlotSpace(value[index-1]) ||
					end == len(value) || !isSlotSpace(value[end]) {
					continue
				}
				return index, end
			}
		}
		state.consume(value[index])
	}
	return -1, -1
}

func trimByteRange(value string, start, end int) (int, int) {
	for start < end && isSlotSpace(value[start]) {
		start++
	}
	for end > start && isSlotSpace(value[end-1]) {
		end--
	}
	return start, end
}

func splitTwigVueTopLevelRanges(
	value string,
	start,
	end int,
	separator byte,
) []struct{ Start, End int } {
	var result []struct{ Start, End int }
	partStart := start
	state := slotScanState{}
	for index := start; index < end; index++ {
		if value[index] == separator && state.topLevel() {
			result = append(result, struct{ Start, End int }{partStart, index})
			partStart = index + 1
			continue
		}
		state.consume(value[index])
	}
	return append(result, struct{ Start, End int }{partStart, end})
}

func eventPayloadType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "[") {
		close := matchingSlotDelimiter(value, 0, '[', ']')
		if close < 0 {
			return ""
		}
		parameters := splitSlotTopLevel(value[1:close], ',')
		if len(parameters) == 0 {
			return ""
		}
		return vueEventParameterType(parameters[0])
	}
	if strings.HasPrefix(value, "(") {
		close := matchingSlotDelimiter(value, 0, '(', ')')
		if close < 0 {
			return ""
		}
		parameters := splitSlotTopLevel(value[1:close], ',')
		if len(parameters) == 0 {
			return ""
		}
		parameterIndex := 0
		if len(parameters) > 1 && vueEventDiscriminatorParameter(parameters[0]) {
			parameterIndex = 1
		}
		return vueEventParameterType(parameters[parameterIndex])
	}
	switch trimAdminTypeParentheses(value) {
	case "void", "undefined", "never":
		return ""
	}
	// Legacy @event annotations describe the payload directly rather than as a
	// callable signature. Retain that source-owned type for template bindings.
	return value
}

func vueEventParameterType(parameter string) string {
	parameter = strings.TrimSpace(parameter)
	rest := strings.HasPrefix(parameter, "...")
	if colon := indexSlotTopLevel(parameter, ':'); colon >= 0 {
		parameter = strings.TrimSpace(parameter[colon+1:])
	}
	if rest && strings.HasSuffix(parameter, "[]") {
		parameter = strings.TrimSpace(strings.TrimSuffix(parameter, "[]"))
	}
	return parameter
}

func vueEventDiscriminatorParameter(parameter string) bool {
	parameter = strings.TrimSpace(parameter)
	colon := indexSlotTopLevel(parameter, ':')
	if colon < 0 {
		return false
	}
	name := strings.TrimSpace(strings.TrimSuffix(parameter[:colon], "?"))
	switch name {
	case "event", "evt", "e":
	default:
		return false
	}
	eventType := strings.TrimSpace(parameter[colon+1:])
	return len(eventType) >= 2 &&
		(eventType[0] == '\'' || eventType[0] == '"') &&
		eventType[len(eventType)-1] == eventType[0]
}

func nativeEventPayloadType(name string) string {
	switch CanonicalEventName(name) {
	case "click", "dblclick", "mousedown", "mouseup", "mousemove",
		"mouseenter", "mouseleave", "contextmenu":
		return "MouseEvent"
	case "keydown", "keyup", "keypress":
		return "KeyboardEvent"
	case "focus", "blur", "focusin", "focusout":
		return "FocusEvent"
	case "drag", "dragstart", "dragend", "dragenter", "dragleave",
		"dragover", "drop":
		return "DragEvent"
	case "input", "change", "submit", "reset":
		return "Event"
	default:
		return ""
	}
}

// VueIterableElementType extracts the value type exposed by a Vue v-for from
// the runtime/TypeScript spellings commonly used in Administration props.
// Plain Array has no element information and deliberately returns unknown.
func VueIterableElementType(value string) string {
	value = strings.TrimSpace(value)
	if union := splitAdminTypeTopLevel(value, '|'); len(union) > 1 {
		var elementType string
		for _, branch := range union {
			branch = strings.TrimSpace(branch)
			if branch == "null" || branch == "undefined" || branch == "never" {
				continue
			}
			candidate := VueIterableElementType(branch)
			if candidate == "" {
				return ""
			}
			if elementType != "" && elementType != candidate {
				return ""
			}
			elementType = candidate
		}
		return elementType
	}
	value = normalizeVueIterableType(value)
	if value == "number" {
		return "number"
	}
	if value == "string" {
		return "string"
	}
	if strings.HasSuffix(value, "[]") {
		return strings.TrimSpace(strings.TrimSuffix(value, "[]"))
	}
	if name, arguments := parseAdminNamedType(value); len(arguments) == 2 {
		shortName := name
		if separator := strings.LastIndexByte(shortName, '.'); separator >= 0 {
			shortName = shortName[separator+1:]
		}
		switch shortName {
		case "Record":
			return strings.TrimSpace(arguments[1])
		case "Map", "ReadonlyMap":
			return "[" + strings.TrimSpace(arguments[0]) + ", " +
				strings.TrimSpace(arguments[1]) + "]"
		}
	}
	for _, constructor := range []string{
		"Array", "ReadonlyArray", "Iterable", "Set", "EntityCollection",
		"EntitySchema.EntityCollection",
	} {
		prefix := constructor + "<"
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		open := len(constructor)
		close := matchingSlotDelimiter(value, open, '<', '>')
		if close == len(value)-1 {
			element := strings.TrimSpace(value[open+1 : close])
			if strings.HasSuffix(constructor, "EntityCollection") {
				return "Entity<" + element + ">"
			}
			return element
		}
	}
	if len(value) >= 2 && value[0] == '[' &&
		matchingSlotDelimiter(value, 0, '[', ']') == len(value)-1 {
		items := splitSlotTopLevel(value[1:len(value)-1], ',')
		var types []string
		seen := make(map[string]bool)
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			types = append(types, item)
		}
		return strings.Join(types, " | ")
	}
	if strings.HasPrefix(value, "{") {
		var elementType string
		for _, member := range VueTypeMembers(value) {
			elementType = mergeVueTypes(elementType, member.Type)
		}
		return elementType
	}
	return ""
}

// VueIterableBindingType models Vue's v-for binding order. Arrays, strings,
// numeric ranges, and generic iterables expose (value, index), while objects
// and Records expose (value, key, index).
func VueIterableBindingType(value string, ordinal int) string {
	if ordinal <= 0 {
		return VueIterableElementType(value)
	}
	if ordinal >= 2 {
		return "number"
	}
	if keyType := vueIterableKeyType(value); keyType != "" {
		return keyType
	}
	if VueIterableElementType(value) != "" {
		return "number"
	}
	return ""
}

func vueIterableKeyType(value string) string {
	value = strings.TrimSpace(value)
	if union := splitAdminTypeTopLevel(value, '|'); len(union) > 1 {
		var keyType string
		for _, branch := range union {
			branch = strings.TrimSpace(branch)
			if branch == "null" || branch == "undefined" || branch == "never" {
				continue
			}
			candidate := vueIterableKeyType(branch)
			if candidate == "" {
				return ""
			}
			keyType = mergeVueTypes(keyType, candidate)
		}
		return keyType
	}
	value = normalizeVueIterableType(value)
	if name, arguments := parseAdminNamedType(value); len(arguments) == 2 {
		shortName := name
		if separator := strings.LastIndexByte(shortName, '.'); separator >= 0 {
			shortName = shortName[separator+1:]
		}
		if shortName == "Record" {
			return strings.TrimSpace(arguments[0])
		}
	}
	if strings.HasPrefix(value, "{") {
		return "string"
	}
	return ""
}

func normalizeVueIterableType(value string) string {
	value = strings.TrimSpace(value)
	if arrow := strings.LastIndex(value, "=>"); arrow >= 0 {
		value = strings.TrimSpace(value[arrow+2:])
	}
	for {
		open := strings.LastIndex(value, "PropType<")
		if open < 0 {
			break
		}
		angle := open + len("PropType")
		close := matchingSlotDelimiter(value, angle, '<', '>')
		if close < 0 {
			break
		}
		value = strings.TrimSpace(value[angle+1 : close])
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "readonly "))
	for len(value) >= 2 && value[0] == '(' &&
		matchingSlotDelimiter(value, 0, '(', ')') == len(value)-1 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}
