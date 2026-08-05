package symfony

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// ServiceIndex maintains an index of service IDs from XML, YAML, and PHP
// configurator files.
type ServiceIndex struct {
	projectRoot      string
	serviceIndex     *indexer.DataIndexer[Service]
	parameterIndex   *indexer.DataIndexer[Parameter]
	prototypeIndex   *indexer.DataIndexer[ServicePrototype]
	containerWatcher *ContainerWatcher
	phpIndex         *php.PHPIndex

	prototypeRevision atomic.Uint64
	prototypeCacheMu  sync.Mutex
	prototypeCache    []Service
	prototypeCachePHP uint64
	prototypeCacheOwn uint64
	prototypeCacheSet bool

	indexedPathsMu sync.RWMutex
	indexedPaths   map[string]servicePathKind
}

type servicePathKind uint8

const (
	servicePathServices servicePathKind = 1 << iota
	servicePathParameters
	servicePathPrototypes
)

type servicePathState struct {
	touched servicePathKind
	present servicePathKind
}

// NewServiceIndex creates a new service indexer for the given project root
func NewServiceIndex(projectRoot string, configDir string, stores ...*indexer.Store) (*ServiceIndex, error) {
	serviceIndex, err := indexer.NewRepository[Service](filepath.Join(configDir, "symfony.service"), "symfony.services", stores...)
	if err != nil {
		return nil, fmt.Errorf("failed to create service index: %w", err)
	}

	parameterIndex, err := indexer.NewRepository[Parameter](filepath.Join(configDir, "symfony.parameter"), "symfony.parameters", stores...)
	if err != nil {
		_ = serviceIndex.Close()
		return nil, fmt.Errorf("failed to create parameter index: %w", err)
	}

	prototypeIndex, err := indexer.NewRepository[ServicePrototype](filepath.Join(configDir, "symfony.prototype"), "symfony.service_prototypes", stores...)
	if err != nil {
		_ = serviceIndex.Close()
		_ = parameterIndex.Close()
		return nil, fmt.Errorf("failed to create service prototype index: %w", err)
	}
	servicePaths, servicePathErr := serviceIndex.GetAllFilePaths()
	parameterPaths, parameterPathErr := parameterIndex.GetAllFilePaths()
	prototypePaths, prototypePathErr := prototypeIndex.GetAllFilePaths()
	if pathErr := errors.Join(
		servicePathErr,
		parameterPathErr,
		prototypePathErr,
	); pathErr != nil {
		_ = serviceIndex.Close()
		_ = parameterIndex.Close()
		_ = prototypeIndex.Close()
		return nil, fmt.Errorf("load indexed service paths: %w", pathErr)
	}
	indexedPaths := make(
		map[string]servicePathKind,
		len(servicePaths)+len(parameterPaths)+len(prototypePaths),
	)
	for _, path := range servicePaths {
		indexedPaths[path] |= servicePathServices
	}
	for _, path := range parameterPaths {
		indexedPaths[path] |= servicePathParameters
	}
	for _, path := range prototypePaths {
		indexedPaths[path] |= servicePathPrototypes
	}

	idx := &ServiceIndex{
		projectRoot:    projectRoot,
		serviceIndex:   serviceIndex,
		parameterIndex: parameterIndex,
		prototypeIndex: prototypeIndex,
		indexedPaths:   indexedPaths,
	}

	// Initialize the container watcher after the index is created
	containerWatcher, err := NewContainerWatcher(projectRoot)
	if err != nil {
		log.Printf("Failed to initialize container watcher: %v", err)
		// Continue without the container watcher
	} else {
		idx.containerWatcher = containerWatcher
		log.Printf("Symfony container watcher initialized")
	}

	return idx, nil
}

func (idx *ServiceIndex) ID() string {
	return "symfony.service"
}

// SetPHPIndex enables inheritance-aware resolution for instanceof()
// configurator rules.
func (idx *ServiceIndex) SetPHPIndex(phpIndex *php.PHPIndex) {
	idx.phpIndex = phpIndex
	idx.prototypeRevision.Add(1)
}

