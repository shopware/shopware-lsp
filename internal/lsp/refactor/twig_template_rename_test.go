package refactor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestTwigTemplateRenameUpdatesNamespacedTwigAndPHPReferences(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(
		root,
		"MyBundle",
		"src",
		"Resources",
		"views",
		"foo",
		"old.html.twig",
	)
	newPath := filepath.Join(
		root,
		"MyBundle",
		"src",
		"Resources",
		"views",
		"bar",
		"new.html.twig",
	)
	twigUsagePath := filepath.Join(root, "templates", "usage.html.twig")
	phpUsagePath := filepath.Join(root, "src", "Controller.php")
	diskTwigSource := `{% include 'foo/old.html.twig' %}
{% include '@Storefront/foo/old.html.twig' %}
{% include '@MyBundle/foo/old.html.twig' %}
{% include 'MyBundle:foo:old.html.twig' %}`
	overlayTwigSource := "😀 " + diskTwigSource
	phpSource := `<?php $this->render('foo/old.html.twig');`
	for path, source := range map[string]string{
		oldPath:       "target",
		twigUsagePath: diskTwigSource,
		phpUsagePath:  phpSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		oldPath:       "target",
		twigUsagePath: diskTwigSource,
		phpUsagePath:  phpSource,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	params := &protocol.RenameFilesParams{
		Files: []protocol.FileRename{{
			OldURI: uriutil.FileURI(oldPath),
			NewURI: uriutil.FileURI(newPath),
		}},
	}
	document := lsp.NewTextDocument(
		uriutil.FileURI(twigUsagePath),
		overlayTwigSource,
		2,
	)
	edit, err := NewTwigTemplateRenameProvider(index).WillRenameFiles(
		context.Background(),
		&lsp.FileRenameRequest{
			RenameFilesParams: params,
			Documents:         []*lsp.TextDocument{document},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	require.Len(t, edit.Changes, 2)
	require.Len(t, edit.Changes[document.URI], 4)
	require.Len(t, edit.Changes[uriutil.FileURI(phpUsagePath)], 1)

	assertTemplateEditRanges(
		t,
		overlayTwigSource,
		document.LineIndex,
		edit.Changes[document.URI],
	)
	assertTemplateEditRanges(
		t,
		phpSource,
		cst.NewLineIndex(phpSource),
		edit.Changes[uriutil.FileURI(phpUsagePath)],
	)
	var replacements []string
	for _, item := range edit.Changes[document.URI] {
		replacements = append(replacements, item.NewText)
	}
	replacements = append(
		replacements,
		edit.Changes[uriutil.FileURI(phpUsagePath)][0].NewText,
	)
	require.ElementsMatch(t, []string{
		"bar/new.html.twig",
		"@Storefront/bar/new.html.twig",
		"@MyBundle/bar/new.html.twig",
		"MyBundle:bar:new.html.twig",
		"bar/new.html.twig",
	}, replacements)
}

func TestTwigTemplateRenameSkipsAmbiguousOverrideNames(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(
		root,
		"MyBundle",
		"src",
		"Resources",
		"views",
		"foo",
		"old.html.twig",
	)
	overridePath := filepath.Join(
		root,
		"OtherBundle",
		"src",
		"Resources",
		"views",
		"foo",
		"old.html.twig",
	)
	newPath := filepath.Join(filepath.Dir(targetPath), "new.html.twig")
	usagePath := filepath.Join(root, "templates", "usage.html.twig")
	usageSource := `{% include 'foo/old.html.twig' %}
{% include '@MyBundle/foo/old.html.twig' %}`
	for path, source := range map[string]string{
		targetPath:   "target",
		overridePath: "override",
		usagePath:    usageSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		targetPath:   "target",
		overridePath: "override",
		usagePath:    usageSource,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	edit, err := NewTwigTemplateRenameProvider(index).WillRenameFiles(
		context.Background(),
		&lsp.FileRenameRequest{
			RenameFilesParams: &protocol.RenameFilesParams{
				Files: []protocol.FileRename{{
					OldURI: uriutil.FileURI(targetPath),
					NewURI: uriutil.FileURI(newPath),
				}},
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	changes := edit.Changes[uriutil.FileURI(usagePath)]
	require.Len(t, changes, 1)
	require.Equal(t, "@MyBundle/foo/new.html.twig", changes[0].NewText)
}

func TestTemplateRenameNameSelection(t *testing.T) {
	require.Equal(
		t,
		"@App/new.html.twig",
		pickBestTemplateName(
			[]string{"new.html.twig", "@App/new.html.twig"},
			"@App/old.html.twig",
		),
	)
	require.Equal(
		t,
		"Bundle:dir:new.html.twig",
		pickBestTemplateName(
			[]string{"new.html.twig", "Bundle:dir:new.html.twig"},
			"Bundle:dir:old.html.twig",
		),
	)
	require.Equal(
		t,
		"foo/new.html.twig",
		renameTemplateBasename("foo/old.html.twig", "new.html.twig"),
	)
}

func assertTemplateEditRanges(
	t *testing.T,
	source string,
	lineIndex *cst.LineIndex,
	edits []protocol.TextEdit,
) {
	t.Helper()
	for _, edit := range edits {
		start := lineIndex.OffsetUTF16(
			uint32(edit.Range.Start.Line),
			uint32(edit.Range.Start.Character),
		)
		end := lineIndex.OffsetUTF16(
			uint32(edit.Range.End.Line),
			uint32(edit.Range.End.Character),
		)
		require.LessOrEqual(t, end, uint32(len(source)))
		require.Contains(t, source[start:end], "old.html.twig")
	}
}
