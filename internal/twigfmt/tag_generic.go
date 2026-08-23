package twigfmt

import "strings"

// TwigGenericBlockNode represents a Twig statement tag that opens a block,
// e.g. `{% for x in xs %}body{% endfor %}` or `{% embed 't' %}body{%
// endembed %}`. The body is parsed recursively as Twig+HTML content.
//
// Else is populated when the block tag supports an `{% else %}` follower
// (currently just `{% for %}`); it holds the body between `{% else %}` and
// the block's EndTag. Else is nil/empty for tags without an else clause.
type TwigGenericBlockNode struct {
	Name      string
	Args      string // raw args body, with leading/trailing space stripped
	EndTag    string
	Body      NodeList
	Else      NodeList
	OpenTrim  TwigTrim
	ElseTrim  TwigTrim
	CloseTrim TwigTrim
	Line      int
}

func (n *TwigGenericBlockNode) Dump(indent int) string {
	var b strings.Builder
	indentStr := indentConfig.GetIndent()
	for i := 0; i < indent; i++ {
		b.WriteString(indentStr)
	}
	b.WriteString(openStmt(n.OpenTrim.Left))
	b.WriteString(" ")
	b.WriteString(n.Name)
	if n.Args != "" {
		b.WriteString(" ")
		b.WriteString(n.Args)
	}
	b.WriteString(" ")
	b.WriteString(closeStmt(n.OpenTrim.Right))

	// Inline-mixed body (text + {{ x }} only, no nested blocks/elements):
	// flow children verbatim so embedded whitespace drives layout. Without
	// this, the per-child re-indent and TrimSpace strip the spaces around
	// expressions and the layout drifts on every format pass. We never take
	// the inline-mixed path when there's an `{% else %}` branch to render —
	// the else clause needs the structured block layout.
	if len(n.Else) == 0 && blockHasInlineMixedContent(n.Body) {
		for _, child := range n.Body {
			if _, ok := child.(*TwigCommentNode); ok {
				b.WriteString(child.Dump(0))
				continue
			}
			b.WriteString(child.Dump(indent))
		}
		b.WriteString(openStmt(n.CloseTrim.Left))
		b.WriteString(" ")
		b.WriteString(n.EndTag)
		b.WriteString(" ")
		b.WriteString(closeStmt(n.CloseTrim.Right))
		return b.String()
	}

	if len(n.Body) > 0 {
		b.WriteString("\n")
		for i, child := range n.Body {
			if elem, ok := child.(*ElementNode); ok {
				b.WriteString(elem.Dump(indent + 1))
			} else {
				for j := 0; j < indent+1; j++ {
					b.WriteString(indentStr)
				}
				b.WriteString(strings.TrimSpace(child.Dump(indent + 1)))
			}
			if i < len(n.Body)-1 {
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
		for i := 0; i < indent; i++ {
			b.WriteString(indentStr)
		}
	}

	if len(n.Else) > 0 {
		b.WriteString(openStmt(n.ElseTrim.Left))
		b.WriteString(" else ")
		b.WriteString(closeStmt(n.ElseTrim.Right))
		b.WriteString("\n")
		for i, child := range n.Else {
			if elem, ok := child.(*ElementNode); ok {
				b.WriteString(elem.Dump(indent + 1))
			} else {
				for j := 0; j < indent+1; j++ {
					b.WriteString(indentStr)
				}
				b.WriteString(strings.TrimSpace(child.Dump(indent + 1)))
			}
			if i < len(n.Else)-1 {
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
		for i := 0; i < indent; i++ {
			b.WriteString(indentStr)
		}
	}

	b.WriteString(openStmt(n.CloseTrim.Left))
	b.WriteString(" ")
	b.WriteString(n.EndTag)
	b.WriteString(" ")
	b.WriteString(closeStmt(n.CloseTrim.Right))
	return b.String()
}

// TwigStandaloneTagNode represents a Twig tag with no body, e.g.
// `{% include "x.twig" %}`, `{% extends "@base" %}`, `{% set x = 1 %}`,
// `{% sw_include "..." with {} %}`.
type TwigStandaloneTagNode struct {
	Name string
	Args string
	Trim TwigTrim
	Line int
}

func (n *TwigStandaloneTagNode) Dump(indent int) string {
	var b strings.Builder
	indentStr := indentConfig.GetIndent()
	for i := 0; i < indent; i++ {
		b.WriteString(indentStr)
	}
	b.WriteString(openStmt(n.Trim.Left))
	b.WriteString(" ")
	b.WriteString(n.Name)
	if n.Args != "" {
		b.WriteString(" ")
		b.WriteString(n.Args)
	}
	b.WriteString(" ")
	b.WriteString(closeStmt(n.Trim.Right))
	return b.String()
}