func (idx *ServiceIndex) GetCompiledTwigComponents() []ContainerTwigComponent {
	components, _ := idx.GetCompiledTwigComponentsState()
	return components
}

func (idx *ServiceIndex) GetCompiledTwigComponentsState() (
	[]ContainerTwigComponent,
	uint64,
) {
	if idx == nil || idx.containerWatcher == nil {
		return nil, 0
	}
	return idx.containerWatcher.GetTwigComponentsState()
}

// GetDoctrineNamespaceAliasesState exposes the compiled container's legacy
// entity/document namespace aliases without coupling the Doctrine index to the
// service-index implementation.
func (idx *ServiceIndex) GetDoctrineNamespaceAliasesState() (
	map[string][]string,
	uint64,
) {
	if idx == nil || idx.containerWatcher == nil {
		return nil, 0
	}
	return idx.containerWatcher.GetDoctrineNamespaceAliasesState()
}

// ReloadCompiledContainer reparses the currently discovered Symfony container.
// It is useful after an explicit cache warmup; normal editor use is updated by
// the filesystem watcher.
func (idx *ServiceIndex) ReloadCompiledContainer() error {
	if idx == nil || idx.containerWatcher == nil {
		return nil
	}
	_, exists := idx.containerWatcher.containerState()
	if exists {
		return idx.containerWatcher.loadContainer()
	}
	return idx.containerWatcher.findAndLoadContainer()
}

// Index parses supported Symfony service configuration files.
func (idx *ServiceIndex) Index(file *indexer.ParsedFile) error {
	path := file.Path
	var services []Service
	var params []Parameter
	var prototypes []ServicePrototype
	var err error

	if strings.Contains(path, "var/cache") {
		return nil
	}

	// Determine if this is an XML or YAML file based on extension
	ext := file.Extension()
	switch ext {
	case ".xml":
		services, params, prototypes, err = parseXMLServiceConfigTree(
			path,
			file.SyntaxTree(),
			file.LineIndex(),
		)
	case ".yaml", ".yml":
		services, params, prototypes, err = parseYAMLServiceConfigTree(
			path,
			file.SyntaxTree(),
			file.LineIndex(),
		)
	case ".php":
		if !isPHPServiceConfigCandidate(file.Content) {
			if !idx.hasIndexedPath(path) {
				return nil
			}
			break
		}
		tree := file.SyntaxTree()
		if tree == nil {
			return nil
		}
		config, parseErr := parsePHPServiceConfigTree(path, tree.Root, file.LineIndex())
		services, params, prototypes, err = config.Services, config.Parameters, config.Prototypes, parseErr
	default:
		// Not a file type we're interested in
		return nil
	}

	if err != nil {
		return err
	}

	serviceWrite := map[string]map[string]Service{path: {}}
	parameterWrite := map[string]map[string]Parameter{path: {}}
	prototypeWrite := map[string]map[string]ServicePrototype{path: {}}

	for _, service := range services {
		if _, ok := serviceWrite[service.Path]; !ok {
			serviceWrite[service.Path] = make(map[string]Service)
		}
		serviceWrite[service.Path][service.ID] = service
	}

	for _, param := range params {
		if _, ok := parameterWrite[param.Path]; !ok {
			parameterWrite[param.Path] = make(map[string]Parameter)
		}
		parameterWrite[param.Path][param.Name] = param
	}

	for _, prototype := range prototypes {
		key := fmt.Sprintf("%d:%s:%s", prototype.Line, prototype.Namespace, prototype.Resource)
		prototypeWrite[path][key] = prototype
	}

	if err := idx.parameterIndex.BatchSaveItemsIn(file.Mutation(), parameterWrite); err != nil {
		return err
	}

	if err := idx.prototypeIndex.BatchSaveItemsIn(
		file.Mutation(),
		prototypeWrite,
	); err != nil {
		return err
	}

	if err := idx.serviceIndex.BatchSaveItemsIn(file.Mutation(), serviceWrite); err != nil {
		return err
	}
	if err := idx.publishIndexedPathStates(
		serviceIndexPathStates(
			serviceWrite,
			parameterWrite,
			prototypeWrite,
		),
		file.Mutation(),
	); err != nil {
		return err
	}
	addServiceWorkspaceSymbols(file, services, params)

	idx.prototypeRevision.Add(1)
	return nil
}

