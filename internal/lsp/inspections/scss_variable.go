package inspections

import (
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/style"
)

func NewSCSSVariable(
	index *style.Index,
	readiness ...*indexer.WorkspaceSymbolCatalog,
) lsp.Inspection {
	var indexReadiness diagnostics.SCSSVariableIndexReadiness
	if len(readiness) > 0 && readiness[0] != nil {
		indexReadiness = readiness[0]
	}
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "scss.variable",
			Languages: []language.ID{language.SCSS},
			Problems: []lsp.ProblemDefinition{{
				ID:              diagnostics.SCSSVariableUnknownCode,
				Source:          "scss",
				DefaultSeverity: protocol.DiagnosticSeverityError,
			}},
		},
		analyzer: diagnostics.NewSCSSVariableAnalyzer(index, indexReadiness),
	}
}
