package formatter

import "strings"

// attributeEntityEncoding specifies a pair of strings used during
// HTML attribute value encoding.
type attributeEntityEncoding struct {
	From string
	To   string
}

// fromTextToEntities is applied when emitting attribute values: literal
// characters are replaced with their HTML entity form.
var fromTextToEntities = []attributeEntityEncoding{
	{From: "\"", To: "&quot;"},
}

type renderer struct {
	config indentConfig
}

func (r *renderer) render(nodes nodeList) string {
	return nodes.dump(r, 0)
}

func (a *attribute) dump(r *renderer, indent int) string {
	var builder strings.Builder
	indentStr := r.config.getIndent()

	for range indent {
		builder.WriteString(indentStr)
	}

	if a.Value == "" {
		return builder.String() + a.Key
	}

	val := a.Value

	for _, encoding := range fromTextToEntities {
		val = strings.ReplaceAll(val, encoding.From, encoding.To)
	}

	return builder.String() + a.Key + "=\"" + val + "\""
}

// indentConfig controls how the formatter emits indentation.
type indentConfig struct {
	SpaceIndent             bool
	IndentSize              int
	TwigBlockIndentChildren bool
}

// defaultIndentConfig creates a default indentation config with spaces.
func defaultIndentConfig() indentConfig {
	return indentConfig{
		SpaceIndent:             true,
		IndentSize:              4,
		TwigBlockIndentChildren: true,
	}
}

// getIndent returns the indentation string based on configuration.
func (c indentConfig) getIndent() string {
	if c.SpaceIndent {
		return strings.Repeat(" ", c.IndentSize)
	}
	return "\t"
}

func (nodeList nodeList) dump(r *renderer, indent int) string {
	var builder strings.Builder
	for i, node := range nodeList {
		if _, ok := node.(*commentNode); ok {
			builder.WriteString(node.dump(r, indent))
			builder.WriteString("\n")
			continue
		}
		nodeOut := node.dump(r, indent)
		if i > 0 {
			// Add newline between non-comment nodes if not first
			if _, ok := nodeList[i-1].(*commentNode); !ok {
				// Skip the separator when either side already supplies a
				// newline at the boundary — the previous node's output
				// ending with "\n" (e.g. a rawNode whose source text
				// ended at a line break), or the next node's output
				// starting with "\n". Without this, parse → format →
				// parse → format adds one newline per pass at that
				// boundary, which surfaces on CSV/XML export templates
				// that emit one record per line via "{#- -#}" between
				// text segments.
				prev := builder.String()
				if !strings.HasSuffix(prev, "\n") && !strings.HasPrefix(nodeOut, "\n") {
					builder.WriteString("\n")
				}

				// Add extra newline between template elements
				if isTemplateElement(node) && isTemplateElement(nodeList[i-1]) {
					builder.WriteString("\n")
				}
			}
		}
		builder.WriteString(nodeOut)
	}

	// Remove trailing newlines
	result := builder.String()
	if len(nodeList) > 0 {
		result = strings.TrimRight(result, "\n")
		// Only add ending newline if the original string had at least one
		if strings.HasSuffix(builder.String(), "\n") {
			result += "\n"
		}
	}

	return result
}

// blockHasInlineMixedContent reports whether a twigBlockNode's body is
// inline-mixed: at least one rawNode carries non-whitespace text and all
// children are inline types. This is the JS/CSS-in-{% block %} case where
// flowing children as-is is correct; inserting blank lines between them
// (the default block-content formatting) would compound on every pass.
func blockHasInlineMixedContent(children nodeList) bool {
	if len(children) == 0 {
		return false
	}
	hasMeaningfulRaw := false
	for _, c := range children {
		switch n := c.(type) {
		case *rawNode:
			if strings.TrimSpace(n.Text) != "" {
				hasMeaningfulRaw = true
			}
		case *templateExpressionNode, *commentNode, *twigCommentNode:
			// inline
		default:
			return false
		}
	}
	return hasMeaningfulRaw
}

