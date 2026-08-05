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
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigCodeLensProvider struct {
	twigIndexer *twig.TwigIndexer
}

func NewTwigCodeLensProvider(
	twigIndexer *twig.TwigIndexer,
) *TwigCodeLensProvider {
	return &TwigCodeLensProvider{twigIndexer: twigIndexer}
}

func (p *TwigCodeLensProvider) GetCodeLenses(
	ctx context.Context,
	request *lsp.CodeLensRequest,
) ([]protocol.CodeLens, error) {
	if p == nil || p.twigIndexer == nil || request == nil ||
		request.CodeLensParams == nil || request.Document == nil ||
		request.Document.SyntaxTree == nil ||
		request.Document.LineIndex == nil ||
		!strings.HasSuffix(
			strings.ToLower(request.TextDocument.URI),
			".twig",
		) {
		return nil, nil
	}
	filePath, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	current, err := twig.ParseTwigTree(
		filePath,
		request.Document.SyntaxTree,
		request.Document.LineIndex,
	)
	if err != nil || current == nil {
		return nil, err
	}

	templateNames := twig.TemplateNames(filePath)
	references, err := p.twigIndexer.GetTemplateReferences(
		templateNames...,
	)
	if err != nil {
		return nil, err
	}
	overrides, err := p.templateOverrides(filePath, templateNames)
	if err != nil {
		return nil, err
	}
	descendants, err := p.descendantTemplates(
		ctx,
		filePath,
		templateNames,
	)
	if err != nil {
		return nil, err
	}

	var result []protocol.CodeLens
	result = append(
		result,
		templateReferenceCodeLenses(filePath, references)...,
	)
	if targets := twigFileTargets(overrides); len(targets) != 0 {
		result = append(result, relatedLens(
			protocol.Range{},
			countedTwigTitle(
				"Open template override",
				"Open %d template overrides",
				len(targets),
			),
			targets,
		))
	}
	blockLenses, err := p.blockRelationshipCodeLenses(
		current,
		descendants,
		request.Document.LineIndex,
	)
	if err != nil {
		return nil, err
	}
	result = append(result, blockLenses...)
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

func templateReferenceCodeLenses(
	currentPath string,
	references []twig.TemplateReference,
) []protocol.CodeLens {
	var extending []string
	var usages []string
	for _, reference := range references {
		if filepath.Clean(reference.FilePath) == filepath.Clean(currentPath) ||
			!strings.EqualFold(
				filepath.Ext(reference.FilePath),
				".twig",
			) {
			continue
		}
		target := relatedTarget(
			reference.FilePath,
			relatedSourceLine(reference.FilePath, reference.Range.Start),
		)
		switch reference.Kind {
		case twig.TemplateExtendsReference:
			extending = append(extending, target)
		case twig.TemplateIncludeReference,
			twig.TemplateEmbedReference,
			twig.TemplateImportReference,
			twig.TemplateUseReference,
			twig.TemplateFormThemeReference,
			twig.TemplateSourceReference,
			twig.TemplateBlockReference:
			usages = append(usages, target)
		}
	}
	extending = uniqueRelatedTargets(extending)
	usages = uniqueRelatedTargets(usages)
	var result []protocol.CodeLens
	if len(extending) != 0 {
		result = append(result, relatedLens(
			protocol.Range{},
			countedTwigTitle(
				"Open extending template",
				"Open %d extending templates",
				len(extending),
			),
			extending,
		))
	}
	if len(usages) != 0 {
		result = append(result, relatedLens(
			protocol.Range{},
			countedTwigTitle(
				"Open template reference",
				"Open %d template references",
				len(usages),
			),
			usages,
		))
	}
	return result
}

func (p *TwigCodeLensProvider) templateOverrides(
	currentPath string,
	templateNames []string,
) ([]twig.TwigFile, error) {
	var result []twig.TwigFile
	for _, template := range templateNames {
		files, err := p.twigIndexer.GetTwigFilesByRelPath(template)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if filepath.Clean(file.Path) != filepath.Clean(currentPath) {
				result = append(result, file)
			}
		}
	}
	return uniqueTwigFiles(result), nil
}

