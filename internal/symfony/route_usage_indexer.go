package symfony

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/textutil"
)

var routeUsagePHPCandidateMatcher = textutil.NewFoldASCIIMatcher(
	"generate",
	"redirecttoroute",
)

type RouteUsage struct {
	Name string
	Path string
	File string
	Line int
	// Lines preserves every occurrence of one route key in a file. Line is
	// retained as the first occurrence for cache compatibility.
	Lines []int
}

type RouteUsageIndexer struct {
	dataIndexer       *indexer.DataIndexer[RouteUsage]
	controllerIndexer *indexer.DataIndexer[ControllerUsageGroup]
	phpIndex          *php.PHPIndex
}

type preparedRouteUsages struct {
	routes      map[string]map[string]RouteUsage
	controllers map[string]map[string]ControllerUsageGroup
}

var _ indexer.PreparingIndexer = (*RouteUsageIndexer)(nil)

func NewRouteUsageIndexer(configDir string, stores ...*indexer.Store) (*RouteUsageIndexer, error) {
	dataIndexer, err := indexer.NewRepository[RouteUsage](filepath.Join(configDir, "route_usage.db"), "symfony.route_usage", stores...)
	if err != nil {
		return nil, err
	}
	controllerIndexer, err := indexer.NewRepository[ControllerUsageGroup](
		filepath.Join(configDir, "twig_controller_usage.db"),
		"symfony.twig_controller_usage",
		stores...,
	)
	if err != nil {
		_ = dataIndexer.Close()
		return nil, err
	}
	return &RouteUsageIndexer{
		dataIndexer:       dataIndexer,
		controllerIndexer: controllerIndexer,
	}, nil
}

func (idx *RouteUsageIndexer) SetPHPIndex(phpIndex *php.PHPIndex) {
	if idx != nil {
		idx.phpIndex = phpIndex
	}
}

// ControllerUsage is one static Twig controller() call.
type ControllerUsage struct {
	Controller string
	Target     string
	Method     string
	File       string
	Range      cst.TextRange
}

// ControllerUsageGroup preserves multiple equal references in one file while
// retaining a directly queryable controller key in the generic repository.
type ControllerUsageGroup struct {
	Controller string
	Usages     []ControllerUsage
}

func (idx *RouteUsageIndexer) ID() string {
	return "symfony.route_usage"
}

func (idx *RouteUsageIndexer) Index(file *indexer.ParsedFile) error {
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}

func (idx *RouteUsageIndexer) Prepare(
	file *indexer.ParsedFile,
) (any, error) {
	switch file.Extension() {
	case ".twig":
		routes, controllers := idx.prepareTwig(file)
		return &preparedRouteUsages{
			routes:      routes,
			controllers: controllers,
		}, nil
	case ".php":
		return &preparedRouteUsages{routes: idx.preparePHP(file)}, nil
	default:
		return (*preparedRouteUsages)(nil), nil
	}
}

func (idx *RouteUsageIndexer) IndexPrepared(
	file *indexer.ParsedFile,
	value any,
) error {
	prepared, ok := value.(*preparedRouteUsages)
	if !ok {
		return fmt.Errorf("prepared route usages are required for %s", file.Path)
	}
	if prepared == nil {
		return nil
	}
	if err := idx.dataIndexer.BatchSaveItemsIn(
		file.Mutation(),
		prepared.routes,
	); err != nil {
		return err
	}
	if prepared.controllers == nil {
		return nil
	}
	return idx.controllerIndexer.BatchSaveItemsIn(
		file.Mutation(),
		prepared.controllers,
	)
}

func (idx *RouteUsageIndexer) preparePHP(
	file *indexer.ParsedFile,
) map[string]map[string]RouteUsage {
	path := file.Path
	batchSave := map[string]map[string]RouteUsage{path: {}}
	if !routeUsagePHPCandidateMatcher.ContainsBytes(file.Content) {
		return batchSave
	}
	root := file.SyntaxTree().Root
	lineIndex := file.LineIndex()
	validationContext := context.Background()
	if idx.phpIndex != nil {
		validationContext = idx.phpIndex.AddParsedFileContext(
			validationContext,
			file,
			root,
		)
	}
	phpquery.Visit(root, func(call *phpsyntax.Node) bool {
		match := phpquery.StringArgument(call, 0)
		if !IsPHPRouteNameInContext(validationContext, match) {
			return true
		}
		name := phpquery.StringValue(match)
		if match == nil || name == "" {
			return true
		}
		if _, ok := batchSave[path]; !ok {
			batchSave[path] = make(map[string]RouteUsage)
		}
		line, _ := lineIndex.Position(match.RangeTrimmedTrivia().Start)
		addRouteUsage(batchSave[path], name, RouteUsage{
			Name: name,
			File: path,
			Line: int(line) + 1,
		})
		return true
	},
		phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedCall,
		phpsyntax.PhpFunctionCall,
	)

	return batchSave
}

