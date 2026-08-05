package console

import "github.com/shopware/shopware-lsp/internal/indexer"

func addCommandWorkspaceSymbols(
	file *indexer.ParsedFile,
	commands []Command,
) {
	result := make([]indexer.WorkspaceSymbol, 0, len(commands))
	for _, command := range commands {
		if command.Name == "" || command.File == "" {
			continue
		}
		container := "Symfony command"
		if command.Description != "" {
			container += " · " + command.Description
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name: command.Name, ContainerName: container,
			Aliases: []string{command.Class}, Path: command.File,
			Domain: "symfony.command", Kind: indexer.WorkspaceSymbolFunction,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    indexer.WorkspaceSymbolRangeFromText(file, command.Range),
		})
	}
	file.AddWorkspaceSymbols(result...)
}
