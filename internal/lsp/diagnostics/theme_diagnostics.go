package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/theme"
)

// IconProvider is an interface for getting icon information
type IconProvider interface {
	GetIconPacks() []string
	GetIcons(pack string) []string
	GetIcon(pack, icon string) string
}

type ThemeAnalyzer struct {
	iconProvider IconProvider
}

func NewThemeAnalyzer(projectRoot string, extensionIndexer *extension.ExtensionIndexer) *ThemeAnalyzer {
	return &ThemeAnalyzer{
		iconProvider: theme.NewIconProvider(projectRoot, extensionIndexer),
	}
}

func (t *ThemeAnalyzer) Analyze(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".twig":
		return t.twigDiagnostics(ctx, document)
	default:
		return []lsp.Problem{}, nil
	}
}

func (t *ThemeAnalyzer) twigDiagnostics(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	var diagnostics []lsp.Problem

	// Find all sw_icon tags
	for tagNode := range twigquery.IterateNodes(
		document.SyntaxTree.Root,
		twigsyntax.ShopwareIcon,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		// Find the first string that's not in a pair (the icon name)
		var iconNameNode *twigsyntax.Node
		for literal := range twigquery.IterateNodes(tagNode, twigsyntax.TwigLiteralString) {
			if twigquery.ClosestNodeOfKind(literal, twigsyntax.TwigLiteralHashPair) == nil {
				iconNameNode = literal
				break
			}
		}

		if iconNameNode == nil {
			continue
		}

		iconName := twigquery.StringValue(iconNameNode)

		// Extract configuration from the tag
		cfg := twigquery.HashStringMap(tagNode)
		pack, ok := cfg["pack"]
		if !ok {
			pack = "default"
		}

		// Check if the icon exists
		iconPath := t.iconProvider.GetIcon(pack, iconName)
		if iconPath == "" {
			diagnostics = append(diagnostics, lsp.Problem{
				Range:    iconNameNode.RangeTrimmedTrivia(),
				Message:  fmt.Sprintf("Icon '%s' not found in pack '%s'", iconName, pack),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityError,
				ID:       "theme.icon.missing",
				Payload: map[string]any{
					"iconName": iconName,
					"pack":     pack,
				},
			})
		}
	}

	return diagnostics, nil
}
