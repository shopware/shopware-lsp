package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// parseFunction is a concrete parsing function pointer, ported from ludtwig's
// ParseFunction (fn(&mut Parser) -> Option<CompletedMarker>). The bool return is
// the Go equivalent of Rust's Option: true == Some(marker), false == None.
type parseFunction func(p *parser) (completedMarker, bool)

// root parses the whole document into a ROOT node. Port of grammar.rs:root.
func root(p *parser) {
	m := p.start()

	parseMany(
		p,
		func(_ *parser) bool { return false },
		func(p *parser) {
			if _, ok := parseAnyElement(p); !ok && !p.atEnd() {
				// not parsable element encountered
				p.addError(newErrorBuilder("html, text or twig element"))
				// at least consume unparseable input
				p.recover(nil) // exception to the parse_many no recover rule of thumb
			}
		},
	)

	p.complete(m, syntax.Root)
}

// parseMany is the universal loop combinator. Port of grammar.rs:parse_many.
// It records the parser position before each iteration, silently bumps
// TK_UNKNOWN tokens, and breaks on EOF, on the early-exit closure, or when the
// child parser made no progress (position unchanged) — the infinite-loop guard.
func parseMany(p *parser, earlyExit func(*parser) bool, child func(*parser)) {
	for {
		// capture parser token position to prevent infinite loops!
		parserPos := p.getPos()

		if p.at(syntax.TkUnknown) {
			p.bump() // skip / ignore unknown tokens
			continue
		}

		if p.atEnd() || earlyExit(p) {
			break
		}

		child(p)
		if p.getPos() == parserPos {
			break
		}
	}
}

// parseAnyElement dispatches to twig first, then HTML. Port of
// grammar.rs:parse_any_element. Twig always has priority over HTML, recursively
// passing itself as the child parser for tag bodies.
func parseAnyElement(p *parser) (completedMarker, bool) {
	if cm, ok := parseAnyTwig(p, parseAnyElement); ok {
		return cm, true
	}
	return parseAnyHtml(p)
}

// parseLudtwigDirective parses a ludtwig-ignore / ludtwig-ignore-file directive.
// Port of grammar.rs:parse_ludtwig_directive. outer is the already-started
// marker of the enclosing comment; closingKinds are the closing delimiter kinds
// (#}/-#}/~#} for twig comments, --> for HTML comments).
func parseLudtwigDirective(p *parser, outer *marker, closingKinds []syntax.Kind) completedMarker {
	// debug_assert!(parser.at_set(&[ludtwig-ignore-file, ludtwig-ignore]))
	var ignoreKind syntax.Kind
	if p.at(syntax.TkLudtwigIgnoreFile) {
		ignoreKind = syntax.LudtwigDirectiveFileIgnore
	} else {
		ignoreKind = syntax.LudtwigDirectiveIgnore
	}
	p.bump()

	ruleListM := p.start()
	parseMany(
		p,
		func(p *parser) bool { return p.atSet(closingKinds) },
		func(p *parser) {
			// Build a recovery set that includes "," and the closing kinds
			recovery := make([]syntax.Kind, 0, len(closingKinds)+1)
			recovery = append(recovery, syntax.TkComma)
			recovery = append(recovery, closingKinds...)
			p.expect(syntax.TkWord, recovery)
			if p.at(syntax.TkComma) {
				p.bump()
			}
		},
	)
	p.complete(ruleListM, syntax.LudtwigDirectiveRuleList)

	p.expectAny(closingKinds, nil)
	return p.complete(outer, ignoreKind)
}
