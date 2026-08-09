//go:build integration

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/codelens"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	lspsymbol "github.com/shopware/shopware-lsp/internal/lsp/symbol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func requireRealWorldSuggestionDiagnostic(
	t *testing.T,
	document *lsp.TextDocument,
	diagnostics []lsp.Problem,
	code,
	value,
	suggestion string,
) lsp.Problem {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if fmt.Sprint(diagnostic.ID) != code {
			continue
		}
		data, ok := diagnostic.Payload.(map[string]any)
		require.True(t, ok)
		switch suggestions := data["suggestions"].(type) {
		case []string:
			require.Contains(t, suggestions, suggestion)
		case []any:
			require.Contains(t, suggestions, suggestion)
		default:
			t.Fatalf(
				"diagnostic %q has unsupported suggestions %#v",
				code,
				data["suggestions"],
			)
		}
		start := diagnostic.Range.Start
		end := diagnostic.Range.End
		require.Equal(t, value, document.Source[start:end])
		return diagnostic
	}
	t.Fatalf("diagnostic %q not found in %#v", code, diagnostics)
	return lsp.Problem{}
}

func realWorldSymbolSnapshot(
	t *testing.T,
	ctx context.Context,
	workspace *Workspace,
) map[string][]protocol.SymbolInformation {
	t.Helper()
	provider := lspsymbol.NewSymfonyWorkspaceSymbolProvider(
		nil,
		workspaceRouteIndex(t, workspace),
		workspaceConsoleIndex(t, workspace),
		workspaceTwigIndex(t, workspace),
		workspaceDoctrineIndex(t, workspace),
		workspaceTwigComponentIndex(t, workspace),
		workspaceTranslationIndex(t, workspace),
		workspacePHPIndex(t, workspace),
	)
	queries := map[string]string{
		"frontend.home.page": "Symfony route · GET / · " +
			"NavigationController:home",
		"Shopware\\Storefront\\Controller\\NavigationController::home": "Symfony controller · " +
			"Shopware\\Storefront\\Controller\\NavigationController",
		"system:config:get":                     "Symfony command",
		"@Storefront/storefront/base.html.twig": "Twig template",
		"base_body":                             "Twig block",
		"shopware.installer.header_title":       "Translation",
	}
	result := make(map[string][]protocol.SymbolInformation, len(queries))
	for query, container := range queries {
		symbols, err := provider.WorkspaceSymbols(ctx, query)
		require.NoError(t, err)
		requireWorkspaceSymbol(t, symbols, query, container)
		result[query] = symbols
	}
	return result
}

func realWorldRouteEndpointSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
	workspace *Workspace,
	phpIndex *php.PHPIndex,
) []protocol.CodeLens {
	t.Helper()
	controllerPath := filepath.Join(
		root,
		"src",
		"Storefront",
		"Controller",
		"NavigationController.php",
	)
	lenses := realWorldCodeLenses(
		t,
		ctx,
		codelens.NewRouteEndpointCodeLensProvider(
			workspaceServiceIndex(t, workspace),
			phpIndex,
		),
		controllerPath,
	)
	requireRelatedLensTarget(
		t,
		lenses,
		"GET / · frontend.home.page",
		uriutil.FileURIWithFragment(controllerPath, "56"),
	)
	return lenses
}

func realWorldConsoleCommandLensSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
) []protocol.CodeLens {
	t.Helper()
	commandPath := filepath.Join(
		root,
		"src",
		"Core",
		"System",
		"SystemConfig",
		"Command",
		"ConfigGet.php",
	)
	lenses := realWorldCodeLenses(
		t,
		ctx,
		codelens.NewConsoleCommandCodeLensProvider(root),
		commandPath,
	)
	requireCodeLensCommand(
		t,
		lenses,
		"Run system:config:get",
		"shopware.symfony.runConsoleCommand",
		[]any{
			"system:config:get",
			uriutil.FileURI(commandPath),
		},
	)
	return lenses
}

type relatedNavigationSnapshot struct {
	Controller []protocol.CodeLens
	Template   []protocol.CodeLens
}

func realWorldRelatedNavigationSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
	workspace *Workspace,
	phpIndex *php.PHPIndex,
) relatedNavigationSnapshot {
	t.Helper()
	controllerPath := filepath.Join(
		root,
		"src",
		"Storefront",
		"Controller",
		"NavigationController.php",
	)
	templatePath := filepath.Join(
		root,
		"src",
		"Storefront",
		"Resources",
		"views",
		"storefront",
		"page",
		"content",
		"index.html.twig",
	)
	provider := codelens.NewRelatedNavigationCodeLensProvider(
		workspaceTwigIndex(t, workspace),
		phpIndex,
		workspaceRouteIndex(t, workspace),
		workspaceServiceIndex(t, workspace),
	)
	result := relatedNavigationSnapshot{
		Controller: realWorldCodeLenses(
			t,
			ctx,
			provider,
			controllerPath,
		),
		Template: realWorldCodeLenses(
			t,
			ctx,
			provider,
			templatePath,
		),
	}
	requireRelatedLensTarget(
		t,
		result.Controller,
		"related template",
		uriutil.FileURIWithFragment(templatePath, "1"),
	)
	requireRelatedLensTarget(
		t,
		result.Controller,
		"route definition",
		uriutil.FileURIWithFragment(controllerPath, "49"),
	)
	requireRelatedLensTarget(
		t,
		result.Template,
		"rendering PHP location",
		uriutil.FileURIWithFragment(controllerPath, "62"),
	)
	requireRelatedLensTarget(
		t,
		result.Template,
		"related route",
		uriutil.FileURIWithFragment(controllerPath, "49"),
	)
	return result
}

func realWorldCodeLenses(
	t *testing.T,
	ctx context.Context,
	provider lsp.CodeLensProvider,
	path string,
) []protocol.CodeLens {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path),
		string(source),
		1,
	)
	params := &protocol.CodeLensParams{}
	params.TextDocument.URI = document.URI
	lenses, err := provider.GetCodeLenses(ctx, &lsp.CodeLensRequest{
		CodeLensParams: params,
		Document:       document,
	})
	require.NoError(t, err)
	require.NotEmpty(t, lenses)
	return lenses
}

