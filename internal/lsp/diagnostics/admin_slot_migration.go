package diagnostics

import (
	"context"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

var (
	legacyTemplateTagPattern = regexp.MustCompile(`(?is)<template\b[^>]*>`)
	legacySlotPattern        = regexp.MustCompile(`(?i)\s+slot\s*=\s*["']([^"']+)["']`)
	legacySlotScopePattern   = regexp.MustCompile(`(?i)\s+slot-scope\s*=\s*["']([^"']*)["']`)
)

type AdminSlotMigrationAnalyzer struct{}

func NewAdminSlotMigrationAnalyzer() *AdminSlotMigrationAnalyzer {
	return &AdminSlotMigrationAnalyzer{}
}

func (*AdminSlotMigrationAnalyzer) Analyze(
	_ context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || !strings.Contains(document.URI, "Resources/app/administration") {
		return nil, nil
	}
	lowerURI := strings.ToLower(document.URI)
	if !strings.HasSuffix(lowerURI, ".twig") &&
		!strings.HasSuffix(lowerURI, ".vue") {
		return nil, nil
	}
	var result []lsp.Problem
	for _, location := range legacyTemplateTagPattern.FindAllStringIndex(document.Source, -1) {
		if document.SyntaxLanguage == language.Vue &&
			(document.SyntaxTree == nil || lsp.EffectiveSyntaxLanguage(
				language.Vue,
				document.SyntaxTree.Root.NodeAtOffset(uint32(location[0])),
			) != language.Twig) {
			continue
		}
		tag := document.Source[location[0]:location[1]]
		slot := legacySlotPattern.FindStringSubmatch(tag)
		if len(slot) < 2 {
			continue
		}
		scope := ""
		if match := legacySlotScopePattern.FindStringSubmatch(tag); len(match) > 1 {
			scope = match[1]
		}
		replacement := legacySlotPattern.ReplaceAllString(tag, "")
		replacement = legacySlotScopePattern.ReplaceAllString(replacement, "")
		directive := " #" + slot[1]
		if scope != "" {
			directive += `="` + scope + `"`
		}
		replacement = replacement[:len("<template")] + directive + replacement[len("<template"):]
		result = append(result, lsp.Problem{
			ID:       "admin.slot-syntax-deprecated",
			Range:    cst.TextRange{Start: uint32(location[0]), End: uint32(location[1])},
			Message:  "Use Vue v-slot shorthand instead of slot and slot-scope",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-lsp",
			Payload:  map[string]any{"replacement": replacement},
		})
	}
	return result, nil
}
