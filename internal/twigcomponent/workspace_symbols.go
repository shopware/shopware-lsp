package twigcomponent

import "github.com/shopware/shopware-lsp/internal/indexer"

func addComponentWorkspaceSymbols(
	file *indexer.ParsedFile,
	record Record,
) {
	result := make([]indexer.WorkspaceSymbol, 0, len(record.Declarations))
	for _, declaration := range record.Declarations {
		container := "Twig component"
		if declaration.Live {
			container = "Live component"
		}
		if declaration.Class != "" {
			container += " · " + declaration.Class
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name: declaration.Name, ContainerName: container,
			Aliases: []string{declaration.Class, declaration.Template},
			Path:    declaration.File, Domain: "twig.component",
			Kind:     indexer.WorkspaceSymbolClass,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeFromText(file, declaration.NameRange),
		})
	}
	file.AddWorkspaceSymbols(result...)
}
