package extension

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/binder"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

type ExtensionIndexer struct {
	indexer        *indexer.DataIndexer[ShopwareExtension]
	phpIndex       *php.PHPIndex
	fallbackBinder *binder.Binder
}

type preparedExtensions struct {
	items map[string]map[string]ShopwareExtension
}

var _ indexer.PreparingIndexer = (*ExtensionIndexer)(nil)

func NewExtensionIndexer(configDir string, stores ...*indexer.Store) (*ExtensionIndexer, error) {
	indexer, err := indexer.NewRepository[ShopwareExtension](filepath.Join(configDir, "extension.db"), "extensions", stores...)
	if err != nil {
		return nil, err
	}

	return &ExtensionIndexer{
		indexer:        indexer,
		fallbackBinder: binder.New(),
	}, nil
}

func (idx *ExtensionIndexer) SetPHPIndex(phpIndex *php.PHPIndex) {
	if idx != nil {
		idx.phpIndex = phpIndex
	}
}

func (idx *ExtensionIndexer) ID() string {
	return "extension.indexer"
}

func (idx *ExtensionIndexer) Index(file *indexer.ParsedFile) error {
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}

func (idx *ExtensionIndexer) Prepare(
	file *indexer.ParsedFile,
) (any, error) {
	path := file.Path
	if !isValidForIndex(path) {
		return (*preparedExtensions)(nil), nil
	}

	switch file.Extension() {
	case ".php":
		return &preparedExtensions{items: idx.prepareBundle(file)}, nil
	case ".xml":
		items, err := idx.prepareApp(file)
		if err != nil || items == nil {
			return (*preparedExtensions)(nil), err
		}
		return &preparedExtensions{items: items}, nil
	default:
		return (*preparedExtensions)(nil), nil
	}
}

func (idx *ExtensionIndexer) IndexPrepared(
	file *indexer.ParsedFile,
	value any,
) error {
	prepared, ok := value.(*preparedExtensions)
	if !ok {
		return fmt.Errorf("prepared extensions are required for %s", file.Path)
	}
	if prepared == nil {
		return nil
	}
	return idx.indexer.BatchSaveItemsIn(file.Mutation(), prepared.items)
}

func (idx *ExtensionIndexer) prepareBundle(
	file *indexer.ParsedFile,
) map[string]map[string]ShopwareExtension {
	path := file.Path
	root := file.SyntaxTree().Root
	var document *semantic.Document
	if idx.phpIndex != nil {
		document = idx.phpIndex.AnalyzeParsedFile(file)
	} else {
		document = idx.fallbackBinder.Bind(path, 0, root)
	}
	for _, class := range document.Symbols {
		if isShopwareBundle(class) {
			extension := createBundleFromClass(class)
			return map[string]map[string]ShopwareExtension{
				path: {extension.Name: extension},
			}
		}
	}
	return map[string]map[string]ShopwareExtension{path: {}}
}

func (idx *ExtensionIndexer) prepareApp(
	file *indexer.ParsedFile,
) (map[string]map[string]ShopwareExtension, error) {
	path := file.Path
	if filepath.Base(path) != "manifest.xml" {
		return nil, nil
	}

	manifest, err := ParseManifestTree(path, file.SyntaxTree())

	if err != nil {
		log.Printf("Error parsing manifest.xml: %v", err)
		return nil, err
	}

	if manifest == nil {
		return map[string]map[string]ShopwareExtension{path: {}}, nil
	}

	app := ShopwareExtension{
		Name:        manifest.Name,
		Type:        ShopwareExtensionTypeApp,
		Path:        filepath.Dir(path),
		Permissions: manifest.Permissions,
	}

	return map[string]map[string]ShopwareExtension{
		path: {manifest.Name: app},
	}, nil
}

func (idx *ExtensionIndexer) FindAppForFile(path string) (*ShopwareExtension, error) {
	extensions, err := idx.GetAll()
	if err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	var best *ShopwareExtension
	for index := range extensions {
		current := extensions[index]
		if current.Type != ShopwareExtensionTypeApp {
			continue
		}
		root := filepath.Clean(current.Path)
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if best == nil || len(root) > len(best.Path) {
			copy := current
			best = &copy
		}
	}
	return best, nil
}

func (idx *ExtensionIndexer) FindByName(name string) (ShopwareExtension, bool, error) {
	extensions, err := idx.indexer.GetValues(name)
	if err != nil {
		return ShopwareExtension{}, false, err
	}
	if len(extensions) == 0 {
		return ShopwareExtension{}, false, nil
	}
	return extensions[0], true, nil
}

func (idx *ExtensionIndexer) GetExtensionByName(name string) *ShopwareExtension {
	extension, found, err := idx.FindByName(name)
	if err != nil || !found {
		return nil
	}
	return &extension
}

func (idx *ExtensionIndexer) RemovedFiles(paths []string) error {
	return idx.indexer.BatchDeleteByFilePaths(paths)
}

func (idx *ExtensionIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return idx.indexer.BatchDeleteByFilePathsIn(mutation, paths)
}

func (idx *ExtensionIndexer) Close() error {
	return idx.indexer.Close()
}

func (idx *ExtensionIndexer) Clear() error {
	return idx.indexer.Clear()
}

func (idx *ExtensionIndexer) ClearIn(mutation *indexer.Mutation) error {
	return idx.indexer.ClearIn(mutation)
}

func (idx *ExtensionIndexer) GetAll() ([]ShopwareExtension, error) {
	return idx.indexer.GetAllValues()
}
