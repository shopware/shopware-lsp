package translation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslationYAMLInsertion(t *testing.T) {
	idx := newTestIndex(t)
	path := filepath.Join(t.TempDir(), "translations", "messages.en.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	content := []byte("existing: Existing\n")
	require.NoError(t, os.WriteFile(path, content, 0o644))
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, content)))

	insertions, err := idx.Insertions("messages", "new.key")
	require.NoError(t, err)
	require.Len(t, insertions, 1)
	assert.Equal(t, path, insertions[0].File)
	assert.Equal(t, 1, insertions[0].Line)
	assert.Equal(t, 0, insertions[0].Character)
	assert.Equal(t, "'new.key': 'new.key'\n", insertions[0].NewText)

	extracted, err := idx.InsertionsWithValue(
		"messages",
		"welcome.title",
		"Welcome to Bob's shop",
	)
	require.NoError(t, err)
	require.Len(t, extracted, 1)
	assert.Equal(t, "en", extracted[0].Locale)
	assert.Equal(
		t,
		"'welcome.title': 'Welcome to Bob''s shop'\n",
		extracted[0].NewText,
	)
}

func TestTranslationXLIFFInsertions(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
		contains string
	}{
		{
			name:     "XLIFF 1.2",
			filename: "messages.en.xlf",
			content: `<xliff version="1.2"><file><body>
  <trans-unit id="4"><source>existing</source><target>Existing</target></trans-unit>
</body></file></xliff>`,
			contains: `<trans-unit id="5" resname="new.key"><source>new.key</source><target>new.key</target></trans-unit>`,
		},
		{
			name:     "XLIFF 2.0",
			filename: "messages.en.xliff",
			content: `<xliff version="2.0"><file><group>
  <unit id="2"><segment><source>existing</source><target>Existing</target></segment></unit>
</group></file></xliff>`,
			contains: `<unit id="3"><segment><source>new.key</source><target>new.key</target></segment></unit>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			idx := newTestIndex(t)
			path := filepath.Join(t.TempDir(), "translations", test.filename)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			content := []byte(test.content)
			require.NoError(t, os.WriteFile(path, content, 0o644))
			require.NoError(t, idx.Index(indexer.NewParsedFile(path, content)))

			insertions, err := idx.Insertions("messages", "new.key")
			require.NoError(t, err)
			require.Len(t, insertions, 1)
			assert.Contains(t, insertions[0].NewText, test.contains)
			assert.Greater(t, insertions[0].Line, 0)

			extracted, extractErr := idx.InsertionsWithValue(
				"messages",
				"welcome.title",
				"Welcome <friend>",
			)
			require.NoError(t, extractErr)
			require.Len(t, extracted, 1)
			assert.Contains(t, extracted[0].NewText, "Welcome &lt;friend&gt;")
		})
	}
}
