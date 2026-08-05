package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type ShopwareStoreComposerAnalyzer struct{}

func NewShopwareStoreComposerAnalyzer() *ShopwareStoreComposerAnalyzer {
	return &ShopwareStoreComposerAnalyzer{}
}

func (*ShopwareStoreComposerAnalyzer) Analyze(
	_ context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || filepath.Base(document.URI) != "composer.json" ||
		!strings.Contains(document.Source, "shopware-platform-plugin") {
		return nil, nil
	}
	var composer map[string]any
	if err := json.Unmarshal(document.Text, &composer); err != nil {
		return nil, nil
	}
	if composer["type"] != "shopware-platform-plugin" {
		return nil, nil
	}
	rootRange := cst.TextRange{End: uint32(len(document.Source))}
	problem := func(id, key, message string) lsp.Problem {
		rng := jsonKeyRange(document.Source, key)
		if rng.Len() == 0 {
			rng = rootRange
		}
		return lsp.Problem{
			ID: lsp.DiagnosticID(id), Range: rng, Message: message,
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-lsp",
		}
	}
	var result []lsp.Problem
	extra, _ := composer["extra"].(map[string]any)
	if extra == nil {
		result = append(result,
			problem("shopware.store.label", "extra", "Store: localized extra.label is required"),
			problem("shopware.store.description", "extra", "Store: localized extra.description is required"),
			problem("shopware.store.manufacturer-link", "extra", "Store: localized extra.manufacturerLink is required"),
			problem("shopware.store.support-link", "extra", "Store: localized extra.supportLink is required"),
		)
	} else {
		result = append(result, validateLocalizedComposerValue(
			document.Source, extra, "label", "shopware.store.label", false,
		)...)
		result = append(result, validateLocalizedComposerValue(
			document.Source, extra, "description", "shopware.store.description", true,
		)...)
		result = append(result, validateLocalizedComposerValue(
			document.Source, extra, "manufacturerLink", "shopware.store.manufacturer-link", false,
		)...)
		result = append(result, validateLocalizedComposerValue(
			document.Source, extra, "supportLink", "shopware.store.support-link", false,
		)...)
	}
	requirements, _ := composer["require"].(map[string]any)
	if requirements == nil || strings.TrimSpace(fmt.Sprint(requirements["shopware/core"])) == "" ||
		requirements["shopware/core"] == nil {
		result = append(result, problem(
			"shopware.store.require-core",
			"require",
			"Store: composer require must contain shopware/core",
		))
	}
	return result, nil
}

func validateLocalizedComposerValue(
	source string,
	extra map[string]any,
	key,
	id string,
	checkLength bool,
) []lsp.Problem {
	value, ok := extra[key].(map[string]any)
	if !ok {
		return []lsp.Problem{storeComposerProblem(
			source, id, key,
			"Store: extra."+key+" must be an object containing de-DE and en-GB",
		)}
	}
	var result []lsp.Problem
	for _, locale := range []string{"de-DE", "en-GB"} {
		text, _ := value[locale].(string)
		if text == "" {
			result = append(result, storeComposerProblem(
				source, id, key,
				"Store: extra."+key+" requires locale "+locale,
			))
			continue
		}
		if checkLength && (len([]rune(text)) < 150 || len([]rune(text)) > 185) {
			result = append(result, storeComposerProblem(
				source, id, locale,
				fmt.Sprintf("Store: %s description must contain 150 to 185 characters; got %d", locale, len([]rune(text))),
			))
		}
	}
	return result
}

func storeComposerProblem(source, id, key, message string) lsp.Problem {
	rng := jsonKeyRange(source, key)
	if rng.Len() == 0 {
		rng = cst.TextRange{End: uint32(len(source))}
	}
	return lsp.Problem{
		ID: lsp.DiagnosticID(id), Range: rng, Message: message,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-lsp",
	}
}

func jsonKeyRange(source, key string) cst.TextRange {
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*:`)
	location := pattern.FindStringIndex(source)
	if len(location) != 2 {
		return cst.TextRange{}
	}
	return cst.TextRange{Start: uint32(location[0] + 1), End: uint32(location[0] + 1 + len(key))}
}
