package symfony

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

// Route represents a Symfony route from YAML, PHP, or other sources
type Route struct {
	Name       string
	Path       string
	Controller string
	Methods    []string
	FilePath   string
	Line       int
}

// Parameters returns path placeholders in source order. Symfony's inline
// requirement/default forms such as {!id<\d+>?1} are normalized to "id".
func (r Route) Parameters() []string {
	var parameters []string
	seen := make(map[string]struct{})
	for offset := 0; offset < len(r.Path); {
		start := strings.IndexByte(r.Path[offset:], '{')
		if start < 0 {
			break
		}
		start += offset + 1
		if start < len(r.Path) && r.Path[start] == '!' {
			start++
		}
		end := start
		for end < len(r.Path) && isRouteParameterCharacter(r.Path[end], end == start) {
			end++
		}
		if end > start {
			name := r.Path[start:end]
			if _, exists := seen[name]; !exists {
				seen[name] = struct{}{}
				parameters = append(parameters, name)
			}
		}
		closeOffset := strings.IndexByte(r.Path[end:], '}')
		if closeOffset < 0 {
			break
		}
		offset = end + closeOffset + 1
	}
	return parameters
}

func isRouteParameterCharacter(value byte, first bool) bool {
	if value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' {
		return true
	}
	return !first && value >= '0' && value <= '9'
}

type RouteList []Route

func (rl RouteList) GetByController(name string) *Route {
	for _, r := range rl {
		if r.Controller == name {
			return &r
		}
	}
	return nil
}

type RouteIndexer struct {
	dataIndexer           *indexer.DataIndexer[Route]
	resourceImportIndexer *indexer.DataIndexer[RouteResourceImport]
	compiledRoutes        *CompiledRouteWatcher
}

func NewRouteIndexer(configDir string, stores ...*indexer.Store) (*RouteIndexer, error) {
	dataIndexer, err := indexer.NewRepository[Route](filepath.Join(configDir, "route.db"), "symfony.routes", stores...)
	if err != nil {
		return nil, err
	}
	resourceImportIndexer, err := indexer.NewRepository[RouteResourceImport](
		filepath.Join(configDir, "route_resources.db"),
		"symfony.route_resources",
		stores...,
	)
	if err != nil {
		_ = dataIndexer.Close()
		return nil, err
	}
	return &RouteIndexer{
		dataIndexer:           dataIndexer,
		resourceImportIndexer: resourceImportIndexer,
	}, nil
}

func NewProjectRouteIndexer(
	projectRoot,
	configDir string,
	stores ...*indexer.Store,
) (*RouteIndexer, error) {
	idx, err := NewRouteIndexer(configDir, stores...)
	if err != nil {
		return nil, err
	}
	watcher, watcherErr := NewCompiledRouteWatcher(projectRoot)
	if watcherErr != nil {
		log.Printf("Failed to initialize compiled route watcher: %v", watcherErr)
		return idx, nil
	}
	idx.compiledRoutes = watcher
	return idx, nil
}

func (idx *RouteIndexer) ID() string {
	return "symfony.route"
}

func (idx *RouteIndexer) GetRoutes() (RouteList, error) {
	routes, err := idx.dataIndexer.GetAllValues()
	if err != nil || idx.compiledRoutes == nil {
		return routes, err
	}
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		seen[route.Name] = struct{}{}
	}
	compiled, _ := idx.compiledRoutes.Routes()
	for _, route := range compiled {
		if _, exists := seen[route.Name]; exists {
			continue
		}
		seen[route.Name] = struct{}{}
		routes = append(routes, route)
	}
	return routes, nil
}

func (idx *RouteIndexer) GetRoute(name string) ([]Route, error) {
	routes, err := idx.dataIndexer.GetValues(name)
	if err != nil || len(routes) != 0 || idx.compiledRoutes == nil {
		return routes, err
	}
	if route, found := idx.compiledRoutes.Route(name); found {
		return []Route{route}, nil
	}
	return nil, nil
}

