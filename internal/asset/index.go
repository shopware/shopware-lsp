package asset

import (
	"bytes"
	"errors"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

var staticAssetExtensions = map[string]struct{}{
	".avif": {}, ".bmp": {}, ".css": {}, ".eot": {}, ".gif": {},
	".ico": {}, ".jpeg": {}, ".jpg": {}, ".js": {}, ".json": {},
	".less": {}, ".map": {}, ".mjs": {}, ".mp3": {}, ".mp4": {},
	".ogg": {}, ".otf": {}, ".pdf": {}, ".png": {}, ".sass": {},
	".scss": {}, ".svg": {}, ".ttf": {}, ".webm": {}, ".webp": {},
	".woff": {}, ".woff2": {}, ".coffee": {}, ".dart": {},
}

type Index struct {
	root           string
	publicRoots    [2]string
	normalizedRoot string
	records        *indexer.DataIndexer[Resource]
	usages         *indexer.DataIndexer[Usage]
	packages       *indexer.DataIndexer[Package]

	pathsMu       sync.RWMutex
	resourcePaths map[string]struct{}
	usagePaths    map[string]struct{}

	cacheMu         sync.Mutex
	cacheGeneration uint64
	cacheBuiltAt    uint64
	cachedResources []Resource
	cachedNames     NameCatalog
	namesCacheReady bool

	asseticCatalog *asseticCatalog
}

// NameCatalog is an immutable generation-scoped view of the asset names used
// together by diagnostics. Its slices must not be modified by callers.
type NameCatalog struct {
	Assets           []string
	EncoreEntries    []string
	ImportmapEntries []string
	ViteEntries      []string
}

func NewIndex(
	root,
	configDir string,
	stores ...*indexer.Store,
) (*Index, error) {
	records, err := indexer.NewRepository[Resource](
		filepath.Join(configDir, "assets.db"),
		"symfony.assets",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	usages, err := indexer.NewRepository[Usage](
		filepath.Join(configDir, "assets.db"),
		"symfony.asset_usages",
		stores...,
	)
	if err != nil {
		_ = records.Close()
		return nil, err
	}
	packages, err := indexer.NewRepository[Package](
		filepath.Join(configDir, "assets.db"),
		"symfony.asset_packages",
		stores...,
	)
	if err != nil {
		_ = records.Close()
		_ = usages.Close()
		return nil, err
	}
	resourcePaths, err := repositoryPathSet(records)
	if err != nil {
		_ = records.Close()
		_ = usages.Close()
		_ = packages.Close()
		return nil, err
	}
	usagePaths, err := repositoryPathSet(usages)
	if err != nil {
		_ = records.Close()
		_ = usages.Close()
		_ = packages.Close()
		return nil, err
	}
	cleanRoot := filepath.Clean(root)
	return &Index{
		root:           cleanRoot,
		publicRoots:    [2]string{filepath.Join(cleanRoot, "public"), filepath.Join(cleanRoot, "web")},
		normalizedRoot: filepath.ToSlash(cleanRoot),
		records:        records,
		usages:         usages,
		packages:       packages,
		resourcePaths:  resourcePaths,
		usagePaths:     usagePaths,
		asseticCatalog: newAsseticCatalog(filepath.Clean(root)),
	}, nil
}

func (idx *Index) ID() string {
	return "symfony.assets"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	resourceCandidate := idx.isResourceCandidate(file.Path)
	usageCandidate := isUsageCandidate(file)
	packageCandidate := idx.isPackageCandidate(file.Path)
	resourceTracked, usageTracked := idx.tracked(file.Path)
	if !resourceCandidate && !usageCandidate && !packageCandidate &&
		!resourceTracked && !usageTracked {
		return nil
	}
	if packageCandidate {
		packageWrite := map[string]map[string]Package{file.Path: {}}
		tree := file.SyntaxTree()
		if tree != nil && tree.Root != nil {
			var packages []Package
			switch file.Extension() {
			case ".xml":
				packages = PackagesInXML(file.Path, tree.Root)
			case ".yaml", ".yml":
				packages = PackagesInYAML(file.Path, tree.Root)
			case ".php":
				packages = PackagesInPHP(file.Path, tree.Root)
			}
			for position, current := range packages {
				key := strings.ToLower(current.Name) + ":" +
					strings.ToLower(current.BasePath)
				if _, duplicate := packageWrite[file.Path][key]; duplicate {
					key += "#" + strconv.Itoa(position)
				}
				packageWrite[file.Path][key] = current
			}
		}
		if err := idx.packages.BatchSaveItemsIn(
			file.Mutation(),
			packageWrite,
		); err != nil {
			return err
		}
	}
	if resourceCandidate || resourceTracked {
		resources := idx.resourcesInFile(file)
		write := map[string]map[string]Resource{
			file.Path: {},
		}
		for position, resource := range resources {
			resource.Name = normalizeName(resource.Name)
			if resource.Name == "" {
				continue
			}
			key := strconv.Itoa(int(resource.Kind)) + ":" +
				strings.ToLower(resource.Name)
			if _, exists := write[file.Path][key]; exists {
				key += "#" + strconv.Itoa(position)
			}
			write[file.Path][key] = resource
		}
		if err := idx.records.BatchSaveItemsIn(
			file.Mutation(),
			write,
		); err != nil {
			return err
		}
		if err := idx.publishPath(
			file.Path,
			true,
			len(write[file.Path]) != 0,
			file.Mutation(),
		); err != nil {
			return err
		}
	}
	if usageCandidate || usageTracked {
		usageWrite := map[string]map[string]Usage{file.Path: {}}
		tree := file.SyntaxTree()
		if tree != nil && tree.Root != nil {
			for position, reference := range References(file.Path, tree.Root) {
				if reference.Name == "" {
					continue
				}
				usage := Usage{
					Name:    reference.Name,
					Package: reference.Package,
					Kind:    reference.Kind,
					File:    file.Path,
					Range:   ReferenceRange(reference),
				}
				key := strconv.Itoa(int(usage.Kind)) + ":" +
					strings.ToLower(usage.Name) + ":" +
					strconv.FormatUint(uint64(usage.Range.Start), 10)
				if _, exists := usageWrite[file.Path][key]; exists {
					key += "#" + strconv.Itoa(position)
				}
				usageWrite[file.Path][key] = usage
			}
		}
		if err := idx.usages.BatchSaveItemsIn(
			file.Mutation(),
			usageWrite,
		); err != nil {
			return err
		}
		return idx.publishPath(
			file.Path,
			false,
			len(usageWrite[file.Path]) != 0,
			file.Mutation(),
		)
	}
	return nil
}

func (idx *Index) resourcesInFile(
	file *indexer.ParsedFile,
) []Resource {
	base := strings.ToLower(filepath.Base(file.Path))
	switch base {
	case "manifest.json":
		if isAssetMetadataPath(file.Path) {
			return parseManifest(file.Path, file.SyntaxTree())
		}
	case "entrypoints.json":
		if isAssetMetadataPath(file.Path) {
			return parseEntrypoints(file.Path, file.SyntaxTree())
		}
	case "importmap.php":
		return parseImportmap(file.Path, file.SyntaxTree())
	case "installed.php":
		if !isComposerInstalledPath(file.Path) {
			return parseImportmap(file.Path, file.SyntaxTree())
		}
	case "webpack.config.js":
		return parseWebpackConfig(file.Path, file.SyntaxTree())
	case "vite.config.js", "vite.config.ts":
		return parseViteConfig(file.Path, file.SyntaxTree())
	}
	if name, found := idx.publicName(file.Path); found &&
		isStaticAssetPath(file.Path) {
		return []Resource{{
			Name:   name,
			File:   file.Path,
			Target: file.Path,
			Kind:   PublicFile,
		}}
	}
	return nil
}

func (idx *Index) Resources() ([]Resource, error) {
	resources, err := idx.ResourcesView()
	if err != nil {
		return nil, err
	}
	return append([]Resource(nil), resources...), nil
}

// ResourcesView returns the immutable cached resource generation without a
// per-call clone. Callers must not modify the returned slice or resources.
func (idx *Index) ResourcesView() ([]Resource, error) {
	if idx == nil {
		return nil, nil
	}
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	return idx.resourcesViewLocked()
}

func (idx *Index) resourcesViewLocked() ([]Resource, error) {
	if idx.cachedResources != nil &&
		idx.cacheBuiltAt == idx.cacheGeneration {
		return idx.cachedResources, nil
	}
	resources, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	sort.Slice(resources, func(left, right int) bool {
		if resources[left].Name != resources[right].Name {
			return strings.ToLower(resources[left].Name) <
				strings.ToLower(resources[right].Name)
		}
		if resources[left].Kind != resources[right].Kind {
			return resources[left].Kind < resources[right].Kind
		}
		return resources[left].File < resources[right].File
	})
	idx.cachedResources = append([]Resource{}, resources...)
	idx.cacheBuiltAt = idx.cacheGeneration
	idx.namesCacheReady = false
	return idx.cachedResources, nil
}

func (idx *Index) Names() ([]string, error) {
	catalog, err := idx.NameCatalog()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), catalog.Assets...), nil
}

