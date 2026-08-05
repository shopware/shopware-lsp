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

func TestHttpClientOptionHoverShowsTypeAndDefault(t *testing.T) {
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
    ];
    public function withOptions(array $options): static;
}
`),
	)))
	path := filepath.Join(root, "src", "Consumer.php")
	source := `<?php
use Symfony\Contracts\HttpClient\HttpClientInterface;
function configure(HttpClientInterface $client): void {
    $client->withOptions(['timeout' => 2.0]);
}`
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, "'timeout'") + 2)
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
	result, err := NewHttpClientHoverProvider(phpIndex).GetHover(
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
	assert.Contains(t, result.Contents.Value, "Symfony HttpClient option")
	assert.Contains(t, result.Contents.Value, "`timeout`")
	assert.Contains(t, result.Contents.Value, "Default type: `null`")
	assert.Contains(t, result.Contents.Value, "```php\nnull\n```")
	require.NotNil(t, result.Range)
	assert.Equal(t, 27, result.Range.Start.Character)
	assert.Equal(t, 34, result.Range.End.Character)
}
