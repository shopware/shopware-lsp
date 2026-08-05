package completion

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

func TestHttpClientOptionCompletionUsesTypedInterfaceDefaults(t *testing.T) {
	phpIndex, root := httpClientCompletionFixture(t)
	source := `<?php
namespace App;
use Symfony\Contracts\HttpClient\HttpClientInterface;
function fetch(HttpClientInterface $client): void {
    $client->request('GET', '/', [
        'headers' => [],
        'ti' => null,
    ]);
}`
	path := filepath.Join(root, "src", "Consumer.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "'ti'") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	items := NewHttpClientCompletionProvider(phpIndex).GetCompletions(
		ctx,
		responseConstantCompletionRequest(document, node, offset),
	)

	timeout := requireCompletion(t, items, "timeout")
	assert.Equal(t, int(protocol.PropertyCompletion), timeout.Kind)
	assert.Contains(t, timeout.Detail, "null")
	assert.Contains(t, timeout.Documentation.Value, "OPTIONS_DEFAULTS")
	edit, ok := timeout.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "ti", completionRangeText(document, edit.Range))
	assert.Equal(t, "timeout", edit.NewText)
	assert.NotContains(t, completionLabels(items), "headers")
	assert.Contains(t, completionLabels(items), "verify_peer")
}

func TestHttpClientOptionCompletionSupportsNamedWithOptions(t *testing.T) {
	phpIndex, root := httpClientCompletionFixture(t)
	source := `<?php
namespace App;
use Symfony\Contracts\HttpClient\HttpClientInterface;
function configure(HttpClientInterface $client): void {
    $client->withOptions(options: ['' => null]);
}`
	path := filepath.Join(root, "src", "Configure.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "[''") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	items := NewHttpClientCompletionProvider(phpIndex).GetCompletions(
		ctx,
		responseConstantCompletionRequest(document, node, offset),
	)
	assert.ElementsMatch(
		t,
		[]string{"headers", "timeout", "verify_peer"},
		completionLabels(items),
	)
}

func TestHttpClientOptionCompletionRequiresTypedClient(t *testing.T) {
	phpIndex, root := httpClientCompletionFixture(t)
	source := `<?php
namespace App;
class CustomClient {
    public function request(string $method, string $url, array $options): void {}
}
function fetch(CustomClient $client): void {
    $client->request('GET', '/', ['timeout' => null]);
}`
	path := filepath.Join(root, "src", "Custom.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "'timeout'") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	assert.Empty(t, NewHttpClientCompletionProvider(phpIndex).GetCompletions(
		ctx,
		responseConstantCompletionRequest(document, node, offset),
	))
}

func httpClientCompletionFixture(
	t *testing.T,
) (*php.PHPIndex, string) {
	t.Helper()
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "HttpClientInterface.php"),
		[]byte(`<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
        'verify_peer' => true,
    ];
    public function request(string $method, string $url, array $options = []): object;
    public function withOptions(array $options): static;
}
`),
	)))
	return phpIndex, root
}
