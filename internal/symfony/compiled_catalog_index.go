package symfony

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// CompiledCatalogIndex feeds generated metadata through the workspace scanner,
// including transactional publication and deletion. Only selected cache files
// are admitted; cache pools and generated container classes remain excluded.
type CompiledCatalogIndex struct {
	root, id   string
	repository *indexer.DataIndexer[compiledCatalogRecord]
	container  *ContainerCatalog
	routes     *CompiledRouteCatalog
	mu         sync.Mutex
	records    map[string]compiledCatalogRecord
}

type compiledCatalogRecord struct {
	Path       string
	Modified   int64
	Services   []Service
	Parameters []Parameter
	Globals    []ContainerTwigGlobal
	Components []ContainerTwigComponent
	Routes     []Route
}

func NewCompiledContainerIndex(root, cache string, services *ServiceIndex, store *indexer.Store) (*CompiledCatalogIndex, error) {
	return newCompiledCatalogIndex(root, cache, "symfony.compiled_container", services.containerWatcher, nil, store)
}
func NewCompiledRouteIndex(root, cache string, routes *RouteIndexer, store *indexer.Store) (*CompiledCatalogIndex, error) {
	return newCompiledCatalogIndex(root, cache, "symfony.compiled_routes", nil, routes.compiledRoutes, store)
}
func newCompiledCatalogIndex(root, cache, id string, container *ContainerCatalog, routes *CompiledRouteCatalog, store *indexer.Store) (*CompiledCatalogIndex, error) {
	repository, err := indexer.NewRepository[compiledCatalogRecord](filepath.Join(cache, "compiled.db"), id, store)
	if err != nil {
		return nil, err
	}
	values, err := repository.GetAllValues()
	if err != nil {
		_ = repository.Close()
		return nil, err
	}
	idx := &CompiledCatalogIndex{root: root, id: id, repository: repository, container: container, routes: routes, records: make(map[string]compiledCatalogRecord)}
	for _, record := range values {
		idx.records[record.Path] = record
	}
	idx.publish()
	return idx, nil
}
func (idx *CompiledCatalogIndex) ID() string { return idx.id }

func (idx *CompiledCatalogIndex) ShouldEnterDirectory(path string) bool {
	for _, parent := range []string{"var", "app"} {
		base := filepath.Join(idx.root, parent)
		cache := filepath.Join(base, "cache")
		if path == base || path == cache || filepath.Dir(path) == cache {
			return true
		}
	}
	return false
}
func (idx *CompiledCatalogIndex) ShouldIndexPath(path string) bool {
	if !idx.ShouldEnterDirectory(filepath.Dir(path)) {
		return false
	}
	// Files must sit directly in an environment directory.
	parent := filepath.Dir(filepath.Dir(path))
	if parent != filepath.Join(idx.root, "var", "cache") && parent != filepath.Join(idx.root, "app", "cache") {
		return false
	}
	if idx.container != nil {
		return filepath.Base(path) == "Shopware_Core_KernelDevDebugContainer.xml"
	}
	return isCompiledRouteFileName(filepath.Base(path))
}
func (idx *CompiledCatalogIndex) ShouldPreparsePath(path string) bool {
	return idx.ShouldIndexPath(path)
}
func (idx *CompiledCatalogIndex) Prepare(file *indexer.ParsedFile) (any, error) {
	if !idx.ShouldIndexPath(file.Path) {
		return nil, nil
	}
	record := compiledCatalogRecord{Path: file.Path}
	if info, err := os.Stat(file.Path); err == nil {
		record.Modified = info.ModTime().UnixNano()
	}
	if idx.routes != nil {
		record.Routes = ParseCompiledRoutesTree(file.Path, file.SyntaxTree())
		return record, nil
	}
	tree := file.SyntaxTree()
	if tree == nil {
		return nil, nil
	}
	var err error
	record.Services, record.Parameters, err = ParseXMLServicesTree(file.Path, tree, xmlsyntax.NewLineIndex(tree.Source))
	if err != nil {
		return nil, err
	}
	record.Globals = ParseXMLTwigGlobalsTree(file.Path, tree)
	record.Components = ParseXMLTwigComponentsTree(file.Path, tree)
	return record, nil
}
func (idx *CompiledCatalogIndex) Index(file *indexer.ParsedFile) error {
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}
func (idx *CompiledCatalogIndex) IndexPrepared(file *indexer.ParsedFile, prepared any) error {
	record, ok := prepared.(compiledCatalogRecord)
	if !ok {
		return nil
	}
	if err := idx.repository.BatchSaveItemsIn(file.Mutation(), map[string]map[string]compiledCatalogRecord{file.Path: {file.Path: record}}); err != nil {
		return err
	}
	return idx.after(file.Mutation(), func() { idx.records[file.Path] = record })
}
func (idx *CompiledCatalogIndex) after(mutation *indexer.Mutation, update func()) error {
	publish := func() { idx.mu.Lock(); defer idx.mu.Unlock(); update(); idx.publish() }
	if mutation != nil {
		return mutation.AfterCommit(publish)
	}
	publish()
	return nil
}
func (idx *CompiledCatalogIndex) publish() {
	records := make([]compiledCatalogRecord, 0, len(idx.records))
	for _, record := range idx.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if idx.container != nil {
			return a.Path < b.Path
		}
		devA, devB := strings.HasPrefix(filepath.Base(filepath.Dir(a.Path)), "dev"), strings.HasPrefix(filepath.Base(filepath.Dir(b.Path)), "dev")
		if devA != devB {
			return devA
		}
		modernA, modernB := filepath.Base(a.Path) == "url_generating_routes.php", filepath.Base(b.Path) == "url_generating_routes.php"
		if modernA != modernB {
			return modernA
		}
		if a.Modified != b.Modified {
			return a.Modified > b.Modified
		}
		return a.Path < b.Path
	})
	record := compiledCatalogRecord{}
	if len(records) > 0 {
		record = records[0]
	}
	if idx.routes != nil {
		idx.routes.publish(record.Path, record.Routes)
		return
	}
	cw := idx.container
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.containerPath, cw.containerExists = record.Path, record.Path != ""
	cw.services = make(map[string]Service, len(record.Services))
	for _, service := range record.Services {
		cw.services[service.ID] = service
	}
	cw.parameters = make(map[string]Parameter, len(record.Parameters))
	for _, param := range record.Parameters {
		cw.parameters[param.Name] = param
	}
	cw.twigGlobals, cw.twigComponents = record.Globals, record.Components
	cw.revision++
}
func (idx *CompiledCatalogIndex) RemovedFiles(paths []string) error {
	return idx.RemovedFilesIn(paths, nil)
}
func (idx *CompiledCatalogIndex) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	if err := idx.repository.BatchDeleteByFilePathsIn(mutation, paths); err != nil {
		return err
	}
	return idx.after(mutation, func() {
		for _, path := range paths {
			delete(idx.records, path)
		}
	})
}
func (idx *CompiledCatalogIndex) Clear() error { return idx.ClearIn(nil) }

func (idx *CompiledCatalogIndex) ClearIn(mutation *indexer.Mutation) error {
	if err := idx.repository.ClearIn(mutation); err != nil {
		return err
	}
	return idx.after(mutation, func() { clear(idx.records) })
}
func (idx *CompiledCatalogIndex) Close() error { return idx.repository.Close() }
