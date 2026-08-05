package definition

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type twigGuardTarget struct {
	path string
	line int
}

func (p *TwigDefinitionProvider) twigGuardDefinitions(
	request *lsp.DefinitionRequest,
) []protocol.Location {
	if p == nil || p.twigIndexer == nil || request == nil ||
		request.DefinitionParams == nil || request.LineIndex == nil {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	reference, found := twig.GuardReferenceAt(
		request.DocumentContent,
		offset,
	)
	if !found {
		return nil
	}

	var targets []twigGuardTarget
	switch reference.Kind {
	case twig.GuardFunction:
		values, _ := p.twigIndexer.GetTwigFunction(reference.Name)
		targets = make([]twigGuardTarget, 0, len(values))
		for _, value := range values {
			targets = append(targets, twigGuardTarget{
				path: value.FilePath,
				line: value.Line,
			})
		}
	case twig.GuardFilter:
		values, _ := p.twigIndexer.GetTwigFilter(reference.Name)
		targets = make([]twigGuardTarget, 0, len(values))
		for _, value := range values {
			targets = append(targets, twigGuardTarget{
				path: value.FilePath,
				line: value.Line,
			})
		}
	case twig.GuardTest:
		values, _ := p.twigIndexer.GetTwigTest(reference.Name)
		targets = make([]twigGuardTarget, 0, len(values))
		for _, value := range values {
			targets = append(targets, twigGuardTarget{
				path: value.FilePath,
				line: value.Line,
			})
		}
	}

	seen := make(map[string]struct{}, len(targets))
	locations := make([]protocol.Location, 0, len(targets))
	for _, target := range targets {
		if target.path == "" {
			continue
		}
		key := strings.ToLower(target.path) + ":" +
			strconv.Itoa(target.line)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		line := target.line - 1
		if line < 0 {
			line = 0
		}
		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(target.path),
			Range: protocol.Range{
				Start: protocol.Position{Line: line},
				End:   protocol.Position{Line: line},
			},
		})
	}
	return locations
}
