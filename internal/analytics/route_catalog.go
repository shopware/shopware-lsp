package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/pathmatch"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const ListRoutesCommand = "shopware/symfony/analytics/routes"

type RouteCatalogProvider struct {
	root     string
	routes   *symfony.RouteIndexer
	services *symfony.ServiceIndex
	php      *php.PHPIndex
	twig     *twig.TwigIndexer
}

func NewRouteCatalogProvider(
	root string,
	routes *symfony.RouteIndexer,
	services *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
	twigIndex *twig.TwigIndexer,
) *RouteCatalogProvider {
	return &RouteCatalogProvider{
		root:     filepath.Clean(root),
		routes:   routes,
		services: services,
		php:      phpIndex,
		twig:     twigIndex,
	}
}

type RouteCatalogRequest struct {
	RouteName  string `json:"routeName,omitempty"`
	Controller string `json:"controller,omitempty"`
	URLPath    string `json:"urlPath,omitempty"`
	FileGlob   string `json:"fileGlob,omitempty"`
}

type RouteCatalogEntry struct {
	Name               string   `json:"name"`
	Path               string   `json:"path,omitempty"`
	Methods            []string `json:"methods,omitempty"`
	Controller         string   `json:"controller,omitempty"`
	ResolvedController string   `json:"resolvedController,omitempty"`
	SourceURI          string   `json:"sourceUri,omitempty"`
	SourceLine         int      `json:"sourceLine,omitempty"`
	ControllerURI      string   `json:"controllerUri,omitempty"`
	ControllerLine     int      `json:"controllerLine,omitempty"`
	Templates          []string `json:"templates,omitempty"`
}

func (p *RouteCatalogProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		ListRoutesCommand: p.list,
	}
}

func (p *RouteCatalogProvider) list(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var request RouteCatalogRequest
	if raw != nil && len(*raw) != 0 && string(*raw) != "null" {
		if err := json.Unmarshal(*raw, &request); err != nil {
			return nil, fmt.Errorf("invalid route catalog request: %w", err)
		}
	}
	return p.Catalog(ctx, request)
}

func (p *RouteCatalogProvider) Catalog(
	ctx context.Context,
	request RouteCatalogRequest,
) ([]RouteCatalogEntry, error) {
	if p == nil || p.routes == nil || p.php == nil {
		return nil, fmt.Errorf("symfony route catalog is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	routes, err := p.routes.GetRoutes()
	if err != nil {
		return nil, err
	}
	nameFilter := strings.ToLower(strings.TrimSpace(request.RouteName))
	controllerFilter := strings.ToLower(strings.TrimSpace(request.Controller))
	fileGlob := filepath.ToSlash(strings.TrimSpace(request.FileGlob))
	pathMatcher := symfony.NewRoutePathSearchMatcher(request.URLPath)
	filterPath := pathMatcher.SearchPath() != ""
	templates := newRouteTemplateResolver(p.php, p.twig)
	lines := newSourceLineResolver()
	seen := make(map[string]struct{}, len(routes))
	result := make([]RouteCatalogEntry, 0, len(routes))
	for _, route := range routes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if nameFilter != "" &&
			!strings.Contains(strings.ToLower(route.Name), nameFilter) {
			continue
		}
		if filterPath && !pathMatcher.Matches(route.Path) {
			continue
		}
		entry, resolveErr := p.routeEntry(route, templates, lines)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if controllerFilter != "" &&
			!strings.Contains(
				strings.ToLower(entry.Controller),
				controllerFilter,
			) &&
			!strings.Contains(
				strings.ToLower(entry.ResolvedController),
				controllerFilter,
			) {
			continue
		}
		if fileGlob != "" {
			relative := p.relativeControllerPath(entry.ControllerURI)
			if relative == "" || !antPathMatch(fileGlob, relative) {
				continue
			}
		}
		key := strings.Join([]string{
			entry.Name,
			entry.Path,
			entry.Controller,
			entry.SourceURI,
			fmt.Sprint(entry.SourceLine),
		}, "\x00")
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return strings.ToLower(result[left].Name) <
				strings.ToLower(result[right].Name)
		}
		if result[left].SourceURI != result[right].SourceURI {
			return result[left].SourceURI < result[right].SourceURI
		}
		return result[left].SourceLine < result[right].SourceLine
	})
	return result, nil
}

