package suggestion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSimilarRanksSymfonyNames(t *testing.T) {
	result := Similar("product.sho", []string{
		"admin.dashboard",
		"product.search",
		"product.show",
		"product.list",
	})
	assert.NotEmpty(t, result)
	assert.Equal(t, "product.show", result[0])
	assert.LessOrEqual(t, len(result), 5)
}

func TestSimilarTemplatesIgnoresFormatAndExtension(t *testing.T) {
	result := SimilarTemplates("storefront/page/prodcut.html.twig", []string{
		"storefront/page/account.html.twig",
		"storefront/page/product.xml.twig",
	})
	assert.NotEmpty(t, result)
	assert.Equal(t, "storefront/page/product.xml.twig", result[0])
}
