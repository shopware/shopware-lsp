package pathmatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAntMatchesProjectRelativePaths(t *testing.T) {
	assert.True(t, Ant(
		"src/**/ProductController.php",
		"src/ProductController.php",
	))
	assert.True(t, Ant(
		"src/**/ProductController.php",
		"src/Admin/Catalog/ProductController.php",
	))
	assert.True(t, Ant(
		`src\*\ProductController.php`,
		"src/Admin/ProductController.php",
	))
	assert.False(t, Ant(
		"src/**/ProductController.php",
		"tests/ProductController.php",
	))
	assert.False(t, Ant("", "src/ProductController.php"))
}
