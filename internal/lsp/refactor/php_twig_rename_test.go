package refactor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/phpsemantic"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPTwigRenameProviderUpdatesTypedTwigUsages(t *testing.T) {
	projectRoot := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpPath := filepath.Join(projectRoot, "src", "Symbols.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o700))
	phpSource := `<?php
namespace MyNamespace;
class TestBrokenRefactor {
    public string $RestVar;
    public function getFoo(): string { return ''; }
}

namespace Foo;
class CardSuite { public const CLUBS = 'clubs'; }
enum Suit { case CLUBS; }

namespace BugDemo;
const NAMESPACED_CONST = 'value';
`
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o600))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))

	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	templates := map[string]string{
		"field.html.twig":             `{# @var test \MyNamespace\TestBrokenRefactor #} {{ test.RestVar }}`,
		"shortcut.html.twig":          `{# @var test \MyNamespace\TestBrokenRefactor #} {{ test.foo }}`,
		"method.html.twig":            `{# @var test \MyNamespace\TestBrokenRefactor #} {{ test.getFoo() }}`,
		"constant.html.twig":          `{{ constant('Foo\\CardSuite::CLUBS') }}`,
		"constant_accessor.html.twig": `{# @var card \Foo\CardSuite #} {{ card.CLUBS }}`,
		"global.html.twig":            `{{ constant('BugDemo\\NAMESPACED_CONST') }}`,
		"case.html.twig":              `{{ constant('Foo\\Suit::CLUBS') }}`,
		"class.html.twig":             `{# @var test \MyNamespace\TestBrokenRefactor #}`,
	}
	templatePaths := make(map[string]string, len(templates))
	for name, source := range templates {
		path := filepath.Join(projectRoot, "templates", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
		require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
		templatePaths[name] = path
	}
	provider := NewPHPTwigRenameProvider(
		phpsemantic.New(phpIndex),
		twigIndex,
		phpIndex,
	)

	rename := func(t *testing.T, needle, newName string) *protocol.WorkspaceEdit {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(phpPath),
			phpSource,
			1,
		)
		offset := uint32(strings.Index(phpSource, needle) + 2)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.RenameParams{NewName: newName}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		request := &lsp.RenameRequest{
			RenameParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		}
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			phpPath,
			document.Version,
			request.Node,
			request.Root,
		)
		edit, renameErr := provider.Rename(ctx, request)
		require.NoError(t, renameErr)
		require.NotNil(t, edit)
		return edit
	}
	newTexts := func(
		edit *protocol.WorkspaceEdit,
		template string,
	) []string {
		var result []string
		uri := uriutil.FileURI(templatePaths[template])
		for _, textEdit := range edit.Changes[uri] {
			result = append(result, textEdit.NewText)
		}
		return result
	}

	t.Run("public field", func(t *testing.T) {
		edit := rename(t, "RestVar", "RenamedVar")
		assert.Equal(
			t,
			[]string{"RenamedVar"},
			newTexts(edit, "field.html.twig"),
		)
	})
	t.Run("getter shortcut and direct call", func(t *testing.T) {
		edit := rename(t, "getFoo", "getBar")
		assert.Equal(
			t,
			[]string{"bar"},
			newTexts(edit, "shortcut.html.twig"),
		)
		assert.Equal(
			t,
			[]string{"getBar"},
			newTexts(edit, "method.html.twig"),
		)
	})
	t.Run("class constant", func(t *testing.T) {
		edit := rename(t, "CLUBS = 'clubs'", "HEARTS")
		assert.Equal(
			t,
			[]string{`Foo\\CardSuite::HEARTS`},
			newTexts(edit, "constant.html.twig"),
		)
		assert.Equal(
			t,
			[]string{"HEARTS"},
			newTexts(edit, "constant_accessor.html.twig"),
		)
	})
	t.Run("global constant", func(t *testing.T) {
		edit := rename(t, "NAMESPACED_CONST", "RENAMED_CONST")
		assert.Equal(
			t,
			[]string{`BugDemo\\RENAMED_CONST`},
			newTexts(edit, "global.html.twig"),
		)
	})
	t.Run("enum case", func(t *testing.T) {
		edit := rename(t, "CLUBS; }", "HEARTS")
		assert.Equal(
			t,
			[]string{`Foo\\Suit::HEARTS`},
			newTexts(edit, "case.html.twig"),
		)
	})
	t.Run("class annotation", func(t *testing.T) {
		edit := rename(t, "TestBrokenRefactor {", "RenamedClass")
		assert.Equal(
			t,
			[]string{`\MyNamespace\RenamedClass`},
			newTexts(edit, "class.html.twig"),
		)
	})
}
