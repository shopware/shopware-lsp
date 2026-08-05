package documentlink

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// RelatedProvider exposes the reference plugin's file-navigation targets as
// native editor links. Ambiguous references are intentionally left to
// go-to-definition and multi-target code lenses.
type RelatedProvider struct {
	twig          *twig.TwigIndexer
	configuration *symfonyconfig.Index
	routes        *symfony.RouteResourceResolver
}

func NewRelatedProvider(
	twigIndex *twig.TwigIndexer,
	configuration *symfonyconfig.Index,
	phpIndex *php.PHPIndex,
) *RelatedProvider {
	return &RelatedProvider{
		twig:          twigIndex,
		configuration: configuration,
		routes:        symfony.NewRouteResourceResolver(phpIndex),
	}
}

func (p *RelatedProvider) GetDocumentLinks(
	ctx context.Context,
	request *lsp.DocumentLinkRequest,
) ([]protocol.DocumentLink, error) {
	if ctx.Err() != nil || p == nil || request == nil ||
		request.DocumentLinkParams == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.SyntaxTree.Root == nil ||
		request.Document.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, nil
	}

	var result []protocol.DocumentLink
	seen := make(map[string]struct{})
	add := func(rng cst.TextRange, targets []string, tooltip string) {
		target, found := unambiguousDocumentLinkTarget(path, targets)
		if !found {
			return
		}
		key := rng.String() + "\x00" + target
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		result = append(result, protocol.DocumentLink{
			Range:   documentLinkRange(request.Document.LineIndex, rng),
			Target:  uriutil.FileURI(target),
			Tooltip: tooltip,
		})
	}

	if p.twig != nil {
		references := documentTemplateReferences(
			path,
			request.Document.SyntaxLanguage,
			request.Document.SyntaxTree.Root,
		)
		for _, reference := range references {
			if ctx.Err() != nil {
				return result, nil
			}
			files, findErr := p.twig.GetTwigFilesByRelPath(
				reference.Template,
			)
			if findErr != nil {
				return nil, findErr
			}
			targets := make([]string, 0, len(files))
			for _, file := range files {
				targets = append(targets, file.Path)
			}
			add(
				reference.Range,
				targets,
				fmt.Sprintf(
					"Open %s template %q",
					reference.Kind,
					reference.Template,
				),
			)
		}
	}

	if p.routes != nil {
		for _, reference := range symfony.RouteResourceReferences(
			request.Document.SyntaxTree.Root,
		) {
			if ctx.Err() != nil {
				return result, nil
			}
			add(
				reference.Range,
				p.routes.Files(path, reference),
				"Open routing resource",
			)
		}
	}

	if request.Document.SyntaxLanguage == language.PHP ||
		request.Document.SyntaxLanguage == language.YAML {
		for element := range request.Document.SyntaxTree.Root.Descendants() {
			if ctx.Err() != nil {
				return result, nil
			}
			node, ok := element.(*cst.Node)
			if !ok ||
				request.Document.SyntaxLanguage == language.PHP &&
					node.Kind() != phpsyntax.PhpString ||
				request.Document.SyntaxLanguage == language.YAML &&
					node.Kind() != yamlsyntax.YamlScalar {
				continue
			}
			if reference, found :=
				symfonyconfig.ResourceReferenceAt(node); found {
				add(
					reference.Range,
					symfonyconfig.ResourceFiles(path, reference.Path),
					"Open configuration resource",
				)
			}
			if p.configuration == nil {
				continue
			}
			reference, found := symfonyconfig.RootReferenceAt(node)
			if !found {
				continue
			}
			roots, rootErr := p.configuration.Roots(reference.Name)
			if rootErr != nil {
				return nil, rootErr
			}
			targets := make([]string, 0, len(roots))
			for _, root := range roots {
				targets = append(targets, root.File)
			}
			add(
				reference.Range,
				targets,
				fmt.Sprintf(
					"Open %q configuration declaration",
					reference.Name,
				),
			)
		}
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
		return result[left].Target < result[right].Target
	})
	return result, nil
}

func documentTemplateReferences(
	path string,
	languageID language.ID,
	root *cst.Node,
) []twig.TemplateReference {
	switch languageID {
	case language.Twig:
		return twig.TwigTemplateReferences(path, root)
	case language.PHP:
		return twig.PHPTemplateReferences(path, root)
	default:
		return nil
	}
}

func unambiguousDocumentLinkTarget(
	currentPath string,
	candidates []string,
) (string, bool) {
	currentPath = filepath.Clean(currentPath)
	unique := make(map[string]string)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		clean := filepath.Clean(candidate)
		if clean == currentPath {
			continue
		}
		unique[clean] = candidate
	}
	if len(unique) != 1 {
		return "", false
	}
	for _, candidate := range unique {
		return candidate, true
	}
	return "", false
}

func documentLinkRange(
	lineIndex *cst.LineIndex,
	rng cst.TextRange,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

var _ lsp.DocumentLinkProvider = (*RelatedProvider)(nil)
