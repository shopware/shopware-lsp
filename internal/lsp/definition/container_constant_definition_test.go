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

func TestContainerConstantDefinitionForYAMLAndXML(t *testing.T) {
	root := t.TempDir()
	phpPath := filepath.Join(root, "src", "Mode.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o755))
	phpSource := []byte(`<?php
namespace App;
class Mode {
    public const ACTIVE = 'active';
}
enum State {
    case ACTIVE;
}
`)
	require.NoError(t, os.WriteFile(phpPath, phpSource, 0o644))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		phpSource,
	)))
	provider := NewContainerConstantDefinitionProvider(phpIndex)

	tests := []struct {
		uri    string
		source string
	}{
		{
			uri:    uriutil.FileURI(filepath.Join(root, "config", "services.yaml")),
			source: `value: !php/const App\Mode::ACTIVE`,
		},
		{
			uri:    uriutil.FileURI(filepath.Join(root, "config", "services.yaml")),
			source: `value: !php/enum App\State::ACTIVE->value`,
		},
		{
			uri:    uriutil.FileURI(filepath.Join(root, "config", "services.yaml")),
			source: `value: !php/enum App\State`,
		},
		{
			uri: uriutil.FileURI(
				filepath.Join(root, "config", "services.xml"),
			),
			source: `<container><services><service id="app">
<argument type="constant">App\Mode::ACTIVE</argument>
</service></services></container>`,
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(test.uri, test.source, 1)
		target := "ACTIVE"
		if !strings.Contains(test.source, target) {
			target = "State"
		}
		offset := uint32(strings.Index(test.source, target) + 2)
		line, character := document.LineIndex.PositionUTF16(offset)
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
						offset,
					),
				},
			},
		)
		require.Len(t, locations, 1, test.uri)
		assert.Equal(t, uriutil.FileURI(phpPath), locations[0].URI)
		start := documentOffsetForPosition(
			phpSource,
			locations[0].Range.Start,
		)
		end := documentOffsetForPosition(
			phpSource,
			locations[0].Range.End,
		)
		assert.Equal(t, target, string(phpSource[start:end]))
	}
}