func (idx *Index) EntryNames() ([]string, error) {
	catalog, err := idx.NameCatalog()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), catalog.EncoreEntries...), nil
}

func (idx *Index) ImportmapEntryNames() ([]string, error) {
	catalog, err := idx.NameCatalog()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), catalog.ImportmapEntries...), nil
}

func (idx *Index) ViteEntryNames() ([]string, error) {
	catalog, err := idx.NameCatalog()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), catalog.ViteEntries...), nil
}

func (idx *Index) NameCatalog() (NameCatalog, error) {
	if idx == nil {
		return NameCatalog{}, nil
	}
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	resources, err := idx.resourcesViewLocked()
	if err != nil {
		return NameCatalog{}, err
	}
	if idx.namesCacheReady {
		return idx.cachedNames, nil
	}
	var catalog NameCatalog
	assetSeen := make(map[string]struct{}, len(resources))
	encoreSeen := make(map[string]struct{})
	importmapSeen := make(map[string]struct{})
	viteSeen := make(map[string]struct{})
	for _, resource := range resources {
		key := strings.ToLower(resource.Name)
		switch resource.Kind {
		case EncoreEntry:
			if _, duplicate := encoreSeen[key]; !duplicate {
				encoreSeen[key] = struct{}{}
				catalog.EncoreEntries = append(catalog.EncoreEntries, resource.Name)
			}
		case ImportmapModule:
			if resource.Entrypoint {
				if _, duplicate := importmapSeen[key]; !duplicate {
					importmapSeen[key] = struct{}{}
					catalog.ImportmapEntries = append(catalog.ImportmapEntries, resource.Name)
				}
			}
		case ViteEntry:
			if _, duplicate := viteSeen[key]; !duplicate {
				viteSeen[key] = struct{}{}
				catalog.ViteEntries = append(catalog.ViteEntries, resource.Name)
			}
		default:
			if _, duplicate := assetSeen[key]; !duplicate {
				assetSeen[key] = struct{}{}
				catalog.Assets = append(catalog.Assets, resource.Name)
			}
		}
	}
	idx.cachedNames = catalog
	idx.namesCacheReady = true
	return catalog, nil
}

