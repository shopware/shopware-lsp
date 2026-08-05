package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const ListTwigTemplateUsagesCommand = "shopware/symfony/analytics/twig/templateUsages"

type TwigTemplateUsageCatalogProvider struct {
	root       string
	twig       *twig.TwigIndexer
	php        *php.PHPIndex
	routes     *symfony.RouteIndexer
	services   *symfony.ServiceIndex
	components *twigcomponent.Index
}

func NewTwigTemplateUsageCatalogProvider(
	root string,
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
	routes *symfony.RouteIndexer,
	services *symfony.ServiceIndex,
	components *twigcomponent.Index,
) *TwigTemplateUsageCatalogProvider {
	return &TwigTemplateUsageCatalogProvider{
		root:       filepath.Clean(root),
		twig:       twigIndex,
		php:        phpIndex,
		routes:     routes,
		services:   services,
		components: components,
	}
}

type TwigTemplateUsageCatalogRequest struct {
	Template string `json:"template,omitempty"`
	FileGlob string `json:"fileGlob,omitempty"`
}

type TwigTemplateSourceLocation struct {
	FileURI string `json:"fileUri"`
	Line    int    `json:"line,omitempty"`
}

type TwigTemplateRouteEntry struct {
	Name    string   `json:"name"`
	Path    string   `json:"path,omitempty"`
	Methods []string `json:"methods,omitempty"`
}

type TwigTemplateControllerUsage struct {
	Controller string                   `json:"controller"`
	FileURI    string                   `json:"fileUri,omitempty"`
	Line       int                      `json:"line,omitempty"`
	Routes     []TwigTemplateRouteEntry `json:"routes,omitempty"`
}

type TwigTemplateReferenceUsage struct {
	FileURI string `json:"fileUri"`
	Line    int    `json:"line,omitempty"`
}

type TwigTemplateComponentUsage struct {
	Component string `json:"component"`
	Syntax    string `json:"syntax,omitempty"`
	FileURI   string `json:"fileUri"`
	Line      int    `json:"line,omitempty"`
}

type TwigTemplateUsageCatalogEntry struct {
	Template    string                        `json:"template"`
	Files       []TwigTemplateSourceLocation  `json:"files,omitempty"`
	Controllers []TwigTemplateControllerUsage `json:"controllers,omitempty"`
	Includes    []TwigTemplateReferenceUsage  `json:"includes,omitempty"`
	Embeds      []TwigTemplateReferenceUsage  `json:"embeds,omitempty"`
	Extends     []TwigTemplateReferenceUsage  `json:"extends,omitempty"`
	Imports     []TwigTemplateReferenceUsage  `json:"imports,omitempty"`
	Uses        []TwigTemplateReferenceUsage  `json:"uses,omitempty"`
	FormThemes  []TwigTemplateReferenceUsage  `json:"formThemes,omitempty"`
	Components  []TwigTemplateComponentUsage  `json:"components,omitempty"`
}

func (p *TwigTemplateUsageCatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListTwigTemplateUsagesCommand: p.list,
	}
}

func (p *TwigTemplateUsageCatalogProvider) list(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request TwigTemplateUsageCatalogRequest
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return nil, fmt.Errorf(
			"at least one of template or fileGlob is required",
		)
	}
	if err := json.Unmarshal(*raw, &request); err != nil {
		return nil, fmt.Errorf(
			"invalid twig template usage request: %w",
			err,
		)
	}
	return p.Catalog(ctx, request)
}

