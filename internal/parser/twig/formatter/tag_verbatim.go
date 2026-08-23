package formatter

import "strings"

// twigVerbatimNode represents `{% verbatim %}...{% endverbatim %}`.
// The body is preserved as raw text — the parser does NOT recurse into
// the body, since the whole point of `{% verbatim %}` is to disable Twig
// interpretation for its contents.
type twigVerbatimNode struct {
	Body      string
	OpenTrim  twigTrim
	CloseTrim twigTrim
	Line      int
}

// dump renders the verbatim block with its body byte-identical to source.
func (v *twigVerbatimNode) dump(r *renderer, indent int) string {
	var b strings.Builder
	indentStr := r.config.getIndent()
	for i := 0; i < indent; i++ {
		b.WriteString(indentStr)
	}
	b.WriteString(openStmt(v.OpenTrim.Left))
	b.WriteString(" verbatim ")
	b.WriteString(closeStmt(v.OpenTrim.Right))
	b.WriteString(v.Body)
	b.WriteString(openStmt(v.CloseTrim.Left))
	b.WriteString(" endverbatim ")
	b.WriteString(closeStmt(v.CloseTrim.Right))
	return b.String()
}