func containsFoldASCIIBytes(content []byte, needle string) bool {
	if needle == "" {
		return true
	}
	if len(content) < len(needle) {
		return false
	}
	first := lowerASCIIByte(needle[0])
	maxStart := len(content) - len(needle)
	for offset := 0; offset <= maxStart; {
		index := bytes.IndexByte(content[offset:maxStart+1], first)
		if first >= 'a' && first <= 'z' {
			upperIndex := bytes.IndexByte(
				content[offset:maxStart+1],
				first-'a'+'A',
			)
			if index < 0 || upperIndex >= 0 && upperIndex < index {
				index = upperIndex
			}
		}
		if index < 0 {
			return false
		}
		start := offset + index
		matches := true
		for position := range len(needle) {
			if lowerASCIIByte(content[start+position]) !=
				lowerASCIIByte(needle[position]) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
		offset = start + 1
	}
	return false
}

func lowerASCIIByte(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func (idx *RouteUsageIndexer) prepareTwig(
	file *indexer.ParsedFile,
) (
	map[string]map[string]RouteUsage,
	map[string]map[string]ControllerUsageGroup,
) {
	path := file.Path
	tree := file.SyntaxTree()
	lineIndex := file.LineIndex()
	batchSave := map[string]map[string]RouteUsage{path: {}}

	for _, literal := range twigquery.StringArgumentsInFunctions(tree.Root, "seoUrl", "url", "path") {
		name := twigquery.StringValue(literal)
		if name == "" {
			continue
		}

		if batchSave[path] == nil {
			batchSave[path] = make(map[string]RouteUsage)
		}
		start := literal.RangeTrimmedTrivia().Start
		line, _ := lineIndex.Position(start)
		addRouteUsage(batchSave[path], name, RouteUsage{
			Name: name,
			File: path,
			Line: int(line) + 1,
		})
	}
	for _, reference := range TwigRouteComparisonReferences(tree.Root) {
		if reference.Value == "" || reference.Node == nil {
			continue
		}
		line, _ := lineIndex.Position(reference.Range.Start)
		addRouteUsage(batchSave[path], reference.Value, RouteUsage{
			Name: reference.Value,
			File: path,
			Line: int(line) + 1,
		})
	}
	for _, reference := range TwigHTMLRouteReferences(tree.Root) {
		routePath := RouteURLPath(reference.Value)
		if routePath == "" || reference.Node == nil {
			continue
		}
		line, _ := lineIndex.Position(
			reference.Node.RangeTrimmedTrivia().Start,
		)
		key := "@url:" + routePath
		addRouteUsage(batchSave[path], key, RouteUsage{
			Path: routePath,
			File: path,
			Line: int(line) + 1,
		})
	}

	groups := make(map[string]ControllerUsageGroup)
	for _, reference := range TwigControllerReferences(tree.Root) {
		key := ControllerReferenceKey(reference.ControllerReference)
		group := groups[key]
		group.Controller = key
		group.Usages = append(group.Usages, ControllerUsage{
			Controller: reference.Value,
			Target:     reference.Target,
			Method:     reference.Method,
			File:       path,
			Range:      reference.Range,
		})
		groups[key] = group
	}
	return batchSave, map[string]map[string]ControllerUsageGroup{
		path: groups,
	}
}

func (idx *RouteUsageIndexer) RemovedFiles(paths []string) error {
	return errors.Join(
		idx.dataIndexer.BatchDeleteByFilePaths(paths),
		idx.controllerIndexer.BatchDeleteByFilePaths(paths),
	)
}

func (idx *RouteUsageIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return errors.Join(
		idx.dataIndexer.BatchDeleteByFilePathsIn(mutation, paths),
		idx.controllerIndexer.BatchDeleteByFilePathsIn(mutation, paths),
	)
}

func (idx *RouteUsageIndexer) Clear() error {
	return errors.Join(
		idx.dataIndexer.Clear(),
		idx.controllerIndexer.Clear(),
	)
}

func (idx *RouteUsageIndexer) ClearIn(mutation *indexer.Mutation) error {
	return errors.Join(
		idx.dataIndexer.ClearIn(mutation),
		idx.controllerIndexer.ClearIn(mutation),
	)
}

func (idx *RouteUsageIndexer) Close() error {
	return errors.Join(
		idx.dataIndexer.Close(),
		idx.controllerIndexer.Close(),
	)
}

func (idx *RouteUsageIndexer) GetRoute(name string) ([]RouteUsage, error) {
	values, err := idx.dataIndexer.GetValues(name)
	if err != nil {
		return nil, err
	}
	return expandRouteUsageLines(values), nil
}

func (idx *RouteUsageIndexer) GetHTMLRouteUsages() ([]RouteUsage, error) {
	values, err := idx.dataIndexer.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []RouteUsage
	for _, usage := range expandRouteUsageLines(values) {
		if usage.Path != "" {
			result = append(result, usage)
		}
	}
	return result, nil
}

func addRouteUsage(
	usages map[string]RouteUsage,
	key string,
	usage RouteUsage,
) {
	existing, found := usages[key]
	if !found {
		usage.Lines = []int{usage.Line}
		usages[key] = usage
		return
	}
	if existing.Name == "" {
		existing.Name = usage.Name
	}
	if existing.Path == "" {
		existing.Path = usage.Path
	}
	if existing.File == "" {
		existing.File = usage.File
	}
	if existing.Line <= 0 {
		existing.Line = usage.Line
	}
	if len(existing.Lines) == 0 && existing.Line > 0 {
		existing.Lines = append(existing.Lines, existing.Line)
	}
	if !slices.Contains(existing.Lines, usage.Line) {
		existing.Lines = append(existing.Lines, usage.Line)
	}
	usages[key] = existing
}

func expandRouteUsageLines(values []RouteUsage) []RouteUsage {
	var result []RouteUsage
	for _, usage := range values {
		lines := usage.Lines
		if len(lines) == 0 && usage.Line > 0 {
			lines = []int{usage.Line}
		}
		for _, line := range lines {
			occurrence := usage
			occurrence.Line = line
			occurrence.Lines = nil
			result = append(result, occurrence)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		if result[left].Line != result[right].Line {
			return result[left].Line < result[right].Line
		}
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].Path < result[right].Path
	})
	return result
}

func (idx *RouteUsageIndexer) GetControllerUsages(
	reference ControllerReference,
) ([]ControllerUsage, error) {
	if idx == nil || idx.controllerIndexer == nil {
		return nil, nil
	}
	groups, err := idx.controllerIndexer.GetValues(
		ControllerReferenceKey(reference),
	)
	if err != nil {
		return nil, err
	}
	var result []ControllerUsage
	for _, group := range groups {
		result = append(result, group.Usages...)
	}
	sortControllerUsages(result)
	return result, nil
}

func (idx *RouteUsageIndexer) GetAllControllerUsages() (
	[]ControllerUsage,
	error,
) {
	if idx == nil || idx.controllerIndexer == nil {
		return nil, nil
	}
	groups, err := idx.controllerIndexer.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []ControllerUsage
	for _, group := range groups {
		result = append(result, group.Usages...)
	}
	sortControllerUsages(result)
	return result, nil
}

// ControllerUsagesForMethod resolves aliases and service IDs so PHP-side
// references and code lenses find every equivalent Twig spelling.
func (idx *RouteUsageIndexer) ControllerUsagesForMethod(
	className,
	methodName string,
	services *ServiceIndex,
	phpIndex *php.PHPIndex,
) ([]ControllerUsage, error) {
	if className == "" || methodName == "" || phpIndex == nil {
		return nil, nil
	}
	usages, err := idx.GetAllControllerUsages()
	if err != nil {
		return nil, err
	}
	className = strings.TrimLeft(className, `\`)
	var result []ControllerUsage
	seen := make(map[string]struct{})
	for _, usage := range usages {
		reference := ControllerReference{
			Value:  usage.Controller,
			Target: usage.Target,
			Method: usage.Method,
		}
		resolution, resolveErr := ResolveControllerReference(
			reference,
			services,
			phpIndex,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !resolution.MethodDeclared ||
			!strings.EqualFold(
				strings.TrimLeft(resolution.Class.FullyQualified, `\`),
				className,
			) ||
			!strings.EqualFold(resolution.Method.Name, methodName) {
			continue
		}
		key := fmt.Sprintf(
			"%s:%d:%d",
			usage.File,
			usage.Range.Start,
			usage.Range.End,
		)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, usage)
	}
	return result, nil
}

func sortControllerUsages(usages []ControllerUsage) {
	sort.Slice(usages, func(left, right int) bool {
		if usages[left].File != usages[right].File {
			return usages[left].File < usages[right].File
		}
		if usages[left].Range.Start != usages[right].Range.Start {
			return usages[left].Range.Start < usages[right].Range.Start
		}
		return usages[left].Range.End < usages[right].Range.End
	})
}
