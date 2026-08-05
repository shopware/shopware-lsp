package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfiguredServiceMethodDefinitionAcrossYAMLAndXML(t *testing.T) {
	root := t.TempDir()
	phpPath := filepath.Join(root, "src", "Configured.php")
	phpSource := `<?php
namespace App;
class ParentConfigured
{
    public function inherited(): void {}
}
class Configured extends ParentConfigured
{
    public function setLogger(): void {}
    public function onKernelRequest(): void {}
}
class Builder
{
    public function create(): Product { return new Product(); }
    public static function build(): Product { return new Product(); }
}
class Product {}
`
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o755))
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o644))
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	provider := NewServiceXMLDefinitionProvider(nil, phpIndex)

	for _, fixture := range []struct {
		name   string
		file   string
		source string
		method string
	}{
		{
			name: "YAML inherited call",
			file: "services.yaml",
			source: `services:
  app.consumer:
    class: App\Configured
    calls:
      - [inher<caret>ited, []]
`,
			method: "inherited",
		},
		{
			name: "YAML service factory",
			file: "services.yaml",
			source: `services:
  app.builder:
    class: App\Builder
  app.product:
    class: App\Product
    factory: ['@app.builder', cre<caret>ate]
`,
			method: "create",
		},
		{
			name: "YAML class factory string",
			file: "services.yaml",
			source: `services:
  app.product:
    class: App\Product
    factory: 'App\Builder::bu<caret>ild'
`,
			method: "build",
		},
		{
			name: "XML call",
			file: "services.xml",
			source: `<container><services>
  <service id="app.consumer" class="App\Configured">
    <call method="setLog<caret>ger"/>
  </service>
</services></container>`,
			method: "setLogger",
		},
		{
			name: "XML tag callback",
			file: "services.xml",
			source: `<container><services>
  <service id="app.consumer" class="App\Configured">
    <tag name="kernel.event_listener" method="onKernel<caret>Request"/>
  </service>
</services></container>`,
			method: "onKernelRequest",
		},
		{
			name: "XML service factory",
			file: "services.xml",
			source: `<container><services>
  <service id="app.builder" class="App\Builder"/>
  <service id="app.product" class="App\Product">
    <factory service="app.builder" method="cre<caret>ate"/>
  </service>
</services></container>`,
			method: "create",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := definitionCaret(t, fixture.source)
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(root, "config", fixture.file)),
				source,
				1,
			)
			line, character := document.LineIndex.PositionUTF16(
				uint32(offset),
			)
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			locations := provider.GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document:        document,
						Language:        document.SyntaxLanguage,
						DocumentContent: document.Text,
						DocumentTree:    document.SyntaxTree,
						LineIndex:       document.LineIndex,
						Root:            document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							uint32(offset),
						),
					},
				},
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(phpPath), locations[0].URI)
			phpDocument := lsp.NewTextDocument(
				uriutil.FileURI(phpPath),
				phpSource,
				1,
			)
			start := phpDocument.LineIndex.OffsetUTF16(
				uint32(locations[0].Range.Start.Line),
				uint32(locations[0].Range.Start.Character),
			)
			end := phpDocument.LineIndex.OffsetUTF16(
				uint32(locations[0].Range.End.Line),
				uint32(locations[0].Range.End.Character),
			)
			assert.Equal(t, fixture.method, phpSource[start:end])
		})
	}
}

func definitionCaret(t *testing.T, source string) (string, int) {
	t.Helper()
	offset := strings.Index(source, "<caret>")
	require.NotEqual(t, -1, offset)
	return strings.Replace(source, "<caret>", "", 1), offset
}