// isStructuredChild reports whether a node's dump output starts with its
// own indent prefix (vs. an inline value like {{ x }} or <span> text).
// Used by the <p>-children formatter to avoid double-counting whitespace
// when a preceding rawNode already supplies it.
func isStructuredChild(n node) bool {
	switch n.(type) {
	case *twigBlockNode, *twigIfNode, *twigGenericBlockNode,
		*twigStandaloneTagNode, *twigVerbatimNode, *twigCommentNode,
		*parentNode, *commentNode:
		return true
	}
	return false
}

// whitespacePreservingTags lists elements whose rendered output depends on
// the literal whitespace of their content (per HTML's white-space rules).
// The formatter must emit their children byte-for-byte instead of
// re-indenting or re-flowing them.
var whitespacePreservingTags = map[string]bool{
	"pre":      true,
	"textarea": true,
}

// dumpVerbatim renders nodes without inserting or removing any whitespace.
// Used for the contents of whitespace-preserving elements like <pre>, where
// re-indenting would change what the browser displays. Nested elements are
// emitted inline (attributes on one line) and their children recurse in
// verbatim mode too.
func dumpVerbatim(r *renderer, builder *strings.Builder, nodes nodeList) {
	for _, node := range nodes {
		switch n := node.(type) {
		case *elementNode:
			builder.WriteString("<")
			builder.WriteString(n.Tag)
			for _, attr := range n.Attributes {
				builder.WriteString(" ")
				builder.WriteString(attr.dump(r, 0))
			}
			if n.SelfClosing {
				builder.WriteString("/>")
				continue
			}
			builder.WriteString(">")
			dumpVerbatim(r, builder, n.Children)
			if !n.Unclosed {
				builder.WriteString("</")
				builder.WriteString(n.Tag)
				builder.WriteString(">")
			}
		case *twigIfNode:
			for i, br := range n.Branches {
				builder.WriteString(openStmt(br.Trim.Left))
				if i == 0 {
					builder.WriteString(" if ")
					builder.WriteString(br.Condition)
					builder.WriteString(" ")
				} else {
					builder.WriteString(" elseif ")
					builder.WriteString(br.Condition)
					builder.WriteString(" ")
				}
				builder.WriteString(closeStmt(br.Trim.Right))
				dumpVerbatim(r, builder, br.Body)
			}
			if len(n.ElseChildren) > 0 {
				builder.WriteString(openStmt(n.ElseTrim.Left))
				builder.WriteString(" else ")
				builder.WriteString(closeStmt(n.ElseTrim.Right))
				dumpVerbatim(r, builder, n.ElseChildren)
			}
			builder.WriteString(openStmt(n.EndTrim.Left))
			builder.WriteString(" endif ")
			builder.WriteString(closeStmt(n.EndTrim.Right))
		case *twigBlockNode:
			builder.WriteString(openStmt(n.OpenTrim.Left))
			builder.WriteString(" block ")
			builder.WriteString(n.Name)
			builder.WriteString(" ")
			builder.WriteString(closeStmt(n.OpenTrim.Right))
			dumpVerbatim(r, builder, n.Children)
			builder.WriteString(openStmt(n.CloseTrim.Left))
			builder.WriteString(" endblock ")
			builder.WriteString(closeStmt(n.CloseTrim.Right))
		case *twigGenericBlockNode:
			builder.WriteString(openStmt(n.OpenTrim.Left))
			builder.WriteString(" ")
			builder.WriteString(n.Name)
			if n.Args != "" {
				builder.WriteString(" ")
				builder.WriteString(n.Args)
			}
			builder.WriteString(" ")
			builder.WriteString(closeStmt(n.OpenTrim.Right))
			dumpVerbatim(r, builder, n.Body)
			if len(n.Else) > 0 {
				builder.WriteString(openStmt(n.ElseTrim.Left))
				builder.WriteString(" else ")
				builder.WriteString(closeStmt(n.ElseTrim.Right))
				dumpVerbatim(r, builder, n.Else)
			}
			builder.WriteString(openStmt(n.CloseTrim.Left))
			builder.WriteString(" ")
			builder.WriteString(n.EndTag)
			builder.WriteString(" ")
			builder.WriteString(closeStmt(n.CloseTrim.Right))
		default:
			builder.WriteString(node.dump(r, 0))
		}
	}
}

