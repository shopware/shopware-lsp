package theme

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

// ThemeConfigIndexer is responsible for indexing theme.json files
type ThemeConfigIndexer struct {
	configIndex *indexer.DataIndexer[ThemeConfigField]
}

// NewThemeConfigIndexer creates a new theme config indexer
func NewThemeConfigIndexer(configDir string, stores ...*indexer.Store) (*ThemeConfigIndexer, error) {
	configIndexer, err := indexer.NewRepository[ThemeConfigField](filepath.Join(configDir, "theme_config.db"), "theme.config", stores...)
	if err != nil {
		return nil, err
	}

	return &ThemeConfigIndexer{
		configIndex: configIndexer,
	}, nil
}

// ID returns the unique identifier for this indexer
func (t *ThemeConfigIndexer) ID() string {
	return "theme.indexer"
}

// Index processes a file and indexes any theme config fields found
func (t *ThemeConfigIndexer) Index(file *indexer.ParsedFile) error {
	path := file.Path
	// Skip non-theme.json files
	if !strings.HasSuffix(path, "theme.json") {
		return nil
	}

	// Extract theme config fields
	fields, err := ParseThemeConfigTree(file.SyntaxTree(), file.LineIndex(), path)
	if err != nil {
		return err
	}

	// Prepare batch save
	batchSave := map[string]map[string]ThemeConfigField{path: {}}

	for _, field := range fields {
		if _, ok := batchSave[field.Path]; !ok {
			batchSave[field.Path] = make(map[string]ThemeConfigField)
		}
		batchSave[field.Path][field.Key] = field
	}

	return t.configIndex.BatchSaveItemsIn(file.Mutation(), batchSave)
}

// RemovedFiles handles cleanup when files are removed
func (t *ThemeConfigIndexer) RemovedFiles(paths []string) error {
	return t.configIndex.BatchDeleteByFilePaths(paths)
}

func (t *ThemeConfigIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return t.configIndex.BatchDeleteByFilePathsIn(mutation, paths)
}

// Close closes the indexer
func (t *ThemeConfigIndexer) Close() error {
	return t.configIndex.Close()
}

// Clear clears all indexed data
func (t *ThemeConfigIndexer) Clear() error {
	return t.configIndex.Clear()
}

func (t *ThemeConfigIndexer) ClearIn(mutation *indexer.Mutation) error {
	return t.configIndex.ClearIn(mutation)
}

// GetThemeConfigFields returns all theme config field keys
func (t *ThemeConfigIndexer) GetThemeConfigFields() ([]string, error) {
	return t.configIndex.GetAllKeys()
}

// GetThemeConfigField returns all fields for a specific key
func (t *ThemeConfigIndexer) GetThemeConfigField(key string) ([]ThemeConfigField, error) {
	return t.configIndex.GetValues(key)
}

// GetAllThemeConfigFields returns all theme config fields
func (t *ThemeConfigIndexer) GetAllThemeConfigFields() ([]ThemeConfigField, error) {
	return t.configIndex.GetAllValues()
}

// IsThemeFile checks if a file is a theme.json file
func IsThemeFile(path string) bool {
	return strings.HasSuffix(path, "theme.json")
}
