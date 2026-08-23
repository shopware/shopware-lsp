package twigfmt

import "strings"

// TwigVerbatimNode represents `{% verbatim %}...{% endverbatim %}`.
// The body is preserved as raw text — the parser does NOT recurse into
// the body, since the whole point of `{% verbatim %}` is to disable Twig
// interpretation for its contents.
type TwigVerbatimNode struct {
	Body      string
	OpenTrim  TwigTrim
	CloseTrim TwigTrim
	Line      int
}

// Dump renders the verbatim block with its body byte-identical to source.
func (v *TwigVerbatimNode) Dump(indent int) string {
	var b strings.Builder
	indentStr := indentConfig.GetIndent()
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
