package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlainHTMLIsParsedOnDemandButNotBackgroundScanned(t *testing.T) {
	assert.False(t, isScannedPath("/project/build/generated.html"))
	assert.True(t, isScannedPath("/project/templates/page.html.twig"))
	assert.True(t, isScannedPath("/project/Resources/app/administration/src/card.vue"))
	assert.False(t, isScannedPath("/project/vendor/ARCHIVE.PHAR.PHP"))
}
