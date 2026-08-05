package lexer

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
)

func TestLexIsLosslessAndRecognizesSCSS(t *testing.T) {
	source := `$color: #fff;
.button-#{$variant} {
  color: feature("ACCESSIBILITY_TWEAKS");
  // comment
}`
	tokens := Lex(source)

	var reconstructed string
	var variable, interpolation, stringToken bool
	for _, token := range tokens {
		reconstructed += token.Text()
		variable = variable || token.Kind == syntax.TkVariable
		interpolation = interpolation || token.Kind == syntax.TkInterpolationOpen
		stringToken = stringToken || token.Kind == syntax.TkDoubleQuotedString
	}
	if reconstructed != source {
		t.Fatalf("lexer is not lossless: got %q", reconstructed)
	}
	if !variable || !interpolation || !stringToken {
		t.Fatalf("missing SCSS tokens: %#v", tokens)
	}
}

func TestLexDoesNotTreatURLAsLineComment(t *testing.T) {
	tokens := Lex("background: url(data:image/png;base64,abc//def==);")
	for _, token := range tokens {
		if token.Kind == syntax.TkLineComment {
			t.Fatalf("URL became a line comment: %#v", tokens)
		}
	}
}
