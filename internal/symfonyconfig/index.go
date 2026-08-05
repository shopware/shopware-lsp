package symfonyconfig

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

type Record struct {
	File  string
	Roots []Root
}

type Index struct {
	records *indexer.DataIndexer[Record]

	pathsMu sync.RWMutex
	paths   map[string]struct{}
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	records, err := indexer.NewRepository[Record](
		filepath.Join(configDir, "symfony_config.db"),
		"symfony.configuration.roots",
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
	return "symfony.configuration"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil || file.Extension() != ".php" {
		return nil
	}
	if (!bytes.Contains(file.Content, []byte("getConfigTreeBuilder")) ||
		!bytes.Contains(file.Content, []byte("TreeBuilder"))) &&
		!idx.hasIndexedPath(file.Path) {
		return nil
	}
	var roots []Root
	if tree := file.SyntaxTree(); tree != nil && tree.Root != nil {
		roots = rootsInPHP(file.Path, tree.Root)
	}
	write := map[string]map[string]Record{file.Path: {}}
	if len(roots) != 0 {
		write[file.Path]["configuration"] = Record{
			File:  file.Path,
			Roots: roots,
		}
	}
	if err := idx.records.BatchSaveItemsIn(file.Mutation(), write); err != nil {
		return err
	}
	return idx.publishIndexedPath(
		file.Path,
		len(roots) != 0,
		file.Mutation(),
	)
}

func (idx *Index) Roots(name string) ([]Root, error) {
	if idx == nil || name == "" {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []Root
	for _, record := range records {
		for _, root := range record.Roots {
			if strings.EqualFold(root.Name, name) {
				result = append(result, root)
			}
		}
	}
	sortRoots(result)
	return result, nil
}

func (idx *Index) Names() ([]string, error) {
	if idx == nil {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]string)
	for _, record := range records {
		for _, root := range record.Roots {
			if root.Name != "" {
				seen[strings.ToLower(root.Name)] = root.Name
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

func sortRoots(roots []Root) {
	sort.Slice(roots, func(left, right int) bool {
		if roots[left].File != roots[right].File {
			return roots[left].File < roots[right].File
		}
		return roots[left].Range.Start < roots[right].Range.Start
	})
}