func isPHPServiceConfigCandidate(content []byte) bool {
	fluent := bytes.Contains(content, []byte("ContainerConfigurator")) &&
		(bytes.Contains(content, []byte("->services")) ||
			bytes.Contains(content, []byte("->parameters")))
	array := (bytes.Contains(content, []byte("'services'")) ||
		bytes.Contains(content, []byte(`"services"`)) ||
		bytes.Contains(content, []byte("'parameters'")) ||
		bytes.Contains(content, []byte(`"parameters"`))) &&
		(bytes.Contains(content, []byte("return")) ||
			bytes.Contains(content, []byte("::config")))
	return fluent || array
}

func serviceIndexPathStates(
	services map[string]map[string]Service,
	parameters map[string]map[string]Parameter,
	prototypes map[string]map[string]ServicePrototype,
) map[string]servicePathState {
	states := make(
		map[string]servicePathState,
		len(services)+len(parameters)+len(prototypes),
	)
	add := func(path string, kind servicePathKind, present bool) {
		state := states[path]
		state.touched |= kind
		if present {
			state.present |= kind
		}
		states[path] = state
	}
	for path, values := range services {
		add(path, servicePathServices, len(values) != 0)
	}
	for path, values := range parameters {
		add(path, servicePathParameters, len(values) != 0)
	}
	for path, values := range prototypes {
		add(path, servicePathPrototypes, len(values) != 0)
	}
	return states
}

func (idx *ServiceIndex) hasIndexedPath(path string) bool {
	idx.indexedPathsMu.RLock()
	defer idx.indexedPathsMu.RUnlock()
	return idx.indexedPaths[path] != 0
}

func (idx *ServiceIndex) publishIndexedPathStates(
	states map[string]servicePathState,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.indexedPathsMu.Lock()
		defer idx.indexedPathsMu.Unlock()
		for path, state := range states {
			kinds := idx.indexedPaths[path]
			kinds &^= state.touched
			kinds |= state.present
			if kinds == 0 {
				delete(idx.indexedPaths, path)
			} else {
				idx.indexedPaths[path] = kinds
			}
		}
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *ServiceIndex) removeIndexedPaths(paths []string) {
	idx.indexedPathsMu.Lock()
	defer idx.indexedPathsMu.Unlock()
	for _, path := range paths {
		delete(idx.indexedPaths, path)
	}
}

func (idx *ServiceIndex) resetIndexedPaths() {
	idx.indexedPathsMu.Lock()
	defer idx.indexedPathsMu.Unlock()
	clear(idx.indexedPaths)
}

func (idx *ServiceIndex) RemovedFiles(paths []string) error {
	err := errors.Join(
		idx.serviceIndex.BatchDeleteByFilePaths(paths),
		idx.parameterIndex.BatchDeleteByFilePaths(paths),
		idx.prototypeIndex.BatchDeleteByFilePaths(paths),
	)
	if err == nil {
		idx.removeIndexedPaths(paths)
		idx.prototypeRevision.Add(1)
	}
	return err
}

func (idx *ServiceIndex) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	err := errors.Join(
		idx.serviceIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.parameterIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.prototypeIndex.BatchDeleteByFilePathsIn(mutation, paths),
	)
	if err != nil {
		return err
	}
	publish := func() { idx.removeIndexedPaths(paths) }
	if mutation == nil {
		publish()
	} else if err := mutation.AfterCommit(publish); err != nil {
		return err
	}
	idx.prototypeRevision.Add(1)
	return nil
}

