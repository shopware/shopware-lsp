package serializer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

func TestIndexCollectsSerializerDeserializeTargets(t *testing.T) {
	cacheDir := t.TempDir()
	index, err := NewIndex(cacheDir)
	require.NoError(t, err)

	source := `<?php
namespace App;
final class Handler {
    public function run($serializer): void {
        $serializer->deserialize('one', Foobar::class, 'json');
        $serializer->deserialize('two', 'App\Foobar2', 'json');
        $serializer->deserialize('three', '\App\Foobar3[]', 'json');
        $serializer->deserialize('four', Foobar4::class . '[]', 'json');
        $serializer->deserialize('five', Foobar5::class . 'Suffix', 'json');
    }
}
`
	path := "/project/src/Handler.php"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	classes, err := index.Classes()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"App\\Foobar",
		"App\\Foobar2",
		"App\\Foobar3",
		"App\\Foobar4",
	}, classes)
	assert.NotContains(t, classes, "App\\Foobar5")
	for _, className := range classes {
		usages, usageErr := index.Usages(className)
		require.NoError(t, usageErr)
		require.Len(t, usages, 1)
		assert.Equal(t, path, usages[0].File)
	}
	require.NoError(t, index.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restoredClasses, err := restored.Classes()
	require.NoError(t, err)
	assert.Equal(t, classes, restoredClasses)
}

func TestIndexRemovesStaleSerializerTargets(t *testing.T) {
	index, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	path := "/project/src/Handler.php"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php $serializer->deserialize($data, 'App\Model', 'json');`),
	)))
	usages, err := index.Usages("App\\Model")
	require.NoError(t, err)
	require.Len(t, usages, 1)

	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php echo 'unchanged';`),
	)))
	usages, err = index.Usages("App\\Model")
	require.NoError(t, err)
	assert.Empty(t, usages)
}
