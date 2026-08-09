package admin

import (
	"bytes"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

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
