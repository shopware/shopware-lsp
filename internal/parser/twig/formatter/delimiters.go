package formatter

// Helpers that emit Twig delimiter pairs, honoring whitespace-control
// modifiers. The lexer captures `{%-`, `-%}`, `{{-`, `-}}`, `{#-`, `-#}` on
// open/close tokens; the parser stores those flags on the corresponding AST
// node, and the formatter emits them back through these helpers.
//
// Using these everywhere — rather than hard-coded "{%" / "%}" literals —
// ensures a node that was parsed with trim modifiers round-trips exactly.

func openStmt(control byte) string {
	return "{%" + whitespaceControl(control)
}

func closeStmt(control byte) string {
	return whitespaceControl(control) + "%}"
}

func openExpr(control byte) string {
	return "{{" + whitespaceControl(control)
}

func closeExpr(control byte) string {
	return whitespaceControl(control) + "}}"
}

func openComment(control byte) string {
	return "{#" + whitespaceControl(control)
}

func closeComment(control byte) string {
	return whitespaceControl(control) + "#}"
}

func whitespaceControl(control byte) string {
	if control == '-' || control == '~' {
		return string(control)
	}
	return ""
}
