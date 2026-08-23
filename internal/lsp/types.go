package lsp

import (
	"context"
	"encoding/json"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// CompletionProvider is an interface for providing completion items
type CompletionProvider interface {
	// GetCompletions returns completion items for the given parameters
	GetCompletions(ctx context.Context, request *CompletionRequest) []protocol.CompletionItem
	// GetTriggerCharacters returns the characters that trigger this completion provider
	GetTriggerCharacters() []string
}

// HoverProvider is an interface for providing hover information
type HoverProvider interface {
	// GetHover returns hover information for the given parameters
	GetHover(ctx context.Context, request *HoverRequest) (*protocol.Hover, error)
}

type SignatureHelpProvider interface {
	GetSignatureHelp(
		ctx context.Context,
		request *SignatureHelpRequest,
	) (*protocol.SignatureHelp, error)
}

type RenameProvider interface {
	Rename(
		ctx context.Context,
		request *RenameRequest,
	) (*protocol.WorkspaceEdit, error)
}

type ImplementationProvider interface {
	GetImplementation(
		ctx context.Context,
		request *ImplementationRequest,
	) []protocol.Location
}

type TypeHierarchyProvider interface {
	PrepareTypeHierarchy(
		ctx context.Context,
		request *TypeHierarchyPrepareRequest,
	) []protocol.TypeHierarchyItem
	TypeHierarchySupertypes(
		ctx context.Context,
		item protocol.TypeHierarchyItem,
	) []protocol.TypeHierarchyItem
	TypeHierarchySubtypes(
		ctx context.Context,
		item protocol.TypeHierarchyItem,
	) []protocol.TypeHierarchyItem
}

type CallHierarchyProvider interface {
	PrepareCallHierarchy(
		ctx context.Context,
		request *CallHierarchyPrepareRequest,
	) ([]protocol.CallHierarchyItem, error)
	IncomingCalls(
		ctx context.Context,
		request *CallHierarchyCallsRequest,
	) ([]protocol.CallHierarchyIncomingCall, error)
	OutgoingCalls(
		ctx context.Context,
		request *CallHierarchyCallsRequest,
	) ([]protocol.CallHierarchyOutgoingCall, error)
}

type InlayHintProvider interface {
	GetInlayHints(
		ctx context.Context,
		request *InlayHintRequest,
	) ([]protocol.InlayHint, error)
}

type DocumentLinkProvider interface {
	GetDocumentLinks(
		ctx context.Context,
		request *DocumentLinkRequest,
	) ([]protocol.DocumentLink, error)
}

type DocumentSymbolProvider interface {
	GetDocumentSymbols(
		ctx context.Context,
		request *DocumentSymbolRequest,
	) ([]protocol.DocumentSymbol, error)
}

type DocumentHighlightProvider interface {
	GetDocumentHighlights(
		ctx context.Context,
		request *DocumentHighlightRequest,
	) ([]protocol.DocumentHighlight, error)
}

type LinkedEditingRangeProvider interface {
	GetLinkedEditingRanges(
		ctx context.Context,
		request *LinkedEditingRangeRequest,
	) (*protocol.LinkedEditingRanges, error)
}

type FoldingRangeProvider interface {
	GetFoldingRanges(
		ctx context.Context,
		request *FoldingRangeRequest,
	) ([]protocol.FoldingRange, error)
}

type DocumentFormattingProvider interface {
	FormatDocument(
		ctx context.Context,
		request *DocumentFormattingRequest,
	) (formatted string, handled bool, err error)
}

type SelectionRangeProvider interface {
	GetSelectionRanges(
		ctx context.Context,
		request *SelectionRangeRequest,
	) ([]protocol.SelectionRange, error)
}

type DocumentColorProvider interface {
	GetDocumentColors(
		ctx context.Context,
		request *DocumentColorRequest,
	) ([]protocol.ColorInformation, error)
	GetColorPresentations(
		ctx context.Context,
		request *ColorPresentationRequest,
	) ([]protocol.ColorPresentation, error)
}

// SemanticToken is a single-line byte range with a token type from
// protocol.SemanticTokenTypes and an optional modifier bitset.
type SemanticToken struct {
	Range     cst.TextRange
	Type      uint32
	Modifiers uint32
}

type SemanticTokensProvider interface {
	GetSemanticTokens(
		ctx context.Context,
		request *SemanticTokensRequest,
	) ([]SemanticToken, error)
}

// CodeLensProvider is an interface for providing code lenses
type CodeLensProvider interface {
	// GetCodeLenses returns code lenses for the given document
	GetCodeLenses(ctx context.Context, request *CodeLensRequest) ([]protocol.CodeLens, error)
	// ResolveCodeLens resolves the command for a given code lens item
	ResolveCodeLens(ctx context.Context, codeLens *protocol.CodeLens) (*protocol.CodeLens, error)
}

type CommandFunc func(ctx context.Context, args *json.RawMessage) (interface{}, error)

type CommandProvider interface {
	GetCommands(ctx context.Context) map[string]CommandFunc
}