// FindImportmapEntrypoint returns the entrypoint declaration together with
// installed-package metadata for the same module. A non-entrypoint module is
// intentionally not a valid argument to Twig's importmap() function.
func (idx *Index) FindImportmapEntrypoint(
	name string,
) ([]Resource, error) {
	resources, err := idx.Find(name, ImportmapModule)
	if err != nil {
		return nil, err
	}
	found := false
	for _, resource := range resources {
		if resource.Entrypoint {
			found = true
			break
		}
	}
	if !found {
		return nil, nil
	}
	return resources, nil
}

func (idx *Index) Find(name string, kind Kind) ([]Resource, error) {
	resources, err := idx.ResourcesView()
	if err != nil {
		return nil, err
	}
	name = normalizeName(name)
	var result []Resource
	for _, resource := range resources {
		if resource.Kind == kind &&
			strings.EqualFold(resource.Name, name) {
			result = append(result, resource)
		}
	}
	return result, nil
}

func (idx *Index) ViteEntriesForTarget(
	target string,
) ([]Resource, error) {
	if idx == nil || strings.TrimSpace(target) == "" {
		return nil, nil
	}
	resources, err := idx.ResourcesView()
	if err != nil {
		return nil, err
	}
	target = filepath.Clean(target)
	var result []Resource
	for _, resource := range resources {
		if resource.Kind == ViteEntry && resource.Target != "" &&
			filepath.Clean(resource.Target) == target {
			result = append(result, resource)
		}
	}
	return result, nil
}

