package lsp

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

func TestIndexingPublishesStandardAndLegacyProgress(t *testing.T) {
	root := t.TempDir()
	scanner, err := indexer.NewFileScanner(
		root, filepath.Join(t.TempDir(), "scanner.db"),
	)
	require.NoError(t, err)
	server := NewServer(scanner, "", "test")

	serverSide, clientSide := net.Pipe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Start(serverSide, serverSide) }()
	progress := make(chan string, 4)
	legacy := make(chan string, 4)
	client := jsonrpc2.NewConn(
		context.Background(),
		jsonrpc2.NewBufferedStream(clientSide, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(
			_ context.Context,
			_ *jsonrpc2.Conn,
			request *jsonrpc2.Request,
		) (interface{}, error) {
			switch request.Method {
			case "window/workDoneProgress/create":
				progress <- "create"
			case "$/progress":
				var params struct {
					Value struct {
						Kind string `json:"kind"`
					} `json:"value"`
				}
				if request.Params != nil {
					if err := json.Unmarshal(*request.Params, &params); err != nil {
						return nil, err
					}
					progress <- params.Value.Kind
				}
			case "shopware/indexingStarted", "shopware/indexingCompleted":
				legacy <- request.Method
			}
			return nil, nil
		}).SuppressErrClosed(),
	)
	t.Cleanup(func() {
		_ = client.Close()
		_ = clientSide.Close()
		_ = serverSide.Close()
		select {
		case serverErr := <-serverDone:
			require.NoError(t, serverErr)
		case <-time.After(5 * time.Second):
			t.Error("progress test server did not stop")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var initializeResult interface{}
	require.NoError(t, client.Call(ctx, "initialize", map[string]interface{}{
		"rootUri": uriutil.FileURI(root),
		"capabilities": map[string]interface{}{
			"window": map[string]interface{}{"workDoneProgress": true},
		},
	}, &initializeResult))
	require.NoError(t, client.Notify(ctx, "initialized", struct{}{}))
	require.Equal(t, "create", receiveProgress(t, ctx, progress))
	require.Equal(t, "begin", receiveProgress(t, ctx, progress))
	require.Equal(t, "end", receiveProgress(t, ctx, progress))
	require.Equal(t, "shopware/indexingStarted", receiveProgress(t, ctx, legacy))
	require.Equal(t, "shopware/indexingCompleted", receiveProgress(t, ctx, legacy))
	require.NoError(t, client.Call(ctx, "shutdown", struct{}{}, nil))
	require.NoError(t, client.Notify(ctx, "exit", struct{}{}))
}

func receiveProgress(
	t *testing.T,
	ctx context.Context,
	values <-chan string,
) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-ctx.Done():
		t.Fatal("timed out waiting for progress")
		return ""
	}
}
