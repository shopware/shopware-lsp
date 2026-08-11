package app

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

func TestFrameworkClientProfileAdvertisesOnlyFrameworkPresentation(t *testing.T) {
	full := initializeClientProfile(t, "full")
	framework := initializeClientProfile(t, "framework")

	require.Contains(t, full, "implementationProvider")
	require.Contains(t, full, "typeHierarchyProvider")
	require.Contains(t, full, "colorProvider")
	require.NotContains(t, framework, "implementationProvider")
	require.NotContains(t, framework, "typeHierarchyProvider")
	require.NotContains(t, framework, "colorProvider")

	for _, capability := range []string{
		"completionProvider",
		"definitionProvider",
		"referencesProvider",
		"hoverProvider",
		"signatureHelpProvider",
		"diagnosticProvider",
		"codeActionProvider",
		"workspaceSymbolProvider",
		"semanticTokensProvider",
	} {
		require.Contains(t, framework, capability)
	}
	execute := framework["executeCommandProvider"].(map[string]interface{})
	require.Contains(
		t,
		execute["commands"].([]interface{}),
		"shopware/integration/catalog",
	)
}

func initializeClientProfile(
	t *testing.T,
	profile string,
) map[string]interface{} {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	server := lsp.NewServer(nil, "", "test")
	server.SetWorkspaceFactory(func(
		ctx context.Context,
		root string,
		current *lsp.Server,
	) (lsp.WorkspaceRuntime, error) {
		return NewWorkspace(ctx, root, current)
	})

	serverSide, clientSide := net.Pipe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Start(serverSide, serverSide) }()
	client := jsonrpc2.NewConn(
		context.Background(),
		jsonrpc2.NewBufferedStream(clientSide, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(
			context.Context,
			*jsonrpc2.Conn,
			*jsonrpc2.Request,
		) (interface{}, error) {
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
			t.Error("profile test server did not stop")
		}
	})

	params := map[string]interface{}{
		"rootUri": uriutil.FileURI(root),
		"initializationOptions": map[string]interface{}{
			"shopwareClient": map[string]interface{}{
				"protocolVersion":     lsp.ClientProtocolVersion,
				"presentationProfile": profile,
				"supportedCommands":   []string{},
			},
		},
	}
	var result map[string]interface{}
	require.NoError(t, client.Call(
		context.Background(), "initialize", params, &result,
	))
	require.NoError(t, client.Call(
		context.Background(), "shutdown", struct{}{}, nil,
	))
	require.NoError(t, client.Notify(
		context.Background(), "exit", struct{}{},
	))
	return result["capabilities"].(map[string]interface{})
}
