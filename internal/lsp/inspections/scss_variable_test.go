package inspections

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestSCSSVariableInspectionContract(t *testing.T) {
	definition := NewSCSSVariable(nil).Definition()
	require.Equal(t, "scss.variable", definition.ID)
	require.Equal(t, []language.ID{language.SCSS}, definition.Languages)
	require.Len(t, definition.Problems, 1)
	require.Equal(t, diagnostics.SCSSVariableUnknownCode, definition.Problems[0].ID)
	require.Equal(t, protocol.DiagnosticSeverityError, definition.Problems[0].DefaultSeverity)
}
