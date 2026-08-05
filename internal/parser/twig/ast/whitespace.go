package ast

import "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"

// Trim describes a twig whitespace-control modifier on a delimiter token.
// Ports typed.rs::WhitespaceTrimming (plus a None case that Rust models with
// Option).
type Trim uint8

const (
	// TrimNone means no whitespace-control modifier is present.
	TrimNone Trim = iota
	// TrimAll removes all surrounding whitespace, including newlines (the `-`
	// modifier: `{%-`, `{{-`, `{#-`, `-%}`, `-}}`, `-#}`).
	TrimAll
	// TrimKeepNewlines removes surrounding whitespace except newlines (the `~`
	// modifier: `{%~`, `{{~`, `{#~`, `~%}`, `~}}`, `~#}`).
	TrimKeepNewlines
)

// LeadingTrim reports the whitespace-control modifier on the opening delimiter
// of a tag-like node, inspecting its first non-trivia direct child token.
// Ports HasWhitespaceControl::leading_ws_control.
func LeadingTrim(n *syntax.Node) Trim {
	tok := firstNonTriviaToken(n)
	if tok == nil {
		return TrimNone
	}
	switch tok.Kind() {
	case syntax.TkCurlyPercentMinus, syntax.TkOpenCurlyCurlyMinus, syntax.TkOpenCurlyHashtagMinus:
		return TrimAll
	case syntax.TkCurlyPercentTilde, syntax.TkOpenCurlyCurlyTilde, syntax.TkOpenCurlyHashtagTilde:
		return TrimKeepNewlines
	default:
		return TrimNone
	}
}

// TrailingTrim reports the whitespace-control modifier on the closing delimiter
// of a tag-like node, inspecting its last non-trivia direct child token.
// Ports HasWhitespaceControl::trailing_ws_control.
func TrailingTrim(n *syntax.Node) Trim {
	tok := lastNonTriviaToken(n)
	if tok == nil {
		return TrimNone
	}
	switch tok.Kind() {
	case syntax.TkMinusPercentCurly, syntax.TkMinusCloseCurlyCurly, syntax.TkMinusHashtagCloseCurly:
		return TrimAll
	case syntax.TkTildePercentCurly, syntax.TkTildeCloseCurlyCurly, syntax.TkTildeHashtagCloseCurly:
		return TrimKeepNewlines
	default:
		return TrimNone
	}
}
