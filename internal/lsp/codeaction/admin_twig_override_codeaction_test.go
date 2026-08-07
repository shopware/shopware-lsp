package codeaction

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestAdminTwigOverrideActionResolvesOwningComponent(t *testing.T) {
	fixture := newAdminTwigOverrideFixture(t)
	source := `{% block sw_card_header %}<h1>Title</h1>{% endblock %}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(fixture.templatePath),
		source,
		1,
	)
	offset := strings.Index(source, "sw_card_header") + 2
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CodeActionParams{}
	params.TextDocument.URI = document.URI
	params.Range = protocol.Range{
		Start: protocol.Position{Line: int(line), Character: int(character)},
		End:   protocol.Position{Line: int(line), Character: int(character)},
	}
	request := &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Token: document.SyntaxTree.Root.TokenAtOffset(
				uint32(offset),
			),
			Node: document.SyntaxTree.Root.NodeAtOffset(uint32(offset)),
		},
	}

	actions := fixture.provider.GetCodeActions(context.Background(), request)
	require.Len(t, actions, 1)
	assert.Equal(
		t,
		"Override sw_card_header for Administration component sw-card in a plugin",
		actions[0].Title,
	)
	require.NotNil(t, actions[0].Command)
	assert.Equal(t, adminTwigOverrideAction, actions[0].Command.Command)
	assert.Equal(
		t,
		[]any{document.URI, "sw_card_header"},
		actions[0].Command.Arguments,
	)
}

func TestAdminTwigOverrideActionIsNotOfferedForStorefrontTwig(t *testing.T) {
	fixture := newAdminTwigOverrideFixture(t)
	source := `{% block sw_card_header %}{% endblock %}`
	document := lsp.NewTextDocument(
		"file:///project/Resources/views/storefront/card.html.twig",
		source,
		1,
	)
	offset := strings.Index(source, "sw_card_header") + 1
	params := &protocol.CodeActionParams{}
	params.TextDocument.URI = document.URI
	request := &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document,
			Root:     document.SyntaxTree.Root,
			Token:    document.SyntaxTree.Root.TokenAtOffset(uint32(offset)),
			Node:     document.SyntaxTree.Root.NodeAtOffset(uint32(offset)),
		},
	}
	assert.Empty(
		t,
		fixture.provider.GetCodeActions(context.Background(), request),
	)
}

func TestStorefrontTwigActionsDoNotLeakIntoAdministrationTemplates(t *testing.T) {
	root := t.TempDir()
	twigIndex, err := twig.NewTwigIndexer(filepath.Join(root, "twig-cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		filepath.Join(
			root,
			"src/Storefront/Resources/views/storefront/component/card.html.twig",
		),
		[]byte(`{% block sw_card_header %}{% endblock %}`),
	)))

	source := `{% block sw_card_header %}{% endblock %}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
		)),
		source,
		1,
	)
	offset := strings.Index(source, "sw_card_header") + 1
	params := &protocol.CodeActionParams{}
	params.TextDocument.URI = document.URI
	request := &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Token:           document.SyntaxTree.Root.TokenAtOffset(uint32(offset)),
			Node:            document.SyntaxTree.Root.NodeAtOffset(uint32(offset)),
		},
	}
	assert.Empty(
		t,
		NewTwigCodeActionProvider(root, twigIndex).GetCodeActions(
			context.Background(),
			request,
		),
	)
}

func TestAdminTwigOverrideGeneratorCreatesLoadableOverride(t *testing.T) {
	fixture := newAdminTwigOverrideFixture(t)
	administrationSource := filepath.Join(
		fixture.pluginSource,
		"Resources",
		"app",
		"administration",
		"src",
	)
	require.NoError(t, os.MkdirAll(administrationSource, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(administrationSource, "main.js"),
		[]byte("import './module/demo';\n"),
		0o644,
	))

	value, err := fixture.provider.generateAdminTwigOverride(
		context.Background(),
		adminTwigOverrideJSON(t, adminTwigOverrideRequest{
			TextURI:   uriutil.FileURI(fixture.templatePath),
			BlockName: "sw_card_header",
			Extension: "DemoPlugin",
		}),
	)
	require.NoError(t, err)
	result, ok := value.(*adminTwigOverrideResponse)
	require.True(t, ok)
	assert.Equal(t, "sw-card", result.Component)
	assert.Equal(t, 0, result.Line)

	overrideDirectory := filepath.Join(
		administrationSource,
		"extension",
		"sw-card",
	)
	script, err := os.ReadFile(filepath.Join(overrideDirectory, "index.js"))
	require.NoError(t, err)
	assert.Equal(t, `import template from './sw-card.html.twig';

Shopware.Component.override('sw-card', {
    template,
});
`, string(script))
	template, err := os.ReadFile(
		filepath.Join(overrideDirectory, "sw-card.html.twig"),
	)
	require.NoError(t, err)
	assert.Equal(t, `{% block sw_card_header %}

{% endblock %}
`, string(template))
	entry, err := os.ReadFile(filepath.Join(administrationSource, "main.js"))
	require.NoError(t, err)
	assert.Equal(t, "import './extension/sw-card';\nimport './module/demo';\n", string(entry))
	assert.Equal(
		t,
		uriutil.FileURI(filepath.Join(overrideDirectory, "sw-card.html.twig")),
		result.URI,
	)
}

