package dal

import "github.com/shopware/shopware-lsp/internal/indexer"

func addDefinitionWorkspaceSymbols(
	file *indexer.ParsedFile,
	definitions map[string]Definition,
) {
	var result []indexer.WorkspaceSymbol
	for _, definition := range definitions {
		result = append(result, indexer.WorkspaceSymbol{
			Name:          definition.Name,
			ContainerName: "Shopware DAL entity · " + definition.Class,
			Aliases:       []string{definition.Class}, Path: definition.File,
			Domain: "shopware.dal.entity", Kind: indexer.WorkspaceSymbolClass,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeFromText(file, definition.NameRange),
		})
		for _, field := range definition.Fields {
			result = append(result, indexer.WorkspaceSymbol{
				Name:          field.Name,
				ContainerName: "Shopware DAL field · " + definition.Name + " · " + field.Type,
				Path:          definition.File, Domain: "shopware.dal.field",
				Kind:     indexer.WorkspaceSymbolField,
				Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
				Range:    indexer.WorkspaceSymbolRangeFromText(file, field.Range),
			})
		}
	}
	file.AddWorkspaceSymbols(result...)
}
