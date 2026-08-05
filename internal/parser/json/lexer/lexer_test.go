package lexer

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/json/syntax"
)

func TestLex(t *testing.T) {
	source := "{\n  \"value\": -12.5e+2,\n  \"enabled\": true,\n  \"nothing\": null\n}"
	tokens := Lex(source)

	var kinds []syntax.Kind
	var reconstructed string
	for _, token := range tokens {
		kinds = append(kinds, token.Kind)
		reconstructed += token.Text()
	}
	if reconstructed != source {
		t.Fatalf("lexer is not lossless: got %q", reconstructed)
	}

	expected := []syntax.Kind{
		syntax.TkOpenBrace,
		syntax.TkLineBreak,
		syntax.TkWhitespace,
		syntax.TkString,
		syntax.TkColon,
		syntax.TkWhitespace,
		syntax.TkNumber,
		syntax.TkComma,
		syntax.TkLineBreak,
		syntax.TkWhitespace,
		syntax.TkString,
		syntax.TkColon,
		syntax.TkWhitespace,
		syntax.TkTrue,
		syntax.TkComma,
		syntax.TkLineBreak,
		syntax.TkWhitespace,
		syntax.TkString,
		syntax.TkColon,
		syntax.TkWhitespace,
		syntax.TkNull,
		syntax.TkLineBreak,
		syntax.TkCloseBrace,
	}
	if len(kinds) != len(expected) {
		t.Fatalf("got %d tokens, want %d: %#v", len(kinds), len(expected), kinds)
	}
	for index := range expected {
		if kinds[index] != expected[index] {
			t.Fatalf("token %d kind = %s, want %s", index, kinds[index], expected[index])
		}
	}
}

func TestLexMalformedStringRemainsLossless(t *testing.T) {
	source := "\"unterminated\nnext"
	tokens := Lex(source)
	var reconstructed string
	for _, token := range tokens {
		reconstructed += token.Text()
	}
	if reconstructed != source {
		t.Fatalf("lexer is not lossless: got %q", reconstructed)
	}
	if tokens[0].Kind != syntax.TkString || tokens[1].Kind != syntax.TkLineBreak {
		t.Fatalf("unexpected malformed-string tokens: %#v", tokens)
	}
}