func isTemplateElement(node node) bool {
	if elem, ok := node.(*elementNode); ok {
		return elem.Tag == "template"
	}
	// Also consider twig blocks as template elements for spacing purposes
	if _, ok := node.(*twigBlockNode); ok {
		return true
	}
	return false
}

func (n *rawNode) dump(_ *renderer, _ int) string {
	return n.Text
}

func (c *commentNode) dump(r *renderer, indent int) string {
	var builder strings.Builder
	indentStr := r.config.getIndent()
	for range indent {
		builder.WriteString(indentStr)
	}

	builder.WriteString("<!-- ")
	builder.WriteString(c.Text)
	builder.WriteString(" -->")

	return builder.String()
}

func (t *templateExpressionNode) dump(_ *renderer, _ int) string {
	return openExpr(t.Trim.Left) + t.Expression + closeExpr(t.Trim.Right)
}

// dump renders an HTML element with attributes, children, and end tag.
func (e *elementNode) dump(r *renderer, indent int) string {
	var builder strings.Builder
	indentStr := r.config.getIndent()
	writeIndent(&builder, indent, indentStr)
	builder.WriteString("<")
	builder.WriteString(e.Tag)
	if e.dumpAttributes(r, &builder, indent) {
		writeIndent(&builder, indent, indentStr)
	}
	if e.SelfClosing {
		builder.WriteString("/>")
		return builder.String()
	}
	builder.WriteString(">")
	if len(e.Children) > 0 {
		if whitespacePreservingTags[strings.ToLower(e.Tag)] {
			dumpVerbatim(r, &builder, e.Children)
			e.writeClosingTag(&builder)
			return builder.String()
		}
		if e.Tag == "p" {
			e.dumpParagraphChildren(r, &builder, indent)
		} else {
			e.dumpChildren(r, &builder, indent)
		}
	}
	e.writeClosingTag(&builder)
	return builder.String()
}

func (e *elementNode) dumpAttributes(
	r *renderer,
	builder *strings.Builder,
	indent int,
) bool {
	if len(e.Attributes) == 0 {
		return false
	}
	if len(e.Attributes) == 1 {
		attribute := e.Attributes[0]
		attributeText := attribute.dump(r, indent+1)
		_, isIf := attribute.(*twigIfNode)
		if len(attributeText) > 80 || isIf {
			builder.WriteString("\n")
			builder.WriteString(attributeText)
			builder.WriteString("\n")
			return true
		}
		builder.WriteString(" ")
		builder.WriteString(attribute.dump(r, 0))
		return false
	}
	for _, attribute := range e.Attributes {
		builder.WriteString("\n")
		builder.WriteString(attribute.dump(r, indent+1))
	}
	builder.WriteString("\n")
	return true
}

func (e *elementNode) dumpParagraphChildren(
	r *renderer,
	builder *strings.Builder,
	indent int,
) {
	if hasLongTemplateExpression(r, e.Children) {
		dumpExpandedInlineChildren(r, builder, e.Children, indent)
		return
	}
	for _, child := range e.Children {
		if isStructuredChild(child) {
			text := strings.TrimRight(builder.String(), " \t")
			builder.Reset()
			builder.WriteString(text)
		}
		if _, ok := child.(*elementNode); ok {
			builder.WriteString(child.dump(r, 0))
			continue
		}
		builder.WriteString(child.dump(r, indent))
	}
}

type childLayout struct {
	simple                    bool
	hasLongTemplateExpression bool
	templateExpressions       int
	multipleShortExpressions  bool
}

