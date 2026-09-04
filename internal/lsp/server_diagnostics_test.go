package lsp

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

type lifecycleInspection struct {
	testInspection
	versionTwoStarted chan struct{}
	startedOnce       sync.Once
}

type diagnosticsBatchIndexer struct {
	ready         atomic.Bool
	indexStarted  chan struct{}
	releaseIndex  chan struct{}
	finishStarted chan struct{}
	releaseFinish chan struct{}
	indexOnce     sync.Once
	finishOnce    sync.Once
}

func (index *diagnosticsBatchIndexer) ID() string { return "diagnostics-batch" }

func (index *diagnosticsBatchIndexer) Index(*indexer.ParsedFile) error {
	index.indexOnce.Do(func() { close(index.indexStarted) })
	<-index.releaseIndex
	return nil
}

func (*diagnosticsBatchIndexer) RemovedFiles([]string) error { return nil }
func (*diagnosticsBatchIndexer) Close() error                { return nil }
func (*diagnosticsBatchIndexer) Clear() error                { return nil }
func (*diagnosticsBatchIndexer) BeginIndexingBatch([]string) {}

func (index *diagnosticsBatchIndexer) EndIndexingBatch() error {
	index.finishOnce.Do(func() { close(index.finishStarted) })
	<-index.releaseFinish
	index.ready.Store(true)
	return nil
}

type indexedStateInspection struct {
	testInspection
	index *diagnosticsBatchIndexer
}

func (inspection indexedStateInspection) Inspect(
	ctx context.Context,
	document *TextDocument,
	reporter ProblemReporter,
) error {
	if inspection.index.ready.Load() {
		return nil
	}
	return inspection.testInspection.Inspect(ctx, document, reporter)
}

func (inspection *lifecycleInspection) Inspect(
	ctx context.Context,
	document *TextDocument,
	reporter ProblemReporter,
) error {
	if document.Version == 2 {
		inspection.startedOnce.Do(func() {
			close(inspection.versionTwoStarted)
		})
		<-ctx.Done()
		return nil
	}
	return inspection.testInspection.Inspect(ctx, document, reporter)
}

func TestDocumentDiagnosticLifecycleOverJSONRPC(t *testing.T) {
	server := NewServer(nil, t.TempDir(), "test")
	inspection := &lifecycleInspection{
		versionTwoStarted: make(chan struct{}),
	}
	server.RegisterInspection(inspection)
	client, published := startDiagnosticLifecycleClient(t, server)
	const uri = "file:///project/test.yaml"

	require.NoError(t, client.Notify(
		context.Background(),
		"textDocument/didOpen",
		map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri": uri, "version": 1, "text": "value: bad\n",
			},
		},
	))
	opened := waitForPublishedDiagnostics(t, published)
	require.Equal(t, uri, opened.URI)
	require.Equal(t, 1, opened.Version)
	require.Len(t, opened.Diagnostics, 1)

	require.NoError(t, notifyDiagnosticLifecycleChange(client, uri, 2))
	select {
	case <-inspection.versionTwoStarted:
	case <-time.After(time.Second):
		t.Fatal("version 2 diagnostics did not start")
	}
	require.NoError(t, notifyDiagnosticLifecycleChange(client, uri, 3))
	changed := waitForPublishedDiagnostics(t, published)
	require.Equal(t, uri, changed.URI)
	require.Equal(t, 3, changed.Version)
	require.Len(t, changed.Diagnostics, 1)

	require.NoError(t, client.Notify(
		context.Background(),
		"textDocument/didClose",
		map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": uri},
		},
	))
	closed := waitForPublishedDiagnostics(t, published)
	require.Equal(t, uri, closed.URI)
	require.Zero(t, closed.Version)
	require.Empty(t, closed.Diagnostics)
	_, found := server.documentManager.GetDocument(uri)
	require.False(t, found)

	select {
	case late := <-published:
		t.Fatalf("received diagnostics after close: %+v", late)
	case <-time.After(2 * diagnosticsDebounce):
	}
}

func TestInitialIndexRefreshWaitsForBatchPublication(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "test.yaml")
	const source = "value: bad\n"
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))

	batch := &diagnosticsBatchIndexer{
		indexStarted:  make(chan struct{}),
		releaseIndex:  make(chan struct{}),
		finishStarted: make(chan struct{}),
		releaseFinish: make(chan struct{}),
	}
	scanner, err := indexer.NewFileScanner(
		root,
		filepath.Join(t.TempDir(), "scanner.db"),
	)
	require.NoError(t, err)
	scanner.AddIndexer(batch)
	server := NewServer(scanner, root, "test")
	server.RegisterInspection(indexedStateInspection{index: batch})
	client, published := startDiagnosticLifecycleClient(t, server)

	var releaseIndexOnce, releaseFinishOnce sync.Once
	releaseIndex := func() {
		releaseIndexOnce.Do(func() { close(batch.releaseIndex) })
	}
	releaseFinish := func() {
		releaseFinishOnce.Do(func() { close(batch.releaseFinish) })
	}
	t.Cleanup(func() {
		releaseIndex()
		releaseFinish()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var initializeResult interface{}
	require.NoError(t, client.Call(ctx, "initialize", map[string]interface{}{
		"rootUri": uriutil.FileURI(root),
	}, &initializeResult))
	require.NoError(t, client.Notify(ctx, "initialized", struct{}{}))
	requireChannelClosed(t, batch.indexStarted, "initial indexing did not start")

	uri := uriutil.FileURI(path)
	require.NoError(t, client.Notify(ctx, "textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri, "version": 1, "text": source,
		},
	}))
	releaseIndex()
	requireChannelClosed(t, batch.finishStarted, "batch publication did not start")
	pullParams := &protocol.DiagnosticParams{}
	pullParams.TextDocument.URI = uri
	pulled := server.diagnostic(ctx, pullParams).(protocol.DiagnosticResult)
	require.Empty(t, pulled.Items)
	select {
	case premature := <-published:
		t.Fatalf("diagnostics refreshed before batch publication: %+v", premature)
	case <-time.After(2 * diagnosticsDebounce):
	}

	releaseFinish()
	afterPublication := waitForPublishedDiagnostics(t, published)
	require.Equal(t, uri, afterPublication.URI)
	require.Empty(t, afterPublication.Diagnostics)
}

