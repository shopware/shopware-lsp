package lsp

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) implementation(
	ctx context.Context,
	params *protocol.ImplementationParams,
) []protocol.Location {
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	request := &ImplementationRequest{
		ImplementationParams: params,
		SyntaxContext:        syntax,
	}
	ctx = s.enrichContext(ctx, syntax)
	var result []protocol.Location
	for _, provider := range s.implementationProviders {
		result = append(result, provider.GetImplementation(ctx, request)...)
	}
	return result
}

func (s *Server) prepareTypeHierarchy(
	ctx context.Context,
	params *protocol.PrepareTypeHierarchyParams,
) []protocol.TypeHierarchyItem {
	syntax, _ := s.documentManager.SyntaxContext(
		params.TextDocument.URI,
		params.Position.Line,
		params.Position.Character,
	)
	request := &TypeHierarchyPrepareRequest{
		PrepareTypeHierarchyParams: params,
		SyntaxContext:              syntax,
	}
	ctx = s.enrichContext(ctx, syntax)
	var result []protocol.TypeHierarchyItem
	for _, provider := range s.typeHierarchyProviders {
		result = append(result, provider.PrepareTypeHierarchy(ctx, request)...)
	}
	return result
}

func (s *Server) typeHierarchySupertypes(
	ctx context.Context,
	item protocol.TypeHierarchyItem,
) []protocol.TypeHierarchyItem {
	var result []protocol.TypeHierarchyItem
	for _, provider := range s.typeHierarchyProviders {
		result = append(result, provider.TypeHierarchySupertypes(ctx, item)...)
	}
	return result
}

func (s *Server) typeHierarchySubtypes(
	ctx context.Context,
	item protocol.TypeHierarchyItem,
) []protocol.TypeHierarchyItem {
	var result []protocol.TypeHierarchyItem
	for _, provider := range s.typeHierarchyProviders {
		result = append(result, provider.TypeHierarchySubtypes(ctx, item)...)
	}
	return result
}
