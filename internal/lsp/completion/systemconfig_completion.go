package completion

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/systemconfig"
)

type SystemConfigCompletionProvider struct {
	indexer  *systemconfig.SystemConfigIndexer
	phpIndex *php.PHPIndex
}

func (s *SystemConfigCompletionProvider) GetCompletions(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if filepath.Ext(params.TextDocument.URI) == ".twig" &&
		params.Node != nil &&
		twigquery.StringInFunction(params.Node, "config") {
		completions, err := s.indexer.GetAllSystemConfigEntries()
		if err != nil {
			return nil
		}

		var completionItems []protocol.CompletionItem
		for _, completion := range completions {
			completionItems = append(completionItems, protocol.CompletionItem{
				Label: completion.Name,
			})
		}

		return completionItems
	}

	if filepath.Ext(params.TextDocument.URI) == ".php" {
		if params.Node == nil {
			return nil
		}
		return s.phpCompletion(ctx, params)
	}

	return nil
}

func (s *SystemConfigCompletionProvider) phpCompletion(ctx context.Context, params *lsp.CompletionRequest) []protocol.CompletionItem {
	if s.phpIndex.IsMethodCalledOnClass(ctx, params.Node, params.DocumentContent, "Shopware\\Core\\System\\SystemConfig\\SystemConfigService") {
		if s.phpIndex.IsMethodCalledName(ctx, params.Node, params.DocumentContent, "get", "getInt", "getString", "getFloat", "getBool", "set") {
			completions, err := s.indexer.GetAllSystemConfigEntries()
			if err != nil {
				return nil
			}

			var completionItems []protocol.CompletionItem
			for _, completion := range completions {
				completionItems = append(completionItems, protocol.CompletionItem{
					Label: completion.Name,
				})
			}

			return completionItems
		}

		if s.phpIndex.IsMethodCalledName(ctx, params.Node, params.DocumentContent, "getDomain") {
			completions, err := s.indexer.GetAllSystemConfigEntries()
			if err != nil {
				return nil
			}

			var uniqueDomains []string
			for _, completion := range completions {
				if !slices.Contains(uniqueDomains, completion.Namespace) {
					uniqueDomains = append(uniqueDomains, completion.Namespace)
				}
			}

			var completionItems []protocol.CompletionItem
			for _, domain := range uniqueDomains {
				completionItems = append(completionItems, protocol.CompletionItem{
					Label: domain,
				})
			}

			return completionItems
		}
	}

	return nil
}

func (s *SystemConfigCompletionProvider) GetTriggerCharacters() []string {
	return []string{}
}

func NewSystemConfigCompletion(indexer *systemconfig.SystemConfigIndexer, phpIndexer *php.PHPIndex) *SystemConfigCompletionProvider {
	return &SystemConfigCompletionProvider{
		indexer:  indexer,
		phpIndex: phpIndexer,
	}
}
