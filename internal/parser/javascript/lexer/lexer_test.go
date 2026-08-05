package lexer

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
)

func TestLexLosslessJavaScript(t *testing.T) {
	source := "import template from './x.html.twig';\nexport default { props: { title: String }, load: () => import('./x') }; // end"
	tokens := Lex(source)
	var rebuilt string
	for _, token := range tokens {
		rebuilt += token.Text()
	}
	assert.Equal(t, source, rebuilt)
	assert.Contains(t, kinds(tokens), syntax.TkString)
	assert.Contains(t, kinds(tokens), syntax.TkArrow)
	assert.Contains(t, kinds(tokens), syntax.TkLineComment)
}

func TestLexIncompleteString(t *testing.T) {
	tokens := Lex(`Component.extend('child', 'sw-parent`)
	assert.Equal(t, syntax.TkString, tokens[len(tokens)-1].Kind)
	assert.Equal(t, `'sw-parent`, tokens[len(tokens)-1].Text())
}

func kinds(tokens []Token) []syntax.Kind {
	result := make([]syntax.Kind, len(tokens))
	for index, token := range tokens {
		result[index] = token.Kind
	}
	return result
}
