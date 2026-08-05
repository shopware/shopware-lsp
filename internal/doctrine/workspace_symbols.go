package doctrine

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

func addModelWorkspaceSymbols(
	file *indexer.ParsedFile,
	models []Model,
) {
	result := make([]indexer.WorkspaceSymbol, 0, len(models)*2)
	for _, model := range models {
		if model.Class == "" || model.File == "" {
			continue
		}
		name := strings.Trim(model.Class, `\`)
		if separator := strings.LastIndex(name, `\`); separator >= 0 {
			name = name[separator+1:]
		}
		rangeValue := indexer.WorkspaceSymbolRangeFromText(file, model.NameRange)
		result = append(result, indexer.WorkspaceSymbol{
			Name:          name,
			ContainerName: "Doctrine " + model.Kind.String() + " · " + model.Class,
			Aliases:       []string{model.Class, model.Table}, Path: model.File,
			Domain: "doctrine.model", Kind: indexer.WorkspaceSymbolClass,
			Priority: indexer.WorkspaceSymbolPriorityFramework,
			Range:    rangeValue,
		})
		if model.Table != "" {
			result = append(result, indexer.WorkspaceSymbol{
				Name:          model.Table,
				ContainerName: "Doctrine table · " + model.Class,
				Aliases:       []string{model.Class}, Path: model.File,
				Domain: "doctrine.table", Kind: indexer.WorkspaceSymbolStruct,
				Priority: indexer.WorkspaceSymbolPriorityFramework,
				Range:    rangeValue,
			})
		}
	}
	file.AddWorkspaceSymbols(result...)
}
