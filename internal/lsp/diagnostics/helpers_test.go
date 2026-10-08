package diagnostics

import "github.com/shopware/shopware-lsp/internal/lsp"

func problemsWithCode(
	diagnostics []lsp.Problem,
	code lsp.DiagnosticID,
) []lsp.Problem {
	var result []lsp.Problem
	for _, diagnostic := range diagnostics {
		if diagnostic.ID == code {
			result = append(result, diagnostic)
		}
	}
	return result
}
