package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThemeAnalyzer_twigDiagnostics(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []lsp.Problem
	}{
		{
			name:     "valid icon with default pack",
			content:  `{% sw_icon 'heart' %}`,
			expected: []lsp.Problem{},
		},
		{
			name:    "invalid icon with default pack",
			content: `{% sw_icon 'nonexistent-icon' %}`,
			expected: []lsp.Problem{
				{
					Range:    cst.TextRange{Start: 11, End: 29},
					Message:  "Icon 'nonexistent-icon' not found in pack 'default'",
					Source:   "shopware",
					Severity: protocol.DiagnosticSeverityError,
					ID:       "theme.icon.missing",
					Payload: map[string]any{
						"iconName": "nonexistent-icon",
						"pack":     "default",
					},
				},
			},
		},
		{
			name:    "invalid icon with custom pack",
			content: `{% sw_icon 'missing' {'pack': 'custom'} %}`,
			expected: []lsp.Problem{
				{
					Range:    cst.TextRange{Start: 11, End: 20},
					Message:  "Icon 'missing' not found in pack 'custom'",
					Source:   "shopware",
					Severity: protocol.DiagnosticSeverityError,
					ID:       "theme.icon.missing",
					Payload: map[string]any{
						"iconName": "missing",
						"pack":     "custom",
					},
				},
			},
		},
		{
			name: "multiple icons with mixed validity",
			content: `{% sw_icon 'heart' %}
{% sw_icon 'invalid-icon' %}
{% sw_icon 'home' %}`,
			expected: []lsp.Problem{
				{
					Range:    cst.TextRange{Start: 33, End: 47},
					Message:  "Icon 'invalid-icon' not found in pack 'default'",
					Source:   "shopware",
					Severity: protocol.DiagnosticSeverityError,
					ID:       "theme.icon.missing",
					Payload: map[string]any{
						"iconName": "invalid-icon",
						"pack":     "default",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create temp directory with mock icon structure
			tempDir := t.TempDir()

			// Create mock icon provider that returns specific icons
			mockIconProvider := &mockIconProvider{
				icons: map[string]map[string]string{
					"default": {
						"heart": tempDir + "/icon/default/heart.svg",
						"home":  tempDir + "/icon/default/home.svg",
					},
				},
			}

			provider := &ThemeAnalyzer{
				iconProvider: mockIconProvider,
			}

			diagnostics, err := provider.Analyze(ctx, diagnosticsDocument("test.twig", []byte(tt.content)))
			require.NoError(t, err)

			assert.Equal(t, len(tt.expected), len(diagnostics))
			for i, expected := range tt.expected {
				assert.Equal(t, expected.Range, diagnostics[i].Range)
				assert.Equal(t, expected.Message, diagnostics[i].Message)
				assert.Equal(t, expected.Severity, diagnostics[i].Severity)
				assert.Equal(t, expected.ID, diagnostics[i].ID)
				assert.Equal(t, expected.Payload, diagnostics[i].Payload)
			}
		})
	}
}

// mockIconProvider is a test mock for theme.IconProvider
type mockIconProvider struct {
	icons map[string]map[string]string
}

func (m *mockIconProvider) GetIconPacks() []string {
	packs := make([]string, 0, len(m.icons))
	for pack := range m.icons {
		packs = append(packs, pack)
	}
	return packs
}

func (m *mockIconProvider) GetIcons(pack string) []string {
	icons := make([]string, 0)
	if packIcons, ok := m.icons[pack]; ok {
		for icon := range packIcons {
			icons = append(icons, icon)
		}
	}
	return icons
}

func (m *mockIconProvider) GetIcon(pack, icon string) string {
	if packIcons, ok := m.icons[pack]; ok {
		return packIcons[icon]
	}
	return ""
}
