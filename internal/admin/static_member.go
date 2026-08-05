package admin

import (
	"strings"

	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func javaScriptStaticMember(
	node *jssyntax.Node,
	prefix string,
) (receiver []string, memberName string, matched bool) {
	member := jsquery.MemberExpressionAt(node)
	if member == nil {
		return nil, "", false
	}
	cursor := member.ChildNodeCursor()
	if !cursor.Next() {
		return nil, "", false
	}
	directReceiver := cursor.Node()
	if node != member && javaScriptNodeWithin(node, directReceiver) {
		return nil, "", false
	}
	segments, incomplete, matched := javaScriptStaticExpression(
		javaScriptTrimmedNodeText(member), prefix,
	)
	if !matched || len(segments) == 0 && !incomplete {
		return nil, "", false
	}
	if incomplete {
		names := staticExpressionSegmentNames(segments)
		if len(names) != len(segments) {
			return nil, "", false
		}
		return names, "", true
	}
	last := segments[len(segments)-1]
	if last.Called || last.Indexed {
		return nil, "", false
	}
	receiver = staticExpressionSegmentNames(segments[:len(segments)-1])
	if len(receiver) != len(segments)-1 {
		return nil, "", false
	}
	return receiver, last.Name, true
}

func javaScriptStaticMemberNameNode(
	node *jssyntax.Node,
	prefix string,
) *jssyntax.Node {
	_, name, matched := javaScriptStaticMember(node, prefix)
	if !matched || name == "" {
		return nil
	}
	member := jsquery.MemberExpressionAt(node)
	if member == nil {
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

func javaScriptTrimmedNodeText(node *jssyntax.Node) string {
	if node == nil {
		return ""
	}
	full := node.Range()
	trimmed := node.RangeTrimmedTrivia()
	start := int(trimmed.Start - full.Start)
	end := int(trimmed.End - full.Start)
	text := node.Text()
	if start < 0 || start > end || end > len(text) {
		return text
	}
	return text[start:end]
}

func javaScriptStaticExpression(
	expression,
	prefix string,
) ([]vueStaticExpressionSegment, bool, bool) {
	expression = strings.TrimSpace(expression)
	if prefix == "" || !strings.HasPrefix(expression, prefix) ||
		len(expression) > len(prefix) &&
			isVueIdentifierPart(expression[len(prefix)]) {
		return nil, false, false
	}
	incomplete := strings.HasSuffix(expression, ".") ||
		strings.HasSuffix(expression, "?.")
	parseValue := expression
	if incomplete {
		parseValue = strings.TrimSuffix(parseValue, ".")
		parseValue = strings.TrimSuffix(parseValue, "?")
	}
	segments, end, ok := vueStaticExpressionSegments(parseValue, len(prefix))
	if !ok || strings.TrimSpace(parseValue[end:]) != "" {
		return nil, false, false
	}
	return segments, incomplete, true
}

func staticExpressionPath(
	path string,
) ([]vueStaticExpressionSegment, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, true
	}
	segments, end, ok := vueStaticExpressionSegments("."+path, 0)
	return segments, ok && end == len(path)+1
}

func staticExpressionSegmentNames(
	segments []vueStaticExpressionSegment,
) []string {
	result := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment.Called || segment.Indexed || segment.Name == "" {
			return nil
		}
		result = append(result, segment.Name)
	}
	return result
}
