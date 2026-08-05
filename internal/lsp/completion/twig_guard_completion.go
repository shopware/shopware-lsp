package completion

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
)

type twigGuardCallable struct {
	name        string
	deprecated  bool
	deprecation string
}

func (p *TwigCompletionProvider) twigGuardCompletions(
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.twigIndexer == nil || request == nil ||
		request.CompletionParams == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	guard, found := twig.GuardCompletionAt(
		request.DocumentContent,
		offset,
	)
	if !found {
		return nil
	}
	if !guard.Callable {
		return twigGuardTypeCompletions(guard.Prefix)
	}

	callables := p.twigGuardCallables(guard.Kind)
	statuses := make(map[string]twigCallableCompletionDeprecation)
	displayNames := make(map[string]string)
	for _, callable := range callables {
		if strings.Contains(callable.name, "*") ||
			!strings.HasPrefix(
				strings.ToLower(callable.name),
				strings.ToLower(guard.Prefix),
			) {
			continue
		}
		key := strings.ToLower(callable.name)
		status, exists := statuses[key]
		if !exists {
			status.all = true
			displayNames[key] = callable.name
		}
		status.all = status.all && callable.deprecated
		if status.message == "" {
			status.message = callable.deprecation
		}
		statuses[key] = status
	}

	items := make([]protocol.CompletionItem, 0, len(statuses))
	for key, status := range statuses {
		name := displayNames[key]
		item := protocol.CompletionItem{
			Label:      name,
			InsertText: name,
			Kind:       int(protocol.FunctionCompletion),
			Detail:     "Twig " + guard.Kind.String(),
		}
		applyTwigCallableCompletionDeprecation(
			&item,
			guard.Kind.String(),
			status,
		)
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Label) <
			strings.ToLower(items[right].Label)
	})
	return items
}

func twigGuardTypeCompletions(prefix string) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for _, kind := range []twig.GuardKind{
		twig.GuardFunction,
		twig.GuardFilter,
		twig.GuardTest,
	} {
		name := kind.String()
		if !strings.HasPrefix(
			strings.ToLower(name),
			strings.ToLower(prefix),
		) {
			continue
		}
		items = append(items, protocol.CompletionItem{
			Label:      name,
			InsertText: name,
			Kind:       int(protocol.KeywordCompletion),
			Detail:     "Twig guard type",
		})
	}
	return items
}

func (p *TwigCompletionProvider) twigGuardCallables(
	kind twig.GuardKind,
) []twigGuardCallable {
	var result []twigGuardCallable
	switch kind {
	case twig.GuardFunction:
		values, _ := p.twigIndexer.GetAllTwigFunctions()
		result = make([]twigGuardCallable, 0, len(values))
		for _, value := range values {
			result = append(result, twigGuardCallable{
				name:        value.Name,
				deprecated:  value.Deprecated,
				deprecation: value.Deprecation,
			})
		}
	case twig.GuardFilter:
		values, _ := p.twigIndexer.GetAllTwigFilters()
		result = make([]twigGuardCallable, 0, len(values))
		for _, value := range values {
			result = append(result, twigGuardCallable{
				name:        value.Name,
				deprecated:  value.Deprecated,
				deprecation: value.Deprecation,
			})
		}
	case twig.GuardTest:
		values, _ := p.twigIndexer.GetAllTwigTests()
		result = make([]twigGuardCallable, 0, len(values))
		for _, value := range values {
			result = append(result, twigGuardCallable{
				name:        value.Name,
				deprecated:  value.Deprecated,
				deprecation: value.Deprecation,
			})
		}
	}
	return result
}
