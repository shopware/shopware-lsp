package inspections

import (
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/php"
)

func NewShopwarePHPSemantic(phpIndex *php.PHPIndex) lsp.Inspection {
	return NewAnalyzerInspection(
		"shopware.php.semantic",
		[]language.ID{language.PHP},
		"shopware-lsp",
		[]string{
			string(diagnostics.ShopwarePHPRepositoryInLoopCode),
			string(diagnostics.ShopwarePHPInternalClassExtensionCode),
			string(diagnostics.ShopwarePHPInternalFunctionCallCode),
			string(diagnostics.ShopwarePHPInternalMethodCallCode),
			string(diagnostics.ShopwarePHPSessionConstructorCode),
			string(diagnostics.ShopwarePHPSessionPaymentHandlerCode),
			string(diagnostics.ShopwarePHPSessionStoreAPICode),
			string(diagnostics.ShopwarePHPScheduledTaskIntervalCode),
			string(diagnostics.ShopwarePHPUserStoreTokenCode),
			string(diagnostics.ShopwarePHPConcreteDecoratorExtensionCode),
		},
		diagnostics.NewShopwarePHPSemanticAnalyzer(phpIndex),
	)
}
