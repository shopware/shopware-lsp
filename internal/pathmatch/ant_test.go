package pathmatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCompileMatchesLSPGlobSyntax(t *testing.T) {
	t.Parallel()
	matcher, err := Compile([]string{
		"**/generated/**",
		"custom/plugins/{Legacy,Archived}/**/*.[pt]hp",
		"assets/file[!0-9].js",
	})
	require.NoError(t, err)
	assert.True(t, matcher.Match("generated/cache.php"))
	assert.True(t, matcher.Match("src/generated/deep/cache.php"))
	assert.True(t, matcher.Match("custom/plugins/Legacy/src/file.php"))
	assert.True(t, matcher.Match("custom/plugins/Archived/src/file.thp"))
	assert.True(t, matcher.Match("assets/filea.js"))
	assert.False(t, matcher.Match("assets/file1.js"))
	assert.False(t, matcher.Match("custom/plugins/Current/src/file.php"))
}

func TestCompileRejectsMalformedPatterns(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{"", "src/[abc", "src/{one}"} {
		_, err := Compile([]string{pattern})
		assert.Error(t, err, pattern)
	}
}
