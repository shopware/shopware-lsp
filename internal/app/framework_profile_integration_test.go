//go:build integration

package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

func TestShopwareTrunkFrameworkClientProfile(t *testing.T) {
	root := realWorldProjectRoot(t)
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	server := lsp.NewServer(nil, "", "integration-test")
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
	indexed := make(chan error, 1)
	client := jsonrpc2.NewConn(
		context.Background(),
		jsonrpc2.NewBufferedStream(clientSide, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(
			_ context.Context,
			_ *jsonrpc2.Conn,
			request *jsonrpc2.Request,
		) (interface{}, error) {
			switch request.Method {
			case "shopware/indexingCompleted":
				indexed <- nil
			case "shopware/indexingFailed":
				var params struct {
					Message string `json:"message"`
				}
				if request.Params != nil {
					_ = json.Unmarshal(*request.Params, &params)
				}
				indexed <- &frameworkIndexError{message: params.Message}
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
		case <-time.After(10 * time.Second):
			t.Error("framework integration server did not stop")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var initializeResult struct {
		Capabilities map[string]interface{} `json:"capabilities"`
	}
	require.NoError(t, client.Call(ctx, "initialize", map[string]interface{}{
		"rootUri": uriutil.FileURI(root),
		"initializationOptions": map[string]interface{}{
			"shopwareClient": map[string]interface{}{
				"protocolVersion":     lsp.ClientProtocolVersion,
				"presentationProfile": "framework",
				"supportedCommands":   []string{},
			},
		},
	}, &initializeResult))
	require.NotContains(t, initializeResult.Capabilities, "implementationProvider")
	require.NotContains(t, initializeResult.Capabilities, "typeHierarchyProvider")
	require.Contains(t, initializeResult.Capabilities, "completionProvider")
	require.Contains(t, initializeResult.Capabilities, "definitionProvider")
	require.Contains(t, initializeResult.Capabilities, "diagnosticProvider")
	require.NoError(t, client.Notify(ctx, "initialized", struct{}{}))
	select {
	case indexErr := <-indexed:
		require.NoError(t, indexErr)
	case <-ctx.Done():
		t.Fatal("timed out waiting for real-world framework indexing")
	}

	var symbols []protocol.SymbolInformation
	require.NoError(t, client.Call(ctx, "workspace/symbol", map[string]interface{}{
		"query": "SystemConfigService",
	}, &symbols))
	require.NotEmpty(t, symbols)

	path := filepath.Join(
		root, "src/Core/Framework/Demodata/DemodataContext.php",
	)
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	uri := uriutil.FileURI(path)
	require.NoError(t, client.Notify(ctx, "textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri, "languageId": "php", "version": 1,
			"text": string(source),
		},
	}))
	var diagnostics protocol.DiagnosticResult
	require.NoError(t, client.Call(ctx, "textDocument/diagnostic", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
	}, &diagnostics))
	for _, diagnostic := range diagnostics.Items {
		require.False(t, strings.HasPrefix(diagnostic.Source, "shopware-php"))
		require.False(t, strings.HasPrefix(stringDiagnosticCode(diagnostic.Code), "php."))
	}

	require.NoError(t, client.Call(ctx, "shutdown", struct{}{}, nil))
	require.NoError(t, client.Notify(ctx, "exit", struct{}{}))
}

type frameworkIndexError struct{ message string }

func (err *frameworkIndexError) Error() string {
	if err.message == "" {
		return "framework integration indexing failed"
	}
	return err.message
}

func stringDiagnosticCode(code any) string {
	value, _ := code.(string)
	return value
}