func (p *TwigTemplateUsageCatalogProvider) Catalog(
	ctx context.Context,
	request TwigTemplateUsageCatalogRequest,
) ([]TwigTemplateUsageCatalogEntry, error) {
	if p == nil || p.twig == nil || p.php == nil {
		return nil, fmt.Errorf("twig template usage catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	templateFilter := strings.ToLower(strings.TrimSpace(request.Template))
	fileGlob := filepath.ToSlash(strings.TrimSpace(request.FileGlob))
	if templateFilter == "" && fileGlob == "" {
		return nil, fmt.Errorf(
			"at least one of template or fileGlob is required",
		)
	}
	names, err := p.twig.GetAllTemplateFiles()
	if err != nil {
		return nil, err
	}
	matching := make(map[string]struct{})
	for _, name := range names {
		if templateFilter == "" ||
			strings.Contains(strings.ToLower(name), templateFilter) {
			matching[name] = struct{}{}
		}
	}
	if request.Template != "" {
		for _, name := range p.templateNamesForPathInput(request.Template) {
			matching[name] = struct{}{}
		}
	}
	routeMap, err := p.controllerRoutes()
	if err != nil {
		return nil, err
	}
	lines := newSourceLineResolver()
	result := make([]TwigTemplateUsageCatalogEntry, 0, len(matching))
	for name := range matching {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files, fileErr := p.twig.GetTwigFilesByRelPath(name)
		if fileErr != nil {
			return nil, fileErr
		}
		files = uniqueTwigFiles(files)
		if fileGlob != "" && !p.templateFilesMatchGlob(files, fileGlob) {
			continue
		}
		entry, entryErr := p.catalogEntry(name, files, routeMap, lines)
		if entryErr != nil {
			return nil, entryErr
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Template) <
			strings.ToLower(result[right].Template)
	})
	return result, nil
}

func (p *TwigTemplateUsageCatalogProvider) catalogEntry(
	template string,
	files []twig.TwigFile,
	routes map[string][]TwigTemplateRouteEntry,
	lines *sourceLineResolver,
) (TwigTemplateUsageCatalogEntry, error) {
	entry := TwigTemplateUsageCatalogEntry{Template: template}
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		entry.Files = append(entry.Files, TwigTemplateSourceLocation{
			FileURI: uriutil.FileURI(file.Path),
			Line:    1,
		})
	}
	references, err := p.twig.GetTemplateReferences(template)
	if err != nil {
		return TwigTemplateUsageCatalogEntry{}, err
	}
	for _, reference := range references {
		line := lines.line(reference.FilePath, reference.Range.Start)
		location := TwigTemplateReferenceUsage{
			FileURI: uriutil.FileURI(reference.FilePath),
			Line:    line,
		}
		switch reference.Kind {
		case twig.TemplateIncludeReference:
			entry.Includes = append(entry.Includes, location)
		case twig.TemplateEmbedReference:
			entry.Embeds = append(entry.Embeds, location)
		case twig.TemplateExtendsReference:
			entry.Extends = append(entry.Extends, location)
		case twig.TemplateImportReference:
			entry.Imports = append(entry.Imports, location)
		case twig.TemplateUseReference:
			entry.Uses = append(entry.Uses, location)
		case twig.TemplateFormThemeReference:
			entry.FormThemes = append(entry.FormThemes, location)
		case twig.TemplateRenderReference,
			twig.TemplateAttributeReference,
			twig.TemplateAnnotationReference:
			controller, found := p.controllerUsage(reference, routes, lines)
			if found {
				entry.Controllers = append(entry.Controllers, controller)
			}
		}
	}
	entry.Controllers = uniqueTemplateControllers(entry.Controllers)
	entry.Includes = uniqueTemplateReferenceUsages(entry.Includes)
	entry.Embeds = uniqueTemplateReferenceUsages(entry.Embeds)
	entry.Extends = uniqueTemplateReferenceUsages(entry.Extends)
	entry.Imports = uniqueTemplateReferenceUsages(entry.Imports)
	entry.Uses = uniqueTemplateReferenceUsages(entry.Uses)
	entry.FormThemes = uniqueTemplateReferenceUsages(entry.FormThemes)
	entry.Components, err = p.componentUsages(files, lines)
	if err != nil {
		return TwigTemplateUsageCatalogEntry{}, err
	}
	return entry, nil
}

func (p *TwigTemplateUsageCatalogProvider) controllerUsage(
	reference twig.TemplateReference,
	routes map[string][]TwigTemplateRouteEntry,
	lines *sourceLineResolver,
) (TwigTemplateControllerUsage, bool) {
	symbols := p.php.SemanticSnapshot().SymbolsIn(reference.FilePath)
	method, found := enclosingTemplateMethod(symbols, reference.Range)
	if !found {
		return TwigTemplateControllerUsage{}, false
	}
	lineRange := method.SelectionRange
	if lineRange.Len() == 0 {
		lineRange = method.Range
	}
	return TwigTemplateControllerUsage{
		Controller: method.FullyQualified,
		FileURI:    uriutil.FileURI(method.Path),
		Line:       lines.line(method.Path, lineRange.Start),
		Routes: append(
			[]TwigTemplateRouteEntry(nil),
			routes[strings.ToLower(method.FullyQualified)]...,
		),
	}, true
}

