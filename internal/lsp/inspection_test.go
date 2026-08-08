package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

const testInspectionFixID FixID = "replace-bad"

type testInspection struct{}

func (testInspection) Definition() InspectionDefinition {
	return InspectionDefinition{
		ID:        "test.invalid-value",
		Languages: []language.ID{language.YAML},
		Problems: []ProblemDefinition{{
			ID:              "test.invalid-value",
			Source:          "test",
			DefaultSeverity: protocol.DiagnosticSeverityWarning,
		}},
	}
}

func (testInspection) Inspect(
	_ context.Context,
	document *TextDocument,
	reporter ProblemReporter,
) error {
	rng := cst.TextRange{Start: 7, End: 10}
	return reporter.Report(Problem{
		ID:      "test.invalid-value",
		Range:   rng,
		Element: document.SyntaxTree.Root.DescendantForRange(rng),
		Message: "Invalid value",
		Payload: map[string]string{"replacement": "good"},
		Fixes:   []BoundFix{BindFix(testInspectionFixID, map[string]string{"replacement": "good"})},
	})
}

func (testInspection) QuickFixes() []QuickFix { return []QuickFix{testInspectionFix{}} }

type defaultDisabledInspection struct{}

func (defaultDisabledInspection) Definition() InspectionDefinition {
	definition := testInspection{}.Definition()
	definition.ID = "test.default-disabled"
	definition.Problems[0].ID = "test.default-disabled"
	definition.Problems[0].DisabledByDefault = true
	return definition
}

func (defaultDisabledInspection) Inspect(
	_ context.Context,
	document *TextDocument,
	reporter ProblemReporter,
) error {
	rng := cst.TextRange{Start: 7, End: 10}
	return reporter.Report(Problem{
		ID: "test.default-disabled", Range: rng,
		Element: document.SyntaxTree.Root.DescendantForRange(rng),
		Message: "Opt-in diagnostic",
	})
}

func (defaultDisabledInspection) QuickFixes() []QuickFix { return nil }

type testInspectionFix struct{}

func (testInspectionFix) ID() FixID { return testInspectionFixID }

func (testInspectionFix) Present(
	_ context.Context,
	fixContext FixContext,
) (FixPresentation, bool, error) {
	payload, err := DecodeBoundFixPayload[map[string]string](fixContext)
	return FixPresentation{
		Title:      fmt.Sprintf("Replace with %s", payload["replacement"]),
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: FixLazy,
	}, payload["replacement"] != "", err
}

type multipleFixInspection struct{ testInspection }

func (multipleFixInspection) Inspect(
	_ context.Context,
	document *TextDocument,
	reporter ProblemReporter,
) error {
	rng := cst.TextRange{Start: 7, End: 10}
	return reporter.Report(Problem{
		ID:      "test.invalid-value",
		Range:   rng,
		Element: document.SyntaxTree.Root.DescendantForRange(rng),
		Message: "Invalid value",
		Fixes: []BoundFix{
			BindFix(testInspectionFixID, map[string]string{"replacement": "good"}),
			BindFix(testInspectionFixID, map[string]string{"replacement": "better"}),
		},
	})
}

func (testInspectionFix) Build(
	_ context.Context,
	fixContext FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := DecodeBoundFixPayload[map[string]string](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	rng := textRangeFromProtocol(fixContext.Document.LineIndex, fixContext.Diagnostic.Range)
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.ReplaceRange(rng, payload["replacement"]); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	version := fixContext.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(fixContext.Document.URI, &version, fixContext.Document.Source, edits),
	}}, nil
}

func TestInspectionBindsAndLazilyResolvesExactQuickFix(t *testing.T) {
	root := t.TempDir()
	uri := uriutil.FileURI(root + "/test.yaml")
	server := NewServer(nil, root, "test")
	server.codeActionResolveSupport = true
	server.RegisterInspection(testInspection{})
	server.documentManager.OpenDocument(uri, "value: bad\n", 3)
	document, found := server.documentManager.GetDocument(uri)
	require.True(t, found)
	diagnostics := server.collectDiagnostics(context.Background(), document)
	require.Len(t, diagnostics, 1)
	data, ok := diagnostics[0].Data.(map[string]any)
	require.True(t, ok)
	require.NotContains(t, data, "replacement")
	require.Contains(t, data, diagnosticEnvelopeKey)
	envelope, err := decodeDiagnosticEnvelope(data)
	require.NoError(t, err)
	var payload map[string]string
	require.NoError(t, json.Unmarshal(envelope.Payload, &payload))
	require.Equal(t, "good", payload["replacement"])

	params := &protocol.CodeActionParams{
		Range: diagnostics[0].Range,
		Context: protocol.CodeActionContext{
			Diagnostics: diagnostics,
			Only:        []string{"quickfix"},
		},
	}
	params.TextDocument.URI = uri
	actions := server.codeAction(context.Background(), params)
	require.Len(t, actions, 1)
	require.Nil(t, actions[0].Edit)
	require.True(t, actions[0].IsPreferred)

	resolved := server.resolveCodeAction(context.Background(), actions[0])
	require.Nil(t, resolved.Disabled)
	require.NotNil(t, resolved.Edit)
	require.Len(t, resolved.Edit.DocumentChanges, 1)
	require.Equal(t, 3, *resolved.Edit.DocumentChanges[0].TextDocument.Version)
	require.Equal(t, "good", resolved.Edit.DocumentChanges[0].Edits[0].NewText)
}

