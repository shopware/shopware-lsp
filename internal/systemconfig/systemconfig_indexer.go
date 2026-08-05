package systemconfig

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

// SystemConfigIndexer is responsible for indexing system config XML files
type SystemConfigIndexer struct {
	configIndex *indexer.DataIndexer[SystemConfigEntry]
}

// NewSystemConfigIndexer creates a new system config indexer
func NewSystemConfigIndexer(configDir string, stores ...*indexer.Store) (*SystemConfigIndexer, error) {
	configIndexer, err := indexer.NewRepository[SystemConfigEntry](filepath.Join(configDir, "system_config.db"), "system_config", stores...)
	if err != nil {
		return nil, err
	}

	return &SystemConfigIndexer{
		configIndex: configIndexer,
	}, nil
}

// ID returns the unique identifier for this indexer
func (s *SystemConfigIndexer) ID() string {
	return "systemconfig.indexer"
}

// Index processes a file and indexes any system config entries found
func (s *SystemConfigIndexer) Index(file *indexer.ParsedFile) error {
	path := file.Path
	fileContent := file.Content
	// Skip non-system config files
	if !strings.HasSuffix(path, ".xml") || strings.Contains(path, "/_fixtures/") || strings.Contains(path, "/_fixture/") {
		return nil
	}

	// Replacing with an empty set clears entries when a file stops being a
	// system-config document but keeps the same path.
	if !IsSystemConfigXML(fileContent) {
		return s.configIndex.BatchSaveItemsIn(
			file.Mutation(),
			map[string]map[string]SystemConfigEntry{path: {}},
		)
	}

	// We already have the file content, so we can pass it directly
	entries, err := IndexSystemConfigTree(path, file.SyntaxTree(), file.LineIndex())
	if err != nil {
		return err
	}

	// Prepare batch save
	batchSave := map[string]map[string]SystemConfigEntry{path: {}}

	for _, entry := range entries {
		if _, ok := batchSave[entry.FilePath]; !ok {
			batchSave[entry.FilePath] = make(map[string]SystemConfigEntry)
		}
		batchSave[entry.FilePath][entry.Name] = entry
	}

	return s.configIndex.BatchSaveItemsIn(file.Mutation(), batchSave)
}

// RemovedFiles handles cleanup when files are removed
func (s *SystemConfigIndexer) RemovedFiles(paths []string) error {
	return s.configIndex.BatchDeleteByFilePaths(paths)
}

func (s *SystemConfigIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return s.configIndex.BatchDeleteByFilePathsIn(mutation, paths)
}

// Close closes the indexer
func (s *SystemConfigIndexer) Close() error {
	return s.configIndex.Close()
}

// Clear clears all indexed data
func (s *SystemConfigIndexer) Clear() error {
	return s.configIndex.Clear()
}

func (s *SystemConfigIndexer) ClearIn(mutation *indexer.Mutation) error {
	return s.configIndex.ClearIn(mutation)
}

// GetSystemConfigEntries returns all system config entry keys
func (s *SystemConfigIndexer) GetSystemConfigEntries() ([]string, error) {
	return s.configIndex.GetAllKeys()
}

// GetSystemConfigEntry returns all entries for a specific key
func (s *SystemConfigIndexer) GetSystemConfigEntry(key string) ([]SystemConfigEntry, error) {
	return s.configIndex.GetValues(key)
}

// GetAllSystemConfigEntries returns all system config entries
func (s *SystemConfigIndexer) GetAllSystemConfigEntries() ([]SystemConfigEntry, error) {
	return s.configIndex.GetAllValues()
}