// GetAllServices returns all indexed service IDs
func (idx *ServiceIndex) GetAllServices() ([]string, error) {
	dbServiceIDs, err := idx.serviceIndex.GetAllKeys()
	if err != nil {
		return nil, err
	}

	dbServiceMap := make(map[string]struct{}, len(dbServiceIDs))
	for _, id := range dbServiceIDs {
		dbServiceMap[id] = struct{}{}
	}
	prototypeServices, err := idx.expandedPrototypeServices()
	if err != nil {
		return nil, err
	}
	for _, service := range prototypeServices {
		if _, exists := dbServiceMap[service.ID]; exists {
			continue
		}
		dbServiceMap[service.ID] = struct{}{}
		dbServiceIDs = append(dbServiceIDs, service.ID)
	}

	// If container watcher is available, add any services that aren't in the database
	if idx.containerWatcher != nil && idx.containerWatcher.ContainerExists() {
		cwServices := idx.containerWatcher.GetAllServices()

		// Add container watcher services that aren't in the database
		for _, id := range cwServices {
			if _, exists := dbServiceMap[id]; !exists {
				dbServiceMap[id] = struct{}{}
				dbServiceIDs = append(dbServiceIDs, id)
			}
		}
	}

	sort.Strings(dbServiceIDs)
	return dbServiceIDs, nil
}

// GetAllServiceDefinitions returns one definition per service ID. Source
// definitions take precedence over prototype expansion and the compiled
// container, matching GetServiceByID.
func (idx *ServiceIndex) GetAllServiceDefinitions() ([]Service, error) {
	explicit, err := idx.serviceIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	prototypes, err := idx.expandedPrototypeServices()
	if err != nil {
		return nil, err
	}

	result := make([]Service, 0, len(explicit)+len(prototypes))
	seen := make(map[string]struct{}, len(explicit)+len(prototypes))
	appendDefinitions := func(definitions []Service) {
		for _, service := range definitions {
			if service.ID == "" {
				continue
			}
			if _, exists := seen[service.ID]; exists {
				continue
			}
			seen[service.ID] = struct{}{}
			result = append(result, service)
		}
	}
	appendDefinitions(explicit)
	appendDefinitions(prototypes)
	if idx.containerWatcher != nil && idx.containerWatcher.ContainerExists() {
		appendDefinitions(idx.containerWatcher.GetAllServiceDefinitions())
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].ID < result[right].ID
	})
	return result, nil
}

// GetServiceByID returns a specific service by its ID
func (idx *ServiceIndex) GetServiceByID(id string) (Service, bool, error) {
	services, err := idx.serviceIndex.GetValues(id)
	if err != nil {
		return Service{}, false, err
	}

	if len(services) > 0 {
		service, resolveErr := idx.withResolvedInstanceofTags(services[0])
		if resolveErr != nil {
			return Service{}, false, resolveErr
		}
		return service, true, nil
	}

	prototypeServices, prototypeErr := idx.expandedPrototypeServices()
	if prototypeErr != nil {
		return Service{}, false, prototypeErr
	}
	for _, service := range prototypeServices {
		if service.ID != id {
			continue
		}
		resolved, resolveErr := idx.withResolvedInstanceofTags(service)
		if resolveErr != nil {
			return Service{}, false, resolveErr
		}
		return resolved, true, nil
	}

	// If not found in database, fallback to container watcher
	if idx.containerWatcher != nil && idx.containerWatcher.ContainerExists() {
		service, found := idx.containerWatcher.GetServiceByID(id)
		return service, found, nil
	}

	return Service{}, false, nil
}

// GetTwigGlobals returns globals registered on the compiled Twig service.
func (idx *ServiceIndex) GetTwigGlobals() []ContainerTwigGlobal {
	if idx == nil || idx.containerWatcher == nil {
		return nil
	}
	return idx.containerWatcher.GetTwigGlobals()
}

// Close shuts down the database and cleans up temporary files
func (idx *ServiceIndex) Close() error {
	var closeErrors []error

	// Close the container watcher if it exists
	if idx.containerWatcher != nil {
		if watcherErr := idx.containerWatcher.Close(); watcherErr != nil {
			log.Printf("Error closing container watcher: %v", watcherErr)
			closeErrors = append(closeErrors, watcherErr)
		}
		idx.containerWatcher = nil
	}

	closeErrors = append(closeErrors, idx.serviceIndex.Close(), idx.parameterIndex.Close(), idx.prototypeIndex.Close())
	return errors.Join(closeErrors...)
}

