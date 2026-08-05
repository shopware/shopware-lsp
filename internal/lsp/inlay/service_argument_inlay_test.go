package inlay

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

func TestYAMLServiceArgumentInlayHints(t *testing.T) {
	provider := newServiceArgumentInlayProvider(t)
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		`services:
  app.consumer:
    class: App\Consumer
    arguments: ['@app.logger', '%app.channel%']
    calls:
      - [setLogger, ['@app.logger', 10]]
`,
		1,
	)
	hints, err := provider.GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, hints, 4)
	assert.Equal(
		t,
		[]string{"LoggerInterface", "channel", "LoggerInterface", "priority"},
		inlayHintLabels(hints),
	)
	assert.Equal(t, "$logger: App\\LoggerInterface", hints[0].Tooltip)
	assert.Equal(t, protocol.InlayHintKindParameter, hints[0].Kind)
	assert.True(t, hints[0].PaddingLeft)
}

func TestYAMLServiceArgumentInlayHintsResolveNamedAndInheritedParameters(
	t *testing.T,
) {
	provider := newServiceArgumentInlayProvider(t)
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		`services:
  App\ChildConsumer:
    arguments:
      $channel: storefront
      $logger: '@app.logger'
`,
		1,
	)
	hints, err := provider.GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, hints, 2)
	assert.Equal(t, []string{"channel", "LoggerInterface"}, inlayHintLabels(hints))
}

func TestXMLServiceArgumentInlayHints(t *testing.T) {
	provider := newServiceArgumentInlayProvider(t)
	document := lsp.NewTextDocument(
		"file:///project/config/services.xml",
		`<container>
  <services>
    <service id="app.consumer" class="App\Consumer">
      <argument type="service" id="app.logger"/>
      <argument>%app.channel%</argument>
      <call method="setLogger">
        <argument type="service" id="app.logger"/>
        <argument>10</argument>
      </call>
    </service>
  </services>
</container>`,
		1,
	)
	hints, err := provider.GetInlayHints(
		context.Background(),
		inlayHintRequest(document),
	)
	require.NoError(t, err)
	require.Len(t, hints, 4)
	assert.Equal(
		t,
		[]string{"LoggerInterface", "channel", "LoggerInterface", "priority"},
		inlayHintLabels(hints),
	)
	for _, hint := range hints {
		assert.Greater(t, hint.Position.Line, 2)
	}
}

func TestServiceArgumentInlayHintsRespectRequestedRange(t *testing.T) {
	provider := newServiceArgumentInlayProvider(t)
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		`services:
  app.consumer:
    class: App\Consumer
    arguments:
      - '@app.logger'
      - '%app.channel%'
`,
		1,
	)
	request := inlayHintRequest(document)
	request.Range.Start.Line = 5
	request.Range.End.Line = 5
	request.Range.End.Character = 80
	hints, err := provider.GetInlayHints(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, hints, 1)
	assert.Equal(t, "channel", hints[0].Label)
}

func newServiceArgumentInlayProvider(
	t *testing.T,
) *ServiceArgumentProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/src/LoggerInterface.php": `<?php
namespace App;
interface LoggerInterface {}
`,
		"/project/src/Consumer.php": `<?php
namespace App;
class Consumer
{
    public function __construct(
        LoggerInterface $logger,
        string $channel,
    ) {}

    public function setLogger(
        LoggerInterface $logger,
        int $priority,
    ): void {}
}
`,
		"/project/src/ChildConsumer.php": `<?php
namespace App;
class ChildConsumer extends Consumer {}
`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	return NewServiceArgumentProvider(nil, phpIndex)
}

func inlayHintRequest(document *lsp.TextDocument) *lsp.InlayHintRequest {
	params := &protocol.InlayHintParams{}
	params.TextDocument.URI = document.URI
	line, character := document.LineIndex.PositionUTF16(
		uint32(len(document.Source)),
	)
	params.Range.End = protocol.Position{
		Line:      int(line),
		Character: int(character),
	}
	return &lsp.InlayHintRequest{
		InlayHintParams: params,
		Document:        document,
	}
}

func inlayHintLabels(hints []protocol.InlayHint) []string {
	result := make([]string, 0, len(hints))
	for _, hint := range hints {
		switch label := hint.Label.(type) {
		case string:
			result = append(result, label)
		case []protocol.InlayHintLabelPart:
			var value string
			for _, part := range label {
				value += part.Value
			}
			result = append(result, value)
		}
	}
	return result
}
