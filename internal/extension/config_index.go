package extension

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// ConfigIndex owns complete CLI extension configuration for each extension.
// Disk snapshots persist in the shared store; editor overlays never modify the
// persisted generation.
type ConfigIndex struct {
	repository *indexer.DataIndexer[Config]
	mu         sync.RWMutex
	live       map[string]*Config
	changed    func(string)
}

type preparedConfig struct{ config Config }

var _ indexer.PreparingIndexer = (*ConfigIndex)(nil)
var _ indexer.TransactionalRemover = (*ConfigIndex)(nil)
var _ indexer.TransactionalClearer = (*ConfigIndex)(nil)

func NewConfigIndex(cache string, stores ...*indexer.Store) (*ConfigIndex, error) {
	repository, err := indexer.NewRepository[Config](filepath.Join(cache, "extension_config.db"), "extension_config", stores...)
	if err != nil {
		return nil, err
	}
	return &ConfigIndex{repository: repository, live: make(map[string]*Config)}, nil
}

func (*ConfigIndex) ID() string                       { return "extension.config" }
func (*ConfigIndex) IsConfiguration(path string) bool { _, ok := ConfigurationRoot(path); return ok }

var defaultExtensionConfig = DefaultConfig()

// GetConfig returns the first existing configuration in CLI precedence order.
// Returned snapshots are immutable: callers must not modify them or their
// nested slices, maps, and pointers. Only the three indexed paths are queried.
func (idx *ConfigIndex) GetConfig(dir string) (*Config, error) {
	if idx == nil {
		return &defaultExtensionConfig, nil
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, location := range ConfigLocations {
		path := filepath.Join(dir, location)
		if config, found := idx.live[path]; found {
			return config, nil
		}
		values, err := idx.repository.GetValuesView(path)
		if err != nil {
			return nil, fmt.Errorf("query extension configuration %s: %w", path, err)
		}
		if len(values) != 0 {
			return &values[0], nil
		}
	}
	return &defaultExtensionConfig, nil
}
func (idx *ConfigIndex) StoreComplianceEnabled(dir string) (bool, error) {
	config, err := idx.GetConfig(dir)
	if err != nil {
		return false, err
	}
	return config.Validation.StoreCompliance, nil
}
func (idx *ConfigIndex) SetOnChange(changed func(string)) {
	idx.mu.Lock()
	idx.changed = changed
	idx.mu.Unlock()
}
func (idx *ConfigIndex) notify(path string) {
	idx.mu.RLock()
	changed := idx.changed
	idx.mu.RUnlock()
	if changed != nil {
		changed(path)
	}
}
func (idx *ConfigIndex) UpdateLiveDocument(path string, root *cst.Node) {
	path = filepath.Clean(path)
	config := ParseConfigTree(path, root)
	idx.mu.Lock()
	idx.live[path] = &config
	idx.mu.Unlock()
	idx.notify(path)
}
func (idx *ConfigIndex) RemoveLiveDocument(path string) {
	path = filepath.Clean(path)
	idx.mu.Lock()
	delete(idx.live, path)
	idx.mu.Unlock()
	idx.notify(path)
}
func (idx *ConfigIndex) Prepare(file *indexer.ParsedFile) (any, error) {
	if !idx.IsConfiguration(file.Path) {
		return (*preparedConfig)(nil), nil
	}
	return &preparedConfig{config: ParseConfigTree(file.Path, file.SyntaxTree().Root)}, nil
}
func (idx *ConfigIndex) Index(file *indexer.ParsedFile) error {
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}
func (idx *ConfigIndex) IndexPrepared(file *indexer.ParsedFile, value any) error {
	prepared, ok := value.(*preparedConfig)
	if !ok {
		return fmt.Errorf("prepared extension configuration is required for %s", file.Path)
	}
	if prepared == nil {
		return nil
	}
	path := filepath.Clean(file.Path)
	if err := idx.repository.BatchSaveItemsIn(file.Mutation(), map[string]map[string]Config{path: {path: prepared.config}}); err != nil {
		return err
	}
	return idx.publish(file.Mutation(), []string{path})
}
func (idx *ConfigIndex) publish(mutation *indexer.Mutation, paths []string) error {
	publish := func() {
		for _, path := range paths {
			idx.notify(path)
		}
	}
	if mutation != nil {
		return mutation.AfterCommit(publish)
	}
	publish()
	return nil
}
func (idx *ConfigIndex) RemovedFiles(paths []string) error {
	return idx.RemovedFilesIn(paths, nil)
}
func (idx *ConfigIndex) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	var configurations []string
	for _, path := range paths {
		if idx.IsConfiguration(path) {
			configurations = append(configurations, filepath.Clean(path))
		}
	}
	if len(configurations) == 0 {
		return nil
	}
	if err := idx.repository.BatchDeleteByFilePathsIn(mutation, configurations); err != nil {
		return err
	}
	return idx.publish(mutation, configurations)
}
func (idx *ConfigIndex) Clear() error { return idx.ClearIn(nil) }
func (idx *ConfigIndex) ClearIn(mutation *indexer.Mutation) error {
	if err := idx.repository.ClearIn(mutation); err != nil {
		return err
	}
	return idx.publish(mutation, []string{""})
}
func (idx *ConfigIndex) Close() error { idx.SetOnChange(nil); return idx.repository.Close() }
