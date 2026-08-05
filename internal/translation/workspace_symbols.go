package translation

import "github.com/shopware/shopware-lsp/internal/indexer"

func addMessageWorkspaceSymbols(
	file *indexer.ParsedFile,
	messages []Message,
) {
	result := make([]indexer.WorkspaceSymbol, 0, len(messages)*2)
	domains := make(map[string]struct{})
	for _, message := range messages {
		if message.Key == "" || message.File == "" {
			continue
		}
		rangeValue := indexer.WorkspaceSymbolRange{
			Start: indexer.WorkspaceSymbolPosition{
				Line: message.Line, Character: message.Character,
			},
			End: indexer.WorkspaceSymbolPosition{
				Line: message.EndLine, Character: message.EndCharacter,
			},
		}
		domainKey := message.Domain + "\x00" + message.Locale
		if message.Domain != "" {
			if _, exists := domains[domainKey]; !exists {
				domains[domainKey] = struct{}{}
				result = append(result, indexer.WorkspaceSymbol{
					Name:          message.Domain,
					ContainerName: "Translation domain · " + message.Locale,
					Path:          message.File, Domain: "translation.domain",
					Kind:     indexer.WorkspaceSymbolNamespace,
					Priority: indexer.WorkspaceSymbolPriorityFramework,
					Range:    rangeValue,
				})
			}
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name:          message.Key,
			ContainerName: "Translation · " + message.Domain + " · " + message.Locale,
			Path:          message.File, Domain: "translation.message",
			Kind:     indexer.WorkspaceSymbolString,
			Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
			Range:    rangeValue,
		})
	}
	file.AddWorkspaceSymbols(result...)
}