func TestWorkspacePlanDeleteRequiresCurrentInWorkspaceSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "services.xml")
	uri := uriutil.FileURI(path)
	require.NoError(t, os.WriteFile(path, []byte("<container/>"), 0o644))

	server := NewServer(nil, root, "test")
	server.documentManager.OpenDocument(uri, "<container/>", 4)
	version := 4
	plan := rewrite.WorkspacePlan{Deletes: []rewrite.DeleteFilePlan{{
		URI: uri, Version: &version, Source: "<container/>",
	}}}
	require.NoError(t, server.validateWorkspacePlan(context.Background(), plan))

	staleSource := plan
	staleSource.Deletes = append([]rewrite.DeleteFilePlan(nil), plan.Deletes...)
	staleSource.Deletes[0].Source = "<changed/>"
	require.ErrorIs(
		t, server.validateWorkspacePlan(context.Background(), staleSource),
		rewrite.ErrStaleHandle,
	)

	outsidePath := filepath.Join(t.TempDir(), "services.xml")
	require.NoError(t, os.WriteFile(outsidePath, []byte("<container/>"), 0o644))
	outside := rewrite.WorkspacePlan{Deletes: []rewrite.DeleteFilePlan{{
		URI: uriutil.FileURI(outsidePath), Source: "<container/>",
	}}}
	require.Error(t, server.validateWorkspacePlan(context.Background(), outside))
}

func TestInspectionRuleCanBeDisabledByDefaultAndExplicitlyEnabled(t *testing.T) {
	root := t.TempDir()
	uri := uriutil.FileURI(root + "/test.yaml")
	server := NewServer(nil, root, "test")
	server.RegisterInspection(defaultDisabledInspection{})
	server.documentManager.OpenDocument(uri, "value: bad\n", 1)
	document, found := server.documentManager.GetDocument(uri)
	require.True(t, found)
	require.Empty(t, server.collectDiagnostics(context.Background(), document))

	result := server.replaceEditorConfiguration(context.Background(), projectconfig.Partial{
		Diagnostics: &projectconfig.DiagnosticsConfig{Rules: map[string]projectconfig.Severity{
			"test.default-disabled": projectconfig.SeverityWarning,
		}},
	})
	require.True(t, result.Applied)
	require.Len(t, server.collectDiagnostics(context.Background(), document), 1)

	catalog := server.configurationCatalog()
	for _, inspection := range catalog.Inspections {
		if inspection.ID == "test.default-disabled" {
			require.Len(t, inspection.Rules, 1)
			require.False(t, inspection.Rules[0].DefaultEnabled)
			return
		}
	}
	t.Fatal("default-disabled inspection missing from configuration catalog")
}

func TestInspectionResolveDisablesStaleAction(t *testing.T) {
	root := t.TempDir()
	uri := uriutil.FileURI(root + "/test.yaml")
	server := NewServer(nil, root, "test")
	server.codeActionResolveSupport = true
	server.RegisterInspection(testInspection{})
	server.documentManager.OpenDocument(uri, "value: bad\n", 3)
	document, _ := server.documentManager.GetDocument(uri)
	diagnostics := server.collectDiagnostics(context.Background(), document)
	params := &protocol.CodeActionParams{
		Range:   diagnostics[0].Range,
		Context: protocol.CodeActionContext{Diagnostics: diagnostics},
	}
	params.TextDocument.URI = uri
	action := server.codeAction(context.Background(), params)[0]
	server.documentManager.UpdateDocument(uri, "value: changed\n", 4)
	resolved := server.resolveCodeAction(context.Background(), action)
	require.NotNil(t, resolved.Disabled)
	require.Contains(t, resolved.Disabled.Reason, "document changed")
	require.Nil(t, resolved.Edit)
}

func TestInspectionPreservesMultipleBindingsForTheSameQuickFix(t *testing.T) {
	root := t.TempDir()
	uri := uriutil.FileURI(root + "/test.yaml")
	server := NewServer(nil, root, "test")
	server.RegisterInspection(multipleFixInspection{})
	server.documentManager.OpenDocument(uri, "value: bad\n", 3)
	document, found := server.documentManager.GetDocument(uri)
	require.True(t, found)
	diagnostics := server.collectDiagnostics(context.Background(), document)
	require.Len(t, diagnostics, 1)

	params := &protocol.CodeActionParams{
		Range: diagnostics[0].Range,
		Context: protocol.CodeActionContext{
			Diagnostics: diagnostics,
			Only:        []string{"quickfix"},
		},
	}
	params.TextDocument.URI = uri
	actions := server.codeAction(context.Background(), params)
	require.Len(t, actions, 2)
	require.Equal(t, "Replace with good", actions[0].Title)
	require.Equal(t, "Replace with better", actions[1].Title)
	require.Equal(t, "good", actions[0].Edit.DocumentChanges[0].Edits[0].NewText)
	require.Equal(t, "better", actions[1].Edit.DocumentChanges[0].Edits[0].NewText)
}

func TestInspectionCodeActionHonorsContextOnly(t *testing.T) {
	root := t.TempDir()
	uri := uriutil.FileURI(root + "/test.yaml")
	server := NewServer(nil, root, "test")
	server.RegisterInspection(testInspection{})
	server.documentManager.OpenDocument(uri, "value: bad\n", 3)
	document, _ := server.documentManager.GetDocument(uri)
	diagnostics := server.collectDiagnostics(context.Background(), document)
	params := &protocol.CodeActionParams{
		Range: diagnostics[0].Range,
		Context: protocol.CodeActionContext{
			Diagnostics: diagnostics,
			Only:        []string{"refactor"},
		},
	}
	params.TextDocument.URI = uri
	require.Empty(t, server.codeAction(context.Background(), params))
}