func (idx *ServiceIndex) Clear() error {
	err := errors.Join(idx.serviceIndex.Clear(), idx.parameterIndex.Clear(), idx.prototypeIndex.Clear())
	if err == nil {
		idx.resetIndexedPaths()
		idx.prototypeRevision.Add(1)
	}
	return err
}

func (idx *ServiceIndex) ClearIn(mutation *indexer.Mutation) error {
	err := errors.Join(
		idx.serviceIndex.ClearIn(mutation),
		idx.parameterIndex.ClearIn(mutation),
		idx.prototypeIndex.ClearIn(mutation),
	)
	if err != nil {
		return err
	}
	if mutation == nil {
		idx.resetIndexedPaths()
	} else if err := mutation.AfterCommit(idx.resetIndexedPaths); err != nil {
		return err
	}
	idx.prototypeRevision.Add(1)
	return nil
}

// GetAllTags returns all tag names in the index
func (idx *ServiceIndex) GetAllTags() ([]string, error) {
	values, err := idx.serviceIndex.GetAllValues()
	if err != nil {
		return nil, err
	}

	tagMap := make(map[string]struct{})
	for _, value := range values {
		for tag := range value.Tags {
			tagMap[tag] = struct{}{}
		}
		for tag := range value.InstanceofTags {
			tagMap[tag] = struct{}{}
		}
	}
	prototypes, err := idx.prototypeIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	for _, prototype := range prototypes {
		for tag := range prototype.Tags {
			tagMap[tag] = struct{}{}
		}
		for tag := range prototype.InstanceofTags {
			tagMap[tag] = struct{}{}
		}
	}

	tags := make([]string, 0)
	for tag := range tagMap {
		tags = append(tags, tag)
	}

	sort.Strings(tags)

	return tags, nil
}

// GetServicesByTag returns all service IDs that have the specified tag
func (idx *ServiceIndex) GetServicesByTag(tagName string) ([]string, error) {
	values, err := idx.serviceIndex.GetAllValues()
	if err != nil {
		return nil, err
	}

	services := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	explicitDefinitions := make(map[string]struct{}, len(values))
	for _, value := range values {
		explicitDefinitions[value.ID] = struct{}{}
		if _, explicit := value.Tags[tagName]; explicit {
			services = append(services, value.ID)
			seen[value.ID] = struct{}{}
			continue
		}
		targetType, conditional := value.InstanceofTags[tagName]
		if !conditional || idx.phpIndex == nil {
			continue
		}
		matches, resolveErr := idx.phpIndex.IsSubtypeOf(value.Class, targetType)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if matches {
			services = append(services, value.ID)
			seen[value.ID] = struct{}{}
		}
	}

	prototypeServices, err := idx.expandedPrototypeServices()
	if err != nil {
		return nil, err
	}
	for _, service := range prototypeServices {
		if _, explicitDefinition := explicitDefinitions[service.ID]; explicitDefinition {
			continue
		}
		if _, explicitTag := service.Tags[tagName]; explicitTag {
			services = append(services, service.ID)
			seen[service.ID] = struct{}{}
			continue
		}
		targetType, conditional := service.InstanceofTags[tagName]
		if !conditional || idx.phpIndex == nil {
			continue
		}
		matches, resolveErr := idx.phpIndex.IsSubtypeOf(service.Class, targetType)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if matches {
			services = append(services, service.ID)
			seen[service.ID] = struct{}{}
		}
	}

	sort.Strings(services)
	return services, nil
}

