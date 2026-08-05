package lexer

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	"github.com/stretchr/testify/assert"
)

func TestLexLosslessXML(t *testing.T) {
	source := "<?xml version=\"1.0\"?>\n<root xmlns:x=\"urn:test\" disabled='yes'><x:item id=\"a&amp;b\">text &lt; <![CDATA[<raw>]]></x:item><!-- ok --></root>"
	tokens := Lex(source)

	var rebuilt string
	for _, token := range tokens {
		rebuilt += token.Text()
	}

	assert.Equal(t, source, rebuilt)
	assert.Contains(t, tokenKinds(tokens), syntax.TkProcessingInstruction)
	assert.Contains(t, tokenKinds(tokens), syntax.TkCdata)
	assert.Contains(t, tokenKinds(tokens), syntax.TkComment)
	assert.Contains(t, tokenKinds(tokens), syntax.TkEntityReference)
}

func TestLexIncompleteAttributeValue(t *testing.T) {
	tokens := Lex(`<argument type="service" id="App\Service`)
	assert.Equal(t, syntax.TkAttributeValue, tokens[len(tokens)-1].Kind)
	assert.Equal(t, `"App\Service`, tokens[len(tokens)-1].Text())
}

func TestLexDoctypeInternalSubset(t *testing.T) {
	source := `<!DOCTYPE root [<!ELEMENT root (#PCDATA)>]><root>ok</root>`
	tokens := Lex(source)
	assert.Equal(t, syntax.TkDoctype, tokens[0].Kind)
	assert.Equal(t, `<!DOCTYPE root [<!ELEMENT root (#PCDATA)>]>`, tokens[0].Text())
}

func tokenKinds(tokens []Token) []syntax.Kind {
	kinds := make([]syntax.Kind, len(tokens))
	for index, token := range tokens {
		kinds[index] = token.Kind
	}
	return kinds
}
