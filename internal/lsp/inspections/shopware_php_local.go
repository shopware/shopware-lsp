package inspections

import (
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
)

func NewShopwarePHPLocal() lsp.Inspection {
	return NewAnalyzerInspection(
		"shopware.php.local",
		[]language.ID{language.PHP},
		"shopware-lsp",
		[]string{
			string(diagnostics.ShopwarePHPSuperglobalCode),
			string(diagnostics.ShopwarePHPDisallowedFunctionCode),
			string(diagnostics.ShopwarePHPSessionFunctionCode),
			string(diagnostics.ShopwarePHPGlobBraceCode),
			string(diagnostics.ShopwarePHPTLSVerificationCode),
			string(diagnostics.ShopwarePHPPredictableSaltCode),
			string(diagnostics.ShopwarePHPWeakKeyCode),
			string(diagnostics.ShopwarePHPInsecureCookieCode),
			string(diagnostics.ShopwarePHPForeignKeyChecksCode),
		},
		diagnostics.NewShopwarePHPLocalAnalyzer(),
	)
}
