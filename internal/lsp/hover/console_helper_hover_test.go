package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestConsoleHelperHoverShowsResolvedClass(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "Console.php"),
		[]byte(`<?php
namespace Symfony\Component\Console\Helper;
interface HelperInterface { public function getName(): string; }
abstract class Helper implements HelperInterface {}
class HelperSet { public function has(string $name): bool {} }
`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "QuestionHelper.php"),
		[]byte(`<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
/** Helps ask interactive questions. */
class QuestionHelper extends Helper {
    public function getName(): string { return 'question'; }
}`),
	)))
	path := filepath.Join(root, "src", "Consumer.php")
	source := `<?php
use Symfony\Component\Console\Helper\HelperSet;
function inspect(HelperSet $helpers): void {
    $helpers->has('question');
}`
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, "question") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	result, err := NewConsoleHelperHoverProvider(phpIndex).GetHover(
		ctx,
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
	assert.Contains(t, result.Contents.Value, "Symfony Console helper")
	assert.Contains(t, result.Contents.Value, "`question`")
	assert.Contains(t, result.Contents.Value, "`App\\QuestionHelper`")
	assert.Contains(t, result.Contents.Value, "interactive questions")
	require.NotNil(t, result.Range)
	assert.Equal(t, 19, result.Range.Start.Character)
	assert.Equal(t, 27, result.Range.End.Character)
}