func TestAdminTwigOverrideGeneratorAppendsAndIsIdempotent(t *testing.T) {
	fixture := newAdminTwigOverrideFixture(t)
	request := func(block string) interface{} {
		value, err := fixture.provider.generateAdminTwigOverride(
			context.Background(),
			adminTwigOverrideJSON(t, adminTwigOverrideRequest{
				TextURI:   uriutil.FileURI(fixture.templatePath),
				BlockName: block,
				Extension: "DemoPlugin",
			}),
		)
		require.NoError(t, err)
		return value
	}

	request("sw_card_header")
	request("sw_card_body")
	templatePath := filepath.Join(
		fixture.pluginSource,
		"Resources/app/administration/src/extension/sw-card/sw-card.html.twig",
	)
	content, err := os.ReadFile(templatePath)
	require.NoError(t, err)
	expected := `{% block sw_card_header %}

{% endblock %}

{% block sw_card_body %}

{% endblock %}
`
	assert.Equal(t, expected, string(content))

	request("sw_card_header")
	unchanged, err := os.ReadFile(templatePath)
	require.NoError(t, err)
	assert.Equal(t, expected, string(unchanged))
	entry, err := os.ReadFile(filepath.Join(
		fixture.pluginSource,
		"Resources/app/administration/src/main.js",
	))
	require.NoError(t, err)
	assert.Equal(t, "import './extension/sw-card';\n", string(entry))
}

func TestAdminTwigOverrideGeneratorPreservesConflictingFiles(t *testing.T) {
	fixture := newAdminTwigOverrideFixture(t)
	overrideDirectory := filepath.Join(
		fixture.pluginSource,
		"Resources/app/administration/src/extension/sw-card",
	)
	require.NoError(t, os.MkdirAll(overrideDirectory, 0o755))
	scriptPath := filepath.Join(overrideDirectory, "index.js")
	original := []byte(`import template from './sw-card.html.twig';

Shopware.Component.override('another-component', { template });
`)
	require.NoError(t, os.WriteFile(scriptPath, original, 0o644))

	value, err := fixture.provider.generateAdminTwigOverride(
		context.Background(),
		adminTwigOverrideJSON(t, adminTwigOverrideRequest{
			TextURI:   uriutil.FileURI(fixture.templatePath),
			BlockName: "sw_card_header",
			Extension: "DemoPlugin",
		}),
	)
	require.NoError(t, err)
	lspError, ok := value.(*protocol.ShopwareLspError)
	require.True(t, ok)
	assert.Equal(t, "admin.override.file_conflict", lspError.Code)
	assert.Contains(t, lspError.Message, "does not override component")
	unchanged, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	assert.Equal(t, original, unchanged)
	_, err = os.Stat(filepath.Join(overrideDirectory, "sw-card.html.twig"))
	assert.True(t, os.IsNotExist(err))
}

type adminTwigOverrideFixture struct {
	provider     *AdminTwigOverrideProvider
	templatePath string
	pluginSource string
}

func newAdminTwigOverrideFixture(t *testing.T) adminTwigOverrideFixture {
	t.Helper()
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "admin-cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	extensionIndex, err := extension.NewExtensionIndexer(
		filepath.Join(root, "extension-cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, extensionIndex.Close()) })

	templatePath := filepath.Join(
		root,
		"platform/src/Administration/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
	)
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name:           "sw-card",
		FilePath:       filepath.Join(filepath.Dir(templatePath), "index.js"),
		DefinitionPath: filepath.Join(filepath.Dir(templatePath), "index.js"),
		TemplatePath:   templatePath,
		Blocks: []admin.TwigBlock{{
			Name:     "sw_card_header",
			Line:     1,
			FilePath: templatePath,
		}},
	}))

	pluginSource := filepath.Join(root, "custom/plugins/DemoPlugin/src")
	pluginClass := filepath.Join(pluginSource, "DemoPlugin.php")
	require.NoError(t, extensionIndex.Index(indexer.NewParsedFile(
		pluginClass,
		[]byte(`<?php declare(strict_types=1);

namespace Demo;

use Shopware\Core\Framework\Plugin;

final class DemoPlugin extends Plugin
{
}
`),
	)))

	return adminTwigOverrideFixture{
		provider:     NewAdminTwigOverrideProvider(adminIndex, extensionIndex),
		templatePath: templatePath,
		pluginSource: pluginSource,
	}
}

func adminTwigOverrideJSON(
	t *testing.T,
	value adminTwigOverrideRequest,
) *json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	message := json.RawMessage(raw)
	return &message
}
