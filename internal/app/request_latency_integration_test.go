//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

const realWorldRequestSamples = 21

type realWorldRequestLatency struct {
	Median time.Duration
	P95    time.Duration
	Max    time.Duration
}

func measureRealWorldLSPRequestLatency(
	t *testing.T,
	ctx context.Context,
	server *lsp.Server,
	root string,
) {
	t.Helper()
	require.NotNil(t, server)
	serverSide, clientSide := net.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Start(serverSide, serverSide)
	}()

	published := make(chan string, 16)
	client := jsonrpc2.NewConn(
		ctx,
		jsonrpc2.NewBufferedStream(
			clientSide, jsonrpc2.VSCodeObjectCodec{},
		),
		jsonrpc2.HandlerWithError(func(
			_ context.Context,
			_ *jsonrpc2.Conn,
			request *jsonrpc2.Request,
		) (interface{}, error) {
			if request.Method != "textDocument/publishDiagnostics" ||
				request.Params == nil {
				return nil, nil
			}
			var params struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal(*request.Params, &params); err == nil {
				select {
				case published <- params.URI:
				default:
				}
			}
			return nil, nil
		}).SuppressErrClosed(),
	)
	t.Cleanup(func() {
		_ = client.Close()
		_ = clientSide.Close()
		_ = serverSide.Close()
		select {
		case err := <-serverDone:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("real-world LSP latency server did not stop")
		}
	})

	initialize := map[string]interface{}{
		"processId": os.Getpid(),
		"rootUri":   uriutil.FileURI(root),
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"codeAction": map[string]interface{}{
					"dataSupport": true,
					"resolveSupport": map[string]interface{}{
						"properties": []string{"edit"},
					},
				},
			},
		},
	}
	var initializeResult interface{}
	require.NoError(
		t,
		client.Call(ctx, "initialize", initialize, &initializeResult),
	)
	require.NoError(t, client.Notify(ctx, "initialized", struct{}{}))

	phpDocument := openRealWorldLatencyDocument(
		t, ctx, client, filepath.Join(
			root,
			"src/Core/Framework/Demodata/DemodataContext.php",
		),
	)
	adminTemplate := openRealWorldLatencyDocument(
		t, ctx, client, filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/app/component/base/sw-card/sw-card.html.twig",
		),
	)
	adminDefinition := openRealWorldLatencyDocument(
		t, ctx, client, filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/app/component/base/sw-card/index.ts",
		),
	)
	linkedTemplate := openRealWorldLatencyDocument(
		t, ctx, client, filepath.Join(
			root,
			"src/Administration/Resources/views/administration/index.html.twig",
		),
	)
	diagnosticTemplate := openRealWorldLatencyDocument(
		t, ctx, client, filepath.Join(
			root,
			"src/Administration/Resources/app/administration/src/module/sw-settings-number-range/page/sw-settings-number-range-detail/sw-settings-number-range-detail.html.twig",
		),
	)
	waitForRealWorldDiagnostics(
		t, published,
		[]string{
			phpDocument.URI,
			adminTemplate.URI,
			adminDefinition.URI,
			linkedTemplate.URI,
			diagnosticTemplate.URI,
		},
	)

	phpPosition := realWorldLatencyPosition(
		t, phpDocument, "DemodataContext", 3,
	)
	adminPosition := realWorldLatencyPosition(
		t, adminTemplate, "getSlots", 3,
	)

	phpHoverParams := &protocol.HoverParams{}
	phpHoverParams.TextDocument.URI = phpDocument.URI
	phpHoverParams.Position = phpPosition
	phpHover, phpHoverLatency := measureRealWorldRequest[*protocol.Hover](
		t, ctx, client, "hover/php", "textDocument/hover",
		phpHoverParams, 25*time.Millisecond,
	)
	require.NotNil(t, phpHover)

	adminHoverParams := &protocol.HoverParams{}
	adminHoverParams.TextDocument.URI = adminTemplate.URI
	adminHoverParams.Position = adminPosition
	adminHover, adminHoverLatency := measureRealWorldRequest[*protocol.Hover](
		t, ctx, client, "hover/admin-twig", "textDocument/hover",
		adminHoverParams, 25*time.Millisecond,
	)
	require.NotNil(t, adminHover)

	phpActionsParams := realWorldLatencyCodeActionParams(
		phpDocument.URI, phpPosition, nil, nil,
	)
	_, phpActionsLatency := measureRealWorldRequest[[]protocol.CodeAction](
		t, ctx, client, "codeAction/php/all", "textDocument/codeAction",
		phpActionsParams, 50*time.Millisecond,
	)
	phpQuickFixParams := realWorldLatencyCodeActionParams(
		phpDocument.URI,
		phpPosition,
		nil,
		[]string{string(protocol.CodeActionQuickFix)},
	)
	_, phpQuickFixLatency := measureRealWorldRequest[[]protocol.CodeAction](
		t, ctx, client, "codeAction/php/quickfix",
		"textDocument/codeAction", phpQuickFixParams, 10*time.Millisecond,
	)
	adminActionsParams := realWorldLatencyCodeActionParams(
		adminTemplate.URI, adminPosition, nil, nil,
	)
	adminActions, adminActionsLatency := measureRealWorldRequest[[]protocol.CodeAction](
		t, ctx, client, "codeAction/admin-twig/all",
		"textDocument/codeAction", adminActionsParams, 50*time.Millisecond,
	)
	require.NotEmpty(t, adminActions)

	diagnosticParams := &protocol.DiagnosticParams{}
	diagnosticParams.TextDocument.URI = diagnosticTemplate.URI
	diagnosticResult, diagnosticsLatency := measureRealWorldRequest[protocol.DiagnosticResult](
		t, ctx, client, "diagnostic/admin-twig", "textDocument/diagnostic",
		diagnosticParams, 10*time.Millisecond,
	)
	var unknownEvent protocol.Diagnostic
	for _, diagnostic := range diagnosticResult.Items {
		if fmt.Sprint(diagnostic.Code) == "admin.component.unknown-event" {
			unknownEvent = diagnostic
			break
		}
	}
	require.Equal(
		t, "admin.component.unknown-event", fmt.Sprint(unknownEvent.Code),
	)
	diagnosticActionsParams := realWorldLatencyCodeActionParams(
		diagnosticTemplate.URI,
		unknownEvent.Range.Start,
		[]protocol.Diagnostic{unknownEvent},
		[]string{string(protocol.CodeActionQuickFix)},
	)
	diagnosticActions, diagnosticActionsLatency := measureRealWorldRequest[[]protocol.CodeAction](
		t, ctx, client, "codeAction/admin-diagnostic",
		"textDocument/codeAction", diagnosticActionsParams, 25*time.Millisecond,
	)
	require.NotEmpty(t, diagnosticActions)
	resolvedAction, resolveLatency := measureRealWorldRequest[protocol.CodeAction](
		t, ctx, client, "codeAction/resolve", "codeAction/resolve",
		diagnosticActions[0], 25*time.Millisecond,
	)
	require.NotNil(t, resolvedAction.Edit)

	documentLinkParams := &protocol.DocumentLinkParams{}
	documentLinkParams.TextDocument.URI = linkedTemplate.URI
	links, documentLinkLatency := measureRealWorldRequest[[]protocol.DocumentLink](
		t, ctx, client, "documentLink/twig", "textDocument/documentLink",
		documentLinkParams, 25*time.Millisecond,
	)
	require.NotEmpty(t, links)

	definitionSymbolParams := &protocol.DocumentSymbolParams{}
	definitionSymbolParams.TextDocument.URI = adminDefinition.URI
	definitionSymbols, definitionSymbolLatency := measureRealWorldRequest[[]protocol.DocumentSymbol](
		t, ctx, client, "documentSymbol/admin-ts",
		"textDocument/documentSymbol", definitionSymbolParams,
		25*time.Millisecond,
	)
	require.NotEmpty(t, definitionSymbols)
	templateSymbolParams := &protocol.DocumentSymbolParams{}
	templateSymbolParams.TextDocument.URI = adminTemplate.URI
	templateSymbols, templateSymbolLatency := measureRealWorldRequest[[]protocol.DocumentSymbol](
		t, ctx, client, "documentSymbol/admin-twig",
		"textDocument/documentSymbol", templateSymbolParams,
		25*time.Millisecond,
	)
	require.NotEmpty(t, templateSymbols)

	t.Logf(
		"interactive LSP p50/p95 (JSON-RPC): hover_php=%s/%s, hover_admin=%s/%s, code_action_php=%s/%s, code_action_php_quickfix=%s/%s, code_action_admin=%s/%s, diagnostics_admin=%s/%s, code_action_diagnostic=%s/%s, code_action_resolve=%s/%s, document_link=%s/%s, document_symbol_ts=%s/%s, document_symbol_twig=%s/%s",
		phpHoverLatency.Median, phpHoverLatency.P95,
		adminHoverLatency.Median, adminHoverLatency.P95,
		phpActionsLatency.Median, phpActionsLatency.P95,
		phpQuickFixLatency.Median, phpQuickFixLatency.P95,
		adminActionsLatency.Median, adminActionsLatency.P95,
		diagnosticsLatency.Median, diagnosticsLatency.P95,
		diagnosticActionsLatency.Median, diagnosticActionsLatency.P95,
		resolveLatency.Median, resolveLatency.P95,
		documentLinkLatency.Median, documentLinkLatency.P95,
		definitionSymbolLatency.Median, definitionSymbolLatency.P95,
		templateSymbolLatency.Median, templateSymbolLatency.P95,
	)

	require.NoError(t, client.Call(ctx, "shutdown", struct{}{}, nil))
	require.NoError(t, client.Notify(ctx, "exit", struct{}{}))
}

