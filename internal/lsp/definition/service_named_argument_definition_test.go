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
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLServiceNamedArgumentDefinition(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpPath := filepath.Join(t.TempDir(), "Consumer.php")
	phpSource := `<?php
namespace App;
class ParentConsumer {
    public function __construct(string $logger, int $count = 0) {}
}
	class ChildConsumer extends ParentConsumer {}
`
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o600))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	provider := NewServiceXMLDefinitionProvider(nil, phpIndex)

	source := `services:
  app.consumer:
    class: App\ChildConsumer
    arguments:
      $logger: '@logger'`
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "$logger") + 3)
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
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(phpPath), locations[0].URI)
	start := strings.Index(phpSource, "$logger")
	startLine, startCharacter := lsp.NewTextDocument(
		"file://"+phpPath,
		phpSource,
		1,
	).LineIndex.PositionUTF16(uint32(start))
	assert.Equal(t, int(startLine), locations[0].Range.Start.Line)
	assert.Equal(t, int(startCharacter), locations[0].Range.Start.Character)
}

func TestYAMLServiceNamedArgumentDefinitionIgnoresUnknownAndFactory(
	t *testing.T,
) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Consumer.php",
		[]byte(`<?php
namespace App;
class Consumer {
    public function __construct(string $logger) {}
}`),
	)))
	provider := NewServiceXMLDefinitionProvider(nil, phpIndex)
	for _, source := range []string{
		`services:
  app.consumer:
    class: App\Consumer
    arguments:
      $missing: value`,
		`services:
  app.consumer:
    class: App\Consumer
    factory: ['@factory', create]
    arguments:
      $logger: value`,
	} {
		document := lsp.NewTextDocument(
			"file:///project/config/services.yaml",
			source,
			1,
		)
		offset := uint32(strings.LastIndex(source, "$") + 2)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		assert.Empty(t, provider.GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
				DefinitionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document:        document,
					DocumentContent: document.Text,
					DocumentTree:    document.SyntaxTree,
					LineIndex:       document.LineIndex,
					Root:            document.SyntaxTree.Root,
					Node: document.SyntaxTree.Root.NodeAtOffset(
						offset,
					),
				},
			},
		))
	}
}

func TestYAMLServiceDefaultBindingDefinition(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpPath := filepath.Join(t.TempDir(), "Consumers.php")
	phpSource := `<?php
namespace Psr\Log;
interface LoggerInterface {}

namespace BindArgument;
class Consumer {
    public function __construct(
        $proxyUrl,
        string $defaultUri,
        iterable $rules,
        \Psr\Log\LoggerInterface $logger,
    ) {}
}
class MismatchConsumer {
    public function __construct(int $defaultUri) {}
}
`
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o600))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	provider := NewServiceXMLDefinitionProvider(nil, phpIndex)

	for _, test := range []struct {
		name          string
		key           string
		service       string
		parameter     string
		expectMatches int
	}{
		{
			name:          "name only",
			key:           "$proxyUrl",
			service:       `BindArgument\Consumer`,
			parameter:     "$proxyUrl",
			expectMatches: 1,
		},
		{
			name:          "scalar type",
			key:           "string $defaultUri",
			service:       `BindArgument\Consumer`,
			parameter:     "$defaultUri",
			expectMatches: 1,
		},
		{
			name:          "iterable type",
			key:           "iterable $rules",
			service:       `BindArgument\Consumer`,
			parameter:     "$rules",
			expectMatches: 1,
		},
		{
			name:          "object type",
			key:           `Psr\Log\LoggerInterface $logger`,
			service:       `BindArgument\Consumer`,
			parameter:     "$logger",
			expectMatches: 1,
		},
		{
			name:          "type mismatch",
			key:           "string $defaultUri",
			service:       `BindArgument\MismatchConsumer`,
			parameter:     "$defaultUri",
			expectMatches: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "services:\n" +
				"  _defaults:\n" +
				"    bind:\n" +
				"      " + test.key + ": value\n" +
				"  " + test.service + ": ~\n"
			document := lsp.NewTextDocument(
				"file:///project/config/services.yaml",
				source,
				1,
			)
			offset := uint32(strings.Index(source, "$") + 2)
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
			require.Len(t, locations, test.expectMatches)
			if test.expectMatches == 0 {
				return
			}
			assert.Equal(t, uriutil.FileURI(phpPath), locations[0].URI)
			parameterOffset := strings.Index(phpSource, test.parameter)
			parameterLine, parameterCharacter := lsp.NewTextDocument(
				"file://"+phpPath,
				phpSource,
				1,
			).LineIndex.PositionUTF16(uint32(parameterOffset))
			assert.Equal(t, int(parameterLine), locations[0].Range.Start.Line)
			assert.Equal(
				t,
				int(parameterCharacter),
				locations[0].Range.Start.Character,
			)
		})
	}
}

func TestYAMLServiceDefaultBindingDefinitionExpandsPrototypeServices(
	t *testing.T,
) {
	projectRoot := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpPath := filepath.Join(projectRoot, "src", "PrototypeConsumer.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(phpPath), 0o700))
	phpSource := `<?php
namespace App;
class PrototypeConsumer {
    public function __construct(string $projectDir) {}
}`
	require.NoError(t, os.WriteFile(phpPath, []byte(phpSource), 0o600))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))

	serviceIndex, err := symfony.NewServiceIndex(
		projectRoot,
		t.TempDir(),
	)
	require.NoError(t, err)
	serviceIndex.SetPHPIndex(phpIndex)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	provider := NewServiceXMLDefinitionProvider(serviceIndex, phpIndex)

	configPath := filepath.Join(projectRoot, "config", "services.yaml")
	source := `services:
  _defaults:
    bind:
      string $projectDir: '%kernel.project_dir%'
  'App\':
    resource: '../src'
`
	document := lsp.NewTextDocument(
		uriutil.FileURI(configPath),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "$projectDir") + 2)
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
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(phpPath), locations[0].URI)
}
