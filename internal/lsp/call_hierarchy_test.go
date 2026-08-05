package lsp

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCallHierarchyProvider struct {
	prepared []protocol.CallHierarchyItem
	incoming []protocol.CallHierarchyIncomingCall
	outgoing []protocol.CallHierarchyOutgoingCall
	docCount *int
}

func (p testCallHierarchyProvider) PrepareCallHierarchy(
	_ context.Context,
	_ *CallHierarchyPrepareRequest,
) ([]protocol.CallHierarchyItem, error) {
	return p.prepared, nil
}

func (p testCallHierarchyProvider) IncomingCalls(
	_ context.Context,
	request *CallHierarchyCallsRequest,
) ([]protocol.CallHierarchyIncomingCall, error) {
	if p.docCount != nil {
		*p.docCount = len(request.Documents)
	}
	return p.incoming, nil
}

func (p testCallHierarchyProvider) OutgoingCalls(
	_ context.Context,
	request *CallHierarchyCallsRequest,
) ([]protocol.CallHierarchyOutgoingCall, error) {
	if p.docCount != nil {
		*p.docCount = len(request.Documents)
	}
	return p.outgoing, nil
}

func TestCallHierarchyAggregatesProvidersAndSuppliesOpenDocuments(t *testing.T) {
	server := NewServer(nil, "", "test")
	t.Cleanup(func() { require.NoError(t, server.CloseAll()) })
	uri := "file:///workspace/component.js"
	server.documentManager.OpenDocument(uri, "function save() {}\nsave();\n", 1)

	callee := protocol.CallHierarchyItem{
		Name: "save", Kind: protocol.SymbolMethod, URI: uri,
		Range: protocol.Range{
			Start: protocol.Position{}, End: protocol.Position{Character: 15},
		},
		SelectionRange: protocol.Range{
			Start: protocol.Position{Character: 9},
			End:   protocol.Position{Character: 13},
		},
	}
	caller := protocol.CallHierarchyItem{
		Name: "component.js", Kind: protocol.SymbolFile, URI: uri,
		Range: protocol.Range{End: protocol.Position{Line: 2}},
	}
	firstRange := protocol.Range{
		Start: protocol.Position{Line: 1},
		End:   protocol.Position{Line: 1, Character: 4},
	}
	secondRange := protocol.Range{
		Start: protocol.Position{Line: 2},
		End:   protocol.Position{Line: 2, Character: 4},
	}
	docCount := 0
	server.RegisterCallHierarchyProvider(testCallHierarchyProvider{
		prepared: []protocol.CallHierarchyItem{callee},
		incoming: []protocol.CallHierarchyIncomingCall{{
			From: caller, FromRanges: []protocol.Range{secondRange, firstRange},
		}},
		outgoing: []protocol.CallHierarchyOutgoingCall{{
			To: callee, FromRanges: []protocol.Range{secondRange},
		}},
		docCount: &docCount,
	})
	server.RegisterCallHierarchyProvider(testCallHierarchyProvider{
		prepared: []protocol.CallHierarchyItem{callee},
		incoming: []protocol.CallHierarchyIncomingCall{{
			From: caller, FromRanges: []protocol.Range{firstRange},
		}},
		outgoing: []protocol.CallHierarchyOutgoingCall{{
			To: callee, FromRanges: []protocol.Range{firstRange},
		}},
	})

	prepared, err := server.prepareCallHierarchy(
		context.Background(),
		&protocol.CallHierarchyPrepareParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Character: 10},
		},
	)
	require.NoError(t, err)
	require.Equal(t, []protocol.CallHierarchyItem{callee}, prepared)

	incoming, err := server.callHierarchyIncomingCalls(
		context.Background(), callee,
	)
	require.NoError(t, err)
	require.Len(t, incoming, 1)
	assert.Equal(t, []protocol.Range{firstRange, secondRange}, incoming[0].FromRanges)
	assert.Equal(t, 1, docCount)

	outgoing, err := server.callHierarchyOutgoingCalls(
		context.Background(), caller,
	)
	require.NoError(t, err)
	require.Len(t, outgoing, 1)
	assert.Equal(t, []protocol.Range{firstRange, secondRange}, outgoing[0].FromRanges)
}
