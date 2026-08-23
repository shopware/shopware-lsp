package lsp

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// ContextEnricher adds domain semantic state to a request context.
type ContextEnricher func(context.Context, SyntaxContext) context.Context

func (s *Server) enrichContext(ctx context.Context, syntax SyntaxContext) context.Context {
	if enrich := s.contextEnrichers[syntax.Language]; enrich != nil {
		return enrich(ctx, syntax)
	}
	return ctx
}

// SyntaxContext enriches a wire-protocol request with the immutable document
// snapshot and generic CST position context used by feature providers.
type SyntaxContext struct {
	Document        *TextDocument
	Language        language.ID
	DocumentContent []byte
	DocumentTree    *cst.Tree
	LineIndex       *cst.LineIndex
	Root            *cst.Node
	Token           *cst.Token
	Node            *cst.Node
}

func newSyntaxContext(document *TextDocument, line, character int) SyntaxContext {
	if document == nil {
		return SyntaxContext{}
	}

	root, token, node := syntaxAtPosition(
		document.SyntaxTree,
		document.LineIndex,
		line,
		character,
	)
	return SyntaxContext{
		Document:        document,
		Language:        document.SyntaxLanguage,
		DocumentContent: document.Text,
		DocumentTree:    document.SyntaxTree,
		LineIndex:       document.LineIndex,
		Root:            root,
		Token:           token,
		Node:            node,
	}
}

type CompletionRequest struct {
	*protocol.CompletionParams
	SyntaxContext
}

type DefinitionRequest struct {
	*protocol.DefinitionParams
	SyntaxContext
}

type ImplementationRequest struct {
	*protocol.ImplementationParams
	SyntaxContext
}

type TypeHierarchyPrepareRequest struct {
	*protocol.PrepareTypeHierarchyParams
	SyntaxContext
}

type CallHierarchyPrepareRequest struct {
	*protocol.CallHierarchyPrepareParams
	SyntaxContext
}

type CallHierarchyCallsRequest struct {
	Item      protocol.CallHierarchyItem
	Documents []*TextDocument
}

type ReferenceRequest struct {
	*protocol.ReferenceParams
	SyntaxContext
}

type DocumentHighlightRequest struct {
	*protocol.DocumentHighlightParams
	SyntaxContext
}

type LinkedEditingRangeRequest struct {
	*protocol.LinkedEditingRangeParams
	SyntaxContext
}

type FoldingRangeRequest struct {
	*protocol.FoldingRangeParams
	Document *TextDocument
}

type DocumentFormattingRequest struct {
	*protocol.DocumentFormattingParams
	Document *TextDocument
}

type SelectionRangeRequest struct {
	*protocol.SelectionRangeParams
	Document *TextDocument
}

type DocumentColorRequest struct {
	*protocol.DocumentColorParams
	Document *TextDocument
}

type ColorPresentationRequest struct {
	*protocol.ColorPresentationParams
	Document *TextDocument
}

type HoverRequest struct {
	*protocol.HoverParams
	SyntaxContext
}

type SignatureHelpRequest struct {
	*protocol.SignatureHelpParams
	SyntaxContext
}

type RenameRequest struct {
	*protocol.RenameParams
	SyntaxContext
}

type InlayHintRequest struct {
	*protocol.InlayHintParams
	Document *TextDocument
}

type DocumentLinkRequest struct {
	*protocol.DocumentLinkParams
	Document *TextDocument
}

type DocumentSymbolRequest struct {
	*protocol.DocumentSymbolParams
	Document *TextDocument
}

type SemanticTokensRequest struct {
	*protocol.SemanticTokensParams
	Document *TextDocument
}

type CodeActionRequest struct {
	*protocol.CodeActionParams
	SyntaxContext
}

type CodeLensRequest struct {
	*protocol.CodeLensParams
	Document *TextDocument
}
