package twig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

func TestDocumentationCommentTextSupportsTwig329Delimiters(t *testing.T) {
	tests := map[string]string{
		"{## Description #}":    "Description",
		"{## Description ##}":   "Description",
		"{##- Description #}":   "Description",
		"{## Description -##}":  "Description",
		"{##~ Description ~##}": "Description",
		" ## Binding docs ":     "Binding docs",
	}
	for source, expected := range tests {
		actual, found := DocumentationCommentText(source)
		require.True(t, found, source)
		assert.Equal(t, expected, actual, source)
	}
	_, found := DocumentationCommentText("{##}")
	assert.False(t, found)
	_, found = DocumentationCommentText("{# regular #}")
	assert.False(t, found)
}

func TestDocumentationBeforeCombinesCommentsAndHonorsBoundaries(t *testing.T) {
	source := `{## First line. ##}
{# regular comment #}
{## Second line. #}
{% block documented %}{% endblock %}
{## Not attached. #}
Visible text
{% block undocumented %}{% endblock %}`
	result := twigparser.Parse(source)
	require.Empty(t, result.Errors)
	blocks := twigquery.Nodes(result.Tree.Root, twigsyntax.TwigBlock)
	require.Len(t, blocks, 2)
	assert.Equal(
		t,
		"First line.\nSecond line.",
		DocumentationBefore(blocks[0]),
	)
	assert.Empty(t, DocumentationBefore(blocks[1]))
}

func TestDocumentationForNodeFindsBindingComments(t *testing.T) {
	source := `{% set
    ## Number of unread messages.
    unread_count = messages|length
%}
{% for
    ## Product identifier.
    product_id,
    ## Product in the current iteration.
    product
    in products
%}{% endfor %}`
	result := twigparser.Parse(source)
	require.Empty(t, result.Errors)
	for name, expected := range map[string]string{
		"unread_count": "Number of unread messages.",
		"product_id":   "Product identifier.",
		"product\n":    "Product in the current iteration.",
	} {
		offset := strings.Index(source, name)
		require.NotEqual(t, -1, offset, name)
		node := result.Tree.Root.NodeAtOffset(uint32(offset))
		require.NotNil(t, node, name)
		assert.Equal(t, expected, DocumentationForNode(node), name)
	}
}
