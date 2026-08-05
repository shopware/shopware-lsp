package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetReferencesConnectDeclarationAndTwigUsages(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "public", "build")
	require.NoError(t, os.MkdirAll(build, 0o755))
	assetPath := filepath.Join(build, "app.css")
	firstPath := filepath.Join(root, "templates", "first.html.twig")
	secondPath := filepath.Join(root, "templates", "second.html.twig")
	require.NoError(t, os.MkdirAll(filepath.Dir(firstPath), 0o755))
	firstSource := `{{ asset('build/app.css') }}`
	secondSource := `<link rel="stylesheet" href="/build/app.css">`
	for path, source := range map[string]string{
		assetPath:  "body{}",
		firstPath:  firstSource,
		secondPath: secondSource,
	} {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		assetPath:  "body{}",
		firstPath:  firstSource,
		secondPath: secondSource,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(firstPath),
		firstSource,
		1,
	)
	offset := uint32(strings.Index(firstSource, "build/app.css") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewAssetReferenceProvider(
		index,
		nil,
	).GetReferences(
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
	assert.Equal(t, uriutil.FileURI(assetPath), locations[0].URI)
	assert.ElementsMatch(
		t,
		[]string{uriutil.FileURI(firstPath), uriutil.FileURI(secondPath)},
		[]string{locations[1].URI, locations[2].URI},
	)
}

func TestViteReferencesConnectTargetConfigAndTwigUsages(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "assets", "app.js")
	configPath := filepath.Join(root, "vite.config.js")
	firstPath := filepath.Join(root, "templates", "first.html.twig")
	secondPath := filepath.Join(root, "templates", "second.html.twig")
	files := map[string]string{
		targetPath: "console.log('app');",
		configPath: `export default defineConfig({
  build: {rollupOptions: {input: {app: './assets/app.js'}}}
});`,
		firstPath:  `{{ vite_entry_script_tags('app') }}`,
		secondPath: `{{ vite_entry_link_tags('app') }}`,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range files {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(firstPath),
		files[firstPath],
		1,
	)
	offset := uint32(strings.Index(files[firstPath], "app") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, referenceErr := NewAssetReferenceProvider(
		index,
		nil,
	).GetReferences(
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
	require.NoError(t, referenceErr)
	require.Len(t, locations, 4)
	actual := make([]string, 0, len(locations))
	for _, location := range locations {
		actual = append(actual, location.URI)
	}
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(targetPath),
		uriutil.FileURI(configPath),
		uriutil.FileURI(firstPath),
		uriutil.FileURI(secondPath),
	}, actual)
}

func TestAssetPackageReferencesConnectConfigAndQualifiedUsages(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(
		root,
		"src",
		"Administration",
		"Resources",
		"config",
		"routes.xml",
	)
	firstPath := filepath.Join(root, "templates", "first.html.twig")
	secondPath := filepath.Join(root, "templates", "second.html.twig")
	firstSource := `{{ asset('administration/app.js', '@Administration') }}`
	secondSource := `{{ asset('administration/other.js', '@Administration') }}`
	files := map[string]string{
		config:     "<routes/>",
		firstPath:  firstSource,
		secondPath: secondSource,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range files {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(firstPath),
		firstSource,
		1,
	)
	offset := uint32(strings.Index(firstSource, "@Administration") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewAssetReferenceProvider(index, nil).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				Root:            document.SyntaxTree.Root,
				Node:            node,
				LineIndex:       document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	assert.Equal(t, uriutil.FileURI(config), locations[0].URI)
	assert.ElementsMatch(
		t,
		[]string{uriutil.FileURI(firstPath), uriutil.FileURI(secondPath)},
		[]string{locations[1].URI, locations[2].URI},
	)
}

func TestImportmapReferencesConnectMappingTargetAndTwigUsages(t *testing.T) {
	root := t.TempDir()
	importmapPath := filepath.Join(root, "importmap.php")
	target := filepath.Join(root, "assets", "app.js")
	firstPath := filepath.Join(root, "templates", "first.html.twig")
	secondPath := filepath.Join(root, "templates", "second.html.twig")
	importmap := `<?php
return [
    'app' => ['path' => './assets/app.js', 'entrypoint' => true],
];`
	firstSource := `{{ importmap('app') }}`
	secondSource := `{% block javascripts %}{{ importmap('app') }}{% endblock %}`
	files := map[string]string{
		importmapPath: importmap,
		target:        "console.log('app')",
		firstPath:     firstSource,
		secondPath:    secondSource,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range files {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(firstPath),
		firstSource,
		1,
	)
	offset := uint32(strings.Index(firstSource, "app") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewAssetReferenceProvider(index, nil).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				Root:            document.SyntaxTree.Root,
				Node:            node,
				LineIndex:       document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	assert.Equal(t, uriutil.FileURI(target), locations[0].URI)
	assert.ElementsMatch(
		t,
		[]string{uriutil.FileURI(firstPath), uriutil.FileURI(secondPath)},
		[]string{locations[1].URI, locations[2].URI},
	)
}
