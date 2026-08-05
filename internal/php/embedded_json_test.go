package php

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
)

func TestEmbeddedJSONLiteralsResolveReceiversDecodeAndMapStrings(
	t *testing.T,
) {
	index, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/vendor/JsonResponse.php",
		[]byte(`<?php
namespace Symfony\Component\HttpFoundation;
class JsonResponse {
    public static function fromJsonString(string $data): static {}
    public function setJson(string $data): static {}
}
`),
	)))

	source := `<?php
namespace App;
use Symfony\Component\HttpFoundation\JsonResponse as Response;

final class CustomResponse extends Response {}
final class OtherResponse {
    public function setJson(string $data): void {}
}

function send(
    Response $response,
    CustomResponse $custom,
    OtherResponse $other,
    string $value,
): void {
    $response->setJson('{"ok":true}');
    Response::fromJsonString("{\"name\":\"Shopware\"}");
    $custom->setJson(data: '{"items":[1]}');
    $other->setJson('{broken}');
    $response->setJson("{\"dynamic\":\"$value\"}");
}
`
	parsed := phpparser.Parse(source)
	require.Empty(t, parsed.Errors)
	literals := EmbeddedJSONLiterals(
		index,
		"/project/src/Controller.php",
		1,
		source,
		parsed.Tree.Root,
	)
	require.Len(t, literals, 3)
	assert.Equal(t, `{"ok":true}`, literals[0].Value)
	assert.Equal(t, `{"name":"Shopware"}`, literals[1].Value)
	assert.Equal(t, `{"items":[1]}`, literals[2].Value)

	nameStart := strings.Index(literals[1].Value, "name")
	require.NotEqual(t, -1, nameStart)
	nameRange := literals[1].SourceRange(cst.TextRange{
		Start: uint32(nameStart),
		End:   uint32(nameStart + len("name")),
	})
	assert.Equal(
		t,
		"name",
		source[nameRange.Start:nameRange.End],
	)
	require.Len(
		t,
		literals[1].SourceOffsets,
		len(literals[1].Value)+1,
	)
}
