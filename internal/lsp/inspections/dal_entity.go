package inspections

import (
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
)

func NewDALEntity(index *dal.Index) lsp.Inspection {
	return NewAnalyzerInspection(
		"shopware.dal.entity",
		[]language.ID{language.JavaScript, language.Vue},
		"shopware-lsp",
		[]string{"shopware.dal.entity-not-found"},
		diagnostics.NewDALEntityAnalyzer(index),
	)
}
