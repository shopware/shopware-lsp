package completion

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type SymfonyConfigCompletionProvider struct {
	index *symfonyconfig.Index
}

func NewSymfonyConfigCompletionProvider(
	index *symfonyconfig.Index,
) *SymfonyConfigCompletionProvider {
	return &SymfonyConfigCompletionProvider{index: index}
}

func (p *SymfonyConfigCompletionProvider) GetCompletions(
	_ context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if p == nil || p.index == nil || request == nil ||
		request.Node == nil {
		return nil
	}
	if _, found := symfonyconfig.RootReferenceAt(request.Node); !found {
		return nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil || !strings.Contains(
		filepath.ToSlash(path),
		"/config/",
	) {
		return nil
	}
	names, err := p.index.Names()
	if err != nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		detail := "Symfony configuration root"
		if roots, rootsErr := p.index.Roots(name); rootsErr == nil &&
			len(roots) != 0 && roots[0].Class != "" {
			detail += " · " + roots[0].Class
		}
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   int(protocol.PropertyCompletion),
			Detail: detail,
		})
	}
	sortCompletionItems(items)
	return items
}

func (p *SymfonyConfigCompletionProvider) GetTriggerCharacters() []string {
	return []string{"'", "\""}
}
