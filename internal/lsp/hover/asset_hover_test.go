package hover

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetHoverDescribesResolvedPublicAsset(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "public", "build")
	require.NoError(t, os.MkdirAll(build, 0o755))
	assetPath := filepath.Join(build, "app.css")
	require.NoError(t, os.WriteFile(assetPath, []byte("body{}"), 0o644))
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		assetPath,
		[]byte("body{}"),
	)))
	source := `{{ asset('build/app.css') }}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "build/app.css") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewAssetHoverProvider(
		root,
		index,
		nil,
	).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
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
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony asset")
	assert.Contains(t, result.Contents.Value, "public/build/app.css")
}

func TestAssetHoverDescribesNamedBundlePackage(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(
		root,
		"src",
		"Administration",
		"Resources",
		"config",
		"routes.xml",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(config), 0o755))
	require.NoError(t, os.WriteFile(config, []byte("<routes/>"), 0o644))
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		config,
		[]byte("<routes/>"),
	)))
	source := `{{ asset('administration/app.js', '@Administration') }}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "@Administration") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewAssetHoverProvider(root, index, nil).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
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
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony asset package")
	assert.Contains(t, result.Contents.Value, "bundle package")
	assert.Contains(t, result.Contents.Value, "bundles/administration")
}

func TestAssetHoverDescribesViteEntrypoint(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "vite.config.js")
	config := `export default defineConfig({
  build: {rollupOptions: {input: {frontend: './assets/frontend.js'}}}
});`
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		configPath,
		[]byte(config),
	)))
	source := `{{ vite_entry_script_tags('frontend') }}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "frontend") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, hoverErr := NewAssetHoverProvider(
		root,
		index,
		nil,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	})
	require.NoError(t, hoverErr)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony Vite entry")
	assert.Contains(t, result.Contents.Value, "assets/frontend.js")
}

func TestAssetHoverDescribesImportmapEntrypoint(t *testing.T) {
	root := t.TempDir()
	importmapPath := filepath.Join(root, "importmap.php")
	importmap := `<?php
return [
    'app' => [
        'url' => 'https://cdn.example.test/app.js',
        'version' => '1.2.3',
        'entrypoint' => true,
    ],
];`
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		importmapPath,
		[]byte(importmap),
	)))
	source := `{{ importmap('app') }}`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "app") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewAssetHoverProvider(root, index, nil).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
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
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "AssetMapper entrypoint")
	assert.Contains(t, result.Contents.Value, "version `1.2.3`")
	assert.Contains(t, result.Contents.Value, "cdn.example.test/app.js")
}

func TestAssetHoverDescribesTwigHTMLAsset(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "public", "images", "logo.svg")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("<svg/>"), 0o644))
	index, err := asset.NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		target,
		[]byte("<svg/>"),
	)))
	source := `<img src="/images/logo.svg">`
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "templates", "page.html.twig"),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "images/logo.svg") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewAssetHoverProvider(root, index, nil).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
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
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Twig HTML asset")
	assert.Contains(t, result.Contents.Value, "public/images/logo.svg")
}
