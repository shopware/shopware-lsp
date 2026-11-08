package theme

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtensionResourceRootUsesBundleDirectory(t *testing.T) {
	bundle := filepath.Join("custom", "plugins", "Demo", "src", "Demo.php")
	assert.Equal(t, filepath.Join("custom", "plugins", "Demo", "src"), extensionResourceRoot(bundle))
	assert.Equal(t, filepath.Join("custom", "apps", "Demo"), extensionResourceRoot(filepath.Join("custom", "apps", "Demo")))
	assert.Equal(t, filepath.Join("src", "Storefront"), extensionResourceRoot(filepath.Join("src", "Storefront")))
}