func (idx *Index) FindAssets(name string) ([]Resource, error) {
	resources, err := idx.ResourcesView()
	if err != nil {
		return nil, err
	}
	name = normalizeName(name)
	var result []Resource
	for _, resource := range resources {
		if resource.Kind != EncoreEntry &&
			resource.Kind != ViteEntry &&
			strings.EqualFold(resource.Name, name) {
			result = append(result, resource)
		}
	}
	return result, nil
}

func (idx *Index) Packages() ([]Package, error) {
	if idx == nil {
		return nil, nil
	}
	values, err := idx.packages.GetAllValues()
	if err != nil {
		return nil, err
	}
	return deduplicatePackages(values), nil
}

func (idx *Index) PackageNames() ([]string, error) {
	packages, err := idx.Packages()
	if err != nil {
		return nil, err
	}
	var result []string
	seen := make(map[string]struct{}, len(packages))
	for _, current := range packages {
		key := strings.ToLower(current.Name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, current.Name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result, nil
}

func (idx *Index) FindPackages(name string) ([]Package, error) {
	packages, err := idx.Packages()
	if err != nil {
		return nil, err
	}
	var result []Package
	for _, current := range packages {
		if strings.EqualFold(current.Name, strings.TrimSpace(name)) {
			result = append(result, current)
		}
	}
	return result, nil
}

func (idx *Index) FindAssetsForPackage(
	name,
	packageName string,
) ([]Resource, error) {
	if strings.TrimSpace(packageName) == "" {
		return idx.FindAssets(name)
	}
	packages, err := idx.FindPackages(packageName)
	if err != nil || len(packages) == 0 {
		return nil, err
	}
	resources, err := idx.ResourcesView()
	if err != nil {
		return nil, err
	}
	targets := packageAssetTargets(name, packageName, packages)
	var result []Resource
	for _, resource := range resources {
		if resource.Kind == EncoreEntry || resource.Kind == ViteEntry {
			continue
		}
		for _, target := range targets {
			if strings.EqualFold(resource.Name, target) ||
				target == "theme/*/"+normalizeName(name) &&
					themeResourceMatches(resource.Name, name) {
				result = append(result, resource)
				break
			}
		}
	}
	return result, nil
}

// FindAsseticAssets resolves a direct file, directory, or glob operand from a
// legacy Assetic stylesheets/javascripts tag. Bundle operands use their
// @Bundle/Resources/public prefix even when no explicit Asset package exists.
func (idx *Index) FindAsseticAssets(
	name,
	packageName string,
) ([]Resource, error) {
	name = normalizeName(name)
	if name == "" {
		return nil, nil
	}
	resources, err := idx.ResourcesView()
	if err != nil {
		return nil, err
	}
	pattern := name
	if packageName != "" {
		basePath := asseticBundleBasePath(packageName)
		if basePath == "" {
			return nil, nil
		}
		pattern = normalizeName(basePath + "/" + name)
	}
	var result []Resource
	for _, resource := range resources {
		if resource.Kind == EncoreEntry || resource.Kind == ViteEntry {
			continue
		}
		if asseticResourceMatches(resource.Name, pattern) {
			result = append(result, resource)
		}
	}
	return result, nil
}

func (idx *Index) AsseticNamedNames() ([]string, error) {
	resources, err := idx.asseticNamedResources(false)
	if err != nil {
		return nil, err
	}
	var result []string
	seen := make(map[string]struct{})
	for _, resource := range resources {
		key := strings.ToLower(resource.Name)
		if resource.Name == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, resource.Name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result, nil
}

func (idx *Index) FindAsseticNamed(name string) ([]Resource, error) {
	resources, err := idx.asseticNamedResources(false)
	if err != nil {
		return nil, err
	}
	var result []Resource
	for _, resource := range resources {
		if strings.EqualFold(resource.Name, strings.TrimPrefix(name, "@")) {
			result = append(result, resource)
		}
	}
	return result, nil
}

func (idx *Index) ReloadAsseticCatalog() error {
	if idx == nil || idx.asseticCatalog == nil {
		return nil
	}
	_, err := idx.asseticCatalog.resources(true)
	return err
}

func (idx *Index) asseticNamedResources(
	force bool,
) ([]Resource, error) {
	if idx == nil || idx.asseticCatalog == nil {
		return nil, nil
	}
	return idx.asseticCatalog.resources(force)
}

func (idx *Index) AsseticNames(packageName string) ([]string, error) {
	resources, err := idx.ResourcesView()
	if err != nil {
		return nil, err
	}
	prefix := ""
	if packageName != "" {
		prefix = asseticBundleBasePath(packageName)
		if prefix == "" {
			return nil, nil
		}
		prefix += "/"
	}
	var result []string
	seen := make(map[string]struct{})
	for _, resource := range resources {
		if resource.Kind == EncoreEntry || resource.Kind == ViteEntry {
			continue
		}
		name := normalizeName(resource.Name)
		if prefix != "" {
			if !strings.HasPrefix(
				strings.ToLower(name),
				strings.ToLower(prefix),
			) {
				continue
			}
			name = name[len(prefix):]
		}
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
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result, nil
}

func asseticResourceMatches(name, pattern string) bool {
	name = normalizeName(name)
	pattern = normalizeName(pattern)
	if strings.EqualFold(name, pattern) {
		return true
	}
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(
			strings.ToLower(name),
			strings.ToLower(pattern),
		)
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return false
	}
	matched, err := pathpkg.Match(
		strings.ToLower(pattern),
		strings.ToLower(name),
	)
	return err == nil && matched
}

func asseticBundleBasePath(packageName string) string {
	name := strings.TrimPrefix(strings.TrimSpace(packageName), "@")
	name = strings.TrimSuffix(strings.ToLower(name), "bundle")
	if name == "" {
		return ""
	}
	return "bundles/" + name
}

func (idx *Index) NamesForPackage(packageName string) ([]string, error) {
	if strings.TrimSpace(packageName) == "" {
		return idx.Names()
	}
	packages, err := idx.FindPackages(packageName)
	if err != nil || len(packages) == 0 {
		return nil, err
	}
	resources, err := idx.ResourcesView()
	if err != nil {
		return nil, err
	}
	var result []string
	seen := make(map[string]struct{})
	for _, resource := range resources {
		if resource.Kind == EncoreEntry || resource.Kind == ViteEntry {
			continue
		}
		for _, current := range packages {
			logical, matches := "", false
			if strings.EqualFold(packageName, "theme") &&
				current.BasePath == "" {
				logical, matches = themeLogicalName(resource.Name)
			} else {
				logical, matches = packageLogicalName(
					resource.Name,
					current,
				)
			}
			if !matches {
				continue
			}
			key := strings.ToLower(logical)
			if _, duplicate := seen[key]; duplicate {
				break
			}
			seen[key] = struct{}{}
			result = append(result, logical)
			break
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result, nil
}

func (idx *Index) Usages(
	name string,
	kind ReferenceKind,
) ([]Usage, error) {
	if idx == nil {
		return nil, nil
	}
	values, err := idx.usages.GetAllValues()
	if err != nil {
		return nil, err
	}
	name = normalizeName(name)
	var result []Usage
	for _, usage := range values {
		if usage.Kind == kind && strings.EqualFold(usage.Name, name) {
			result = append(result, usage)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

func (idx *Index) UsagesForPackage(
	name string,
	kind ReferenceKind,
	packageName string,
) ([]Usage, error) {
	values, err := idx.Usages(name, kind)
	if err != nil {
		return nil, err
	}
	var result []Usage
	for _, usage := range values {
		if strings.EqualFold(usage.Package, packageName) {
			result = append(result, usage)
		}
	}
	return result, nil
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	err := errors.Join(
		idx.records.BatchDeleteByFilePaths(paths),
		idx.usages.BatchDeleteByFilePaths(paths),
		idx.packages.BatchDeleteByFilePaths(paths),
	)
	if err == nil {
		idx.removePaths(paths)
		idx.invalidateResources()
	}
	return err
}

func (idx *Index) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil {
		return nil
	}
	err := errors.Join(
		idx.records.BatchDeleteByFilePathsIn(mutation, paths),
		idx.usages.BatchDeleteByFilePathsIn(mutation, paths),
		idx.packages.BatchDeleteByFilePathsIn(mutation, paths),
	)
	if err != nil {
		return err
	}
	if mutation == nil {
		idx.removePaths(paths)
		idx.invalidateResources()
		return nil
	}
	return mutation.AfterCommit(func() {
		idx.removePaths(paths)
		idx.invalidateResources()
	})
}

func (idx *Index) Clear() error {
	if idx == nil {
		return nil
	}
	err := errors.Join(
		idx.records.Clear(),
		idx.usages.Clear(),
		idx.packages.Clear(),
	)
	if err == nil {
		idx.resetPaths()
		idx.invalidateResources()
	}
	return err
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	err := errors.Join(
		idx.records.ClearIn(mutation),
		idx.usages.ClearIn(mutation),
		idx.packages.ClearIn(mutation),
	)
	if err != nil {
		return err
	}
	if mutation == nil {
		idx.resetPaths()
		idx.invalidateResources()
		return nil
	}
	return mutation.AfterCommit(func() {
		idx.resetPaths()
		idx.invalidateResources()
	})
}

func (idx *Index) Close() error {
	if idx == nil {
		return nil
	}
	return errors.Join(
		idx.records.Close(),
		idx.usages.Close(),
		idx.packages.Close(),
	)
}

func (idx *Index) ShouldEnterDirectory(path string) bool {
	relative, found := idx.publicRelative(path)
	if !found {
		_, _, bundlePublic := idx.bundlePublicRelative(path)
		return bundlePublic
	}
	if relative == "" {
		return true
	}
	first := relative
	if position := strings.IndexByte(first, byte(os.PathSeparator)); position >= 0 {
		first = first[:position]
	}
	return !isDynamicPublicDirectory(first)
}

func (idx *Index) ShouldIndexPath(path string) bool {
	relative, found := idx.publicRelative(path)
	if found {
		return relative != "" &&
			isIndexablePublicRelative(relative) &&
			isStaticAssetPath(path)
	}
	_, bundleAsset := idx.bundlePublicName(path)
	return bundleAsset && isStaticAssetPath(path)
}

func (idx *Index) ShouldPreparsePath(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "manifest.json", "entrypoints.json":
		return isAssetMetadataPath(path)
	case "importmap.php":
		return true
	case "installed.php":
		return !isComposerInstalledPath(path)
	default:
		return false
	}
}

func (idx *Index) publicName(path string) (string, bool) {
	relative, found := idx.publicRelative(path)
	if found && relative != "" {
		return normalizeName(filepath.ToSlash(relative)), true
	}
	return idx.bundlePublicName(path)
}

func (idx *Index) bundlePublicName(path string) (string, bool) {
	relative, publicName, found := idx.bundlePublicRelative(path)
	if !found || relative == "" {
		return "", false
	}
	return "bundles/" + publicName + "/" + relative, true
}

func (idx *Index) bundlePublicRelative(
	path string,
) (string, string, bool) {
	path = filepath.Clean(path)
	normalized := filepath.ToSlash(path)
	const marker = "/Resources/public"
	position := strings.LastIndex(normalized, marker)
	if position < 0 ||
		len(normalized) > position+len(marker) &&
			normalized[position+len(marker)] != '/' {
		return "", "", false
	}
	bundleDir := normalized[:position]
	if !slashPathWithin(idx.normalizedRoot, bundleDir) {
		return "", "", false
	}
	bundle := pathpkg.Base(bundleDir)
	publicName := strings.TrimSuffix(strings.ToLower(bundle), "bundle")
	relative := normalizeName(normalized[position+len(marker):])
	if publicName == "" {
		return "", "", false
	}
	return relative, publicName, true
}

func (idx *Index) publicRelative(path string) (string, bool) {
	path = filepath.Clean(path)
	for _, root := range idx.publicRoots {
		if path == root {
			return "", true
		}
		if len(path) > len(root) &&
			strings.HasPrefix(path, root) &&
			os.IsPathSeparator(path[len(root)]) {
			return path[len(root)+1:], true
		}
	}
	return "", false
}

func slashPathWithin(root, candidate string) bool {
	if root == "." {
		return candidate == "." ||
			candidate != ".." &&
				!strings.HasPrefix(candidate, "../") &&
				!strings.HasPrefix(candidate, "/")
	}
	if strings.HasSuffix(root, "/") {
		return strings.HasPrefix(candidate, root)
	}
	return candidate == root ||
		len(candidate) > len(root) &&
			strings.HasPrefix(candidate, root) &&
			candidate[len(root)] == '/'
}

func isIndexablePublicRelative(relative string) bool {
	position := strings.IndexByte(relative, byte(os.PathSeparator))
	return position < 0 || !isDynamicPublicDirectory(relative[:position])
}

func isDynamicPublicDirectory(name string) bool {
	return strings.EqualFold(name, "cache") ||
		strings.EqualFold(name, "files") ||
		strings.EqualFold(name, "media") ||
		strings.EqualFold(name, "thumbnail") ||
		strings.EqualFold(name, "thumbnails") ||
		strings.EqualFold(name, "upload") ||
		strings.EqualFold(name, "uploads")
}

func isStaticAssetPath(path string) bool {
	_, exists := staticAssetExtensions[strings.ToLower(filepath.Ext(path))]
	return exists
}

func isAssetMetadataPath(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(normalized, "/public/") ||
		strings.Contains(normalized, "/web/") ||
		strings.Contains(normalized, "/build/") ||
		strings.Contains(normalized, "/.vite/")
}

func (idx *Index) isResourceCandidate(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == "webpack.config.js" {
		return true
	}
	if base == "vite.config.js" || base == "vite.config.ts" {
		return true
	}
	if base == "importmap.php" ||
		base == "installed.php" && !isComposerInstalledPath(path) {
		return true
	}
	if (base == "manifest.json" || base == "entrypoints.json") &&
		isAssetMetadataPath(path) {
		return true
	}
	_, found := idx.publicName(path)
	return found && isStaticAssetPath(path)
}

func isComposerInstalledPath(path string) bool {
	return strings.EqualFold(filepath.Base(filepath.Dir(path)), "composer")
}

func (idx *Index) isPackageCandidate(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".php" {
		normalized := filepath.ToSlash(filepath.Clean(path))
		if strings.Contains(normalized, "/DependencyInjection/") {
			return true
		}
		relative, err := filepath.Rel(idx.root, path)
		if err != nil {
			return false
		}
		relative = filepath.ToSlash(relative)
		return strings.HasPrefix(relative, "config/")
	}
	if extension != ".xml" && extension != ".yaml" && extension != ".yml" {
		return false
	}
	normalized := filepath.ToSlash(filepath.Clean(path))
	if strings.Contains(normalized, "/Resources/config/") ||
		strings.Contains(normalized, "/DependencyInjection/") {
		return true
	}
	relative, err := filepath.Rel(idx.root, path)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return false
	}
	relative = filepath.ToSlash(relative)
	return relative == "config" ||
		strings.HasPrefix(relative, "config/")
}

func isUsageCandidate(file *indexer.ParsedFile) bool {
	switch file.Extension() {
	case ".php":
		return bytes.Contains(file.Content, []byte("getUrl")) ||
			bytes.Contains(file.Content, []byte("getVersion"))
	case ".twig":
		return bytes.Contains(file.Content, []byte("asset(")) ||
			bytes.Contains(file.Content, []byte("importmap(")) ||
			bytes.Contains(file.Content, []byte("encore_entry_")) ||
			bytes.Contains(file.Content, []byte("vite_entry_")) ||
			bytes.Contains(file.Content, []byte("<link")) ||
			bytes.Contains(file.Content, []byte("<script")) ||
			bytes.Contains(file.Content, []byte("<img"))
	default:
		return false
	}
}

func repositoryPathSet[T any](
	repository *indexer.DataIndexer[T],
) (map[string]struct{}, error) {
	paths, err := repository.GetAllFilePaths()
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		result[path] = struct{}{}
	}
	return result, nil
}

func (idx *Index) tracked(path string) (bool, bool) {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, resource := idx.resourcePaths[path]
	_, usage := idx.usagePaths[path]
	return resource, usage
}

func (idx *Index) publishPath(
	path string,
	resource,
	present bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.pathsMu.Lock()
		paths := idx.usagePaths
		if resource {
			paths = idx.resourcePaths
		}
		if present {
			paths[path] = struct{}{}
		} else {
			delete(paths, path)
		}
		idx.pathsMu.Unlock()
		if resource {
			idx.invalidateResources()
		}
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *Index) removePaths(paths []string) {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	for _, path := range paths {
		delete(idx.resourcePaths, path)
		delete(idx.usagePaths, path)
	}
}

func (idx *Index) resetPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	clear(idx.resourcePaths)
	clear(idx.usagePaths)
}

func (idx *Index) invalidateResources() {
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	idx.cacheGeneration++
	idx.cachedResources = nil
	idx.cachedNames = NameCatalog{}
	idx.namesCacheReady = false
}

func packageAssetTargets(
	name,
	packageName string,
	packages []Package,
) []string {
	name = normalizeName(name)
	var result []string
	seen := make(map[string]struct{})
	add := func(value string) {
		value = normalizeName(value)
		key := strings.ToLower(value)
		if value == "" || key == "" {
			return
		}
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	for _, current := range packages {
		if current.BasePath == "" {
			add(name)
			continue
		}
		add(current.BasePath + "/" + name)
	}
	if strings.EqualFold(packageName, "theme") {
		add("theme/*/" + name)
	}
	return result
}

func packageLogicalName(
	resourceName string,
	current Package,
) (string, bool) {
	resourceName = normalizeName(resourceName)
	if current.BasePath == "" {
		return resourceName, true
	}
	prefix := normalizeName(current.BasePath) + "/"
	if len(resourceName) <= len(prefix) ||
		!strings.EqualFold(resourceName[:len(prefix)], prefix) {
		return "", false
	}
	return resourceName[len(prefix):], true
}

func themeResourceMatches(resourceName, logicalName string) bool {
	logical, found := themeLogicalName(resourceName)
	return found && strings.EqualFold(
		logical,
		normalizeName(logicalName),
	)
}

func themeLogicalName(resourceName string) (string, bool) {
	parts := strings.Split(normalizeName(resourceName), "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "theme") {
		return "", false
	}
	return strings.Join(parts[2:], "/"), true
}