func (p *TwigTemplateUsageCatalogProvider) controllerRoutes() (
	map[string][]TwigTemplateRouteEntry,
	error,
) {
	result := make(map[string][]TwigTemplateRouteEntry)
	if p.routes == nil {
		return result, nil
	}
	routes, err := p.routes.GetRoutes()
	if err != nil {
		return nil, err
	}
	for _, route := range routes {
		reference, found := symfony.ParseControllerReference(route.Controller)
		if !found {
			continue
		}
		resolution, resolveErr := symfony.ResolveControllerReference(
			reference,
			p.services,
			p.php,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !resolution.MethodFound {
			continue
		}
		key := strings.ToLower(resolution.Method.FullyQualified)
		result[key] = append(result[key], TwigTemplateRouteEntry{
			Name:    route.Name,
			Path:    route.Path,
			Methods: normalizedRouteMethods(route.Methods),
		})
	}
	for key := range result {
		sort.Slice(result[key], func(left, right int) bool {
			return strings.ToLower(result[key][left].Name) <
				strings.ToLower(result[key][right].Name)
		})
	}
	return result, nil
}

func (p *TwigTemplateUsageCatalogProvider) componentUsages(
	files []twig.TwigFile,
	lines *sourceLineResolver,
) ([]TwigTemplateComponentUsage, error) {
	if p.components == nil {
		return nil, nil
	}
	var result []TwigTemplateComponentUsage
	for _, file := range files {
		components, err := p.components.ComponentsForTemplate(file.Path)
		if err != nil {
			return nil, err
		}
		for _, component := range components {
			usages, usageErr := p.components.Usages(component.Name)
			if usageErr != nil {
				return nil, usageErr
			}
			for _, usage := range usages {
				if usage.File == file.Path {
					continue
				}
				result = append(result, TwigTemplateComponentUsage{
					Component: component.Name,
					Syntax:    usage.Kind.String(),
					FileURI:   uriutil.FileURI(usage.File),
					Line:      lines.line(usage.File, usage.Range.Start),
				})
			}
		}
	}
	seen := make(map[string]struct{}, len(result))
	unique := result[:0]
	for _, usage := range result {
		key := usage.Component + "\x00" + usage.FileURI + "\x00" +
			fmt.Sprint(usage.Line)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, usage)
	}
	sort.Slice(unique, func(left, right int) bool {
		if unique[left].FileURI != unique[right].FileURI {
			return unique[left].FileURI < unique[right].FileURI
		}
		if unique[left].Line != unique[right].Line {
			return unique[left].Line < unique[right].Line
		}
		return unique[left].Component < unique[right].Component
	})
	return unique, nil
}

func (p *TwigTemplateUsageCatalogProvider) templateNamesForPathInput(
	input string,
) []string {
	input = strings.TrimSpace(input)
	if input == "" || filepath.IsAbs(input) {
		return nil
	}
	path := filepath.Clean(filepath.Join(p.root, filepath.FromSlash(input)))
	relative, err := filepath.Rel(p.root, path)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	if file, found, _ := p.twig.GetTwigFileByPath(path); found {
		return twig.TemplateNames(file.Path)
	}
	return nil
}

func (p *TwigTemplateUsageCatalogProvider) templateFilesMatchGlob(
	files []twig.TwigFile,
	glob string,
) bool {
	for _, file := range files {
		relative := p.relativePath(file.Path)
		if relative != "" && antPathMatch(glob, relative) {
			return true
		}
	}
	return false
}

func (p *TwigTemplateUsageCatalogProvider) relativePath(path string) string {
	relative, err := filepath.Rel(p.root, path)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative)
}

func uniqueTwigFiles(files []twig.TwigFile) []twig.TwigFile {
	seen := make(map[string]struct{}, len(files))
	result := make([]twig.TwigFile, 0, len(files))
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		if _, duplicate := seen[file.Path]; duplicate {
			continue
		}
		seen[file.Path] = struct{}{}
		result = append(result, file)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Path < result[right].Path
	})
	return result
}

func uniqueTemplateControllers(
	controllers []TwigTemplateControllerUsage,
) []TwigTemplateControllerUsage {
	seen := make(map[string]struct{}, len(controllers))
	result := make([]TwigTemplateControllerUsage, 0, len(controllers))
	for _, controller := range controllers {
		key := strings.ToLower(controller.Controller) + "\x00" +
			controller.FileURI + "\x00" + fmt.Sprint(controller.Line)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, controller)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Controller) <
			strings.ToLower(result[right].Controller)
	})
	return result
}

func uniqueTemplateReferenceUsages(
	usages []TwigTemplateReferenceUsage,
) []TwigTemplateReferenceUsage {
	seen := make(map[string]struct{}, len(usages))
	result := make([]TwigTemplateReferenceUsage, 0, len(usages))
	for _, usage := range usages {
		key := usage.FileURI + "\x00" + fmt.Sprint(usage.Line)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, usage)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].FileURI != result[right].FileURI {
			return result[left].FileURI < result[right].FileURI
		}
		return result[left].Line < result[right].Line
	})
	return result
}

var _ lsp.CommandProvider = (*TwigTemplateUsageCatalogProvider)(nil)
