package codelens

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// SymfonyConfigCodeLensProvider is the portable counterpart of the reference
// plugin's PHP/YAML configuration line markers.
type SymfonyConfigCodeLensProvider struct {
	index *symfonyconfig.Index
}

func NewSymfonyConfigCodeLensProvider(
	index *symfonyconfig.Index,
) *SymfonyConfigCodeLensProvider {
	return &SymfonyConfigCodeLensProvider{index: index}
}

func (p *SymfonyConfigCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.index == nil || request == nil ||
		request.CodeLensParams == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".php", ".yaml", ".yml":
	default:
		return nil, nil
	}

	var result []protocol.CodeLens
	seen := make(map[string]struct{})
	rootTargetCache := make(map[string][]string)
	for element := range request.Document.SyntaxTree.Root.Descendants() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		node, ok := element.(*cst.Node)
		if !ok {
			continue
		}
		if extension == ".php" && node.Kind() != phpsyntax.PhpString ||
			extension != ".php" &&
				node.Kind() != yamlsyntax.YamlScalar {
			continue
		}
		if reference, found := symfonyconfig.RootReferenceAt(node); found {
			key := "root:" + reference.Range.String()
			if _, duplicate := seen[key]; !duplicate {
				seen[key] = struct{}{}
				cacheKey := strings.ToLower(reference.Name)
				targets, cached := rootTargetCache[cacheKey]
				if !cached {
					var targetErr error
					targets, targetErr = p.rootTargets(reference.Name)
					if targetErr != nil {
						return nil, targetErr
					}
					rootTargetCache[cacheKey] = targets
				}
				if len(targets) != 0 {
					title := "Open configuration declaration"
					if len(targets) > 1 {
						title = fmt.Sprintf(
							"Open %d configuration declarations",
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
			}
		}
		if reference, found := symfonyconfig.ResourceReferenceAt(node); found {
			key := "resource:" + reference.Range.String()
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			var targets []string
			for _, target := range symfonyconfig.ResourceFiles(
				path,
				reference.Path,
			) {
				targets = append(targets, relatedTarget(target, 1))
			}
			targets = uniqueRelatedTargets(targets)
			if len(targets) == 0 {
				continue
			}
			title := "Open configuration resource"
			if len(targets) > 1 {
				title = fmt.Sprintf(
					"Open %d configuration resources",
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
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Range.Start.Line !=
			result[right].Range.Start.Line {
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

func (p *SymfonyConfigCodeLensProvider) rootTargets(
	name string,
) ([]string, error) {
	roots, err := p.index.Roots(name)
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, root := range roots {
		targets = append(targets, relatedTarget(
			root.File,
			relatedSourceLine(root.File, root.Range.Start),
		))
	}
	return uniqueRelatedTargets(targets), nil
}

func (p *SymfonyConfigCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	lens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return lens, nil
}

var _ lsp.CodeLensProvider = (*SymfonyConfigCodeLensProvider)(nil)
