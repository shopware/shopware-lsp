package lsp

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/require"
)

func TestClientCommandFilteringKeepsOnlyUsablePresentations(t *testing.T) {
	server := NewServer(nil, "", "test")
	require.NoError(t, server.configureClientIntegration(
		&protocol.ShopwareClientOptions{
			ProtocolVersion:   ClientProtocolVersion,
			SupportedCommands: []string{"shopware.supported"},
		},
	))

	actions := server.filterCodeActionsForClient([]protocol.CodeAction{
		{Title: "supported", Command: testCommandAction("shopware.supported")},
		{Title: "unsupported", Command: testCommandAction("shopware.unsupported")},
		{Title: "edit", Edit: &protocol.WorkspaceEdit{}, Command: testCommandAction("shopware.unsupported")},
		{Title: "passive"},
	})
	require.Len(t, actions, 3)
	require.Equal(t, "supported", actions[0].Title)
	require.Equal(t, "edit", actions[1].Title)
	require.Nil(t, actions[1].Command)
	require.Equal(t, "passive", actions[2].Title)

	lenses := server.filterCodeLensesForClient([]protocol.CodeLens{
		{Command: testCommand("shopware.supported")},
		{Command: testCommand("shopware.unsupported")},
		{},
	})
	require.Len(t, lenses, 2)

	items := []protocol.CompletionItem{{Command: testCommand("shopware.unsupported")}}
	server.filterCompletionCommandsForClient(items)
	require.Nil(t, items[0].Command)

	hints := []protocol.InlayHint{{
		Label: []protocol.InlayHintLabelPart{{
			Value: "variables", Command: testCommand("shopware.unsupported"),
		}},
	}}
	server.filterInlayHintCommandsForClient(hints)
	parts := hints[0].Label.([]protocol.InlayHintLabelPart)
	require.Nil(t, parts[0].Command)
}

func TestLegacyClientCommandsRemainUnfiltered(t *testing.T) {
	server := NewServer(nil, "", "test")
	actions := server.filterCodeActionsForClient([]protocol.CodeAction{{
		Title: "legacy", Command: testCommandAction("shopware.anything"),
	}})
	require.Len(t, actions, 1)
}

func TestFrameworkProfileKeepsSuppressedInspectionRegistered(t *testing.T) {
	server := NewServer(nil, "", "test")
	require.NoError(t, server.configureClientIntegration(
		&protocol.ShopwareClientOptions{
			ProtocolVersion:     ClientProtocolVersion,
			PresentationProfile: string(PresentationProfileFramework),
		},
	))
	server.RegisterInspection(profileSuppressedInspection{})
	_, registered := server.inspections.inspection("php.semantic")
	require.True(t, registered)
	document := NewTextDocument("file:///fixture.yaml", "value: bad\n", 1)
	require.Empty(t, server.diagnosticsForDocument(context.Background(), document))

	full := NewServer(nil, "", "test")
	full.RegisterInspection(profileSuppressedInspection{})
	require.Len(t, full.diagnosticsForDocument(context.Background(), document), 1)
}

type profileSuppressedInspection struct{}

func (profileSuppressedInspection) Definition() InspectionDefinition {
	return InspectionDefinition{
		ID:        "php.semantic",
		Languages: []language.ID{language.YAML},
		Problems: []ProblemDefinition{{
			ID: "php.parse", Source: "test",
			DefaultSeverity: protocol.DiagnosticSeverityError,
		}},
	}
}

func (profileSuppressedInspection) Inspect(
	_ context.Context,
	document *TextDocument,
	reporter ProblemReporter,
) error {
	rng := cst.TextRange{Start: 0, End: 5}
	return reporter.Report(Problem{
		ID: "php.parse", Message: "test", Range: rng,
		Element: document.SyntaxTree.Root.DescendantForRange(rng),
	})
}

func (profileSuppressedInspection) QuickFixes() []QuickFix { return nil }

func testCommand(id string) *protocol.Command {
	return &protocol.Command{Title: id, Command: id}
}

func testCommandAction(id string) *protocol.CommandAction {
	return &protocol.CommandAction{Title: id, Command: id}
}
