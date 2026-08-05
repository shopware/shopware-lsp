package twig

import (
	"strings"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeeReferencesSupportExplicitAndLegacyTargets(t *testing.T) {
	source := `{# @see App\Controller\ProductController::show #}
{# @see @Storefront/storefront/page/product.html.twig #}
{# \DateTime:format #}
{# ordinary prose #}
{% raw %}{# @see App\Ignored #}{% endraw %}`
	root := twigparser.Parse(source).Tree.Root
	references := SeeReferences(root)
	require.Len(t, references, 3)
	assert.Equal(
		t,
		"App\\Controller\\ProductController::show",
		references[0].Target,
	)
	assert.Equal(
		t,
		"@Storefront/storefront/page/product.html.twig",
		references[1].Target,
	)
	assert.Equal(t, `\DateTime:format`, references[2].Target)
	for _, reference := range references {
		assert.Equal(
			t,
			reference.Target,
			source[reference.Range.Start:reference.Range.End],
		)
	}
}

func TestSeeCompletionAtReturnsExactTargetRange(t *testing.T) {
	sourceWithCaret := `{# @see App\Controller\Prod<caret>uctController #}`
	offset := strings.Index(sourceWithCaret, "<caret>")
	require.NotEqual(t, -1, offset)
	source := strings.Replace(sourceWithCaret, "<caret>", "", 1)
	root := twigparser.Parse(source).Tree.Root
	context, found := SeeCompletionAt(root, uint32(offset))
	require.True(t, found)
	assert.Equal(t, `App\Controller\Prod`, context.Prefix)
	assert.Equal(
		t,
		`App\Controller\ProductController`,
		source[context.Range.Start:context.Range.End],
	)
}
