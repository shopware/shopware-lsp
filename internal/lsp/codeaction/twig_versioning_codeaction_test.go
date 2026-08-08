package codeaction

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigVersioningContextActionAddsCommentWithoutDiagnostic(t *testing.T) {
	root, service, currentPath := twigVersioningActionFixture(t)
	_ = root
	source := `{% sw_extends '@Storefront/storefront/page/example.html.twig' %}
{% block content %}local{% endblock %}`
	request := twigVersioningActionRequest(currentPath, source, "content", nil)
	actions := NewTwigCodeActionProvider(service).GetCodeActions(
		context.Background(),
		request,
	)
	action := actionByTitle(actions, "Shopware: Add Twig block version comment")
	require.NotNil(t, action)
	require.NotNil(t, action.Edit)
	require.Len(t, action.Edit.Changes[request.TextDocument.URI], 1)
	assert.Contains(
		t,
		action.Edit.Changes[request.TextDocument.URI][0].NewText,
		"shopware-block:",
	)
}

func TestTwigVersioningContextActionsDoNotDuplicateDiagnosticFixes(t *testing.T) {
	_, service, currentPath := twigVersioningActionFixture(t)
	source := `{% sw_extends '@Storefront/storefront/page/example.html.twig' %}
{# shopware-block: deadbeef@6.6.0.0 #}
{% block content %}local{% endblock %}`
	diagnostic := protocol.Diagnostic{Code: "twig.versioning.outdated"}
	request := twigVersioningActionRequest(
		currentPath,
		source,
		"content",
		[]protocol.Diagnostic{diagnostic},
	)
	actions := NewTwigCodeActionProvider(service).GetCodeActions(
		context.Background(),
		request,
	)
	assert.Nil(t, actionByTitle(actions, "Shopware: Update Twig block version comment"))
	assert.Nil(t, actionByTitle(actions, "Shopware: Show Twig block difference"))
}

func TestTwigVersioningDiffIsNotOfferedForThirdPartyUpstream(t *testing.T) {
	root := t.TempDir()
	idx, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	upstreamPath := filepath.Join(
		root, "custom", "plugins", "Theme", "src", "Resources", "views",
		"storefront", "card.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		upstreamPath,
		[]byte(`{% block card %}upstream{% endblock %}`),
	)))
	currentPath := filepath.Join(
		root, "custom", "plugins", "Plugin", "src", "Resources", "views",
		"storefront", "card.html.twig",
	)
	source := `{% sw_extends '@Theme/storefront/card.html.twig' %}
{# shopware-block: deadbeef@1.0.0 #}
{% block card %}local{% endblock %}`
	actions := NewTwigCodeActionProvider(
		twig.NewVersioningService(root, idx, ""),
	).GetCodeActions(
		context.Background(),
		twigVersioningActionRequest(currentPath, source, "card", nil),
	)
	assert.NotNil(t, actionByTitle(actions, "Shopware: Update Twig block version comment"))
	assert.Nil(t, actionByTitle(actions, "Shopware: Show Twig block difference"))
}

func twigVersioningActionFixture(
	t *testing.T,
) (string, *twig.VersioningService, string) {
	t.Helper()
	root := t.TempDir()
	idx, err := twig.NewTwigIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	upstreamPath := filepath.Join(
		root, "src", "Storefront", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		upstreamPath,
		[]byte(`{% block content %}upstream{% endblock %}`),
	)))
	currentPath := filepath.Join(
		root, "custom", "plugins", "Example", "src", "Resources", "views",
		"storefront", "page", "example.html.twig",
	)
	return root, twig.NewVersioningService(root, idx, "6.7.2"), currentPath
}

func twigVersioningActionRequest(
	path,
	source,
	blockName string,
	diagnostics []protocol.Diagnostic,
) *lsp.CodeActionRequest {
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := strings.LastIndex(source, "{% block "+blockName) + len("{% block ") + 1
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CodeActionParams{
		Range: protocol.Range{
			Start: protocol.Position{Line: int(line), Character: int(character)},
			End:   protocol.Position{Line: int(line), Character: int(character)},
		},
		Context: protocol.CodeActionContext{Diagnostics: diagnostics},
	}
	params.TextDocument.URI = document.URI
	return &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root:  document.SyntaxTree.Root,
			Token: document.SyntaxTree.Root.TokenAtOffset(uint32(offset)),
			Node:  document.SyntaxTree.Root.NodeAtOffset(uint32(offset)),
		},
	}
}

func actionByTitle(
	actions []protocol.CodeAction,
	title string,
) *protocol.CodeAction {
	for index := range actions {
		if actions[index].Title == title {
			return &actions[index]
		}
	}
	return nil
}