func (e *elementNode) dumpChildren(
	r *renderer,
	builder *strings.Builder,
	indent int,
) {
	layout := analyzeChildLayout(r, e.Children, indent)
	if !layout.simple {
		dumpComplexChildren(r, builder, e.Children, indent)
		return
	}
	if layout.hasLongTemplateExpression ||
		(layout.templateExpressions > 1 && !layout.multipleShortExpressions) {
		dumpExpandedInlineChildren(r, builder, e.Children, indent)
		return
	}
	for _, child := range e.Children {
		builder.WriteString(child.dump(r, indent))
	}
}

func analyzeChildLayout(
	r *renderer,
	children nodeList,
	indent int,
) childLayout {
	layout := childLayout{simple: true}
	for _, child := range children {
		switch value := child.(type) {
		case *templateExpressionNode:
			layout.templateExpressions++
			layout.hasLongTemplateExpression = layout.hasLongTemplateExpression ||
				len(value.dump(r, 0)) > 30
		case *rawNode, *commentNode:
		default:
			layout.simple = false
			return layout
		}
	}
	if len(children) == 1 {
		if raw, ok := children[0].(*rawNode); ok && rawHasIndentedContent(raw.Text) {
			layout.simple = false
			return layout
		}
	}
	if layout.templateExpressions > 1 && !layout.hasLongTemplateExpression {
		totalLength := 0
		for _, child := range children {
			if expression, ok := child.(*templateExpressionNode); ok {
				totalLength += len(expression.dump(r, indent+1))
			}
		}
		layout.multipleShortExpressions = totalLength <= 100
	}
	return layout
}

func rawHasIndentedContent(text string) bool {
	if !strings.Contains(text, "\n") {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(line) > len(trimmed) {
			return true
		}
	}
	return false
}

func hasLongTemplateExpression(r *renderer, children nodeList) bool {
	for _, child := range children {
		if expression, ok := child.(*templateExpressionNode); ok &&
			len(expression.dump(r, 0)) > 30 {
			return true
		}
	}
	return false
}

func dumpExpandedInlineChildren(
	r *renderer,
	builder *strings.Builder,
	children nodeList,
	indent int,
) {
	indentStr := r.config.getIndent()
	builder.WriteString("\n")
	for _, child := range children {
		switch value := child.(type) {
		case *templateExpressionNode:
			writeIndent(builder, indent+1, indentStr)
			builder.WriteString(value.dump(r, indent+1))
			builder.WriteString("\n")
		case *rawNode:
			trimmed := strings.TrimSpace(value.Text)
			if trimmed != "" {
				writeIndent(builder, indent+1, indentStr)
				builder.WriteString(trimmed)
				builder.WriteString("\n")
			}
		default:
			builder.WriteString(child.dump(r, indent+1))
		}
	}
	writeIndent(builder, indent, indentStr)
}

func dumpComplexChildren(
	r *renderer,
	builder *strings.Builder,
	children nodeList,
	indent int,
) {
	children = nonEmptyNodes(children)
	for index, child := range children {
		builder.WriteString("\n")
		if index > 0 && isTemplateElement(child) && isTemplateElement(children[index-1]) {
			builder.WriteString("\n")
		}
		dumpComplexChild(r, builder, child, indent)
	}
	builder.WriteString("\n")
	writeIndent(builder, indent, r.config.getIndent())
}

func nonEmptyNodes(children nodeList) nodeList {
	result := make(nodeList, 0, len(children))
	for _, child := range children {
		if raw, ok := child.(*rawNode); ok && strings.TrimSpace(raw.Text) == "" {
			continue
		}
		result = append(result, child)
	}
	return result
}

func dumpComplexChild(
	r *renderer,
	builder *strings.Builder,
	child node,
	indent int,
) {
	switch value := child.(type) {
	case *elementNode:
		builder.WriteString(value.dump(r, indent+1))
	case *twigBlockNode:
		builder.WriteString(value.dump(r, indent+1))
	case *rawNode:
		dumpComplexRaw(r, builder, value.Text, indent)
	default:
		writeIndent(builder, indent+1, r.config.getIndent())
		builder.WriteString(strings.TrimSpace(child.dump(r, indent+1)))
	}
}

