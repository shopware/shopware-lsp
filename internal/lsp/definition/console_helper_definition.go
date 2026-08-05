package definition

import (
	"context"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
)

type ConsoleHelperDefinitionProvider struct {
	phpIndex *php.PHPIndex
	catalog  *console.HelperCatalog
}

func NewConsoleHelperDefinitionProvider(
	phpIndex *php.PHPIndex,
) *ConsoleHelperDefinitionProvider {
	return &ConsoleHelperDefinitionProvider{
		phpIndex: phpIndex,
		catalog:  console.NewHelperCatalog(phpIndex),
	}
}

func (p *ConsoleHelperDefinitionProvider) GetDefinition(
	ctx context.Context,
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.phpIndex == nil || request == nil ||
		request.Node == nil {
		return nil
	}
	reference, found := console.HelperReferenceAt(request.Node)
	if !found || reference.Name == "" || !console.ValidateHelperReference(
		ctx,
		p.phpIndex,
		reference,
		request.DocumentContent,
	) {
		return nil
	}
	var result []protocol.Location
	seen := make(map[string]struct{})
	for _, helper := range p.catalog.Helpers() {
		if helper.Name != reference.Name {
			continue
		}
		key := helper.File + ":" + helper.Range.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if location, ok := consoleLocation(helper.File, helper.Range); ok {
			result = append(result, location)
		}
	}
	return result
}

var _ lsp.GotoDefinitionProvider = (*ConsoleHelperDefinitionProvider)(nil)
