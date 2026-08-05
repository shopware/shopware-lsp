package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestSecurityReferencesConnectDeclarationsAndUses(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "security.yaml")
	phpPath := filepath.Join(root, "src", "Controller.php")
	twigPath := filepath.Join(root, "templates", "article.html.twig")
	for _, path := range []string{configPath, phpPath, twigPath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	configSource := "security:\n  role_hierarchy:\n    ROLE_EDITOR: ROLE_USER\n"
	phpSource := "<?php\n$authorization->isGranted('ROLE_EDITOR');\n"
	twigSource := "{{ is_granted('ROLE_EDITOR') }}\n"
	for path, source := range map[string]string{
		configPath: configSource,
		phpPath:    phpSource,
		twigPath:   twigSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		configPath: configSource,
		phpPath:    phpSource,
		twigPath:   twigSource,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	document := lsp.NewTextDocument(
		uriutil.FileURI(twigPath),
		twigSource,
		1,
	)
	offset := uint32(strings.Index(twigSource, "ROLE_EDITOR") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewSecurityReferenceProvider(index).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	uris := make([]string, 0, len(locations))
	for _, location := range locations {
		uris = append(uris, location.URI)
	}
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(configPath),
		uriutil.FileURI(phpPath),
		uriutil.FileURI(twigPath),
	}, uris)
}

func TestSecurityReferencesConnectProviderConfiguration(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "security.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	source := `security:
  providers:
    app_users:
      memory: null
    fallback:
      chain:
        providers: [app_users]
  firewalls:
    main:
      provider: app_users
`
	require.NoError(t, os.WriteFile(configPath, []byte(source), 0o644))
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		configPath,
		[]byte(source),
	)))

	document := lsp.NewTextDocument(
		uriutil.FileURI(configPath),
		source,
		1,
	)
	offset := uint32(strings.LastIndex(source, "app_users") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewSecurityReferenceProvider(index).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	for _, location := range locations {
		assert.Equal(t, uriutil.FileURI(configPath), location.URI)
	}
}
