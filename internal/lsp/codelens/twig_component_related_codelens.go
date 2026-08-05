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
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// TwigComponentRelatedCodeLensProvider exposes the reference plugin's UX
// component gutter navigation as portable LSP code lenses.
type TwigComponentRelatedCodeLensProvider struct {
	index *twigcomponent.Index
	php   *php.PHPIndex
}

func NewTwigComponentRelatedCodeLensProvider(
	index *twigcomponent.Index,
	phpIndex *php.PHPIndex,
) *TwigComponentRelatedCodeLensProvider {
	return &TwigComponentRelatedCodeLensProvider{
		index: index,
		php:   phpIndex,
	}
}

func (p *TwigComponentRelatedCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.index == nil || request == nil ||
		request.CodeLensParams == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".twig",
		) {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}

	var result []protocol.CodeLens
	templateLens, found, err := p.templateCodeLens(ctx, path)
	if err != nil {
		return nil, err
	}
	if found {
		result = append(result, templateLens)
	}

	for _, usage := range twigcomponent.BlockUsagesInTwig(
		request.Document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		blocks, blockErr := p.index.Blocks(usage.Component)
		if blockErr != nil {
			return nil, blockErr
		}
		var targets []string
		for _, block := range blocks {
			if block.Name != usage.Name {
				continue
			}
			targets = append(
				targets,
				relatedTarget(block.File, block.Line),
			)
		}
		targets = uniqueRelatedTargets(targets)
		if len(targets) == 0 {
			continue
		}
		title := "Open component block"
		if len(targets) > 1 {
			title = fmt.Sprintf(
				"Open %d component blocks",
				len(targets),
			)
		}
		result = append(result, relatedLens(
			relatedProtocolRange(
				usage.Range,
				request.Document.LineIndex,
			),
			title,
			targets,
		))
	}

	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line != result[right].Range.Start.Line {
			return result[left].Range.Start.Line <
				result[right].Range.Start.Line
		}
		if result[left].Range.Start.Character !=
			result[right].Range.Start.Character {
			return result[left].Range.Start.Character <
				result[right].Range.Start.Character
		}
		return result[left].Command.Title < result[right].Command.Title
	})
	return result, nil
}

func (p *TwigComponentRelatedCodeLensProvider) templateCodeLens(
	ctx context.Context,
	path string,
) (protocol.CodeLens, bool, error) {
	components, err := p.index.ComponentsForTemplate(path)
	if err != nil {
		return protocol.CodeLens{}, false, err
	}
	var targets []string
	names := make(map[string]struct{}, len(components))
	for _, component := range components {
		if ctx.Err() != nil {
			return protocol.CodeLens{}, false, ctx.Err()
		}
		if component.Name != "" {
			names[component.Name] = struct{}{}
		}
		if component.Class == "" || p.php == nil {
			continue
		}
		class, found := p.php.FindClass(component.Class)
		if !found {
			continue
		}
		targets = append(
			targets,
			relatedTarget(
				class.Path,
				relatedSourceLine(
					class.Path,
					class.SelectionRange.Start,
				),
			),
		)
	}
	for name := range names {
		usages, usageErr := p.index.Usages(name)
		if usageErr != nil {
			return protocol.CodeLens{}, false, usageErr
		}
		for _, usage := range usages {
			if filepath.Clean(usage.File) == filepath.Clean(path) {
				continue
			}
			targets = append(
				targets,
				relatedTarget(
					usage.File,
					relatedSourceLine(
						usage.File,
						usage.Range.Start,
					),
				),
			)
		}
	}
	targets = uniqueRelatedTargets(targets)
	if len(targets) == 0 {
		return protocol.CodeLens{}, false, nil
	}
	return relatedLens(
		protocol.Range{},
		"Open UX component",
		targets,
	), true, nil
}

func (p *TwigComponentRelatedCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}
