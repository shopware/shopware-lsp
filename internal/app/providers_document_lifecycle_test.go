package app

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

func TestAdministrationDocumentCloseRemovesOverlayAndRefreshesDependents(
	t *testing.T,
) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(
		filepath.Join(root, "cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	server := lsp.NewServer(nil, root, "test")
	registerAdministrationDocumentObserver(server, adminIndex)
	client, published := startAdministrationLifecycleClient(t, server)
	adminRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	definitionURI := uriutil.FileURI(filepath.Join(
		adminRoot, "component/live.js",
	))
	consumerURI := uriutil.FileURI(filepath.Join(
		adminRoot, "component/consumer.html.twig",
	))

	notifyAdministrationDocumentOpen(
		t, client, consumerURI, 1, "{{ component.visible }}\n",
	)
	waitForAdministrationDiagnostics(t, published, consumerURI)

	definition := `Shopware.Component.register('sw-live-only', {
    props: { title: { type: String, required: true } },
    data() { return { visible: true }; },
});`
	notifyAdministrationDocumentOpen(t, client, definitionURI, 1, definition)
	waitForAdministrationDiagnostics(
		t, published, definitionURI, consumerURI,
	)
	component, err := adminIndex.GetEffectiveComponent("sw-live-only")
	require.NoError(t, err)
	require.NotNil(t, component)

	require.NoError(t, client.Notify(
		context.Background(),
		"textDocument/didClose",
		map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": definitionURI},
		},
	))
	closed := waitForAdministrationDiagnostics(
		t, published, definitionURI, consumerURI,
	)
	require.Empty(t, closed[definitionURI].Diagnostics)
	require.Zero(t, closed[definitionURI].Version)
	require.Equal(t, 1, closed[consumerURI].Version)
	component, err = adminIndex.GetEffectiveComponent("sw-live-only")
	require.NoError(t, err)
	require.Nil(t, component)
}

func startAdministrationLifecycleClient(
	t *testing.T,
	server *lsp.Server,
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
			t.Error("Administration lifecycle server did not stop")
		}
	})
	return client, published
}

func notifyAdministrationDocumentOpen(
	t *testing.T,
	client *jsonrpc2.Conn,
	uri string,
	version int,
	text string,
) {
	t.Helper()
	require.NoError(t, client.Notify(
		context.Background(),
		"textDocument/didOpen",
		map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri": uri, "version": version, "text": text,
			},
		},
	))
}

func waitForAdministrationDiagnostics(
	t *testing.T,
	published <-chan protocol.PublishDiagnosticsParams,
	uris ...string,
) map[string]protocol.PublishDiagnosticsParams {
	t.Helper()
	pending := make(map[string]bool, len(uris))
	for _, uri := range uris {
		pending[uri] = true
	}
	results := make(map[string]protocol.PublishDiagnosticsParams, len(uris))
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for len(pending) > 0 {
		select {
		case params := <-published:
			if pending[params.URI] {
				results[params.URI] = params
				delete(pending, params.URI)
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for Administration diagnostics: %v", pending)
		}
	}
	return results
}