func dumpComplexRaw(
	r *renderer,
	builder *strings.Builder,
	text string,
	indent int,
) {
	if !strings.Contains(text, "\n") {
		writeIndent(builder, indent+1, r.config.getIndent())
		builder.WriteString(strings.TrimSpace(text))
		return
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimLeft(line, " \t"); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	for index, line := range lines {
		writeIndent(builder, indent+1, r.config.getIndent())
		builder.WriteString(line)
		if index < len(lines)-1 {
			builder.WriteString("\n")
		}
	}
}

func (e *elementNode) writeClosingTag(builder *strings.Builder) {
	if e.Unclosed {
		return
	}
	builder.WriteString("</")
	builder.WriteString(e.Tag)
	builder.WriteString(">")
}

func writeIndent(builder *strings.Builder, indent int, indentStr string) {
	for range indent {
		builder.WriteString(indentStr)
	}
}

func (t *twigBlockNode) dump(r *renderer, indent int) string {
	var builder strings.Builder
	indentStr := r.config.getIndent()
	for range indent {
		builder.WriteString(indentStr)
	}
	builder.WriteString(openStmt(t.OpenTrim.Left))
	builder.WriteString(" block ")
	builder.WriteString(t.Name)
	builder.WriteString(" ")
	builder.WriteString(closeStmt(t.OpenTrim.Right))

	// Inline content: all children are text or short expressions (no nested
	// block/element). This is the common case for {% block %} wrapping JS or
	// CSS where embedded {{ x }} interpolations break naive block-format
	// rules (`\n\n` between children would add blank lines that compound on
	// every format pass). Concatenate children as-is so the embedded
	// whitespace from RawNodes drives layout.
	if blockHasInlineMixedContent(t.Children) {
		for _, child := range t.Children {
			// Twig comments inside inline-mixed bodies (e.g. {# note #}
			// between JS statements) get their visible indent from the
			// preceding rawNode; calling dump(indent) would have them add
			// their own indent on top, compounding on every pass.
			if _, ok := child.(*twigCommentNode); ok {
				builder.WriteString(child.dump(r, 0))
				continue
			}
			builder.WriteString(child.dump(r, indent))
		}
		builder.WriteString(openStmt(t.CloseTrim.Left))
		builder.WriteString(" endblock ")
		builder.WriteString(closeStmt(t.CloseTrim.Right))
		return builder.String()
	}

	// Filter out empty nodes and normalize newlines
	var nonEmptyChildren nodeList
	for _, child := range t.Children {
		if raw, ok := child.(*rawNode); ok {
			if strings.TrimSpace(raw.Text) != "" {
				nonEmptyChildren = append(nonEmptyChildren, raw)
			}
		} else if twigBlock, ok := child.(*twigBlockNode); ok {
			if strings.TrimSpace(twigBlock.dump(r, 0)) != "" {
				nonEmptyChildren = append(nonEmptyChildren, twigBlock)
			}
		} else {
			nonEmptyChildren = append(nonEmptyChildren, child)
		}
	}

	if len(nonEmptyChildren) > 0 {
		builder.WriteString("\n")
		childIndent := indent
		if r.config.TwigBlockIndentChildren {
			childIndent = indent + 1
		}

		for i, child := range nonEmptyChildren {
			if elementChild, ok := child.(*elementNode); ok {
				builder.WriteString(elementChild.dump(r, childIndent))
			} else if tplChild, ok := child.(*templateExpressionNode); ok {
				// Template expressions need proper indentation when they're direct children of twig blocks
				for j := 0; j < childIndent; j++ {
					builder.WriteString(indentStr)
				}
				builder.WriteString(tplChild.dump(r, childIndent))
			} else if rawChild, ok := child.(*rawNode); ok {
				// Trim incidental whitespace from the source so re-formats
				// don't compound newlines on either side of the rawNode.
				for j := 0; j < childIndent; j++ {
					builder.WriteString(indentStr)
				}
				builder.WriteString(strings.TrimSpace(rawChild.Text))
			} else {
				builder.WriteString(child.dump(r, childIndent))
			}

			_, isComment := child.(*commentNode)

			if i < len(nonEmptyChildren)-1 {
				// Add an extra newline between elements
				if isComment {
					builder.WriteString("\n")
				} else {
					builder.WriteString("\n\n")
				}
			}
		}
		builder.WriteString("\n")

		for i := 0; i < indent; i++ {
			builder.WriteString(indentStr)
		}

		builder.WriteString(openStmt(t.CloseTrim.Left))
		builder.WriteString(" endblock ")
		builder.WriteString(closeStmt(t.CloseTrim.Right))
	} else {
		builder.WriteString(openStmt(t.CloseTrim.Left))
		builder.WriteString(" endblock ")
		builder.WriteString(closeStmt(t.CloseTrim.Right))
	}

	return builder.String()
}

func (t *twigIfNode) dump(r *renderer, indent int) string {
	var builder strings.Builder
	indentStr := r.config.getIndent()
	writeIndent := func(n int) {
		for i := 0; i < n; i++ {
			builder.WriteString(indentStr)
		}
	}

	// Each branch (if + any elseifs) renders the same way: a header line
	// at `indent`, then its body indented one level deeper.
	for i, br := range t.Branches {
		writeIndent(indent)
		builder.WriteString(openStmt(br.Trim.Left))
		if i == 0 {
			builder.WriteString(" if ")
		} else {
			builder.WriteString(" elseif ")
		}
		builder.WriteString(br.Condition)
		builder.WriteString(" ")
		builder.WriteString(closeStmt(br.Trim.Right))
		writeIfBranchBody(r, &builder, br.Body, indent, indentStr)
	}

	if len(t.ElseChildren) > 0 {
		writeIndent(indent)
		builder.WriteString(openStmt(t.ElseTrim.Left))
		builder.WriteString(" else ")
		builder.WriteString(closeStmt(t.ElseTrim.Right))
		writeIfBranchBody(r, &builder, t.ElseChildren, indent, indentStr)
	}

	writeIndent(indent)
	builder.WriteString(openStmt(t.EndTrim.Left))
	builder.WriteString(" endif ")
	builder.WriteString(closeStmt(t.EndTrim.Right))
	return builder.String()
}

// writeIfBranchBody emits the body of a single if/elseif/else branch,
// filtering whitespace-only RawNodes and one-level-indented per child.
func writeIfBranchBody(
	r *renderer,
	builder *strings.Builder,
	children nodeList,
	indent int,
	indentStr string,
) {
	var nonEmpty nodeList
	for _, child := range children {
		if raw, ok := child.(*rawNode); ok {
			if strings.TrimSpace(raw.Text) == "" {
				continue
			}
			nonEmpty = append(nonEmpty, raw)
			continue
		}
		nonEmpty = append(nonEmpty, child)
	}
	if len(nonEmpty) == 0 {
		return
	}
	builder.WriteString("\n")
	for i, child := range nonEmpty {
		if elementChild, ok := child.(*elementNode); ok {
			builder.WriteString(elementChild.dump(r, indent+1))
		} else {
			for j := 0; j < indent+1; j++ {
				builder.WriteString(indentStr)
			}
			builder.WriteString(strings.TrimSpace(child.dump(r, indent+1)))
		}
		if i < len(nonEmpty)-1 {
			builder.WriteString("\n")
		}
	}
	builder.WriteString("\n")
}

func (p *parentNode) dump(r *renderer, indent int) string {
	var builder strings.Builder
	indentStr := r.config.getIndent()
	for i := 0; i < indent; i++ {
		builder.WriteString(indentStr)
	}
	builder.WriteString(openStmt(p.Trim.Left))
	builder.WriteString(" parent() ")
	builder.WriteString(closeStmt(p.Trim.Right))
	return builder.String()
}
