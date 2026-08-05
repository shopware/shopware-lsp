package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestTwigTemplateReferencesConnectTwigPHPAndDeclaration(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(
		root,
		"templates",
		"layout",
		"base.html.twig",
	)
	pagePath := filepath.Join(root, "templates", "page.html.twig")
	controllerPath := filepath.Join(root, "src", "PageController.php")
	targetSource := `{% block body %}{% endblock %}`
	pageSource := `{% extends 'layout/base.html.twig' %}`
	controllerSource := `<?php
#[Template('layout/base.html.twig')]
final class PageController {}
`
	for path, source := range map[string]string{
		targetPath:     targetSource,
		pagePath:       pageSource,
		controllerPath: controllerSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		targetPath:     targetSource,
		pagePath:       pageSource,
		controllerPath: controllerSource,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	document := lsp.NewTextDocument(
		uriutil.FileURI(pagePath),
		pageSource,
		2,
	)
	offset := uint32(strings.Index(pageSource, "layout/base") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true

	locations, err := NewTwigTemplateReferenceProvider(index).GetReferences(
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
				Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	require.ElementsMatch(t, []string{
		uriutil.FileURI(targetPath),
		uriutil.FileURI(pagePath),
		uriutil.FileURI(controllerPath),
	}, []string{
		locations[0].URI,
		locations[1].URI,
		locations[2].URI,
	})
	for _, location := range locations {
		if location.URI != uriutil.FileURI(pagePath) {
			continue
		}
		require.Equal(
			t,
			strings.Index(pageSource, "layout/base.html.twig"),
			location.Range.Start.Character,
		)
		require.Equal(
			t,
			strings.Index(pageSource, "layout/base.html.twig")+
				len("layout/base.html.twig"),
			location.Range.End.Character,
		)
	}
}

func TestTwigTemplateReferencesUseUnsavedCurrentDocument(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "templates", "new.html.twig")
	pagePath := filepath.Join(root, "templates", "page.html.twig")
	for _, path := range []string{targetPath, pagePath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	require.NoError(t, os.WriteFile(targetPath, []byte("target"), 0o644))
	require.NoError(t, os.WriteFile(
		pagePath,
		[]byte(`{% include 'old.html.twig' %}`),
		0o644,
	))
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		targetPath,
		[]byte("target"),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		pagePath,
		[]byte(`{% include 'old.html.twig' %}`),
	)))

	source := `{% include 'new.html.twig' %}`
	document := lsp.NewTextDocument(uriutil.FileURI(pagePath), source, 2)
	offset := uint32(strings.Index(source, "new.html.twig") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations, err := NewTwigTemplateReferenceProvider(index).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Root:      document.SyntaxTree.Root,
				Node:      document.SyntaxTree.Root.NodeAtOffset(offset),
				LineIndex: document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, locations, 1)
	require.Equal(t, document.URI, locations[0].URI)
}
