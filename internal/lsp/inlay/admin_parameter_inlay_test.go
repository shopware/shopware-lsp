package inlay

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminParameterHintsReuseComponentSignaturesInJavaScriptAndTwig(
	t *testing.T,
) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-card/index.ts")
	templatePath := filepath.Join(
		filepath.Dir(definitionPath), "sw-card.html.twig",
	)
	definitionSource := `import template from './sw-card.html.twig';
Component.register('sw-card', {
    template,
    methods: {
        save(id: string, options?: { force: boolean }): Promise<void> {
            return Promise.resolve();
        },
        submit(id: string, options: { force: boolean }) {
            this.save(id, options);
            this.save(this.product.id, { force: true });
        },
    },
});`
	templateSource := `<button @click="save(product.id, { force: true }); $emit('saved', product.id); value | default('fallback')">Save</button>`
	for path, source := range map[string]string{
		definitionPath: definitionSource,
		templatePath:   templateSource,
	} {
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path, []byte(source),
		)))
	}
	provider := NewAdminParameterProvider(idx)

	javaScriptHints, err := provider.GetInlayHints(
		context.Background(), adminParameterHintRequest(
			definitionPath, definitionSource,
		),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"id:", "options:"}, adminHintLabels(
		javaScriptHints,
	))
	assert.Contains(t, javaScriptHints[0].Tooltip, "save(id: string")
	assert.True(t, javaScriptHints[0].PaddingRight)

	twigHints, err := provider.GetInlayHints(
		context.Background(), adminParameterHintRequest(
			templatePath, templateSource,
		),
	)
	require.NoError(t, err)
	require.Equal(t, []string{
		"id:", "options:", "event:", "args:",
	}, adminHintLabels(twigHints))
	for _, hint := range twigHints {
		assert.Equal(t, protocol.InlayHintKindParameter, hint.Kind)
	}

	rangeRequest := adminParameterHintRequest(templatePath, templateSource)
	rangeStart := uint32(strings.Index(templateSource, "{ force"))
	rangeEnd := rangeStart + uint32(len("{ force: true }"))
	startLine, startCharacter := rangeRequest.Document.LineIndex.PositionUTF16(
		rangeStart,
	)
	endLine, endCharacter := rangeRequest.Document.LineIndex.PositionUTF16(
		rangeEnd,
	)
	rangeRequest.Range = protocol.Range{
		Start: protocol.Position{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: protocol.Position{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
	rangedHints, err := provider.GetInlayHints(
		context.Background(), rangeRequest,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"options:"}, adminHintLabels(rangedHints))
}

func TestAdminParameterHintsReusePiniaActionSignatures(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	path := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/store/session.ts",
	)
	source := `Shopware.Store.register('session', {
    actions: {
        login(user: string, remember?: boolean): Promise<boolean> {
            return Promise.resolve(true);
        },
    },
});
Shopware.Store.get('session').login('admin', true);`
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source))))

	hints, err := NewAdminParameterProvider(idx).GetInlayHints(
		context.Background(), adminParameterHintRequest(path, source),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"user:", "remember:"}, adminHintLabels(hints))
}

func TestAdminParameterHintSkipsAmbiguousOverloadNames(t *testing.T) {
	help := &protocol.SignatureHelp{Signatures: []protocol.SignatureInformation{
		{Parameters: []protocol.ParameterInformation{{Label: "value: string"}}},
		{Parameters: []protocol.ParameterInformation{{Label: "item: number"}}},
	}}
	_, _, found := adminParameterHint(help, 0)
	assert.False(t, found)
}

func adminParameterHintRequest(
	path,
	source string,
) *lsp.InlayHintRequest {
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	params := &protocol.InlayHintParams{}
	params.TextDocument.URI = document.URI
	line, character := document.LineIndex.PositionUTF16(uint32(len(source)))
	params.Range.End = protocol.Position{
		Line: int(line), Character: int(character),
	}
	return &lsp.InlayHintRequest{
		InlayHintParams: params,
		Document:        document,
	}
}

func adminHintLabels(hints []protocol.InlayHint) []string {
	result := make([]string, 0, len(hints))
	for _, hint := range hints {
		label, _ := hint.Label.(string)
		result = append(result, label)
	}
	return result
}