func (idx *RouteIndexer) GetRouteResourceImports() ([]RouteResourceImport, error) {
	if idx == nil || idx.resourceImportIndexer == nil {
		return nil, nil
	}
	return idx.resourceImportIndexer.GetAllValues()
}

// GetCompiledRoutes returns the currently loaded generated-route catalog
// without applying source-definition precedence.
func (idx *RouteIndexer) GetCompiledRoutes() RouteList {
	if idx == nil || idx.compiledRoutes == nil {
		return nil
	}
	routes, _ := idx.compiledRoutes.Routes()
	return routes
}

// ReloadCompiledRoutes reparses the currently selected Symfony generated
// route catalog. Normal editor use is refreshed through the filesystem
// watcher; this is useful after an explicit cache warmup.
func (idx *RouteIndexer) ReloadCompiledRoutes() error {
	if idx == nil || idx.compiledRoutes == nil {
		return nil
	}
	err := idx.compiledRoutes.Refresh()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (idx *RouteIndexer) FindRoutesByPath(value string) ([]Route, error) {
	routes, err := idx.GetRoutes()
	if err != nil {
		return nil, err
	}
	matcher := NewRoutePathSearchMatcher(value)
	if matcher.SearchPath() == "" {
		return nil, nil
	}
	var exact []Route
	var partial []Route
	for _, route := range routes {
		if !matcher.Matches(route.Path) {
			continue
		}
		if RoutePathMatches(route.Path, matcher.SearchPath()) {
			exact = append(exact, route)
		} else {
			partial = append(partial, route)
		}
	}
	sortRoutes(exact)
	sortRoutes(partial)
	return append(exact, partial...), nil
}

// FindRoutesByPathPrefix returns routes rooted at a complete path segment.
// The full source path is excluded so navigation from a route attribute does
// not point back to the declaration currently under the cursor.
func (idx *RouteIndexer) FindRoutesByPathPrefix(
	prefix,
	fullPath string,
) ([]Route, error) {
	if idx == nil {
		return nil, nil
	}
	routes, err := idx.GetRoutes()
	if err != nil {
		return nil, err
	}
	prefix = normalizedRoutePath(prefix)
	fullPath = normalizedRoutePath(fullPath)
	if prefix == "" {
		return nil, nil
	}
	prefixWithSlash := strings.ToLower(strings.TrimSuffix(prefix, "/") + "/")
	var result []Route
	for _, route := range routes {
		path := normalizedRoutePath(route.Path)
		if path == "" || strings.EqualFold(path, fullPath) {
			continue
		}
		pathWithSlash := strings.ToLower(strings.TrimSuffix(path, "/") + "/")
		if strings.HasPrefix(pathWithSlash, prefixWithSlash) {
			result = append(result, route)
		}
	}
	sortRoutes(result)
	return result, nil
}

func sortRoutes(routes []Route) {
	sort.Slice(routes, func(left, right int) bool {
		leftCompiled := isCompiledRouteFileName(
			filepath.Base(routes[left].FilePath),
		)
		rightCompiled := isCompiledRouteFileName(
			filepath.Base(routes[right].FilePath),
		)
		if leftCompiled != rightCompiled {
			return !leftCompiled
		}
		if routes[left].Path != routes[right].Path {
			return routes[left].Path < routes[right].Path
		}
		if routes[left].Name != routes[right].Name {
			return routes[left].Name < routes[right].Name
		}
		if routes[left].FilePath != routes[right].FilePath {
			return routes[left].FilePath < routes[right].FilePath
		}
		return routes[left].Line < routes[right].Line
	})
}

func normalizedRoutePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return "/" + strings.Trim(value, "/")
}

const routePathPlaceholderMarker byte = 0x1f

