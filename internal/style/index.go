package style

import (
	"errors"
	"path/filepath"
	"sort"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

type Index struct {
	declarations *indexer.DataIndexer[ClassCatalog]
	usages       *indexer.DataIndexer[ClassCatalog]
}

type preparedClasses struct {
	declarations []ClassOccurrence
	usages       []ClassOccurrence
	selected     bool
}

func NewIndex(
	configDir string,
	stores ...*indexer.Store,
) (*Index, error) {
	declarations, err := indexer.NewRepository[ClassCatalog](
		filepath.Join(configDir, "styles.db"),
		"style.class_declarations",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	usages, err := indexer.NewRepository[ClassCatalog](
		filepath.Join(configDir, "styles.db"),
		"style.class_usages",
		stores...,
	)
	if err != nil {
		_ = declarations.Close()
		return nil, err
	}
	return &Index{declarations: declarations, usages: usages}, nil
}

func (idx *Index) ID() string {
	return "style.classes"
}

func (idx *Index) Prepare(file *indexer.ParsedFile) (any, error) {
	if idx == nil || file == nil {
		return preparedClasses{}, nil
	}
	extension := file.Extension()
	prepared := preparedClasses{
		selected: extension == ".scss" || extension == ".twig" ||
			extension == ".html" || extension == ".vue",
	}
	if !prepared.selected {
		return prepared, nil
	}
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return prepared, nil
	}
	if extension == ".scss" || extension == ".vue" {
		prepared.declarations = ClassDeclarations(file.Path, tree.Root)
		addSourcePositions(file, prepared.declarations)
	}
	if extension == ".twig" || extension == ".html" || extension == ".vue" {
		prepared.usages = ClassUsages(file.Path, tree.Root)
		addSourcePositions(file, prepared.usages)
	}
	return prepared, nil
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}

func (idx *Index) IndexPrepared(
	file *indexer.ParsedFile,
	value any,
) error {
	if idx == nil || file == nil {
		return nil
	}
	prepared, ok := value.(preparedClasses)
	if !ok || !prepared.selected {
		return nil
	}
	if err := idx.declarations.BatchSaveItemsIn(
		file.Mutation(),
		catalogWrite(file.Path, prepared.declarations),
	); err != nil {
		return err
	}
	return idx.usages.BatchSaveItemsIn(
		file.Mutation(),
		catalogWrite(file.Path, prepared.usages),
	)
}

func addSourcePositions(
	file *indexer.ParsedFile,
	occurrences []ClassOccurrence,
) {
	for position := range occurrences {
		startLine, startCharacter := file.LineIndex().PositionUTF16(
			occurrences[position].Range.Start,
		)
		endLine, endCharacter := file.LineIndex().PositionUTF16(
			occurrences[position].Range.End,
		)
		occurrences[position].Start = SourcePosition{
			Line: int(startLine), Character: int(startCharacter),
		}
		occurrences[position].End = SourcePosition{
			Line: int(endLine), Character: int(endCharacter),
		}
	}
}

func catalogWrite(
	path string,
	occurrences []ClassOccurrence,
) map[string]map[string]ClassCatalog {
	write := map[string]map[string]ClassCatalog{path: {}}
	for _, occurrence := range occurrences {
		if occurrence.Name == "" {
			continue
		}
		catalog := write[path][occurrence.Name]
		catalog.Name = occurrence.Name
		catalog.File = path
		catalog.Occurrences = append(catalog.Occurrences, occurrence)
		write[path][occurrence.Name] = catalog
	}
	return write
}

func (idx *Index) Declarations(name string) ([]ClassOccurrence, error) {
	if idx == nil || idx.declarations == nil || name == "" {
		return nil, nil
	}
	catalogs, err := idx.declarations.GetValues(name)
	if err != nil {
		return nil, err
	}
	return flattenCatalogs(catalogs), nil
}

func (idx *Index) Usages(name string) ([]ClassOccurrence, error) {
	if idx == nil || idx.usages == nil || name == "" {
		return nil, nil
	}
	catalogs, err := idx.usages.GetValues(name)
	if err != nil {
		return nil, err
	}
	return flattenCatalogs(catalogs), nil
}

func flattenCatalogs(catalogs []ClassCatalog) []ClassOccurrence {
	var result []ClassOccurrence
	for _, catalog := range catalogs {
		result = append(result, catalog.Occurrences...)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	return errors.Join(
		idx.declarations.BatchDeleteByFilePaths(paths),
		idx.usages.BatchDeleteByFilePaths(paths),
	)
}

func (idx *Index) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil {
		return nil
	}
	return errors.Join(
		idx.declarations.BatchDeleteByFilePathsIn(mutation, paths),
		idx.usages.BatchDeleteByFilePathsIn(mutation, paths),
	)
}

func (idx *Index) Clear() error {
	if idx == nil {
		return nil
	}
	return errors.Join(idx.declarations.Clear(), idx.usages.Clear())
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	return errors.Join(
		idx.declarations.ClearIn(mutation),
		idx.usages.ClearIn(mutation),
	)
}

func (idx *Index) Close() error {
	if idx == nil {
		return nil
	}
	return errors.Join(idx.declarations.Close(), idx.usages.Close())
}