func (p *TwigCodeLensProvider) descendantTemplates(
	ctx context.Context,
	currentPath string,
	templateNames []string,
) ([]twig.TwigFile, error) {
	visitedPaths := map[string]struct{}{
		filepath.Clean(currentPath): {},
	}
	visitedNames := make(map[string]struct{})
	queue := uniqueTwigTemplateNames(templateNames)
	var result []twig.TwigFile
	for depth := 0; depth < 32 && len(queue) != 0; depth++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var pending []string
		for _, name := range queue {
			key := strings.ToLower(strings.TrimSpace(name))
			if _, visited := visitedNames[key]; visited {
				continue
			}
			visitedNames[key] = struct{}{}
			pending = append(pending, name)
		}
		if len(pending) == 0 {
			break
		}
		references, err := p.twigIndexer.GetTemplateReferences(pending...)
		if err != nil {
			return nil, err
		}
		var next []string
		for _, reference := range references {
			if reference.Kind != twig.TemplateExtendsReference ||
				!strings.EqualFold(
					filepath.Ext(reference.FilePath),
					".twig",
				) {
				continue
			}
			pathKey := filepath.Clean(reference.FilePath)
			if _, visited := visitedPaths[pathKey]; visited {
				continue
			}
			visitedPaths[pathKey] = struct{}{}
			file, found, fileErr := p.twigIndexer.GetTwigFileByPath(
				reference.FilePath,
			)
			if fileErr != nil {
				return nil, fileErr
			}
			if !found {
				continue
			}
			result = append(result, file)
			next = append(next, twig.TemplateNames(file.Path)...)
		}
		queue = uniqueTwigTemplateNames(next)
	}
	return uniqueTwigFiles(result), nil
}

func (p *TwigCodeLensProvider) blockRelationshipCodeLenses(
	current *twig.TwigFile,
	descendants []twig.TwigFile,
	lineIndex *cst.LineIndex,
) ([]protocol.CodeLens, error) {
	var parentBlocks []twig.TemplateBlock
	if current.ExtendsFile != "" {
		var err error
		parentBlocks, err = p.twigIndexer.GetTemplateBlocks(
			current.ExtendsFile,
		)
		if err != nil {
			return nil, err
		}
	}
	descendantPaths := make(map[string]struct{}, len(descendants))
	for _, file := range descendants {
		descendantPaths[filepath.Clean(file.Path)] = struct{}{}
	}
	parentsByName := make(map[string][]string)
	for _, block := range parentBlocks {
		if filepath.Clean(block.FilePath) == filepath.Clean(current.Path) {
			continue
		}
		if _, descendant := descendantPaths[filepath.Clean(block.FilePath)]; descendant {
			continue
		}
		key := strings.ToLower(block.Name)
		parentsByName[key] = append(
			parentsByName[key],
			relatedTarget(block.FilePath, block.Line),
		)
	}
	implementationsByName := make(map[string][]string)
	for _, file := range descendants {
		if filepath.Clean(file.Path) == filepath.Clean(current.Path) {
			continue
		}
		for _, block := range file.Blocks {
			key := strings.ToLower(block.Name)
			implementationsByName[key] = append(
				implementationsByName[key],
				relatedTarget(file.Path, block.Line),
			)
		}
	}

	var result []protocol.CodeLens
	for _, block := range current.Blocks {
		key := strings.ToLower(block.Name)
		rng := relatedProtocolRange(block.NameRange, lineIndex)
		parents := uniqueRelatedTargets(parentsByName[key])
		if len(parents) != 0 {
			result = append(result, relatedLens(
				rng,
				countedTwigTitle(
					"Open parent block",
					"Open %d parent blocks",
					len(parents),
				),
				parents,
			))
		}
		implementations := uniqueRelatedTargets(
			implementationsByName[key],
		)
		if len(implementations) != 0 {
			result = append(result, relatedLens(
				rng,
				countedTwigTitle(
					"Open block implementation",
					"Open %d block implementations",
					len(implementations),
				),
				implementations,
			))
		}
	}
	return result, nil
}

func uniqueTwigFiles(files []twig.TwigFile) []twig.TwigFile {
	byPath := make(map[string]twig.TwigFile)
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		key := filepath.Clean(file.Path)
		if _, exists := byPath[key]; !exists {
			byPath[key] = file
		}
	}
	result := make([]twig.TwigFile, 0, len(byPath))
	for _, file := range byPath {
		result = append(result, file)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Path < result[right].Path
	})
	return result
}

func uniqueTwigTemplateNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func twigFileTargets(files []twig.TwigFile) []string {
	targets := make([]string, 0, len(files))
	for _, file := range files {
		targets = append(targets, relatedTarget(file.Path, 1))
	}
	return uniqueRelatedTargets(targets)
}

func countedTwigTitle(singular, plural string, count int) string {
	if count == 1 {
		return singular
	}
	return fmt.Sprintf(plural, count)
}

func (p *TwigCodeLensProvider) ResolveCodeLens(
	_ context.Context,
	codeLens *protocol.CodeLens,
) (*protocol.CodeLens, error) {
	return codeLens, nil
}
