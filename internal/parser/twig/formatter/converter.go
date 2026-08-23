package formatter

import (
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

type converter struct{}

var parentStatementPattern = regexp.MustCompile(
	`(?s)^\s*\{%([-~]?)\s*parent(?:\(\))?\s*([-~]?)%\}\s*$`,
)

func (converter) nodeList(parent *cst.Node) nodeList {
	if parent == nil {
		return nil
	}
	var result nodeList
	for child := range parent.ChildNodes() {
		if prefix := structuralPrefix(parent, child); prefix != "" {
			appendRaw(&result, prefix)
		}
		node := converter{}.node(child)
		if node == nil {
			continue
		}
		if raw, ok := node.(*rawNode); ok {
			appendRaw(&result, raw.Text)
			continue
		}
		result = append(result, node)
	}
	for index, node := range result {
		raw, ok := node.(*rawNode)
		if !ok {
			continue
		}
		if match := parentStatementPattern.FindStringSubmatch(raw.Text); match != nil {
			result[index] = &parentNode{Trim: twigTrim{
				Left: firstByte(match[1]), Right: firstByte(match[2]),
			}}
		}
	}
	return result
}

func appendRaw(nodes *nodeList, text string) {
	if text == "" {
		return
	}
	if previous, ok := lastRaw(*nodes); ok {
		previous.Text += text
		return
	}
	*nodes = append(*nodes, &rawNode{Text: text})
}

func lastRaw(nodes nodeList) (*rawNode, bool) {
	if len(nodes) == 0 {
		return nil, false
	}
	raw, ok := nodes[len(nodes)-1].(*rawNode)
	return raw, ok
}

func (c converter) node(node *cst.Node) node {
	if node == nil || node.Range().Len() == 0 {
		return nil
	}
	switch node.Kind() {
	case syntax.Body, syntax.Root, syntax.HtmlAttributeList:
		return nodeListNode{nodes: c.nodeList(node)}
	case syntax.HtmlTag:
		return c.htmlTag(node)
	case syntax.HtmlText, syntax.HtmlRawText, syntax.HtmlDoctype, syntax.Error:
		return &rawNode{Text: node.Text()}
	case syntax.HtmlComment:
		return &commentNode{Text: delimitedBody(node.Text(), "<!--", "-->")}
	case syntax.HtmlAttribute:
		return c.htmlAttribute(node)
	case syntax.TwigVar:
		body, trim, ok := parseDelimited(node.Text(), "{{", "}}")
		if !ok {
			return &rawNode{Text: node.Text()}
		}
		return &templateExpressionNode{Expression: body, Trim: trim}
	case syntax.TwigComment:
		if body, trim, symmetric, ok := parseDocumentationComment(
			node.Text(),
		); ok {
			return &twigCommentNode{
				Body:          body,
				Trim:          trim,
				Documentation: true,
				Symmetric:     symmetric,
			}
		}
		body, trim, ok := parseDelimited(node.Text(), "{#", "#}")
		if !ok {
			return &rawNode{Text: node.Text()}
		}
		return &twigCommentNode{Body: body, Trim: trim}
	case syntax.TwigBlock:
		return c.twigBlock(node)
	case syntax.TwigIf:
		return c.twigIf(node)
	case syntax.TwigFor:
		return c.genericBlock(node, "for", "endfor")
	case syntax.TwigEmbed:
		return c.genericBlock(node, "embed", "endembed")
	case syntax.TwigMacro:
		return c.genericBlock(node, "macro", "endmacro")
	case syntax.TwigApply:
		return c.genericBlock(node, "apply", "endapply")
	case syntax.TwigWith:
		return c.genericBlock(node, "with", "endwith")
	case syntax.TwigAutoescape:
		return c.genericBlock(node, "autoescape", "endautoescape")
	case syntax.TwigSandbox:
		return c.genericBlock(node, "sandbox", "endsandbox")
	case syntax.TwigTrans:
		return c.genericBlock(node, "trans", "endtrans")
	case syntax.TwigCache:
		return c.genericBlock(node, "cache", "endcache")
	case syntax.TwigComponent:
		return c.genericBlock(node, "component", "endcomponent")
	case syntax.TwigAssetic:
		return c.genericBlock(node, "", "")
	case syntax.ShopwareSilentFeatureCall:
		return c.genericBlock(node, "sw_silent_feature_call", "endsw_silent_feature_call")
	case syntax.TwigSet:
		if childNode(node, syntax.Body) != nil {
			return c.genericBlock(node, "set", "endset")
		}
		return c.standalone(node)
	case syntax.TwigVerbatim:
		return c.verbatim(node)
	case syntax.TwigExtends, syntax.TwigInclude, syntax.TwigUse,
		syntax.TwigImport, syntax.TwigFrom, syntax.TwigDo,
		syntax.TwigDeprecated, syntax.TwigFlush, syntax.TwigProps,
		syntax.TwigFormTheme, syntax.ShopwareTwigSwExtends,
		syntax.ShopwareTwigSwInclude, syntax.ShopwareReturn,
		syntax.ShopwareIcon, syntax.ShopwareThumbnails:
		return c.standalone(node)
	default:
		return &rawNode{Text: node.Text()}
	}
}

// nodeListNode lets nested generic BODY/attribute-list nodes participate in
// conversion without flattening at every call site.
type nodeListNode struct{ nodes nodeList }

func (n nodeListNode) dump(r *renderer, indent int) string { return n.nodes.dump(r, indent) }

func (c converter) htmlTag(node *cst.Node) node {
	starting := childNode(node, syntax.HtmlStartingTag)
	if starting == nil {
		return &rawNode{Text: node.Text()}
	}
	name := tagName(starting)
	if name == "" {
		return &rawNode{Text: node.Text()}
	}
	attributes := nodeList(nil)
	if list := childNode(starting, syntax.HtmlAttributeList); list != nil {
		attributes = c.nodeList(list)
	}
	body := nodeList(nil)
	if bodyNode := childNode(node, syntax.Body); bodyNode != nil {
		body = c.nodeList(bodyNode)
	}
	ending := childNode(node, syntax.HtmlEndingTag)
	closed := ending != nil && ending.Range().Len() > 0
	startText := starting.Text()
	selfClosing := strings.HasSuffix(strings.TrimSpace(startText), "/>") ||
		isVoidElement(name)
	if !selfClosing && !closed {
		// Incomplete and deliberately non-HTML text such as `<<Success>>`
		// must remain byte-identical. Formatting a speculative unclosed element
		// can otherwise split or synthesize markup while the user is typing.
		return &rawNode{Text: node.Text()}
	}
	if closed {
		endingText := ending.Text()
		if delimiter := strings.Index(endingText, "</"); delimiter > 0 {
			appendRaw(&body, endingText[:delimiter])
		}
	}
	return &elementNode{
		Tag: name, Attributes: attributes, Children: body,
		SelfClosing: selfClosing,
		Unclosed:    !selfClosing && !closed,
	}
}

func structuralPrefix(parent, node *cst.Node) string {
	if parent == nil || node == nil || parent.Kind() == syntax.HtmlAttributeList {
		return ""
	}
	var delimiter string
	switch node.Kind() {
	case syntax.TwigVar:
		delimiter = "{"
	case syntax.HtmlTag, syntax.HtmlComment:
		if !bodyBelongsToTag(parent, "p") {
			return ""
		}
		delimiter = "<"
	default:
		return ""
	}
	text := node.Text()
	index := strings.Index(text, delimiter)
	if index <= 0 {
		return ""
	}
	return text[:index]
}

func bodyBelongsToTag(body *cst.Node, name string) bool {
	if body == nil || body.Kind() != syntax.Body {
		return false
	}
	owner := body.Parent()
	if owner == nil || owner.Kind() != syntax.HtmlTag {
		return false
	}
	starting := childNode(owner, syntax.HtmlStartingTag)
	return starting != nil && strings.EqualFold(tagName(starting), name)
}

func (converter) htmlAttribute(node *cst.Node) node {
	text := strings.TrimSpace(node.Text())
	if text == "" {
		return nil
	}
	equal := strings.IndexByte(text, '=')
	if equal < 0 {
		return &attribute{Key: text}
	}
	key := strings.TrimSpace(text[:equal])
	value := strings.TrimSpace(text[equal+1:])
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	return &attribute{Key: key, Value: value}
}

func (c converter) twigBlock(node *cst.Node) node {
	starting := childNode(node, syntax.TwigStartingBlock)
	ending := childNode(node, syntax.TwigEndingBlock)
	body := childNode(node, syntax.Body)
	if starting == nil || ending == nil || ending.Range().Len() == 0 {
		return &rawNode{Text: node.Text()}
	}
	name, args, openTrim, ok := parseStatement(starting.Text())
	if !ok || name != "block" {
		return &rawNode{Text: node.Text()}
	}
	_, _, closeTrim, ok := parseStatement(ending.Text())
	if !ok {
		return &rawNode{Text: node.Text()}
	}
	blockName := strings.Fields(args)
	if len(blockName) == 0 {
		return &rawNode{Text: node.Text()}
	}
	return &twigBlockNode{
		Name: blockName[0], Children: c.nodeList(body),
		OpenTrim: openTrim, CloseTrim: closeTrim,
	}
}

func (c converter) twigIf(node *cst.Node) node {
	children := directNodes(node)
	var branches []twigIfBranch
	var elseChildren nodeList
	var elseTrim, endTrim twigTrim
	for index := 0; index < len(children); index++ {
		child := children[index]
		switch child.Kind() {
		case syntax.TwigIfBlock, syntax.TwigElseIfBlock:
			name, condition, trim, ok := parseStatement(child.Text())
			if !ok || (name != "if" && name != "elseif") {
				return &rawNode{Text: node.Text()}
			}
			body := nextBody(children, index)
			branches = append(branches, twigIfBranch{
				Condition: condition, Body: c.nodeList(body), Trim: trim,
			})
		case syntax.TwigElseBlock:
			_, _, trim, ok := parseStatement(child.Text())
			if !ok {
				return &rawNode{Text: node.Text()}
			}
			elseTrim = trim
			elseChildren = c.nodeList(nextBody(children, index))
		case syntax.TwigEndifBlock:
			_, _, trim, ok := parseStatement(child.Text())
			if !ok {
				return &rawNode{Text: node.Text()}
			}
			endTrim = trim
		}
	}
	if len(branches) == 0 {
		return &rawNode{Text: node.Text()}
	}
	return &twigIfNode{
		Branches: branches, ElseChildren: elseChildren,
		ElseTrim: elseTrim, EndTrim: endTrim,
	}
}

func (c converter) genericBlock(node *cst.Node, fallbackName, fallbackEnd string) node {
	children := directNodes(node)
	var header, ending *cst.Node
	var bodies []*cst.Node
	var elseHeader *cst.Node
	for _, child := range children {
		switch child.Kind() {
		case syntax.Body:
			bodies = append(bodies, child)
		default:
			name, _, _, ok := parseStatement(child.Text())
			if !ok {
				continue
			}
			switch {
			case name == "else":
				elseHeader = child
			case strings.HasPrefix(name, "end"):
				ending = child
			case header == nil:
				header = child
			}
		}
	}
	if header == nil || ending == nil {
		return &rawNode{Text: node.Text()}
	}
	name, args, openTrim, ok := parseStatement(header.Text())
	if !ok {
		return &rawNode{Text: node.Text()}
	}
	endName, _, closeTrim, ok := parseStatement(ending.Text())
	if !ok {
		return &rawNode{Text: node.Text()}
	}
	if fallbackName != "" {
		name = fallbackName
	}
	if fallbackEnd != "" {
		endName = fallbackEnd
	}
	var body, elseBody nodeList
	if len(bodies) > 0 {
		body = c.nodeList(bodies[0])
	}
	if len(bodies) > 1 {
		elseBody = c.nodeList(bodies[1])
	}
	var elseTrim twigTrim
	if elseHeader != nil {
		_, _, elseTrim, _ = parseStatement(elseHeader.Text())
	}
	return &twigGenericBlockNode{
		Name: name, Args: args, EndTag: endName,
		Body: body, Else: elseBody,
		OpenTrim: openTrim, ElseTrim: elseTrim, CloseTrim: closeTrim,
	}
}

func (converter) standalone(node *cst.Node) node {
	name, args, trim, ok := parseStatement(node.Text())
	if !ok {
		return &rawNode{Text: node.Text()}
	}
	return &twigStandaloneTagNode{Name: name, Args: args, Trim: trim}
}

func (converter) verbatim(node *cst.Node) node {
	children := directNodes(node)
	if len(children) < 3 {
		return &rawNode{Text: node.Text()}
	}
	header := children[0]
	ending := children[len(children)-1]
	_, _, openTrim, ok := parseStatement(header.Text())
	if !ok {
		return &rawNode{Text: node.Text()}
	}
	_, _, closeTrim, ok := parseStatement(ending.Text())
	if !ok {
		return &rawNode{Text: node.Text()}
	}
	body := childNode(node, syntax.Body)
	if body == nil {
		return &rawNode{Text: node.Text()}
	}
	return &twigVerbatimNode{
		Body: body.Text(), OpenTrim: openTrim, CloseTrim: closeTrim,
	}
}

func parseStatement(text string) (string, string, twigTrim, bool) {
	body, trim, ok := parseDelimited(text, "{%", "%}")
	if !ok {
		return "", "", twigTrim{}, false
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "", twigTrim{}, false
	}
	end := strings.IndexAny(body, " \t\r\n")
	if end < 0 {
		return body, "", trim, true
	}
	return body[:end], strings.TrimSpace(body[end:]), trim, true
}

func parseDelimited(text, open, close string) (string, twigTrim, bool) {
	start := strings.Index(text, open)
	end := strings.LastIndex(text, close)
	if start < 0 || end < start+len(open) {
		return "", twigTrim{}, false
	}
	bodyStart := start + len(open)
	bodyEnd := end
	var trim twigTrim
	if bodyStart < len(text) && (text[bodyStart] == '-' || text[bodyStart] == '~') {
		trim.Left = text[bodyStart]
		bodyStart++
	}
	if bodyEnd > bodyStart && (text[bodyEnd-1] == '-' || text[bodyEnd-1] == '~') {
		trim.Right = text[bodyEnd-1]
		bodyEnd--
	}
	return text[bodyStart:bodyEnd], trim, true
}

func parseDocumentationComment(
	text string,
) (string, twigTrim, bool, bool) {
	start := strings.Index(text, "{#")
	if start < 0 || !strings.HasPrefix(text[start:], "{##") {
		return "", twigTrim{}, false, false
	}
	bodyStart := start + len("{##")
	var trim twigTrim
	if bodyStart < len(text) &&
		(text[bodyStart] == '-' || text[bodyStart] == '~') {
		trim.Left = text[bodyStart]
		bodyStart++
	}
	for _, closing := range []struct {
		text      string
		control   byte
		symmetric bool
	}{
		{"-##}", '-', true},
		{"~##}", '~', true},
		{"##}", 0, true},
		{"-#}", '-', false},
		{"~#}", '~', false},
		{"#}", 0, false},
	} {
		end := strings.LastIndex(text, closing.text)
		if end < bodyStart {
			continue
		}
		trim.Right = closing.control
		return text[bodyStart:end], trim, closing.symmetric, true
	}
	return "", twigTrim{}, false, false
}

func delimitedBody(text, open, close string) string {
	start := strings.Index(text, open)
	end := strings.LastIndex(text, close)
	if start < 0 || end < start+len(open) {
		return text
	}
	return strings.TrimSpace(text[start+len(open) : end])
}

func directNodes(node *cst.Node) []*cst.Node {
	if node == nil {
		return nil
	}
	result := make([]*cst.Node, 0, node.ChildCount())
	for child := range node.ChildNodes() {
		result = append(result, child)
	}
	return result
}

func childNode(node *cst.Node, kind cst.Kind) *cst.Node {
	if node == nil {
		return nil
	}
	child, _ := node.ChildOfKind(kind).(*cst.Node)
	return child
}

func nextBody(children []*cst.Node, index int) *cst.Node {
	if index+1 < len(children) && children[index+1].Kind() == syntax.Body {
		return children[index+1]
	}
	return nil
}

func tagName(starting *cst.Node) string {
	if token := starting.ChildTokenOfKind(syntax.TkWord); token != nil {
		return token.Text()
	}
	if token := starting.ChildTokenOfKind(syntax.TkTwigComponentName); token != nil {
		return token.Text()
	}
	return ""
}

func isVoidElement(name string) bool {
	switch strings.ToLower(name) {
	case "area", "base", "br", "col", "command", "embed", "hr", "img",
		"input", "keygen", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func firstByte(value string) byte {
	if value == "" {
		return 0
	}
	return value[0]
}
