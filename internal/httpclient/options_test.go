package httpclient

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestOptionsUsePersistedInterfaceConstantMetadata(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/vendor/HttpClientInterface.php",
		[]byte(`<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
        'verify_peer' => true,
    ];
}
`),
	)))

	options := Options(phpIndex)
	require.Len(t, options, 3)
	assert.Equal(t, "headers", options[0].Name)
	assert.Equal(t, "array", options[0].Type.String())
	assert.Equal(t, "[]", options[0].Default)
	assert.Equal(t, "timeout", options[1].Name)
	assert.Equal(t, "null", options[1].Type.String())
	assert.Equal(t, "verify_peer", options[2].Name)
	assert.Equal(t, "bool", options[2].Type.String())
}

func TestReferenceAtRecognizesOnlyTopLevelHttpClientOptionKeys(t *testing.T) {
	tests := []struct {
		name   string
		source string
		marker string
		found  bool
	}{
		{
			name: "request positional",
			source: `<?php
$client->request('GET', '/', ['timeout' => null]);
`,
			marker: "timeout",
			found:  true,
		},
		{
			name: "request named",
			source: `<?php
$client->request(options: ['timeout' => null], method: 'GET', url: '/');
`,
			marker: "timeout",
			found:  true,
		},
		{
			name: "with options",
			source: `<?php
$client->withOptions(['timeout' => null]);
`,
			marker: "timeout",
			found:  true,
		},
		{
			name: "nested header",
			source: `<?php
$client->request('GET', '/', ['headers' => ['timeout' => null]]);
`,
			marker: "timeout",
			found:  false,
		},
		{
			name: "option value",
			source: `<?php
$client->request('GET', '/', ['label' => 'timeout']);
`,
			marker: "timeout",
			found:  false,
		},
		{
			name: "wrong argument",
			source: `<?php
$client->request('GET', ['timeout' => null]);
`,
			marker: "timeout",
			found:  false,
		},
		{
			name: "unrelated method",
			source: `<?php
$client->send(['timeout' => null]);
`,
			marker: "timeout",
			found:  false,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result := phpparser.Parse(testCase.source)
			offset := uint32(strings.Index(testCase.source, testCase.marker) + 1)
			node := result.Tree.Root.NodeAtOffset(offset)
			reference, found := ReferenceAt(node)
			assert.Equal(t, testCase.found, found)
			if found {
				assert.Equal(t, "timeout", reference.Name)
				actual := testCase.source[reference.Range.Start:reference.Range.End]
				assert.Equal(
					t,
					"timeout",
					actual,
				)
			}
		})
	}
}

func TestUsedOptionNamesExcludesCurrentKey(t *testing.T) {
	source := `<?php
$client->request('GET', '/', [
    'headers' => [],
    'timeout' => null,
]);
`
	root := phpparser.Parse(source).Tree.Root
	node := root.NodeAtOffset(uint32(strings.Index(source, "timeout") + 1))
	reference, found := ReferenceAt(node)
	require.True(t, found)
	assert.Equal(
		t,
		map[string]struct{}{"headers": {}},
		UsedOptionNames(reference),
	)
}

func TestUsedOptionNamesKeepsSeparateDuplicateOfCurrentKey(t *testing.T) {
	source := `<?php
$client->withOptions([
    'timeout' => 1,
    'timeout' => 2,
]);
`
	root := phpparser.Parse(source).Tree.Root
	offset := strings.LastIndex(source, "'timeout'") + 1
	node := root.NodeAtOffset(uint32(offset))
	reference, found := ReferenceAt(node)
	require.True(t, found)
	assert.Equal(
		t,
		map[string]struct{}{"timeout": {}},
		UsedOptionNames(reference),
	)
}
