package twig

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

const abstractControllerClass = "Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController"

// RenderBlockReference connects the template and block string arguments of
// AbstractController::renderBlock()/renderBlockView().
type RenderBlockReference struct {
	Template      string
	Block         string
	TemplateRange cst.TextRange
	BlockRange    cst.TextRange
	Call          *phpsyntax.Node
}

// TemplateBlock is one block declaration reachable from a concrete template,
// including declarations inherited through Twig extends.
type TemplateBlock struct {
	Name     string
	FilePath string
	Range    cst.TextRange
	Line     int
}

func RenderBlockReferencesInPHP(
	root *phpsyntax.Node,
) []RenderBlockReference {
	if root == nil {
		return nil
	}
	var result []RenderBlockReference
	for _, call := range phpquery.Nodes(
		root,
		phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedCall,
		phpsyntax.PhpFunctionCall,
	) {
		name := phpquery.CallMethodName(call)
		if !strings.EqualFold(name, "renderBlock") &&
			!strings.EqualFold(name, "renderBlockView") {
			continue
		}
		templateNode := renderBlockArgument(
			call,
			[]string{"view", "name", "template"},
			0,
		)
		blockNode := renderBlockArgument(call, []string{"block"}, 1)
		if templateNode == nil || blockNode == nil ||
			templateNode.Kind() != phpsyntax.PhpString ||
			blockNode.Kind() != phpsyntax.PhpString ||
			!phpStringIsStatic(templateNode) ||
			!phpStringIsStatic(blockNode) {
			continue
		}
		result = append(result, RenderBlockReference{
			Template: normalizeTemplateReference(
				phpquery.StringValue(templateNode),
			),
			Block: phpquery.StringValue(blockNode),
			TemplateRange: stringContentRange(
				templateNode.Text(),
				templateNode.Range(),
			),
			BlockRange: stringContentRange(
				blockNode.Text(),
				blockNode.Range(),
			),
			Call: call,
		})
	}
	return result
}

func RenderBlockReferenceAt(
	root *phpsyntax.Node,
	offset uint32,
) (RenderBlockReference, bool) {
	for _, reference := range RenderBlockReferencesInPHP(root) {
		if offset >= reference.BlockRange.Start &&
			offset <= reference.BlockRange.End {
			return reference, true
		}
	}
	return RenderBlockReference{}, false
}

func ValidateRenderBlockReference(
	ctx context.Context,
	reference RenderBlockReference,
	index *php.PHPIndex,
	content []byte,
) bool {
	return reference.Call != nil && index != nil &&
		index.IsMethodCalledOnClass(
			ctx,
			reference.Call,
			content,
			abstractControllerClass,
		)
}

func renderBlockArgument(
	call *phpsyntax.Node,
	names []string,
	fallback int,
) *phpsyntax.Node {
	for index, argument := range phpquery.Arguments(call) {
		name := phpquery.ArgumentName(argument)
		for _, candidate := range names {
			if strings.EqualFold(name, candidate) {
				return phpquery.ArgumentExpression(call, index)
			}
		}
	}
	argument := phpquery.Argument(call, fallback)
	if argument == nil || phpquery.ArgumentName(argument) != "" {
		return nil
	}
	return phpquery.ArgumentExpression(call, fallback)
}

func (idx *TwigIndexer) GetTemplateBlocks(
	templates ...string,
) ([]TemplateBlock, error) {
	if idx == nil {
		return nil, nil
	}
	visitedTemplates := make(map[string]struct{})
	seenBlocks := make(map[string]struct{})
	var result []TemplateBlock
	var visit func(string, int) error
	visit = func(template string, depth int) error {
		if depth > 32 {
			return nil
		}
		template = normalizeTemplateReference(template)
		if template == "" {
			return nil
		}
		if _, visited := visitedTemplates[template]; visited {
			return nil
		}
		visitedTemplates[template] = struct{}{}
		files, err := idx.GetTwigFilesByRelPath(template)
		if err != nil {
			return err
		}
		for _, file := range files {
			for _, block := range file.Blocks {
				key := filepath.Clean(file.Path) + "\x00" +
					block.NameRange.String() + "\x00" + block.Name
				if _, duplicate := seenBlocks[key]; duplicate {
					continue
				}
				seenBlocks[key] = struct{}{}
				result = append(result, TemplateBlock{
					Name:     block.Name,
					FilePath: file.Path,
					Range:    block.NameRange,
					Line:     block.Line,
				})
			}
			if file.ExtendsFile != "" {
				if err := visit(file.ExtendsFile, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, template := range templates {
		if err := visit(template, 0); err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if comparison := compareFold(
			result[left].Name,
			result[right].Name,
		); comparison != 0 {
			return comparison < 0
		}
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

// GetAllTemplateBlocks returns every indexed block declaration once, with its
// source path and exact name range. It backs project-wide symbol navigation.
func (idx *TwigIndexer) GetAllTemplateBlocks() ([]TemplateBlock, error) {
	if idx == nil {
		return nil, nil
	}
	files, err := idx.twigFileIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var result []TemplateBlock
	for _, file := range files {
		for _, block := range file.Blocks {
			key := filepath.Clean(file.Path) + "\x00" +
				block.NameRange.String() + "\x00" + block.Name
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, TemplateBlock{
				Name:     block.Name,
				FilePath: file.Path,
				Range:    block.NameRange,
				Line:     block.Line,
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if !strings.EqualFold(result[left].Name, result[right].Name) {
			return compareFold(
				result[left].Name,
				result[right].Name,
			) < 0
		}
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}
