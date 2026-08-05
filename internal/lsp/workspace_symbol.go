package lsp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

const maxWorkspaceSymbols = 500

type WorkspaceSymbolProvider interface {
	WorkspaceSymbols(
		ctx context.Context,
		query string,
	) ([]protocol.SymbolInformation, error)
}

func (s *Server) RegisterWorkspaceSymbolProvider(
	provider WorkspaceSymbolProvider,
) {
	if provider != nil {
		s.workspaceSymbolProviders = append(
			s.workspaceSymbolProviders,
			provider,
		)
	}
}

func (s *Server) workspaceSymbols(
	ctx context.Context,
	params *protocol.WorkspaceSymbolParams,
) ([]protocol.SymbolInformation, error) {
	if params == nil {
		return nil, nil
	}
	var result []protocol.SymbolInformation
	seen := make(map[string]struct{})
	for _, provider := range s.workspaceSymbolProviders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		symbols, err := provider.WorkspaceSymbols(ctx, params.Query)
		if err != nil {
			return nil, err
		}
		for _, current := range symbols {
			key := fmt.Sprintf(
				"%s\x00%d\x00%s\x00%d:%d:%d:%d",
				current.Name,
				current.Kind,
				current.Location.URI,
				current.Location.Range.Start.Line,
				current.Location.Range.Start.Character,
				current.Location.Range.End.Line,
				current.Location.Range.End.Character,
			)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, current)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		leftScore := workspaceSymbolScore(params.Query, result[left])
		rightScore := workspaceSymbolScore(params.Query, result[right])
		if leftScore != rightScore {
			return leftScore < rightScore
		}
		if result[left].Priority != result[right].Priority {
			return result[left].Priority > result[right].Priority
		}
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		if result[left].ContainerName != result[right].ContainerName {
			return result[left].ContainerName <
				result[right].ContainerName
		}
		return result[left].Location.URI < result[right].Location.URI
	})
	if len(result) > maxWorkspaceSymbols {
		result = result[:maxWorkspaceSymbols]
	}
	return result, nil
}

func workspaceSymbolScore(
	query string,
	symbol protocol.SymbolInformation,
) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 3
	}
	best := 4
	for _, value := range []string{symbol.Name, symbol.ContainerName} {
		value = strings.ToLower(value)
		switch {
		case value == query:
			best = min(best, 0)
		case strings.HasPrefix(value, query):
			best = min(best, 1)
		case strings.Contains(value, query):
			best = min(best, 2)
		}
	}
	return best
}
