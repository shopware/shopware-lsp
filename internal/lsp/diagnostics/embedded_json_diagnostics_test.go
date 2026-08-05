package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestEmbeddedJSONDiagnosticsValidateTypedJsonResponseStrings(
	t *testing.T,
) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
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
use Symfony\Component\HttpFoundation\JsonResponse;

final class Other {
    public function setJson(string $data): void {}
}

function send(JsonResponse $response, Other $other, string $value): void {
    $response->setJson('{"valid":[1,true,null]}');
    $response->setJson('{"broken":}');
    JsonResponse::fromJsonString("{\"first\":true \"second\":false}");
    $other->setJson('{"ignored":}');
    $response->setJson("{\"dynamic\":\"$value\"}");
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/Controller.php",
		source,
		1,
	)
	result, err := NewEmbeddedJSONAnalyzer(
		phpIndex,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, diagnostic := range result {
		assert.Equal(t, invalidEmbeddedJSONCode, diagnostic.ID)
		assert.Equal(t, "symfony", diagnostic.Source)
		assert.Equal(t, protocol.DiagnosticSeverityError, diagnostic.Severity)
		assert.Contains(t, diagnostic.Message, "Invalid JSON: expected")
	}
	firstLine, _ := document.LineIndex.Position(result[0].Range.Start)
	secondLine, _ := document.LineIndex.Position(result[1].Range.Start)
	assert.Equal(t, uint32(9), firstLine)
	assert.Equal(t, uint32(10), secondLine)
}
