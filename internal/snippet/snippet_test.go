package snippet

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSnippetFile(t *testing.T) {
	bytes, err := os.ReadFile("testdata/nested.json")

	assert.NoError(t, err)

	result, err := parseSnippetFile(bytes, "testdata/nested.json")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := map[string]Snippet{
		"foo.foo.name": {
			Key:  "foo.foo.name",
			Text: "title",
			File: "testdata/nested.json",
			Line: 5,
		},
		"foo.name": {
			Key:  "foo.name",
			Text: "title",
			File: "testdata/nested.json",
			Line: 3,
		},
	}

	assert.Equal(t, expected, result)
}

func TestIsAdminSnippetFile(t *testing.T) {
	tempDir := t.TempDir()
	indexer, err := NewSnippetIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = indexer.Close() }()

	tests := []struct {
		path     string
		expected bool
	}{
		// Valid admin snippet paths - all languages
		{"/project/src/Resources/app/administration/src/module/test/snippet/en-GB.json", true},
		{"/project/src/Resources/app/administration/src/module/test/snippet/en.json", true},
		{"/project/src/Resources/app/administration/src/module/test/snippet/de-DE.json", true},
		{"/project/src/Resources/app/administration/src/module/test/snippet/fr-FR.json", true},
		{"/project/custom/plugins/MyPlugin/src/Resources/app/administration/src/snippet/en-GB.json", true},

		// Invalid paths - not a JSON file
		{"/project/src/Resources/app/administration/src/module/test/snippet/README.md", false},

		// Invalid paths - not in snippet folder
		{"/project/src/Resources/app/administration/src/module/test/en-GB.json", false},

		// Invalid paths - not in administration folder
		{"/project/src/Resources/snippet/en-GB.json", false},
		{"/project/src/Resources/app/storefront/src/snippet/en-GB.json", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := indexer.isAdminSnippetFile(tt.path)
			assert.Equal(t, tt.expected, result, "path: %s", tt.path)
		})
	}
}
