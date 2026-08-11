package integration

import (
	"context"
	"encoding/json"

	integrationcatalog "github.com/shopware/shopware-lsp/internal/integration"
	"github.com/shopware/shopware-lsp/internal/lsp"
)

const CatalogCommand = "shopware/integration/catalog"

type Provider struct{}

func NewProvider() *Provider { return &Provider{} }

func (*Provider) GetCommands(context.Context) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{CatalogCommand: catalog}
}

func catalog(
	ctx context.Context,
	_ *json.RawMessage,
) (interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return integrationcatalog.CurrentCatalog(), nil
}

var _ lsp.CommandProvider = (*Provider)(nil)
