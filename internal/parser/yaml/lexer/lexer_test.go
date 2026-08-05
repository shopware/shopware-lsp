package lexer

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

func TestLexIsLosslessAndRecognizesStructure(t *testing.T) {
	source := "services:\n  app.service:\n    class: 'App\\\\Service'\n    tags: [{ name: kernel.event }]\n"
	tokens := Lex(source)

	var reconstructed string
	var hasIndent, hasFlowMapping, hasQuoted bool
	for _, token := range tokens {
		reconstructed += token.Text()
		hasIndent = hasIndent || token.Kind == syntax.TkIndent
		hasFlowMapping = hasFlowMapping || token.Kind == syntax.TkOpenBrace
		hasQuoted = hasQuoted || token.Kind == syntax.TkSingleQuotedScalar
	}
	if reconstructed != source {
		t.Fatalf("lexer is not lossless: got %q", reconstructed)
	}
	if !hasIndent || !hasFlowMapping || !hasQuoted {
		t.Fatalf("missing structural tokens: %#v", tokens)
	}
}

func TestLexRoutePathKeepsBracesInPlainScalar(t *testing.T) {
	tokens := Lex("path: /product/{id}\n")
	if len(tokens) < 3 {
		t.Fatalf("unexpected tokens: %#v", tokens)
	}
	var scalar string
	for _, token := range tokens {
		if token.Kind == syntax.TkPlainScalar && token.Text() != "path" {
			scalar = token.Text()
		}
	}
	if scalar != "/product/{id}" {
		t.Fatalf("route path scalar = %q", scalar)
	}
}

func TestLexBlockScalarAsSingleToken(t *testing.T) {
	source := "description: |\n  first\n  second\nnext: value\n"
	tokens := Lex(source)
	var block string
	for _, token := range tokens {
		if token.Kind == syntax.TkBlockScalar {
			block = token.Text()
		}
	}
	if block != "|\n  first\n  second\n" {
		t.Fatalf("block scalar = %q", block)
	}
}
