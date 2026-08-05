package definition

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

func TestEnvironmentDefinitionNavigatesEveryDeclaration(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, ".env")
	dockerPath := filepath.Join(root, "Dockerfile")
	require.NoError(t, os.WriteFile(
		envPath,
		[]byte("APP_ENV=dev\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		dockerPath,
		[]byte("ENV APP_ENV prod\n"),
		0o644,
	))
	idx, err := environment.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	for path, source := range map[string]string{
		envPath:    "APP_ENV=dev\n",
		dockerPath: "ENV APP_ENV prod\n",
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	source := "parameters:\n  app_env: '%env(APP_ENV)%'\n"
	path := filepath.Join(root, "config", "services.yaml")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "APP_ENV") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewEnvironmentDefinitionProvider(idx).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
			},
		},
	)
	require.Len(t, locations, 2)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(envPath),
		uriutil.FileURI(dockerPath),
	}, []string{locations[0].URI, locations[1].URI})
}

func TestEnvironmentDefinitionFromDirectPHPReferences(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte("APP_ENV=dev\n"), 0o644))
	idx, err := environment.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		envPath,
		[]byte("APP_ENV=dev\n"),
	)))

	for name, source := range map[string]string{
		"Autowire attribute": "<?php #[Autowire(env: 'bool:APP_ENV')] class Config {}",
		"env function":       "<?php env('resolve:APP_ENV');",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, "src", name+".php")
			document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
			offset := uint32(strings.Index(source, "APP_ENV") + 2)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			locations := NewEnvironmentDefinitionProvider(idx).GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						DocumentContent: document.Text,
						LineIndex:       document.LineIndex,
						Root:            document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							offset,
						),
					},
				},
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(envPath), locations[0].URI)
		})
	}
}