func realWorldDocumentSymbols(
	t *testing.T,
	ctx context.Context,
	provider lsp.DocumentSymbolProvider,
	path string,
) []protocol.DocumentSymbol {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(uriutil.FileURI(path), string(source), 1)
	symbols, err := provider.GetDocumentSymbols(
		ctx,
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

func realWorldDocumentHighlights(
	t *testing.T,
	ctx context.Context,
	provider lsp.DocumentHighlightProvider,
	path,
	needle string,
) []protocol.DocumentHighlight {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	offset := uint32(strings.Index(document.Source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DocumentHighlightParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	highlights, err := provider.GetDocumentHighlights(
		ctx,
		&lsp.DocumentHighlightRequest{
			DocumentHighlightParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	return highlights
}

func realWorldLinkedEditingRanges(
	t *testing.T,
	ctx context.Context,
	provider lsp.LinkedEditingRangeProvider,
	path,
	needle string,
) *protocol.LinkedEditingRanges {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	offset := uint32(strings.Index(document.Source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.LinkedEditingRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	ranges, err := provider.GetLinkedEditingRanges(
		ctx,
		&lsp.LinkedEditingRangeRequest{
			LinkedEditingRangeParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	if ranges != nil {
		for _, rangeValue := range ranges.Ranges {
			start := document.LineIndex.OffsetUTF16(
				uint32(rangeValue.Start.Line),
				uint32(rangeValue.Start.Character),
			)
			end := document.LineIndex.OffsetUTF16(
				uint32(rangeValue.End.Line),
				uint32(rangeValue.End.Character),
			)
			require.Equal(t, needle, string(document.Text[start:end]))
		}
	}
	return ranges
}

func realWorldFoldingRanges(
	t *testing.T,
	ctx context.Context,
	provider lsp.FoldingRangeProvider,
	path string,
) []protocol.FoldingRange {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	ranges, err := provider.GetFoldingRanges(
		ctx,
		&lsp.FoldingRangeRequest{
			FoldingRangeParams: &protocol.FoldingRangeParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	return ranges
}

func realWorldSelectionTexts(
	t *testing.T,
	ctx context.Context,
	provider lsp.SelectionRangeProvider,
	path,
	needle string,
) []string {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	offset := uint32(strings.Index(document.Source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.SelectionRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Positions: []protocol.Position{{
			Line: int(line), Character: int(character),
		}},
	}
	ranges, err := provider.GetSelectionRanges(
		ctx,
		&lsp.SelectionRangeRequest{
			SelectionRangeParams: params, Document: document,
		},
	)
	require.NoError(t, err)
	require.Len(t, ranges, 1)
	var result []string
	for current := &ranges[0]; current != nil; current = current.Parent {
		start := document.LineIndex.OffsetUTF16(
			uint32(current.Range.Start.Line),
			uint32(current.Range.Start.Character),
		)
		end := document.LineIndex.OffsetUTF16(
			uint32(current.Range.End.Line),
			uint32(current.Range.End.Character),
		)
		result = append(result, string(document.Text[start:end]))
	}
	return result
}

func realWorldDocumentColors(
	t *testing.T,
	ctx context.Context,
	provider lsp.DocumentColorProvider,
	path string,
) ([]protocol.ColorInformation, *lsp.TextDocument) {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(uriutil.FileURI(path), string(source), 1)
	colors, err := provider.GetDocumentColors(
		ctx,
		&lsp.DocumentColorRequest{
			DocumentColorParams: &protocol.DocumentColorParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	return colors, document
}

func realWorldRangeText(document *lsp.TextDocument, value protocol.Range) string {
	start := document.LineIndex.OffsetUTF16(
		uint32(value.Start.Line), uint32(value.Start.Character),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(value.End.Line), uint32(value.End.Character),
	)
	return string(document.Text[start:end])
}

func adminWorkspaceEditCount(edit *protocol.WorkspaceEdit) int {
	if edit == nil {
		return 0
	}
	count := 0
	for _, edits := range edit.Changes {
		count += len(edits)
	}
	return count
}

func applyRealWorldTextEdits(
	t *testing.T,
	source []byte,
	edits []protocol.TextEdit,
) string {
	t.Helper()
	lineIndex := cst.NewLineIndex(string(source))
	result := string(source)
	for _, edit := range edits {
		start := lineIndex.OffsetUTF16(
			uint32(edit.Range.Start.Line), uint32(edit.Range.Start.Character),
		)
		end := lineIndex.OffsetUTF16(
			uint32(edit.Range.End.Line), uint32(edit.Range.End.Character),
		)
		require.LessOrEqual(t, start, end)
		require.LessOrEqual(t, int(end), len(result))
		result = result[:start] + edit.NewText + result[end:]
	}
	return result
}

func requireRealWorldSelectionContaining(
	t *testing.T,
	values []string,
	needle string,
) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, needle) {
			return
		}
	}
	require.Failf(
		t, "missing selection ancestor", "expected %q in %#v", needle, values,
	)
}

func requireRealWorldFoldingRange(
	t *testing.T,
	ranges []protocol.FoldingRange,
	startLine,
	endLine int,
	kind string,
) {
	t.Helper()
	for _, rangeValue := range ranges {
		if rangeValue.StartLine == startLine && rangeValue.EndLine == endLine &&
			rangeValue.Kind == kind {
			return
		}
	}
	require.Failf(
		t,
		"missing real-world folding range",
		"expected %d..%d kind %q in %#v",
		startLine,
		endLine,
		kind,
		ranges,
	)
}

func realWorldCallHierarchyItems(
	t *testing.T,
	ctx context.Context,
	provider lsp.CallHierarchyProvider,
	path,
	needle string,
) []protocol.CallHierarchyItem {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 1,
	)
	offset := uint32(strings.Index(document.Source, needle) + 1)
	require.Greater(t, int(offset), 0)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CallHierarchyPrepareParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	items, err := provider.PrepareCallHierarchy(
		ctx,
		&lsp.CallHierarchyPrepareRequest{
			CallHierarchyPrepareParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	return items
}

func requireDocumentSymbolNamed(
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
	t.Fatalf("document symbol %q not found in %#v", name, symbols)
	return protocol.DocumentSymbol{}
}

func requireDocumentSymbolNamedRecursive(
	t *testing.T,
	symbols []protocol.DocumentSymbol,
	name string,
) protocol.DocumentSymbol {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol
		}
		if child, found := findDocumentSymbolNamed(symbol.Children, name); found {
			return child
		}
	}
	t.Fatalf("document symbol %q not found recursively in %#v", name, symbols)
	return protocol.DocumentSymbol{}
}

func findDocumentSymbolNamed(
	symbols []protocol.DocumentSymbol,
	name string,
) (protocol.DocumentSymbol, bool) {
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol, true
		}
		if child, found := findDocumentSymbolNamed(symbol.Children, name); found {
			return child, true
		}
	}
	return protocol.DocumentSymbol{}, false
}

type controllerUsageSnapshot struct {
	Usages     []symfony.ControllerUsage
	Controller []protocol.CodeLens
	Template   []protocol.CodeLens
}

func realWorldControllerUsageSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
	workspace *Workspace,
	phpIndex *php.PHPIndex,
) controllerUsageSnapshot {
	t.Helper()
	controllerPath := filepath.Join(
		root,
		"src",
		"Core",
		"Profiling",
		"Controller",
		"ProfilerController.php",
	)
	templatePath := filepath.Join(
		root,
		"src",
		"Core",
		"Profiling",
		"Resources",
		"views",
		"Collector",
		"db.html.twig",
	)
	reference, ok := symfony.ParseControllerReference(
		"Shopware\\Core\\Profiling\\Controller\\ProfilerController::explainAction",
	)
	require.True(t, ok)
	usageIndex := workspaceRouteUsageIndex(t, workspace)
	usages, err := usageIndex.GetControllerUsages(reference)
	require.NoError(t, err)
	require.NotEmpty(t, usages)
	requireControllerUsagePath(t, usages, templatePath)
	provider := codelens.NewControllerRelatedCodeLensProvider(
		usageIndex,
		workspaceServiceIndex(t, workspace),
		phpIndex,
	)
	result := controllerUsageSnapshot{
		Usages: usages,
		Controller: realWorldCodeLenses(
			t,
			ctx,
			provider,
			controllerPath,
		),
		Template: realWorldCodeLenses(
			t,
			ctx,
			provider,
			templatePath,
		),
	}
	requireRelatedLensTarget(
		t,
		result.Controller,
		"Twig controller usage",
		uriutil.FileURIWithFragment(templatePath, "80"),
	)
	requireRelatedLensTarget(
		t,
		result.Template,
		"controller method",
		uriutil.FileURIWithFragment(controllerPath, "29"),
	)
	return result
}

func requireControllerUsagePath(
	t *testing.T,
	usages []symfony.ControllerUsage,
	path string,
) {
	t.Helper()
	for _, usage := range usages {
		if filepath.Clean(usage.File) == filepath.Clean(path) {
			return
		}
	}
	t.Fatalf("controller usage %q not found in %#v", path, usages)
}

type serviceNavigationSnapshot struct {
	Decorators []protocol.CodeLens
	Target     []protocol.CodeLens
}

func realWorldServiceNavigationSnapshot(
	t *testing.T,
	ctx context.Context,
	root string,
	workspace *Workspace,
	phpIndex *php.PHPIndex,
) serviceNavigationSnapshot {
	t.Helper()
	decoratorPath := filepath.Join(
		root,
		"src",
		"Elasticsearch",
		"DependencyInjection",
		"services.php",
	)
	targetPath := filepath.Join(
		root,
		"src",
		"Core",
		"Framework",
		"DependencyInjection",
		"data-abstraction-layer.php",
	)
	provider := codelens.NewServiceRelatedCodeLensProvider(
		workspaceServiceIndex(t, workspace),
		phpIndex,
	)
	result := serviceNavigationSnapshot{
		Decorators: realWorldCodeLenses(
			t,
			ctx,
			provider,
			decoratorPath,
		),
		Target: realWorldCodeLenses(
			t,
			ctx,
			provider,
			targetPath,
		),
	}
	requireRelatedLensFileTargets(
		t,
		result.Decorators,
		"decorated service",
		uriutil.FileURI(targetPath),
		1,
	)
	requireRelatedLensFileTargets(
		t,
		result.Target,
		"decorating services",
		uriutil.FileURI(decoratorPath),
		2,
	)
	return result
}

func requireRelatedLensFileTargets(
	t *testing.T,
	lenses []protocol.CodeLens,
	title,
	fileURI string,
	minimum int,
) {
	t.Helper()

	targets := make(map[string]struct{})
	for _, lens := range lenses {
		if lens.Command == nil ||
			!strings.Contains(lens.Command.Title, title) ||
			len(lens.Command.Arguments) != 1 {
			continue
		}
		currentTargets, ok := lens.Command.Arguments[0].([]string)
		if !ok {
			continue
		}
		for _, current := range currentTargets {
			if current == fileURI || strings.HasPrefix(current, fileURI+"#") {
				targets[current] = struct{}{}
			}
		}
	}
	require.GreaterOrEqual(
		t,
		len(targets),
		minimum,
		"related code lens %q targets in %q",
		title,
		fileURI,
	)
}

func requireRelatedLensTarget(
	t *testing.T,
	lenses []protocol.CodeLens,
	title,
	target string,
) {
	t.Helper()
	for _, lens := range lenses {
		if lens.Command == nil ||
			!strings.Contains(lens.Command.Title, title) ||
			len(lens.Command.Arguments) != 1 {
			continue
		}
		targets, ok := lens.Command.Arguments[0].([]string)
		if !ok {
			continue
		}
		for _, current := range targets {
			if current == target {
				return
			}
		}
	}
	t.Fatalf(
		"related code lens %q target %q not found in %#v",
		title,
		target,
		lenses,
	)
}
