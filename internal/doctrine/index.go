package doctrine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/shopware/dal"
)

// Index persists custom DBAL type registrations and queries the shared
// Shopware DAL index for physical table and column metadata.
type Index struct {
	typeRegistrations *indexer.DataIndexer[TypeRegistration]
	dal               *dal.Index

	pathsMu sync.RWMutex
	paths   map[string]bool

	typeCacheMu       sync.Mutex
	typeCacheRevision uint64
	typeCacheBuiltAt  uint64
	typeGeneration    uint64
	cachedTypes       []TypeDeclaration
}

func NewIndex(cache string, stores ...*indexer.Store) (*Index, error) {
	records, err := indexer.NewRepository[TypeRegistration](filepath.Join(cache, "doctrine_types.db"), "symfony.doctrine.type_registrations", stores...)
	if err != nil {
		return nil, err
	}
	paths, err := records.GetAllFilePaths()
	if err != nil {
		_ = records.Close()
		return nil, err
	}
	indexed := make(map[string]bool, len(paths))
	for _, path := range paths {
		indexed[path] = true
	}
	return &Index{typeRegistrations: records, paths: indexed}, nil
}

func (idx *Index) SetDALIndex(schema *dal.Index) { idx.dal = schema }

func (idx *Index) ID() string { return "symfony.doctrine" }

func (idx *Index) Prepare(file *indexer.ParsedFile) (any, error) {
	if idx == nil || file == nil {
		return nil, nil
	}
	idx.pathsMu.RLock()
	hadTypes := idx.paths[file.Path]
	idx.pathsMu.RUnlock()
	if doctrineCandidates(file) == 0 && !hadTypes {
		return nil, nil
	}
	values := make(map[string]TypeRegistration)
	tree := file.SyntaxTree()
	if tree != nil && tree.Root != nil {
		for position, registration := range TypeRegistrationsInDocument(file.Path, tree.Root) {
			if registration.Name != "" && registration.Class != "" {
				values[strings.ToLower(registration.Name)+"#"+strconv.Itoa(position)] = registration
			}
		}
	}
	return values, nil
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}

func (idx *Index) IndexPrepared(file *indexer.ParsedFile, prepared any) error {
	if idx == nil || file == nil || prepared == nil {
		return nil
	}
	values, ok := prepared.(map[string]TypeRegistration)
	if !ok {
		return fmt.Errorf("invalid DBAL type registration preparation: %T", prepared)
	}
	if err := idx.typeRegistrations.BatchSaveItemsIn(file.Mutation(), map[string]map[string]TypeRegistration{file.Path: values}); err != nil {
		return err
	}
	return idx.publish(file.Mutation(), func() {
		idx.pathsMu.Lock()
		if len(values) > 0 {
			idx.paths[file.Path] = true
		} else {
			delete(idx.paths, file.Path)
		}
		idx.pathsMu.Unlock()
	})
}

func (idx *Index) publish(mutation *indexer.Mutation, change func()) error {
	publish := func() {
		change()
		idx.typeCacheMu.Lock()
		idx.typeGeneration++
		idx.cachedTypes = nil
		idx.typeCacheMu.Unlock()
	}
	if mutation != nil {
		return mutation.AfterCommit(publish)
	}
	publish()
	return nil
}

func (idx *Index) RemovedFiles(paths []string) error { return idx.RemovedFilesIn(paths, nil) }

func (idx *Index) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	if err := idx.typeRegistrations.BatchDeleteByFilePathsIn(mutation, paths); err != nil {
		return err
	}
	return idx.publish(mutation, func() {
		idx.pathsMu.Lock()
		for _, path := range paths {
			delete(idx.paths, path)
		}
		idx.pathsMu.Unlock()
	})
}

func (idx *Index) Close() error { return idx.typeRegistrations.Close() }

func (idx *Index) TypeDeclarations(
	phpIndex *php.PHPIndex,
) []TypeDeclaration {
	if idx == nil || phpIndex == nil {
		return nil
	}
	revision := phpIndex.SemanticSnapshot().Revision
	idx.typeCacheMu.Lock()
	defer idx.typeCacheMu.Unlock()
	if idx.cachedTypes != nil && idx.typeCacheRevision == revision &&
		idx.typeCacheBuiltAt == idx.typeGeneration {
		return append([]TypeDeclaration(nil), idx.cachedTypes...)
	}
	result := TypeDeclarations(phpIndex)
	registrations, err := idx.typeRegistrations.GetAllValues()
	if err == nil {
		seen := make(map[string]struct{}, len(result)+len(registrations))
		for _, declaration := range result {
			seen[typeDeclarationKey(declaration)] = struct{}{}
		}
		for _, registration := range registrations {
			declaration := TypeDeclaration{
				Name:  registration.Name,
				Class: registration.Class,
				File:  registration.File,
				Range: registration.ClassRange,
			}
			if symbol, found := phpIndex.FindClass(registration.Class); found {
				declaration.File = symbol.Path
				declaration.Range = symbol.SelectionRange
			}
			key := typeDeclarationKey(declaration)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, declaration)
		}
		sort.Slice(result, func(left, right int) bool {
			if result[left].Name != result[right].Name {
				return result[left].Name < result[right].Name
			}
			return result[left].Class < result[right].Class
		})
	}
	idx.cachedTypes = append([]TypeDeclaration(nil), result...)
	idx.typeCacheRevision = revision
	idx.typeCacheBuiltAt = idx.typeGeneration
	return result
}

func typeDeclarationKey(declaration TypeDeclaration) string {
	return strings.ToLower(
		strings.TrimSpace(declaration.Name) + "|" +
			normalizeClass(declaration.Class),
	)
}

func (idx *Index) TypeRegistrations(
	name string,
) ([]TypeRegistration, error) {
	if idx == nil || strings.TrimSpace(name) == "" {
		return nil, nil
	}
	values, err := idx.typeRegistrations.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []TypeRegistration
	for _, registration := range values {
		if strings.EqualFold(registration.Name, name) {
			result = append(result, registration)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].NameRange.Start <
			result[right].NameRange.Start
	})
	return result, nil
}

func (idx *Index) Clear() error { return idx.ClearIn(nil) }

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if err := idx.typeRegistrations.ClearIn(mutation); err != nil {
		return err
	}
	return idx.publish(mutation, func() { idx.pathsMu.Lock(); clear(idx.paths); idx.pathsMu.Unlock() })
}