func openRealWorldLatencyDocument(
	t *testing.T,
	ctx context.Context,
	client *jsonrpc2.Conn,
	path string,
) *lsp.TextDocument {
	t.Helper()
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	document := lsp.NewTextDocument(uriutil.FileURI(path), string(source), 1)
	require.NotNil(t, document.SyntaxTree)
	require.NoError(t, client.Notify(
		ctx,
		"textDocument/didOpen",
		map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri":     document.URI,
				"version": document.Version,
				"languageId": strings.TrimPrefix(
					strings.ToLower(filepath.Ext(path)), ".",
				),
				"text": document.Source,
			},
		},
	))
	return document
}

func waitForRealWorldDiagnostics(
	t *testing.T,
	published <-chan string,
	uris []string,
) {
	t.Helper()
	pending := make(map[string]bool, len(uris))
	for _, uri := range uris {
		pending[uri] = true
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for len(pending) > 0 {
		select {
		case uri := <-published:
			delete(pending, uri)
		case <-timer.C:
			t.Fatalf("timed out waiting for diagnostics for %v", pending)
		}
	}
}

func realWorldLatencyPosition(
	t *testing.T,
	document *lsp.TextDocument,
	needle string,
	delta int,
) protocol.Position {
	t.Helper()
	offset := strings.Index(document.Source, needle)
	require.NotEqual(t, -1, offset)
	offset += delta
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	return protocol.Position{Line: int(line), Character: int(character)}
}

func realWorldLatencyCodeActionParams(
	uri string,
	position protocol.Position,
	diagnostics []protocol.Diagnostic,
	only []string,
) *protocol.CodeActionParams {
	params := &protocol.CodeActionParams{
		Range: protocol.Range{Start: position, End: position},
		Context: protocol.CodeActionContext{
			Diagnostics: diagnostics,
			Only:        only,
		},
	}
	params.TextDocument.URI = uri
	return params
}

func measureRealWorldRequest[T any](
	t *testing.T,
	ctx context.Context,
	client *jsonrpc2.Conn,
	name string,
	method string,
	params interface{},
	p95Budget time.Duration,
) (T, realWorldRequestLatency) {
	t.Helper()
	samples := realWorldLatencySampleCount()
	durations := make([]time.Duration, 0, samples-1)
	var latest T
	for sample := 0; sample < samples; sample++ {
		var result T
		started := time.Now()
		err := client.Call(ctx, method, params, &result)
		elapsed := time.Since(started)
		require.NoError(t, err, "%s sample %d", name, sample)
		latest = result
		if sample > 0 {
			durations = append(durations, elapsed)
		}
	}
	sort.Slice(durations, func(left, right int) bool {
		return durations[left] < durations[right]
	})
	latency := realWorldRequestLatency{
		Median: durations[len(durations)/2],
		P95:    durations[int(math.Ceil(float64(len(durations))*0.95))-1],
		Max:    durations[len(durations)-1],
	}
	require.LessOrEqual(
		t,
		latency.P95,
		p95Budget,
		"%s p95 latency exceeded budget (median=%s max=%s)",
		name,
		latency.Median,
		latency.Max,
	)
	return latest, latency
}

func realWorldLatencySampleCount() int {
	value, err := strconv.Atoi(strings.TrimSpace(
		os.Getenv("SHOPWARE_LSP_LATENCY_SAMPLES"),
	))
	if err != nil || value < 2 {
		return realWorldRequestSamples
	}
	return value
}