// RoutePathMatches performs exact reverse matching for concrete URLs against
// Symfony placeholders such as /catalog/{id}. It intentionally remains exact
// because reference lookup must not treat partial URLs as usages.
func RoutePathMatches(pattern, value string) bool {
	value = NormalizeRouteSearchPath(value)
	if pattern == "" || value == "" {
		return false
	}
	routePath := neutralizeRoutePlaceholders(pattern)
	width := len(value) + 1
	memo := make([]byte, (len(routePath)+1)*width)
	return exactRoutePathMatch(routePath, value, 0, 0, width, memo)
}

func exactRoutePathMatch(
	routePath,
	value string,
	routeIndex,
	valueIndex,
	width int,
	memo []byte,
) bool {
	if routeIndex == len(routePath) {
		return valueIndex == len(value)
	}
	if valueIndex == len(value) {
		return false
	}
	key := routeIndex*width + valueIndex
	if memo[key] != 0 {
		return memo[key] == 1
	}

	matches := false
	if routePath[routeIndex] == routePathPlaceholderMarker {
		for offset := valueIndex; offset < len(value); offset++ {
			if !isRoutePlaceholderPathByte(value[offset]) {
				break
			}
			if exactRoutePathMatch(
				routePath,
				value,
				routeIndex+1,
				offset+1,
				width,
				memo,
			) {
				matches = true
				break
			}
		}
	} else if routePath[routeIndex] == value[valueIndex] {
		matches = exactRoutePathMatch(
			routePath,
			value,
			routeIndex+1,
			valueIndex+1,
			width,
			memo,
		)
	}
	if matches {
		memo[key] = 1
	} else {
		memo[key] = 2
	}
	return matches
}

// RoutePathSearchMatcher performs partial reverse-route matching while
// treating every Symfony placeholder as one URL path segment. Constructing it
// once lets callers scan the whole route index without reparsing the query or
// rebuilding its prefix table for every route.
type RoutePathSearchMatcher struct {
	searchPath      string
	maxPrefixLength int
	prefixTable     []int
}

func NewRoutePathSearchMatcher(searchPath string) RoutePathSearchMatcher {
	searchPath = NormalizeRouteSearchPath(searchPath)
	maxPrefixLength := max(0, len(searchPath)-2)
	return RoutePathSearchMatcher{
		searchPath:      searchPath,
		maxPrefixLength: maxPrefixLength,
		prefixTable: buildRouteSearchPrefixTable(
			searchPath,
			maxPrefixLength,
		),
	}
}

func (matcher RoutePathSearchMatcher) SearchPath() string {
	return matcher.searchPath
}

// RoutePathSearchMatches is the convenience form for one route. Index scans
// should construct RoutePathSearchMatcher once and call Matches repeatedly.
func RoutePathSearchMatches(pattern, searchPath string) bool {
	return NewRoutePathSearchMatcher(searchPath).Matches(pattern)
}

func (matcher RoutePathSearchMatcher) Matches(routePath string) bool {
	if routePath == "" || matcher.searchPath == "" {
		return false
	}
	if strings.Contains(routePath, matcher.searchPath) {
		return true
	}
	if matcher.maxPrefixLength == 0 ||
		!strings.ContainsRune(routePath, '{') {
		return false
	}

	routePath = neutralizeRoutePlaceholders(routePath)
	prefix, found := matcher.longestSearchPrefixMatch(routePath)
	if !found {
		return false
	}
	width := len(matcher.searchPath) + 1
	memo := make([]byte, (len(routePath)+1)*width)
	return matcher.matchesRoutePrefix(
		routePath,
		prefix.routeIndex+prefix.length,
		prefix.length,
		width,
		memo,
	)
}

type routeSearchPrefix struct {
	routeIndex int
	length     int
}

