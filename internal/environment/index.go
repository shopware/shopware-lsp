package environment

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

type Index struct {
	records *indexer.DataIndexer[Occurrence]

	pathsMu sync.RWMutex
	paths   map[string]struct{}
}

func NewIndex(
	configDir string,
	stores ...*indexer.Store,
) (*Index, error) {
	records, err := indexer.NewRepository[Occurrence](
		filepath.Join(configDir, "environment.db"),
		"symfony.environment.occurrences",
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
	return "symfony.environment"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	if !environmentCandidate(file) && !idx.hasPath(file.Path) {
		return nil
	}
	var occurrences []Occurrence
	switch {
	case isDotEnvPath(file.Path):
		occurrences = append(occurrences, parseDotEnv(file.Source)...)
	case isDockerfilePath(file.Path):
		occurrences = append(
			occurrences,
			parseDockerfile(file.Source)...,
		)
	case isDockerComposePath(file.Path):
		occurrences = append(
			occurrences,
			parseDockerCompose(file.Source)...,
		)
	}
	if supportsSymfonyEnvReference(file.Path) {
		for _, reference := range References(file.Source) {
			occurrences = append(occurrences, Occurrence{
				Kind:       ReferenceOccurrence,
				Source:     SymfonyEnvSource,
				Name:       reference.Name,
				Range:      reference.Range,
				NameRange:  reference.NameRange,
				Processors: reference.Processors,
			})
		}
		if strings.EqualFold(filepath.Ext(file.Path), ".php") {
			if tree := file.SyntaxTree(); tree != nil {
				for _, reference := range PHPReferences(tree.Root) {
					occurrences = append(occurrences, Occurrence{
						Kind:       ReferenceOccurrence,
						Source:     SymfonyEnvSource,
						Name:       reference.Name,
						Range:      reference.Range,
						NameRange:  reference.NameRange,
						Processors: reference.Processors,
					})
				}
			}
		}
	}

	items := map[string]map[string]Occurrence{file.Path: {}}
	for _, occurrence := range occurrences {
		occurrence.File = file.Path
		items[file.Path][occurrenceKey(occurrence)] = occurrence
	}
	if err := idx.records.BatchSaveItemsIn(file.Mutation(), items); err != nil {
		return err
	}
	return idx.publishPath(
		file.Path,
		len(items[file.Path]) != 0,
		file.Mutation(),
	)
}

func (idx *Index) Variables() ([]Variable, error) {
	if idx == nil {
		return nil, nil
	}
	occurrences, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	grouped := make(map[string]*Variable)
	for _, occurrence := range occurrences {
		if occurrence.Name == "" {
			continue
		}
		variable := grouped[occurrence.Name]
		if variable == nil {
			variable = &Variable{Name: occurrence.Name}
			grouped[occurrence.Name] = variable
		}
		if occurrence.Kind == DeclarationOccurrence {
			variable.Declarations = append(
				variable.Declarations,
				occurrence,
			)
		} else {
			variable.References = append(variable.References, occurrence)
		}
	}
	result := make([]Variable, 0, len(grouped))
	for _, variable := range grouped {
		sortOccurrences(variable.Declarations)
		sortOccurrences(variable.References)
		result = append(result, *variable)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result, nil
}

func (idx *Index) Variable(name string) (Variable, bool, error) {
	variables, err := idx.Variables()
	if err != nil {
		return Variable{}, false, err
	}
	for _, variable := range variables {
		if variable.Name == name {
			return variable, true, nil
		}
	}
	return Variable{}, false, nil
}

func (idx *Index) Names() ([]string, error) {
	variables, err := idx.Variables()
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(variables))
	for _, variable := range variables {
		if len(variable.Declarations) != 0 {
			result = append(result, variable.Name)
		}
	}
	return result, nil
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.BatchDeleteByFilePaths(paths); err != nil {
		return err
	}
	idx.removePaths(paths)
	return nil
}

func (idx *Index) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.BatchDeleteByFilePathsIn(
		mutation,
		paths,
	); err != nil {
		return err
	}
	if mutation == nil {
		idx.removePaths(paths)
		return nil
	}
	return mutation.AfterCommit(func() { idx.removePaths(paths) })
}

func (idx *Index) Clear() error {
	if idx == nil {
		return nil
	}
	if err := idx.records.Clear(); err != nil {
		return err
	}
	idx.resetPaths()
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
		idx.resetPaths()
		return nil
	}
	return mutation.AfterCommit(idx.resetPaths)
}

func (idx *Index) Close() error {
	if idx == nil {
		return nil
	}
	return idx.records.Close()
}

func (idx *Index) ShouldEnterDirectory(string) bool {
	return false
}

func (idx *Index) ShouldIndexPath(path string) bool {
	return isDotEnvPath(path) || isDockerfilePath(path)
}

func environmentCandidate(file *indexer.ParsedFile) bool {
	if isDotEnvPath(file.Path) ||
		isDockerfilePath(file.Path) ||
		isDockerComposePath(file.Path) {
		return true
	}
	return supportsSymfonyEnvReference(file.Path) &&
		(strings.Contains(file.Source, envPrefix) ||
			strings.EqualFold(filepath.Ext(file.Path), ".php") &&
				(hasPHPEnvCallCandidate(file.Source) ||
					strings.Contains(file.Source, "Autowire") &&
						strings.Contains(file.Source, "env")))
}

func hasPHPEnvCallCandidate(source string) bool {
	for cursor := 0; cursor < len(source); {
		relative := strings.Index(source[cursor:], "env")
		if relative < 0 {
			return false
		}
		start := cursor + relative
		next := start + len("env")
		for next < len(source) &&
			(source[next] == ' ' ||
				source[next] == '\t' ||
				source[next] == '\r' ||
				source[next] == '\n') {
			next++
		}
		if next < len(source) && source[next] == '(' {
			return true
		}
		cursor = start + len("env")
	}
	return false
}

func supportsSymfonyEnvReference(path string) bool {
	extension := filepath.Ext(path)
	return strings.EqualFold(extension, ".php") ||
		strings.EqualFold(extension, ".yaml") ||
		strings.EqualFold(extension, ".yml") ||
		strings.EqualFold(extension, ".xml")
}

func occurrenceKey(occurrence Occurrence) string {
	return fmt.Sprintf(
		"%s:%d:%d:%d:%s",
		occurrence.File,
		occurrence.Kind,
		occurrence.Range.Start,
		occurrence.Range.End,
		occurrence.Name,
	)
}

func sortOccurrences(values []Occurrence) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].File == values[right].File {
			return values[left].Range.Start < values[right].Range.Start
		}
		return values[left].File < values[right].File
	})
}

func (idx *Index) hasPath(path string) bool {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, exists := idx.paths[path]
	return exists
}

func (idx *Index) publishPath(
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

func (idx *Index) removePaths(paths []string) {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	for _, path := range paths {
		delete(idx.paths, path)
	}
}

func (idx *Index) resetPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	idx.paths = make(map[string]struct{})
}

var _ indexer.Indexer = (*Index)(nil)
var _ indexer.TransactionalRemover = (*Index)(nil)
var _ indexer.TransactionalClearer = (*Index)(nil)
var _ indexer.SupplementalPathIndexer = (*Index)(nil)
