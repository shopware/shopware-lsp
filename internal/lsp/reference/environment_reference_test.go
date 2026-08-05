package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentReferencesIncludeDeclarationOnRequest(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, ".env")
	yamlPath := filepath.Join(root, "config", "services.yaml")
	phpPath := filepath.Join(root, "src", "Config.php")
	files := map[string]string{
		envPath:  "DATABASE_URL=mysql://localhost\n",
		yamlPath: "database: '%env(resolve:DATABASE_URL)%'\n",
		phpPath: "<?php\n#[Autowire('%env(DATABASE_URL)%')]\n" +
			"#[Autowire(env: 'resolve:DATABASE_URL')]\n" +
			"class Config {}\nenv('string:DATABASE_URL');\n",
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	idx, err := environment.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	for path, source := range files {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	document := lsp.NewTextDocument(
		uriutil.FileURI(yamlPath),
		files[yamlPath],
		1,
	)
	offset := uint32(strings.Index(document.Source, "DATABASE_URL") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewEnvironmentReferenceProvider(idx).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 5)
	uris := []string{
		locations[0].URI,
		locations[1].URI,
		locations[2].URI,
		locations[3].URI,
		locations[4].URI,
	}
	assert.Contains(t, uris, uriutil.FileURI(envPath))
	assert.Contains(t, uris, uriutil.FileURI(yamlPath))
	assert.Contains(t, uris, uriutil.FileURI(phpPath))
}
