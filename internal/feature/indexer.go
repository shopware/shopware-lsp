package feature

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

type FeatureIndexer struct {
	featureIndex *indexer.DataIndexer[Feature]
}

func NewFeatureIndexer(configDir string, stores ...*indexer.Store) (*FeatureIndexer, error) {
	featureIndex, err := indexer.NewRepository[Feature](filepath.Join(configDir, "feature_flags.db"), "feature.flags", stores...)
	if err != nil {
		return nil, err
	}

	return &FeatureIndexer{
		featureIndex: featureIndex,
	}, nil
}

func (i *FeatureIndexer) ID() string {
	return "feature.indexer"
}

func (i *FeatureIndexer) Index(file *indexer.ParsedFile) error {
	path := file.Path
	// Only index .yaml files that might contain feature flags
	if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
		return nil
	}

	// Check if the file contains "feature" in the path
	if !strings.Contains(strings.ToLower(path), "feature") {
		return nil
	}

	// Extract feature flags from the file
	features, err := ParseFeatureTree(file.SyntaxTree(), file.LineIndex(), path)
	if err != nil {
		return fmt.Errorf("parsing feature file: %w", err)
	}

	// Store the features in the database
	batchSave := map[string]map[string]Feature{path: {}}

	// Group features by file
	for _, feature := range features {
		if _, ok := batchSave[feature.File]; !ok {
			batchSave[feature.File] = make(map[string]Feature)
		}
		batchSave[feature.File][feature.Name] = feature
	}

	// Save to the database
	if err := i.featureIndex.BatchSaveItemsIn(file.Mutation(), batchSave); err != nil {
		return fmt.Errorf("saving features: %w", err)
	}

	return nil
}

func (i *FeatureIndexer) RemovedFiles(paths []string) error {
	// Remove files from the database
	if err := i.featureIndex.BatchDeleteByFilePaths(paths); err != nil {
		return fmt.Errorf("removing features: %w", err)
	}

	return nil
}

func (i *FeatureIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return i.featureIndex.BatchDeleteByFilePathsIn(mutation, paths)
}

func (i *FeatureIndexer) Close() error {
	return i.featureIndex.Close()
}

func (i *FeatureIndexer) Clear() error {
	return i.featureIndex.Clear()
}

func (i *FeatureIndexer) ClearIn(mutation *indexer.Mutation) error {
	return i.featureIndex.ClearIn(mutation)
}

func (i *FeatureIndexer) GetFeatureByName(name string) ([]Feature, error) {
	return i.featureIndex.GetValues(name)
}

func (i *FeatureIndexer) GetAllFeatures() ([]Feature, error) {
	return i.featureIndex.GetAllValues()
}
