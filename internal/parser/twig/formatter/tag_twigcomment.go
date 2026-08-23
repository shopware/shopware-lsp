package formatter

import "strings"

// twigCommentNode represents a `{# ... #}` Twig comment. The body is the
// raw text between the delimiters, preserved verbatim (including leading
// and trailing whitespace).
type twigCommentNode struct {
	Body          string
	Trim          twigTrim
	Line          int
	Documentation bool
	Symmetric     bool
}

func (c *twigCommentNode) dump(r *renderer, indent int) string {
	var b strings.Builder
	indentStr := r.config.getIndent()
	for i := 0; i < indent; i++ {
		b.WriteString(indentStr)
	}
	if c.Documentation {
		b.WriteString("{##" + whitespaceControl(c.Trim.Left))
	} else {
		b.WriteString(openComment(c.Trim.Left))
	}
	b.WriteString(normalizeTwigCommentBody(c.Body))
	if c.Documentation && c.Symmetric {
		b.WriteString(whitespaceControl(c.Trim.Right) + "##}")
	} else {
		b.WriteString(closeComment(c.Trim.Right))
	}
	return b.String()
}

func normalizeTwigCommentBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return body
	}
	return " " + trimmed + " "
}
