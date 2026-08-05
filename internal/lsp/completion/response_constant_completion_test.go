package completion

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseConstantsCompleteSetStatusCodeAndComparisons(t *testing.T) {
	root := t.TempDir()
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	stubPath := filepath.Join(root, "vendor", "Response.php")
	stub := `<?php
namespace Symfony\Component\HttpFoundation;
class Response {
    public const HTTP_OK = 200;
    public const HTTP_NOT_FOUND = 404;
    public const INTERNAL_VALUE = 'not a status';
    public function setStatusCode(int $code): static { return $this; }
    public function getStatusCode(): int { return 200; }
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		stubPath,
		[]byte(stub),
	)))
	provider := NewResponseConstantCompletionProvider(idx)

	tests := []struct {
		file   string
		source string
		marker string
	}{
		{
			file: "SetStatus.php",
			source: `<?php
namespace App;
use Symfony\Component\HttpFoundation\Response;
function update(Response $response): void {
    $response->setStatusCode();
}`,
			marker: "setStatusCode(",
		},
		{
			file: "CompareStatus.php",
			source: `<?php
namespace App;
use Symfony\Component\HttpFoundation\Response as HttpResponse;
function missing(HttpResponse $response): bool {
    return $response->getStatusCode() === 0;
}`,
			marker: "=== ",
		},
	}
	for index, test := range tests {
		path := filepath.Join(root, "src", test.file)
		document := lsp.NewTextDocument(
			uriutil.FileURI(path),
			test.source,
			1,
		)
		offset := uint32(strings.Index(test.source, test.marker) +
			len(test.marker))
		if !strings.HasSuffix(test.marker, "(") {
			offset = uint32(strings.LastIndex(test.source, "0"))
		}
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		ctx := idx.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		items := provider.GetCompletions(
			ctx,
			responseConstantCompletionRequest(document, node, offset),
		)
		labels := completionLabels(items)
		if index == 0 {
			assert.Contains(t, labels, "Response::HTTP_OK")
			assert.Contains(t, labels, "Response::HTTP_NOT_FOUND")
		} else {
			assert.Contains(t, labels, "HttpResponse::HTTP_OK")
		}
		for _, label := range labels {
			assert.NotContains(t, label, "INTERNAL_VALUE")
		}
	}
}

func TestResponseConstantsRequireTypedSymfonyResponseContext(t *testing.T) {
	root := t.TempDir()
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	source := `<?php
class CustomResponse {
    public function setStatusCode(int $code): void {}
}
function update(CustomResponse $response): void {
    $response->setStatusCode();
}`
	path := filepath.Join(root, "Custom.php")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "setStatusCode(") +
		len("setStatusCode("))
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	assert.Empty(
		t,
		NewResponseConstantCompletionProvider(idx).GetCompletions(
			ctx,
			responseConstantCompletionRequest(document, node, offset),
		),
	)
}

func responseConstantCompletionRequest(
	document *lsp.TextDocument,
	node *phpsyntax.Node,
	offset uint32,
) *lsp.CompletionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.CompletionRequest{
		CompletionParams: params,
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