// GetServicesByType returns concrete services whose configured class can be
// assigned to targetName. It includes explicit, prototype-expanded, and
// compiled-container definitions.
func (idx *ServiceIndex) GetServicesByType(
	targetName string,
) ([]Service, error) {
	targetName = strings.TrimPrefix(strings.TrimSpace(targetName), "\\")
	if targetName == "" {
		return nil, nil
	}
	explicit, err := idx.serviceIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	prototypes, err := idx.expandedPrototypeServices()
	if err != nil {
		return nil, err
	}
	candidates := append(explicit, prototypes...)
	if idx.containerWatcher != nil && idx.containerWatcher.ContainerExists() {
		candidates = append(
			candidates,
			idx.containerWatcher.GetAllServiceDefinitions()...,
		)
	}

	seen := make(map[string]struct{}, len(candidates))
	var result []Service
	for _, service := range candidates {
		className := strings.TrimPrefix(service.Class, "\\")
		if className == "" && strings.Contains(service.ID, "\\") {
			className = strings.TrimPrefix(service.ID, "\\")
		}
		if className == "" {
			continue
		}
		matches := strings.EqualFold(className, targetName)
		if !matches && idx.phpIndex != nil {
			var matchErr error
			matches, matchErr = idx.phpIndex.IsSubtypeOf(
				className,
				targetName,
			)
			if matchErr != nil {
				return nil, matchErr
			}
		}
		key := strings.ToLower(service.ID)
		if !matches {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, service)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].ID) <
			strings.ToLower(result[right].ID)
	})
	return result, nil
}

func (idx *ServiceIndex) withResolvedInstanceofTags(service Service) (Service, error) {
	if idx.phpIndex == nil || len(service.InstanceofTags) == 0 || service.Class == "" {
		return service, nil
	}
	resolvedTags := make(map[string]string, len(service.Tags)+len(service.InstanceofTags))
	for tag, value := range service.Tags {
		resolvedTags[tag] = value
	}
	service.Tags = resolvedTags
	for tag, targetType := range service.InstanceofTags {
		matches, err := idx.phpIndex.IsSubtypeOf(service.Class, targetType)
		if err != nil {
			return Service{}, err
		}
		if matches {
			service.Tags[tag] = ""
		}
	}
	return service, nil
}

func (idx *ServiceIndex) expandedPrototypeServices() ([]Service, error) {
	if idx.phpIndex == nil {
		return nil, nil
	}
	phpRevision := idx.phpIndex.Revision()
	prototypeRevision := idx.prototypeRevision.Load()

	idx.prototypeCacheMu.Lock()
	defer idx.prototypeCacheMu.Unlock()
	if idx.prototypeCacheSet && idx.prototypeCachePHP == phpRevision &&
		idx.prototypeCacheOwn == prototypeRevision {
		return cloneServices(idx.prototypeCache), nil
	}

	prototypes, err := idx.prototypeIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	classes := idx.phpIndex.ClassSymbols()
	sort.Slice(prototypes, func(left, right int) bool {
		if prototypes[left].Path == prototypes[right].Path {
			return prototypes[left].Line < prototypes[right].Line
		}
		return prototypes[left].Path < prototypes[right].Path
	})

	expanded := make(map[string]Service)
	for _, prototype := range prototypes {
		for _, class := range classes {
			if !prototypeMatchesClass(prototype, class) {
				continue
			}
			expanded[class.FullyQualified] = Service{
				ID:             class.FullyQualified,
				Class:          class.FullyQualified,
				Autowire:       prototype.Autowire,
				AutowireSet:    prototype.AutowireSet,
				Tags:           cloneStringMap(prototype.Tags),
				InstanceofTags: cloneStringMap(prototype.InstanceofTags),
				Path:           prototype.Path,
				Line:           prototype.Line,
				Range:          prototype.Range,
			}
		}
	}

	services := make([]Service, 0, len(expanded))
	for _, service := range expanded {
		services = append(services, service)
	}
	sort.Slice(services, func(left, right int) bool {
		return services[left].ID < services[right].ID
	})
	idx.prototypeCache = cloneServices(services)
	idx.prototypeCachePHP = phpRevision
	idx.prototypeCacheOwn = prototypeRevision
	idx.prototypeCacheSet = true
	return services, nil
}

