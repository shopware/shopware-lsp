package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestHttpClientOptionDefinitionNavigatesInterfaceConstantKey(
	t *testing.T,
) {
	root := t.TempDir()
	interfacePath := filepath.Join(
		root,
		"vendor",
		"HttpClientInterface.php",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(interfacePath), 0o755))
	interfaceSource := `<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
    ];
    public function request(string $method, string $url, array $options = []): object;
}`
	require.NoError(t, os.WriteFile(
		interfacePath,
		[]byte(interfaceSource),
		0o600,
	))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		interfacePath,
		[]byte(interfaceSource),
	)))
	consumerPath := filepath.Join(root, "src", "Consumer.php")
	consumerSource := `<?php
use Symfony\Contracts\HttpClient\HttpClientInterface;
function fetch(HttpClientInterface $client): void {
    $client->request('GET', '/', ['timeout' => null]);
}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(consumerPath),
		consumerSource,
		1,
	)
	offset := uint32(strings.LastIndex(consumerSource, "'timeout'") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		consumerPath,
		1,
		node,
		document.SyntaxTree.Root,
	)
	locations := NewHttpClientDefinitionProvider(phpIndex).GetDefinition(
		ctx,
		consoleDefinitionRequest(document, node),
	)

	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(interfacePath), locations[0].URI)
	assert.Equal(t, 4, locations[0].Range.Start.Line)
	assert.Equal(t, 8, locations[0].Range.Start.Character)
	assert.Equal(t, 17, locations[0].Range.End.Character)
}
