package definition

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslationDefinitionResolvesTwigKeyAndDomain(t *testing.T) {
	idx, err := translation.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	path := "/project/translations/admin.en.yaml"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte("admin.dashboard: Dashboard\n"),
	)))
	provider := NewTranslationDefinitionProvider(idx, nil)

	keySource := `{{ 'admin.dashboard'|trans({}, 'admin') }}`
	keyDocument := lsp.NewTextDocument(
		"file:///project/template.twig",
		keySource,
		1,
	)
	keyNode := keyDocument.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.Index(keySource, "admin.dashboard") + 2),
	)
	keyLocations := provider.GetDefinition(
		context.Background(),
		translationDefinitionRequest(keyDocument, keyNode),
	)
	require.Len(t, keyLocations, 1)
	assert.Equal(t, uriutil.FileURI(path), keyLocations[0].URI)
	assert.Equal(t, 0, keyLocations[0].Range.Start.Line)

	domainOffset := strings.LastIndex(keySource, "admin")
	domainNode := keyDocument.SyntaxTree.Root.NodeAtOffset(
		uint32(domainOffset + 2),
	)
	domainLocations := provider.GetDefinition(
		context.Background(),
		translationDefinitionRequest(keyDocument, domainNode),
	)
	require.Len(t, domainLocations, 1)
	assert.Equal(t, uriutil.FileURI(path), domainLocations[0].URI)
}

func TestPHPDocTranslationAssistantTagDefinition(t *testing.T) {
	idx, err := translation.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	path := "/project/translations/admin.en.yaml"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte("admin.dashboard: Dashboard\n"),
	)))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/TranslationAssistant.php",
		[]byte(`<?php
/**
 * @param string $key #TranslationKey
 * @param string $domain #TranslationDomain
 */
function resolve_translation(string $key, string $domain): void {}

/** @param string $domain #TranslationDomain */
function resolve_domain(string $domain): void {}
`),
	)))
	provider := NewTranslationDefinitionProvider(idx, phpIndex)
	for _, fixture := range []struct {
		name   string
		source string
		needle string
	}{
		{
			name: "key with named sibling domain",
			source: `<?php resolve_translation(
    domain: 'admin',
    key: 'admin.dashboard',
);`,
			needle: "admin.dashboard",
		},
		{
			name:   "domain",
			source: "<?php resolve_domain('admin');",
			needle: "admin",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/Usage.php",
				fixture.source,
				1,
			)
			offset := uint32(
				strings.LastIndex(fixture.source, fixture.needle) + 2,
			)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/Usage.php",
				document.Version,
				node,
				document.SyntaxTree.Root,
			)
			locations := provider.GetDefinition(
				ctx,
				translationDefinitionRequest(document, node),
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(path), locations[0].URI)
		})
	}
}

func translationDefinitionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
) *lsp.DefinitionRequest {
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}
