package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigMacroCompletionProvider struct {
	index *twig.TwigIndexer
}

func NewTwigMacroCompletionProvider(
	index *twig.TwigIndexer,
) *TwigMacroCompletionProvider {
	return &TwigMacroCompletionProvider{index: index}
}

func (p *TwigMacroCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil || request.Root == nil ||
		strings.ToLower(filepath.Ext(request.TextDocument.URI)) != ".twig" ||
		twigquery.ClosestNodeOfKind(
			request.Node,
			twigsyntax.TwigVar,
		) == nil {
		return nil
	}
	path, _ := uriutil.Path(request.TextDocument.URI)
	current, found := twig.MacroCompletionAt(
		path,
		request.Root,
		request.Node,
	)
	if !found {
		return nil
	}
	if len(current.Templates) != 0 {
		return p.namespaceCompletions(path, request, current.Templates)
	}
	return p.directCompletions(path, request, current.Direct)
}

func (p *TwigMacroCompletionProvider) namespaceCompletions(
	path string,
	request *lsp.CompletionRequest,
	templates []string,
) []protocol.CompletionItem {
	var macros []twig.Macro
	for _, template := range templates {
		current, err := p.index.GetMacros(template)
		if err == nil {
			macros = append(macros, current...)
		}
	}
	macros = overlayCurrentMacros(path, request, templates, macros)
	seen := make(map[string]struct{})
	var items []protocol.CompletionItem
	for _, macro := range macros {
		key := strings.ToLower(macro.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, macroCompletionItem(macro.Name, macro))
	}
	sortCompletionItems(items)
	return items
}

func (p *TwigMacroCompletionProvider) directCompletions(
	path string,
	request *lsp.CompletionRequest,
	imports []twig.MacroImport,
) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for _, current := range imports {
		var definitions []twig.Macro
		for _, template := range current.Templates {
			macros, err := p.index.FindMacro(template, current.Name)
			if err == nil {
				definitions = append(definitions, macros...)
			}
		}
		definitions = overlayCurrentMacros(
			path,
			request,
			current.Templates,
			definitions,
		)
		var definition twig.Macro
		for _, macro := range definitions {
			if strings.EqualFold(macro.Name, current.Name) {
				definition = macro
				break
			}
		}
		items = append(
			items,
			macroCompletionItem(current.Alias, definition),
		)
	}
	sortCompletionItems(items)
	return items
}

func overlayCurrentMacros(
	path string,
	request *lsp.CompletionRequest,
	templates []string,
	persisted []twig.Macro,
) []twig.Macro {
	currentTemplates := twig.TemplateNames(path)
	relevant := false
	for _, target := range templates {
		if containsTemplateName(currentTemplates, target) {
			relevant = true
			break
		}
	}
	if !relevant {
		return persisted
	}
	result := persisted[:0]
	for _, macro := range persisted {
		if macro.FilePath != path {
			result = append(result, macro)
		}
	}
	return append(
		result,
		twig.MacrosInDocument(path, request.Root)...,
	)
}

func macroCompletionItem(label string, macro twig.Macro) protocol.CompletionItem {
	detail := "Twig macro"
	if macro.Name != "" {
		detail = macro.Signature()
	}
	return protocol.CompletionItem{
		Label:            label,
		Kind:             int(protocol.MethodCompletion),
		Detail:           detail,
		InsertText:       label + "($0)",
		InsertTextFormat: int(protocol.SnippetTextFormat),
	}
}

func containsTemplateName(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (p *TwigMacroCompletionProvider) GetTriggerCharacters() []string {
	return []string{"."}
}
