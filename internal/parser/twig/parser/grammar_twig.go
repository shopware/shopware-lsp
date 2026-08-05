package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// parseAnyTwig dispatches on the current token to a twig construct. Port of
// grammar/twig.rs:parse_any_twig. childParser determines what is allowed inside
// the twig construct's body (element children, attributes, string content, raw
// text). Returns (_, false) — Rust None — when not at a twig open token.
func parseAnyTwig(p *parser, childParser parseFunction) (completedMarker, bool) {
	if p.atTwigBlockOpen() {
		return parseTwigBlockStatement(p, childParser)
	} else if p.atTwigVarOpen() {
		return parseTwigVarStatement(p), true
	} else if p.atTwigCommentOpen() {
		return parseTwigCommentStatement(p), true
	}
	return completedMarker{}, false
}

// parseTwigCommentStatement parses a {# ... #} comment, detecting ludtwig-ignore
// directives. Port of grammar/twig.rs:parse_twig_comment_statement.
func parseTwigCommentStatement(p *parser) completedMarker {
	// debug_assert!(parser.at_twig_comment_open())
	m := p.start()
	p.bump()

	if p.atSet([]syntax.Kind{syntax.TkLudtwigIgnoreFile, syntax.TkLudtwigIgnore}) {
		return parseLudtwigDirective(p, m, twigCommentCloseSet)
	}
	return parseTwigPlainComment(p, m)
}

// parseTwigPlainComment bumps everything until the comment close into a flat
// TWIG_COMMENT node. Port of grammar/twig.rs:parse_twig_plain_comment.
func parseTwigPlainComment(p *parser, outer *marker) completedMarker {
	parseMany(
		p,
		func(p *parser) bool { return p.atSet(twigCommentCloseSet) },
		func(p *parser) {
			p.bump()
		},
	)

	p.expectAny(twigCommentCloseSet, nil)
	return p.complete(outer, syntax.TwigComment)
}

// parseTwigVarStatement parses a {{ expr }} output statement. Port of
// grammar/twig.rs:parse_twig_var_statement.
func parseTwigVarStatement(p *parser) completedMarker {
	// debug_assert!(parser.at_twig_var_open())
	m := p.start()
	p.bump()

	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("twig expression"))
		p.recover(twigExpressionRecoverySet)
	}

	p.expectAny(twigVarCloseSet, nil)
	return p.complete(m, syntax.TwigVar)
}