func (matcher RoutePathSearchMatcher) longestSearchPrefixMatch(
	routePath string,
) (routeSearchPrefix, bool) {
	matched := 0
	best := routeSearchPrefix{routeIndex: -1}
	for routeIndex := 0; routeIndex < len(routePath); routeIndex++ {
		if matched == matcher.maxPrefixLength {
			break
		}
		value := routePath[routeIndex]
		for matched > 0 && value != matcher.searchPath[matched] {
			matched = matcher.prefixTable[matched-1]
		}
		if value != matcher.searchPath[matched] {
			continue
		}
		matched++
		if matched > best.length {
			best = routeSearchPrefix{
				routeIndex: routeIndex - matched + 1,
				length:     matched,
			}
			if matched == matcher.maxPrefixLength {
				break
			}
		}
	}
	return best, best.length > 0
}

func (matcher RoutePathSearchMatcher) matchesRoutePrefix(
	routePath string,
	routeIndex,
	searchIndex,
	width int,
	memo []byte,
) bool {
	if searchIndex == len(matcher.searchPath) {
		return true
	}
	if routeIndex == len(routePath) {
		return false
	}
	key := routeIndex*width + searchIndex
	if memo[key] != 0 {
		return memo[key] == 1
	}

	matches := false
	if routePath[routeIndex] == routePathPlaceholderMarker {
		for offset := searchIndex; offset < len(matcher.searchPath); offset++ {
			if !isRoutePlaceholderPathByte(matcher.searchPath[offset]) {
				break
			}
			if matcher.matchesRoutePrefix(
				routePath,
				routeIndex+1,
				offset+1,
				width,
				memo,
			) {
				matches = true
				break
			}
		}
	} else if routePath[routeIndex] == matcher.searchPath[searchIndex] {
		matches = matcher.matchesRoutePrefix(
			routePath,
			routeIndex+1,
			searchIndex+1,
			width,
			memo,
		)
	}
	if matches {
		memo[key] = 1
	} else {
		memo[key] = 2
	}
	return matches
}

func buildRouteSearchPrefixTable(
	searchPath string,
	maxPrefixLength int,
) []int {
	prefixTable := make([]int, maxPrefixLength)
	matched := 0
	for index := 1; index < maxPrefixLength; index++ {
		value := searchPath[index]
		for matched > 0 && value != searchPath[matched] {
			matched = prefixTable[matched-1]
		}
		if value == searchPath[matched] {
			matched++
		}
		prefixTable[index] = matched
	}
	return prefixTable
}

func neutralizeRoutePlaceholders(routePath string) string {
	var result strings.Builder
	result.Grow(len(routePath))
	for offset := 0; offset < len(routePath); offset++ {
		if routePath[offset] != '{' {
			result.WriteByte(routePath[offset])
			continue
		}
		end := strings.IndexByte(routePath[offset+1:], '}')
		if end < 0 {
			result.WriteByte(routePath[offset])
			continue
		}
		result.WriteByte(routePathPlaceholderMarker)
		offset += end + 1
	}
	return result.String()
}

func isRoutePlaceholderPathByte(value byte) bool {
	return value != '/' && value != '?' && value != '#'
}

func (idx *RouteIndexer) Index(file *indexer.ParsedFile) error {
	switch file.Extension() {
	case ".yml", ".yaml":
		return idx.indexYaml(file)
	case ".xml":
		return idx.indexXML(file)
	case ".php":
		return idx.indexPhp(file)
	default:
		return nil
	}
}

func (idx *RouteIndexer) indexXML(file *indexer.ParsedFile) error {
	parsedRoutes, err := ParseXMLRoutesTree(
		file.Path,
		file.SyntaxTree(),
		file.LineIndex(),
	)
	if err != nil {
		return err
	}

	batchSave := map[string]map[string]Route{file.Path: {}}
	for _, route := range parsedRoutes {
		batchSave[file.Path][route.Name] = route
	}
	addRouteWorkspaceSymbols(file, parsedRoutes)
	if err := idx.dataIndexer.BatchSaveItemsIn(
		file.Mutation(),
		batchSave,
	); err != nil {
		return err
	}
	return idx.indexRouteResources(file)
}

