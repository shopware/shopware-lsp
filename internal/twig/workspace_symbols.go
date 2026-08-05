package twig

import "github.com/shopware/shopware-lsp/internal/indexer"

func addTemplateWorkspaceSymbols(
	parsed *indexer.ParsedFile,
	file *TwigFile,
	macros []Macro,
) {
	if parsed == nil || file == nil {
		return
	}
	var result []indexer.WorkspaceSymbol
	for _, name := range TemplateNames(file.Path) {
		result = append(result, indexer.WorkspaceSymbol{
			Name: name, ContainerName: "Twig template", Path: file.Path,
			Domain: "twig.template", Kind: indexer.WorkspaceSymbolFile,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
		})
	}
	for _, block := range file.Blocks {
		result = append(result, indexer.WorkspaceSymbol{
			Name: block.Name, ContainerName: "Twig block", Path: file.Path,
			Domain: "twig.block", Kind: indexer.WorkspaceSymbolField,
			Priority: indexer.WorkspaceSymbolPriorityFrameworkMember,
			Range:    indexer.WorkspaceSymbolRangeFromText(parsed, block.NameRange),
		})
	}
	for _, macro := range macros {
		result = append(result, indexer.WorkspaceSymbol{
			Name: macro.Name, ContainerName: "Twig macro · " + macro.Signature(),
			Path: macro.FilePath, Domain: "twig.macro",
			Kind:     indexer.WorkspaceSymbolFunction,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeFromText(parsed, macro.NameRange),
		})
	}
	parsed.AddWorkspaceSymbols(result...)
}

func addExtensionWorkspaceSymbols(
	file *indexer.ParsedFile,
	functions []TwigFunction,
	filters []TwigFilter,
) {
	result := make([]indexer.WorkspaceSymbol, 0, len(functions)+len(filters))
	for _, function := range functions {
		result = append(result, indexer.WorkspaceSymbol{
			Name: function.Name, ContainerName: "Twig function",
			Aliases: []string{function.Method}, Path: function.FilePath,
			Domain: "twig.function", Kind: indexer.WorkspaceSymbolFunction,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(function.Line),
		})
	}
	for _, filter := range filters {
		result = append(result, indexer.WorkspaceSymbol{
			Name: filter.Name, ContainerName: "Twig filter",
			Aliases: []string{filter.Method}, Path: filter.FilePath,
			Domain: "twig.filter", Kind: indexer.WorkspaceSymbolFunction,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeAtLine(filter.Line),
		})
	}
	file.AddWorkspaceSymbols(result...)
}