func prototypeMatchesClass(prototype ServicePrototype, class semantic.Symbol) bool {
	if class.FullyQualified == "" || class.Kind != semantic.ClassSymbol ||
		class.Flags.Has(semantic.AbstractFlag) {
		return false
	}
	namespace := strings.TrimPrefix(prototype.Namespace, "\\")
	if namespace != "" {
		if !strings.HasSuffix(namespace, "\\") {
			namespace += "\\"
		}
		if !strings.HasPrefix(class.FullyQualified, namespace) {
			return false
		}
	}
	if !pathMatchesConfigPattern(class.Path, prototype.Resource) {
		return false
	}
	for _, exclude := range prototype.Excludes {
		if pathMatchesConfigPattern(class.Path, exclude) {
			return false
		}
	}
	return true
}

func pathMatchesConfigPattern(candidate, pattern string) bool {
	candidate = filepath.Clean(candidate)
	for _, expanded := range expandPathBraces(filepath.Clean(pattern)) {
		if !hasPathMeta(expanded) {
			if samePath(candidate, expanded) || pathWithin(candidate, expanded) {
				return true
			}
			continue
		}
		if matched, err := filepath.Match(expanded, candidate); err == nil && matched {
			return true
		}
		if strings.Contains(expanded, "**") && doubleStarMatch(expanded, candidate) {
			return true
		}
	}
	return false
}

func doubleStarMatch(pattern, candidate string) bool {
	pattern = filepath.ToSlash(pattern)
	candidate = filepath.ToSlash(candidate)
	var expression strings.Builder
	expression.WriteByte('^')
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				expression.WriteString(".*")
				index++
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(pattern[index : index+1]))
		}
	}
	expression.WriteByte('$')
	matched, err := regexp.MatchString(expression.String(), candidate)
	return err == nil && matched
}

func expandPathBraces(pattern string) []string {
	open := strings.IndexByte(pattern, '{')
	if open < 0 {
		return []string{pattern}
	}
	close := strings.IndexByte(pattern[open+1:], '}')
	if close < 0 {
		return []string{pattern}
	}
	close += open + 1
	var expanded []string
	for _, alternative := range strings.Split(pattern[open+1:close], ",") {
		value := pattern[:open] + alternative + pattern[close+1:]
		expanded = append(expanded, expandPathBraces(value)...)
	}
	return expanded
}

func hasPathMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func pathWithin(candidate, directory string) bool {
	relative, err := filepath.Rel(directory, candidate)
	if err != nil || relative == "." || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func cloneServices(services []Service) []Service {
	cloned := make([]Service, len(services))
	for index, service := range services {
		service.Tags = cloneStringMap(service.Tags)
		service.InstanceofTags = cloneStringMap(service.InstanceofTags)
		cloned[index] = service
	}
	return cloned
}

// GetAllParameters returns all parameter names in the index
func (idx *ServiceIndex) GetAllParameters() ([]Parameter, error) {
	values, err := idx.parameterIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(values))
	for _, parameter := range values {
		seen[strings.ToLower(parameter.Name)] = struct{}{}
	}
	if idx.containerWatcher != nil && idx.containerWatcher.ContainerExists() {
		for _, parameter := range idx.containerWatcher.GetAllParameters() {
			key := strings.ToLower(parameter.Name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			values = append(values, parameter)
		}
	}
	sort.Slice(values, func(left, right int) bool {
		return strings.ToLower(values[left].Name) <
			strings.ToLower(values[right].Name)
	})
	return values, nil
}

// GetParameterByName returns a specific parameter value by its name
func (idx *ServiceIndex) GetParameterByName(name string) (Parameter, bool, error) {
	values, err := idx.parameterIndex.GetValues(name)
	if err != nil {
		return Parameter{}, false, err
	}
	if len(values) == 0 && strings.ToLower(name) != name {
		values, err = idx.parameterIndex.GetValues(strings.ToLower(name))
		if err != nil {
			return Parameter{}, false, err
		}
	}
	if len(values) == 0 {
		if idx.containerWatcher != nil && idx.containerWatcher.ContainerExists() {
			parameter, found := idx.containerWatcher.GetParameterByName(name)
			if found {
				return parameter, true, nil
			}
			parameter, found = idx.containerWatcher.GetParameterByName(
				strings.ToLower(name),
			)
			return parameter, found, nil
		}
		return Parameter{}, false, nil
	}
	return values[0], true, nil
}

type Location struct {
	Path string
	Line int
}

func (idx *ServiceIndex) GetServicesUsageByClassName(className string) ([]Location, error) {
	className = strings.TrimPrefix(className, "\\")
	values, err := idx.serviceIndex.GetAllValues()
	if err != nil {
		return nil, err
	}

	locations := make([]Location, 0)
	seen := make(map[Location]struct{})
	appendMatching := func(value Service) {
		if !strings.EqualFold(
			strings.TrimPrefix(value.Class, "\\"),
			className,
		) {
			return
		}
		location := Location{Path: value.Path, Line: value.Line}
		if _, exists := seen[location]; exists {
			return
		}
		seen[location] = struct{}{}
		locations = append(locations, location)
	}

	for _, value := range values {
		appendMatching(value)
	}
	prototypeServices, err := idx.expandedPrototypeServices()
	if err != nil {
		return nil, err
	}
	for _, value := range prototypeServices {
		appendMatching(value)
	}
	if idx.containerWatcher != nil &&
		idx.containerWatcher.ContainerExists() {
		for _, value := range idx.containerWatcher.
			GetAllServiceDefinitions() {
			appendMatching(value)
		}
	}

	sort.Slice(locations, func(left, right int) bool {
		if locations[left].Path != locations[right].Path {
			return locations[left].Path < locations[right].Path
		}
		return locations[left].Line < locations[right].Line
	})
	return locations, nil
}

// GetAutowiredServicesUsageByClassName returns source definitions for services
// whose effective autowire setting is enabled. Defaults and prototype
// expansion are already materialized by the parsers; parent definitions are
// followed here because child definitions inherit unset options.
func (idx *ServiceIndex) GetAutowiredServicesUsageByClassName(
	className string,
) ([]Location, error) {
	if idx == nil || className == "" {
		return nil, nil
	}
	definitions, err := idx.GetAllServiceDefinitions()
	if err != nil {
		return nil, err
	}

	byID := make(map[string][]Service, len(definitions))
	for _, service := range definitions {
		key := strings.ToLower(strings.TrimPrefix(service.ID, "\\"))
		if key != "" {
			byID[key] = append(byID[key], service)
		}
	}

	className = strings.TrimPrefix(className, "\\")
	var locations []Location
	seen := make(map[Location]struct{})
	for _, service := range definitions {
		if !strings.EqualFold(
			strings.TrimPrefix(service.Class, "\\"),
			className,
		) || !effectiveServiceAutowire(
			service,
			byID,
			make(map[string]struct{}),
		) {
			continue
		}
		location := Location{Path: service.Path, Line: service.Line}
		if location.Path == "" {
			continue
		}
		if _, exists := seen[location]; exists {
			continue
		}
		seen[location] = struct{}{}
		locations = append(locations, location)
	}
	sort.Slice(locations, func(left, right int) bool {
		if locations[left].Path != locations[right].Path {
			return locations[left].Path < locations[right].Path
		}
		return locations[left].Line < locations[right].Line
	})
	return locations, nil
}

func effectiveServiceAutowire(
	service Service,
	definitions map[string][]Service,
	visiting map[string]struct{},
) bool {
	if service.AutowireSet {
		return service.Autowire
	}
	parent := strings.ToLower(strings.TrimPrefix(service.Parent, "\\"))
	if parent == "" {
		return false
	}
	if _, recursive := visiting[parent]; recursive {
		return false
	}
	visiting[parent] = struct{}{}
	defer delete(visiting, parent)
	for _, definition := range definitions[parent] {
		if effectiveServiceAutowire(definition, definitions, visiting) {
			return true
		}
	}
	return false
}