func (idx *RouteIndexer) indexYaml(file *indexer.ParsedFile) error {
	parsedRoutes, err := ParseYAMLRoutesTree(file.Path, file.SyntaxTree(), file.LineIndex())
	if err != nil {
		return err
	}

	batchSave := map[string]map[string]Route{file.Path: {}}
	for _, route := range parsedRoutes {
		if route.Name == "" {
			continue
		}
		if _, ok := batchSave[route.FilePath]; !ok {
			batchSave[route.FilePath] = make(map[string]Route)
		}
		batchSave[route.FilePath][route.Name] = route
	}
	addRouteWorkspaceSymbols(file, parsedRoutes)

	if err := idx.dataIndexer.BatchSaveItemsIn(
		file.Mutation(),
		batchSave,
	); err != nil {
		return err
	}
	return idx.indexRouteResources(file)
}

func (idx *RouteIndexer) indexPhp(file *indexer.ParsedFile) error {
	var parsedRoutes []Route
	if containsFoldASCIIBytes(file.Content, "route") {
		parsedRoutes = ParsePHPRoutesTree(
			file.Path,
			file.SyntaxTree(),
			file.LineIndex(),
		)
	}

	batchSave := map[string]map[string]Route{file.Path: {}}
	for _, route := range parsedRoutes {
		if route.Name == "" {
			continue
		}
		if _, ok := batchSave[route.FilePath]; !ok {
			batchSave[route.FilePath] = make(map[string]Route)
		}
		batchSave[route.FilePath][route.Name] = route
	}
	addRouteWorkspaceSymbols(file, parsedRoutes)

	if err := idx.dataIndexer.BatchSaveItemsIn(
		file.Mutation(),
		batchSave,
	); err != nil {
		return err
	}
	return idx.indexRouteResources(file)
}

func (idx *RouteIndexer) indexRouteResources(
	file *indexer.ParsedFile,
) error {
	resources := make(map[string]RouteResourceImport)
	for _, reference := range RouteResourceReferences(
		file.SyntaxTree().Root,
	) {
		key := reference.Range.String()
		resources[key] = RouteResourceImport{
			Path:      reference.Path,
			Loader:    reference.Loader,
			Namespace: reference.Namespace,
			FilePath:  file.Path,
			Range:     reference.Range,
			Nested:    reference.Nested,
		}
	}
	return idx.resourceImportIndexer.BatchSaveItemsIn(
		file.Mutation(),
		map[string]map[string]RouteResourceImport{
			file.Path: resources,
		},
	)
}

func (idx *RouteIndexer) RemovedFiles(paths []string) error {
	return errors.Join(
		idx.dataIndexer.BatchDeleteByFilePaths(paths),
		idx.resourceImportIndexer.BatchDeleteByFilePaths(paths),
	)
}

func (idx *RouteIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return errors.Join(
		idx.dataIndexer.BatchDeleteByFilePathsIn(mutation, paths),
		idx.resourceImportIndexer.BatchDeleteByFilePathsIn(
			mutation,
			paths,
		),
	)
}

func (idx *RouteIndexer) Close() error {
	var watcherErr error
	if idx.compiledRoutes != nil {
		watcherErr = idx.compiledRoutes.Close()
		idx.compiledRoutes = nil
	}
	return errors.Join(
		idx.dataIndexer.Close(),
		idx.resourceImportIndexer.Close(),
		watcherErr,
	)
}

func (idx *RouteIndexer) Clear() error {
	return errors.Join(
		idx.dataIndexer.Clear(),
		idx.resourceImportIndexer.Clear(),
	)
}

func (idx *RouteIndexer) ClearIn(mutation *indexer.Mutation) error {
	return errors.Join(
		idx.dataIndexer.ClearIn(mutation),
		idx.resourceImportIndexer.ClearIn(mutation),
	)
}