func requireChannelClosed(t *testing.T, channel <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

func startDiagnosticLifecycleClient(
	t *testing.T,
	server *Server,
) (*jsonrpc2.Conn, <-chan protocol.PublishDiagnosticsParams) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Start(serverSide, serverSide)
	}()
	published := make(chan protocol.PublishDiagnosticsParams, 8)
	client := jsonrpc2.NewConn(
		context.Background(),
		jsonrpc2.NewBufferedStream(clientSide, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(
			_ context.Context,
			_ *jsonrpc2.Conn,
			request *jsonrpc2.Request,
		) (interface{}, error) {
			if request.Method != "textDocument/publishDiagnostics" ||
				request.Params == nil {
				return nil, nil
			}
			var params protocol.PublishDiagnosticsParams
			if err := json.Unmarshal(*request.Params, &params); err != nil {
				return nil, err
			}
			published <- params
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
			t.Error("diagnostic lifecycle server did not stop")
		}
	})
	return client, published
}

func notifyDiagnosticLifecycleChange(
	client *jsonrpc2.Conn,
	uri string,
	version int,
) error {
	return client.Notify(
		context.Background(),
		"textDocument/didChange",
		map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri": uri, "version": version,
			},
			"contentChanges": []map[string]interface{}{{
				"text": "value: bad\n",
			}},
		},
	)
}

func waitForPublishedDiagnostics(
	t *testing.T,
	published <-chan protocol.PublishDiagnosticsParams,
) protocol.PublishDiagnosticsParams {
	t.Helper()
	select {
	case params := <-published:
		return params
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for published diagnostics")
		return protocol.PublishDiagnosticsParams{}
	}
}

func TestScheduleDiagnosticsCancelsSupersededJob(t *testing.T) {
	server := &Server{
		diagnosticsJobs: make(map[string]*diagnosticsJob),
	}
	const uri = "file:///project/template.twig"

	server.scheduleDiagnostics(uri, 1, time.Hour)
	first := server.diagnosticsJobs[uri]
	require.NotNil(t, first)

	server.scheduleDiagnostics(uri, 2, time.Hour)
	second := server.diagnosticsJobs[uri]
	require.NotNil(t, second)
	require.NotSame(t, first, second)

	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("superseded diagnostics job was not canceled")
	}

	server.diagnosticsMu.Lock()
	require.Same(t, second, server.diagnosticsJobs[uri])
	server.diagnosticsMu.Unlock()

	server.cancelDiagnostics(uri)
	select {
	case <-second.done:
	case <-time.After(time.Second):
		t.Fatal("active diagnostics job was not canceled")
	}

	server.diagnosticsMu.Lock()
	_, exists := server.diagnosticsJobs[uri]
	server.diagnosticsMu.Unlock()
	require.False(t, exists)
}

func TestRefreshOpenDocumentDiagnosticsFiltersDependents(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	const (
		adminURI = "file:///project/Resources/app/administration/src/card.html.twig"
		phpURI   = "file:///project/src/Controller.php"
	)
	server.documentManager.OpenDocument(adminURI, "<sw-card />", 3)
	server.documentManager.OpenDocument(phpURI, "<?php", 4)

	server.RefreshOpenDocumentDiagnostics(func(document *TextDocument) bool {
		return document.URI == adminURI
	})
	server.diagnosticsMu.Lock()
	adminJob := server.diagnosticsJobs[adminURI]
	phpJob := server.diagnosticsJobs[phpURI]
	server.diagnosticsMu.Unlock()
	require.NotNil(t, adminJob)
	require.Nil(t, phpJob)
}

type countingInspection struct {
	testInspection
	calls int
}

func (inspection *countingInspection) Inspect(
	ctx context.Context,
	document *TextDocument,
	reporter ProblemReporter,
) error {
	inspection.calls++
	return inspection.testInspection.Inspect(ctx, document, reporter)
}

func TestDiagnosticsCacheReusesAndInvalidatesDocumentAnalysis(t *testing.T) {
	root := t.TempDir()
	uri := uriutil.FileURI(root + "/test.yaml")
	server := NewServer(nil, root, "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	inspection := &countingInspection{}
	server.RegisterInspection(inspection)
	server.documentManager.OpenDocument(uri, "value: bad\n", 3)
	document, found := server.documentManager.GetDocument(uri)
	require.True(t, found)

	first := server.diagnosticsForDocument(context.Background(), document)
	second := server.diagnosticsForDocument(context.Background(), document)
	require.Equal(t, first, second)
	require.Equal(t, 1, inspection.calls)

	server.scheduleDiagnostics(uri, document.Version, time.Hour)
	third := server.diagnosticsForDocument(context.Background(), document)
	require.Equal(t, first, third)
	require.Equal(t, 2, inspection.calls)
	server.cancelDiagnostics(uri)
}

func TestDiagnosticPerformanceTraceEnvironment(t *testing.T) {
	for _, test := range []struct {
		value   string
		enabled bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"off", false},
		{"1", true}, {"true", true}, {" on ", true},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("SHOPWARE_LSP_TRACE_DIAGNOSTICS", test.value)
			require.Equal(t, test.enabled, diagnosticPerformanceTraceEnabled())
		})
	}
}
