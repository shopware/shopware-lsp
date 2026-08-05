package serializer

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type Usage struct {
	Class string
	File  string
	Range cst.TextRange
	Kind  TargetKind
}

type TargetKind uint8

const (
	StringTarget TargetKind = iota
	ClassConstantTarget
)

type Record struct {
	File   string
	Usages []Usage
}

type Index struct {
	records *indexer.DataIndexer[Record]

	pathsMu sync.RWMutex
	paths   map[string]struct{}
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	records, err := indexer.NewRepository[Record](
		filepath.Join(configDir, "serializer.db"),
		"symfony.serializer.usages",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	paths, err := records.GetAllFilePaths()
	if err != nil {
		_ = records.Close()
		return nil, err
	}
	pathSet := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		pathSet[path] = struct{}{}
	}
	return &Index{records: records, paths: pathSet}, nil
}

func (idx *Index) ID() string {
	return "symfony.serializer"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil || file.Extension() != ".php" {
		return nil
	}
	if !bytes.Contains(file.Content, []byte("deserialize")) &&
		!idx.hasIndexedPath(file.Path) {
		return nil
	}
	var usages []Usage
	if tree := file.SyntaxTree(); tree != nil && tree.Root != nil {
		usages = UsagesInDocument(file.Path, tree.Root)
	}
	write := map[string]map[string]Record{file.Path: {}}
	if len(usages) != 0 {
		write[file.Path]["serializer"] = Record{
			File:   file.Path,
			Usages: usages,
		}
	}
	if err := idx.records.BatchSaveItemsIn(file.Mutation(), write); err != nil {
		return err
	}
	return idx.publishIndexedPath(
		file.Path,
		len(usages) != 0,
		file.Mutation(),
	)
}

func (idx *Index) Usages(className string) ([]Usage, error) {
	if idx == nil || className == "" {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	normalized := normalizeClass(className)
	var result []Usage
	for _, record := range records {
		for _, usage := range record.Usages {
			if strings.EqualFold(usage.Class, normalized) {
				result = append(result, usage)
			}
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

func (idx *Index) Classes() ([]string, error) {
	if idx == nil {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]string)
	for _, record := range records {
		for _, usage := range record.Usages {
			key := strings.ToLower(usage.Class)
			if usage.Class != "" {
				seen[key] = usage.Class
			}
		}
	}
	result := make([]string, 0, len(seen))
	for _, name := range seen {
		result = append(result, name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result, nil
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.BatchDeleteByFilePaths(paths); err != nil {
		return err
	}
	idx.removeIndexedPaths(paths)
	return nil
}

func (idx *Index) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.BatchDeleteByFilePathsIn(mutation, paths); err != nil {
		return err
	}
	publish := func() {
		idx.removeIndexedPaths(paths)
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *Index) Clear() error {
	if idx == nil {
		return nil
	}
	if err := idx.records.Clear(); err != nil {
		return err
	}
	idx.resetIndexedPaths()
	return nil
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.ClearIn(mutation); err != nil {
		return err
	}
	if mutation == nil {
		idx.resetIndexedPaths()
		return nil
	}
	return mutation.AfterCommit(idx.resetIndexedPaths)
}

func (idx *Index) Close() error {
	if idx == nil {
		return nil
	}
	return idx.records.Close()
}

func (idx *Index) hasIndexedPath(path string) bool {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, exists := idx.paths[path]
	return exists
}

func (idx *Index) publishIndexedPath(
	path string,
	present bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.pathsMu.Lock()
		defer idx.pathsMu.Unlock()
		if present {
			idx.paths[path] = struct{}{}
		} else {
			delete(idx.paths, path)
		}
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *Index) removeIndexedPaths(paths []string) {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	for _, path := range paths {
		delete(idx.paths, path)
	}
}

func (idx *Index) resetIndexedPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	clear(idx.paths)
}

func normalizeClass(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "[]")
	return strings.TrimPrefix(name, `\`)
}
