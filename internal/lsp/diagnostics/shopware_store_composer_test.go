package diagnostics

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestShopwareStoreComposerAnalyzer(t *testing.T) {
	description := strings.Repeat("a", 150)
	document := lsp.NewTextDocument("file:///project/custom/plugins/Acme/composer.json", `{
    "type": "shopware-platform-plugin",
    "require": {"shopware/core": "~6.7"},
    "extra": {
        "label": {"de-DE": "Acme", "en-GB": "Acme"},
        "description": {"de-DE": "`+description+`", "en-GB": "short"},
        "manufacturerLink": {"de-DE": "https://example.com", "en-GB": "https://example.com"}
    }
}`, 1)
	problems, err := NewShopwareStoreComposerAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	require.Equal(t, lsp.DiagnosticID("shopware.store.description"), problems[0].ID)
	require.Equal(t, lsp.DiagnosticID("shopware.store.support-link"), problems[1].ID)
}
