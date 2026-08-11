package lsp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/stretchr/testify/require"
)

func TestExecuteCommandUsesRegisteredProductionCommand(t *testing.T) {
	server := NewServer(nil, "", "test")
	server.commandMap["shopware/test"] = func(
		_ context.Context,
		raw *json.RawMessage,
	) (interface{}, error) {
		var input struct {
			Value string `json:"value"`
		}
		require.NoError(t, json.Unmarshal(*raw, &input))
		return map[string]string{"value": input.Value}, nil
	}

	result, err := server.handleExecuteCommand(
		context.Background(),
		executeCommandRequest(t, `{
			"command":"shopware/test",
			"arguments":[{"value":"ok"}]
		}`),
	)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"value": "ok"}, result)
}

func TestExecuteCommandValidatesCommandAndArgumentEnvelope(t *testing.T) {
	server := NewServer(nil, "", "test")
	server.commandMap["shopware/test"] = func(
		context.Context,
		*json.RawMessage,
	) (interface{}, error) {
		return nil, nil
	}

	_, err := server.handleExecuteCommand(
		context.Background(),
		executeCommandRequest(t, `{"command":"shopware/missing"}`),
	)
	require.ErrorContains(t, err, "Unknown Shopware command")

	_, err = server.handleExecuteCommand(
		context.Background(),
		executeCommandRequest(t, `{
			"command":"shopware/test",
			"arguments":[{},{}]
		}`),
	)
	require.ErrorContains(t, err, "at most one JSON argument")
}

func executeCommandRequest(t *testing.T, params string) *jsonrpc2.Request {
	t.Helper()
	raw := json.RawMessage(params)
	return &jsonrpc2.Request{Method: "workspace/executeCommand", Params: &raw}
}
