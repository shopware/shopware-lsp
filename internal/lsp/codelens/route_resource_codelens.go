package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// RouteResourceCodeLensProvider is the portable counterpart of Symfony
// plugin routing-resource gutter markers.
type RouteResourceCodeLensProvider struct {
	index    *symfony.RouteIndexer
	resolver *symfony.RouteResourceResolver
}

func NewRouteResourceCodeLensProvider(
	index *symfony.RouteIndexer,
	phpIndexes ...*php.PHPIndex,
) *RouteResourceCodeLensProvider {
	var phpIndex *php.PHPIndex
	if len(phpIndexes) != 0 {
		phpIndex = phpIndexes[0]
	}
	return &RouteResourceCodeLensProvider{
		index:    index,
		resolver: symfony.NewRouteResourceResolver(phpIndex),
	}
}

func (p *RouteResourceCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || request == nil || request.CodeLensParams == nil ||
		request.Document == nil || request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php", ".yaml", ".yml", ".xml":
	default:
		return nil, nil
	}

	var result []protocol.CodeLens
	seen := make(map[string]struct{})
	currentReferences := symfony.RouteResourceReferences(
		request.Document.SyntaxTree.Root,
	)
	for _, reference := range currentReferences {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		key := reference.Range.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}

		var targets []string
		for _, target := range p.resolver.Files(path, reference) {
			targets = append(targets, relatedTarget(target, 1))
		}
		targets = uniqueRelatedTargets(targets)
		if len(targets) == 0 {
			continue
		}
		title := "Open routing resource"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d matching route files",
				len(targets),
			)
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				reference.Range,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}
	reverse, err := p.reverseResourceLens(path)
	if err != nil {
		return nil, err
	}
	if reverse != nil {
		result = append(result, *reverse)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		return result[left].Range.Start.Character <
			result[right].Range.Start.Character
	})
	return result, nil
}

func (p *RouteResourceCodeLensProvider) reverseResourceLens(
	path string,
) (*protocol.CodeLens, error) {
	if p.index == nil {
		return nil, nil
	}
	imports, err := p.index.GetRouteResourceImports()
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, resource := range imports {
		if filepath.Clean(resource.FilePath) == filepath.Clean(path) ||
			!p.resolver.Matches(
				resource.FilePath,
				path,
				resource.Reference(),
			) {
			continue
		}
		targets = append(targets, relatedTarget(
			resource.FilePath,
			relatedSourceLine(resource.FilePath, resource.Range.Start),
		))
	}
	targets = uniqueRelatedTargets(targets)
	if len(targets) == 0 {
		return nil, nil
	}
	title := "Open routing import"
	if len(targets) > 1 {
		title = fmt.Sprintf("Open %d routing imports", len(targets))
	}
	lens := relatedLens(protocol.Range{}, title, targets)
	return &lens, nil
}

func (p *RouteResourceCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}

var _ lsp.CodeLensProvider = (*RouteResourceCodeLensProvider)(nil)
