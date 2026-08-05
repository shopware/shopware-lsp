// Package ast provides typed wrappers over the untyped syntax tree, a port of
// ludtwig-parser's syntax/typed.rs. Each composite node kind has a zero-cost
// wrapper struct with a checked Cast function and a Syntax escape hatch; some
// wrappers add custom accessors that mirror the Rust accessors exactly.
package ast

import "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"

// firstChildNode returns the first direct child node of the given kind, or nil.
// Ports rowan::ast::support::child (the kind-checked variant).
func firstChildNode(n *syntax.Node, kind syntax.Kind) *syntax.Node {
	if n == nil {
		return nil
	}
	cursor := n.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

// nthChildNode returns the nth (0-based) direct child node of the given kind,
// or nil. Ports support::children(...).nth(i).
func nthChildNode(n *syntax.Node, kind syntax.Kind, i int) *syntax.Node {
	if n == nil {
		return nil
	}
	count := 0
	cursor := n.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() != kind {
			continue
		}
		if count == i {
			return child
		}
		count++
	}
	return nil
}

// childToken returns the first direct child token of the given kind, or nil.
// Ports rowan::ast::support::token.
func childToken(n *syntax.Node, kind syntax.Kind) *syntax.Token {
	if n == nil {
		return nil
	}
	return n.ChildTokenOfKind(kind)
}

// firstNonTriviaToken returns the first non-trivia direct child token, or nil.
// Ports typed.rs::first_non_trivia_token.
func firstNonTriviaToken(n *syntax.Node) *syntax.Token {
	if n == nil {
		return nil
	}
	cursor := n.ChildTokenCursor()
	for cursor.Next() {
		tok := cursor.Token()
		if !tok.Kind().IsTrivia() {
			return tok
		}
	}
	return nil
}

// lastNonTriviaToken returns the last non-trivia direct child token, or nil.
// Ports typed.rs::last_non_trivia_token.
func lastNonTriviaToken(n *syntax.Node) *syntax.Token {
	if n == nil {
		return nil
	}
	var found *syntax.Token
	cursor := n.ChildTokenCursor()
	for cursor.Next() {
		tok := cursor.Token()
		if !tok.Kind().IsTrivia() {
			found = tok
		}
	}
	return found
}

// firstTokenWithKind returns the first direct child token whose kind is one of
// the given kinds, or nil.
func firstTokenWithKind(n *syntax.Node, kinds ...syntax.Kind) *syntax.Token {
	if n == nil {
		return nil
	}
	cursor := n.ChildTokenCursor()
	for cursor.Next() {
		tok := cursor.Token()
		for _, k := range kinds {
			if tok.Kind() == k {
				return tok
			}
		}
	}
	return nil
}

// openingQuoteToken returns the first non-trivia token that appears before the
// first direct child NODE (the inner node). Ports the take_while/find_map logic
// used by TwigLiteralString/HtmlString get_opening_quote.
func openingQuoteToken(n *syntax.Node) *syntax.Token {
	if n == nil {
		return nil
	}
	for index := 0; index < n.ChildCount(); index++ {
		c := n.Child(index)
		if _, isNode := c.(*syntax.Node); isNode {
			// reached the inner string node; no quote found before it
			return nil
		}
		if tok, ok := c.(*syntax.Token); ok && !tok.Kind().IsTrivia() {
			return tok
		}
	}
	return nil
}

// closingQuoteToken returns the first non-trivia token that appears after the
// first direct child NODE (the inner node). Ports the skip_while/find_map logic
// used by TwigLiteralString/HtmlString get_closing_quote.
func closingQuoteToken(n *syntax.Node) *syntax.Token {
	if n == nil {
		return nil
	}
	seenNode := false
	for index := 0; index < n.ChildCount(); index++ {
		c := n.Child(index)
		if _, isNode := c.(*syntax.Node); isNode {
			seenNode = true
			continue
		}
		if tok, ok := c.(*syntax.Token); ok && seenNode && !tok.Kind().IsTrivia() {
			return tok
		}
	}
	return nil
}
