package symbol

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminDocumentSymbolsUseLiveComponentDefinition(t *testing.T) {
	path := filepath.Join(
		t.TempDir(),
		"src/Administration/Resources/app/administration/src/app/component/sw-card/index.ts",
	)
	source := `
import LocalCard from './local-card';

/** @deprecated Use mt-card instead. */
Component.register('sw-card', {
    components: { LocalCard },
    directives: { tooltip: {} },
    inject: ['repositoryFactory'],
    props: {
        /** @deprecated Use heading instead. */
        title: { type: String, required: true },
        count: Number,
    },
    emits: ['save'],
    data() { return { loading: false }; },
    computed: { headline() { return this.title; } },
	methods: {
		/** @deprecated Use save instead. */
		submit(value) { this.$emit('save', value); },
	},
});`
	symbols := adminDocumentSymbolsFor(
		t, NewAdminDocumentSymbolProvider(nil), path, source,
	)
	require.Len(t, symbols, 1)
	component := symbols[0]
	assert.Equal(t, "sw-card", component.Name)
	assert.Equal(t, protocol.SymbolClass, component.Kind)
	assert.True(t, component.Deprecated)
	assert.Equal(t, "sw-card", documentSymbolRangeText(
		t, source, component.SelectionRange,
	))

	expected := map[string]protocol.SymbolKind{
		"local-card":        protocol.SymbolClass,
		"v-tooltip":         protocol.SymbolFunction,
		"repositoryFactory": protocol.SymbolField,
		"title":             protocol.SymbolProperty,
		"count":             protocol.SymbolProperty,
		"save":              protocol.SymbolEvent,
		"loading":           protocol.SymbolField,
		"headline":          protocol.SymbolProperty,
		"submit":            protocol.SymbolMethod,
	}
	for name, kind := range expected {
		current := requireDocumentSymbol(t, component.Children, name)
		assert.Equal(t, kind, current.Kind, name)
		assert.NotEmpty(t, documentSymbolRangeText(
			t, source, current.SelectionRange,
		), name)
	}
	assert.Contains(
		t,
		requireDocumentSymbol(t, component.Children, "title").Detail,
		"required",
	)
	assert.True(t, requireDocumentSymbol(t, component.Children, "title").Deprecated)
	assert.Contains(
		t, requireDocumentSymbol(t, component.Children, "title").Detail,
		"deprecated",
	)
	assert.Contains(
		t,
		documentSymbolRangeText(
			t,
			source,
			requireDocumentSymbol(t, component.Children, "submit").Range,
		),
		"submit(value)",
	)
	assert.True(t, requireDocumentSymbol(t, component.Children, "submit").Deprecated)
	assert.Contains(
		t, requireDocumentSymbol(t, component.Children, "submit").Detail,
		"deprecated",
	)
}

func TestAdminDocumentSymbolsNestLiveSlotsUnderTwigBlocks(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	path := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/component/sw-card/template.html.twig",
	)
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: filepath.Join(filepath.Dir(path), "index.js"),
		DefinitionPath: filepath.Join(filepath.Dir(path), "index.js"),
		TemplatePath:   path,
	}))
	source := `{# @deprecated Use sw_card_body instead. #}
{% block sw_card_content %}
    <slot
        name="header"
        :title="title"
        v-bind="{ actions, active: enabled }"
    ></slot>
{% endblock %}
<slot></slot>`
	symbols := adminDocumentSymbolsFor(
		t, NewAdminDocumentSymbolProvider(adminIndex), path, source,
	)
	require.Len(t, symbols, 1)
	component := symbols[0]
	assert.Equal(t, "sw-card", component.Name)
	block := requireDocumentSymbol(t, component.Children, "sw_card_content")
	assert.Equal(t, protocol.SymbolMethod, block.Kind)
	assert.True(t, block.Deprecated)
	assert.Contains(t, block.Detail, "deprecated")
	header := requireDocumentSymbol(t, block.Children, "header")
	assert.Equal(t, protocol.SymbolProperty, header.Kind)
	for _, field := range []string{"title", "actions", "active"} {
		assert.Equal(
			t,
			protocol.SymbolField,
			requireDocumentSymbol(t, header.Children, field).Kind,
		)
	}
	assert.Equal(
		t,
		protocol.SymbolProperty,
		requireDocumentSymbol(t, component.Children, "default").Kind,
	)
}

func adminDocumentSymbolsFor(
	t *testing.T,
	provider *AdminDocumentSymbolProvider,
	path,
	source string,
) []protocol.DocumentSymbol {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	symbols, err := provider.GetDocumentSymbols(
		context.Background(),
		&lsp.DocumentSymbolRequest{
			DocumentSymbolParams: &protocol.DocumentSymbolParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	return symbols
}

func requireDocumentSymbol(
	t *testing.T,
	symbols []protocol.DocumentSymbol,
	name string,
) protocol.DocumentSymbol {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol
		}
	}
	require.FailNow(t, "document symbol not found", "name: %s; symbols: %#v", name, symbols)
	return protocol.DocumentSymbol{}
}

func documentSymbolRangeText(
	t *testing.T,
	source string,
	rng protocol.Range,
) string {
	t.Helper()
	document := lsp.NewTextDocument("file:///range.ts", source, 1)
	start := document.LineIndex.OffsetUTF16(
		uint32(rng.Start.Line), uint32(rng.Start.Character),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(rng.End.Line), uint32(rng.End.Character),
	)
	require.LessOrEqual(t, start, end)
	require.LessOrEqual(t, end, uint32(len(source)))
	return source[start:end]
}