func (p *RouteCatalogProvider) routeEntry(
	route symfony.Route,
	templates *routeTemplateResolver,
	lines *sourceLineResolver,
) (RouteCatalogEntry, error) {
	entry := RouteCatalogEntry{
		Name:       route.Name,
		Path:       route.Path,
		Methods:    normalizedRouteMethods(route.Methods),
		Controller: route.Controller,
		SourceLine: route.Line,
	}
	if route.FilePath != "" {
		entry.SourceURI = uriutil.FileURI(route.FilePath)
	}
	reference, found := symfony.ParseControllerReference(route.Controller)
	if !found {
		return entry, nil
	}
	resolution, err := symfony.ResolveControllerReference(
		reference,
		p.services,
		p.php,
	)
	if err != nil {
		return RouteCatalogEntry{}, err
	}
	var target semantic.Symbol
	switch {
	case resolution.MethodFound:
		target = resolution.Method
		entry.ResolvedController = resolution.Class.FullyQualified +
			"::" + resolution.Method.Name
	case resolution.ClassFound:
		target = resolution.Class
		entry.ResolvedController = resolution.Class.FullyQualified
	default:
		return entry, nil
	}
	if target.Path != "" {
		entry.ControllerURI = uriutil.FileURI(target.Path)
		rng := target.SelectionRange
		if rng.Len() == 0 {
			rng = target.Range
		}
		entry.ControllerLine = lines.line(target.Path, rng.Start)
	}
	if resolution.MethodFound {
		entry.Templates, err = templates.forMethod(resolution.Method)
		if err != nil {
			return RouteCatalogEntry{}, err
		}
	}
	return entry, nil
}

func normalizedRouteMethods(methods []string) []string {
	result := make([]string, 0, len(methods))
	seen := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			continue
		}
		if _, duplicate := seen[method]; duplicate {
			continue
		}
		seen[method] = struct{}{}
		result = append(result, method)
	}
	return result
}

func (p *RouteCatalogProvider) relativeControllerPath(uri string) string {
	if uri == "" {
		return ""
	}
	path, err := uriutil.Path(uri)
	if err != nil {
		return ""
	}
	relative, err := filepath.Rel(p.root, path)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative)
}

type routeTemplateResolver struct {
	php    *php.PHPIndex
	twig   *twig.TwigIndexer
	byPath map[string]map[string][]string
}

func newRouteTemplateResolver(
	phpIndex *php.PHPIndex,
	twigIndex *twig.TwigIndexer,
) *routeTemplateResolver {
	return &routeTemplateResolver{
		php:    phpIndex,
		twig:   twigIndex,
		byPath: make(map[string]map[string][]string),
	}
}

func (r *routeTemplateResolver) forMethod(
	method semantic.Symbol,
) ([]string, error) {
	if r == nil || r.php == nil || r.twig == nil || method.Path == "" {
		return nil, nil
	}
	if _, loaded := r.byPath[method.Path]; !loaded {
		if err := r.load(method.Path); err != nil {
			return nil, err
		}
	}
	return append(
		[]string(nil),
		r.byPath[method.Path][strings.ToLower(method.FullyQualified)]...,
	), nil
}

func (r *routeTemplateResolver) load(path string) error {
	references, err := r.twig.GetTemplateReferencesByPath(path)
	if err != nil {
		return err
	}
	symbols := r.php.SemanticSnapshot().SymbolsIn(path)
	values := make(map[string]map[string]struct{})
	for _, reference := range references {
		switch reference.Kind {
		case twig.TemplateRenderReference,
			twig.TemplateAttributeReference,
			twig.TemplateAnnotationReference:
		default:
			continue
		}
		method, found := enclosingTemplateMethod(symbols, reference.Range)
		if !found {
			continue
		}
		key := strings.ToLower(method.FullyQualified)
		if values[key] == nil {
			values[key] = make(map[string]struct{})
		}
		values[key][reference.Template] = struct{}{}
	}
	resolved := make(map[string][]string, len(values))
	for method, templates := range values {
		for template := range templates {
			resolved[method] = append(resolved[method], template)
		}
		sort.Strings(resolved[method])
	}
	r.byPath[path] = resolved
	return nil
}

func enclosingTemplateMethod(
	symbols []semantic.Symbol,
	rng cst.TextRange,
) (semantic.Symbol, bool) {
	bestWidth := ^uint32(0)
	var best semantic.Symbol
	found := false
	for _, symbol := range symbols {
		if symbol.Kind != semantic.MethodSymbol {
			continue
		}
		container := symbol.Range
		if symbol.BodyRange.Contains(rng.Start) {
			container = symbol.BodyRange
		} else if !symbol.Range.Contains(rng.Start) {
			continue
		}
		if container.Len() >= bestWidth {
			continue
		}
		best = symbol
		bestWidth = container.Len()
		found = true
	}
	return best, found
}

type sourceLineResolver struct {
	indexes map[string]*cst.LineIndex
	missing map[string]struct{}
}

func newSourceLineResolver() *sourceLineResolver {
	return &sourceLineResolver{
		indexes: make(map[string]*cst.LineIndex),
		missing: make(map[string]struct{}),
	}
}

func (r *sourceLineResolver) line(path string, offset uint32) int {
	if _, missing := r.missing[path]; missing {
		return 0
	}
	index, found := r.indexes[path]
	if !found {
		source, err := os.ReadFile(path)
		if err != nil {
			r.missing[path] = struct{}{}
			return 0
		}
		index = cst.NewLineIndex(string(source))
		r.indexes[path] = index
	}
	line, _ := index.PositionUTF16(offset)
	return int(line) + 1
}

func antPathMatch(pattern, candidate string) bool {
	return pathmatch.Ant(pattern, candidate)
}

var _ lsp.CommandProvider = (*RouteCatalogProvider)(nil)
