package stimulus

import (
	"bytes"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

type Index struct {
	controllers *indexer.DataIndexer[Controller]
	usages      *indexer.DataIndexer[UsageCatalog]

	pathsMu         sync.RWMutex
	controllerPaths map[string]struct{}
	usagePaths      map[string]struct{}
}

func NewIndex(
	configDir string,
	stores ...*indexer.Store,
) (*Index, error) {
	controllers, err := indexer.NewRepository[Controller](
		filepath.Join(configDir, "stimulus.db"),
		"symfony.stimulus.controllers",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	usages, err := indexer.NewRepository[UsageCatalog](
		filepath.Join(configDir, "stimulus.db"),
		"symfony.stimulus.usages",
		stores...,
	)
	if err != nil {
		_ = controllers.Close()
		return nil, err
	}
	controllerPaths, err := repositoryPaths(controllers)
	if err != nil {
		_ = controllers.Close()
		_ = usages.Close()
		return nil, err
	}
	usagePaths, err := repositoryPaths(usages)
	if err != nil {
		_ = controllers.Close()
		_ = usages.Close()
		return nil, err
	}
	return &Index{
		controllers:     controllers,
		usages:          usages,
		controllerPaths: controllerPaths,
		usagePaths:      usagePaths,
	}, nil
}

func (idx *Index) ID() string {
	return "symfony.stimulus"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	controllerCandidate := isControllerCandidate(file)
	usageCandidate := isUsageCandidate(file)
	hadControllers, hadUsages := idx.tracked(file.Path)
	if !controllerCandidate && !usageCandidate &&
		!hadControllers && !hadUsages {
		return nil
	}

	var controllers []Controller
	var references []Reference
	tree := file.SyntaxTree()
	if tree != nil && tree.Root != nil {
		if controllerCandidate || hadControllers {
			switch file.Extension() {
			case ".js", ".ts":
				controllers = ControllersInJavaScript(
					file.Path,
					tree.Root,
					file.Source,
				)
			case ".json":
				controllers = ControllersInJSON(file.Path, tree.Root)
			}
		}
		if usageCandidate || hadUsages {
			references = References(file.Path, tree.Root)
		}
	}

	controllerWrite := map[string]map[string]Controller{file.Path: {}}
	for position, controller := range controllers {
		key := strings.ToLower(controller.Name)
		if key == "" {
			continue
		}
		if _, duplicate := controllerWrite[file.Path][key]; duplicate {
			key += "#" + strconv.Itoa(position)
		}
		controllerWrite[file.Path][key] = controller
	}
	if err := idx.controllers.BatchSaveItemsIn(
		file.Mutation(),
		controllerWrite,
	); err != nil {
		return err
	}

	usageWrite := map[string]map[string]UsageCatalog{file.Path: {}}
	var usages []Usage
	for _, reference := range references {
		name := NormalizeName(reference.Name)
		if name == "" {
			continue
		}
		usages = append(usages, Usage{
			Name:  name,
			File:  file.Path,
			Range: reference.Range,
		})
	}
	if len(usages) != 0 {
		usageWrite[file.Path]["stimulus"] = UsageCatalog{
			File:   file.Path,
			Usages: usages,
		}
	}
	if err := idx.usages.BatchSaveItemsIn(
		file.Mutation(),
		usageWrite,
	); err != nil {
		return err
	}
	if err := idx.publishPath(
		file.Path,
		true,
		len(controllerWrite[file.Path]) != 0,
		file.Mutation(),
	); err != nil {
		return err
	}
	return idx.publishPath(
		file.Path,
		false,
		len(usages) != 0,
		file.Mutation(),
	)
}

func (idx *Index) Controllers() ([]Controller, error) {
	if idx == nil || idx.controllers == nil {
		return nil, nil
	}
	values, err := idx.controllers.GetAllValues()
	if err != nil {
		return nil, err
	}
	sortControllers(values)
	merged := make(map[string]Controller)
	for _, controller := range values {
		key := strings.ToLower(controller.Name)
		current, exists := merged[key]
		if !exists || current.OriginalName == "" &&
			controller.OriginalName != "" {
			merged[key] = controller
		}
	}
	result := make([]Controller, 0, len(merged))
	for _, controller := range merged {
		result = append(result, controller)
	}
	sortControllers(result)
	return result, nil
}

func (idx *Index) Find(name string) ([]Controller, error) {
	if idx == nil || idx.controllers == nil || name == "" {
		return nil, nil
	}
	normalized := NormalizeName(name)
	values, err := idx.controllers.GetValues(strings.ToLower(normalized))
	if err != nil {
		return nil, err
	}
	var result []Controller
	for _, controller := range values {
		if strings.EqualFold(controller.Name, normalized) ||
			strings.EqualFold(controller.OriginalName, name) {
			result = append(result, controller)
		}
	}
	sortControllers(result)
	return result, nil
}

func (idx *Index) Names(forTwig bool) ([]string, error) {
	controllers, err := idx.Controllers()
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(controllers))
	for _, controller := range controllers {
		name := controller.Name
		if forTwig {
			name = controller.TwigName()
		}
		result = append(result, name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result, nil
}

func (idx *Index) Usages(name string) ([]Usage, error) {
	if idx == nil || idx.usages == nil || name == "" {
		return nil, nil
	}
	normalized := NormalizeName(name)
	catalogs, err := idx.usages.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []Usage
	for _, catalog := range catalogs {
		for _, usage := range catalog.Usages {
			if strings.EqualFold(usage.Name, normalized) {
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

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	if err := idx.controllers.BatchDeleteByFilePaths(paths); err != nil {
		return err
	}
	if err := idx.usages.BatchDeleteByFilePaths(paths); err != nil {
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
	if err := idx.controllers.BatchDeleteByFilePathsIn(
		mutation,
		paths,
	); err != nil {
		return err
	}
	if err := idx.usages.BatchDeleteByFilePathsIn(
		mutation,
		paths,
	); err != nil {
		return err
	}
	publish := func() { idx.removePaths(paths) }
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
	if err := idx.controllers.Clear(); err != nil {
		return err
	}
	if err := idx.usages.Clear(); err != nil {
		return err
	}
	idx.resetPaths()
	return nil
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	if err := idx.controllers.ClearIn(mutation); err != nil {
		return err
	}
	if err := idx.usages.ClearIn(mutation); err != nil {
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
	return errors.Join(idx.controllers.Close(), idx.usages.Close())
}

func isControllerCandidate(file *indexer.ParsedFile) bool {
	switch file.Extension() {
	case ".json":
		return strings.EqualFold(
			filepath.Base(file.Path),
			"controllers.json",
		)
	case ".js", ".ts":
		base := strings.TrimSuffix(
			strings.ToLower(filepath.Base(file.Path)),
			file.Extension(),
		)
		return strings.HasSuffix(base, "_controller") ||
			strings.HasSuffix(base, "-controller") ||
			base == "app" || base == "bootstrap"
	default:
		return false
	}
}

func isUsageCandidate(file *indexer.ParsedFile) bool {
	extension := file.Extension()
	return (extension == ".twig" || extension == ".html") &&
		(bytes.Contains(file.Content, []byte("stimulus_controller")) ||
			bytes.Contains(file.Content, []byte("data-controller")))
}

func sortControllers(values []Controller) {
	sort.Slice(values, func(left, right int) bool {
		if !strings.EqualFold(values[left].Name, values[right].Name) {
			return strings.ToLower(values[left].Name) <
				strings.ToLower(values[right].Name)
		}
		if values[left].File != values[right].File {
			return values[left].File < values[right].File
		}
		return values[left].Range.Start < values[right].Range.Start
	})
}

func repositoryPaths[T any](
	repository *indexer.DataIndexer[T],
) (map[string]struct{}, error) {
	paths, err := repository.GetAllFilePaths()
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		result[path] = struct{}{}
	}
	return result, nil
}

func (idx *Index) tracked(path string) (bool, bool) {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, controllers := idx.controllerPaths[path]
	_, usages := idx.usagePaths[path]
	return controllers, usages
}

func (idx *Index) publishPath(
	path string,
	controllerPath,
	present bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.pathsMu.Lock()
		defer idx.pathsMu.Unlock()
		paths := idx.usagePaths
		if controllerPath {
			paths = idx.controllerPaths
		}
		if present {
			paths[path] = struct{}{}
		} else {
			delete(paths, path)
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
		delete(idx.controllerPaths, path)
		delete(idx.usagePaths, path)
	}
}

func (idx *Index) resetPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	clear(idx.controllerPaths)
	clear(idx.usagePaths)
}
