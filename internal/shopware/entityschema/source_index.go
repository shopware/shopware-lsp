package entityschema

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

// SourceDocument retains source only for files which syntactically look like
// class-based DAL definitions or extensions. The semantic index proves the
// effective subtype later; this compact index keeps request paths off disk.
type SourceDocument struct {
	Path    string   `json:"path"`
	Source  string   `json:"source"`
	Classes []string `json:"classes"`
}

type SourceIndex struct {
	documents *indexer.DataIndexer[SourceDocument]
}

func NewSourceIndex(
	configDir string,
	stores ...*indexer.Store,
) (*SourceIndex, error) {
	repository, err := indexer.NewRepository[SourceDocument](
		filepath.Join(configDir, "shopware_entity_schema.db"),
		"shopware.entity_schema.sources",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	return &SourceIndex{documents: repository}, nil
}

func (idx *SourceIndex) ID() string { return "shopware.entity_schema.sources" }

func (idx *SourceIndex) Prepare(file *indexer.ParsedFile) (any, error) {
	if file == nil || file.Extension() != ".php" {
		return (*SourceDocument)(nil), nil
	}
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return (*SourceDocument)(nil), nil
	}
	root := tree.Root
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	var classes []string
	for _, class := range phpquery.Classes(root) {
		if !classBasedSourceCandidate(class) {
			continue
		}
		name := phpquery.ClassName(class)
		if name == "" {
			continue
		}
		classes = append(classes, qualify(namespace, name))
	}
	if len(classes) == 0 {
		return (*SourceDocument)(nil), nil
	}
	return &SourceDocument{
		Path: file.Path, Source: string(file.Source), Classes: classes,
	}, nil
}

func classBasedSourceCandidate(class *phpsyntax.Node) bool {
	for _, parent := range phpquery.ClassExtends(class) {
		switch ShortClass(parent) {
		case "EntityDefinition", "MappingEntityDefinition",
			"EntityTranslationDefinition", "EntityExtension",
			"BulkEntityExtension":
			return true
		}
	}
	text := class.Text()
	if strings.Contains(text, "ENTITY_NAME") {
		return true
	}
	for _, method := range phpquery.Methods(class) {
		switch phpquery.MethodName(method) {
		case "defineFields", "getEntityName", "getDefinitionClass",
			"extendFields", "modifyFields", "extendProtections",
			"defineProtections", "collect":
			return true
		}
	}
	return false
}

func (idx *SourceIndex) Index(file *indexer.ParsedFile) error {
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}

func (idx *SourceIndex) IndexPrepared(
	file *indexer.ParsedFile,
	value any,
) error {
	if idx == nil || idx.documents == nil || file == nil || file.Extension() != ".php" {
		return nil
	}
	items := map[string]map[string]SourceDocument{file.Path: {}}
	document, ok := value.(*SourceDocument)
	if ok && document != nil {
		items[file.Path][file.Path] = *document
	}
	return idx.documents.BatchSaveItemsIn(file.Mutation(), items)
}

func (idx *SourceIndex) Source(path string) (string, bool, error) {
	if idx == nil || idx.documents == nil || path == "" {
		return "", false, nil
	}
	documents, err := idx.documents.GetValues(filepath.Clean(path))
	if err != nil {
		return "", false, err
	}
	for _, document := range documents {
		if filepath.Clean(document.Path) == filepath.Clean(path) {
			return document.Source, true, nil
		}
	}
	return "", false, nil
}

func (idx *SourceIndex) RemovedFiles(paths []string) error {
	if idx == nil || idx.documents == nil {
		return nil
	}
	return idx.documents.BatchDeleteByFilePaths(paths)
}

func (idx *SourceIndex) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil || idx.documents == nil {
		return nil
	}
	return idx.documents.BatchDeleteByFilePathsIn(mutation, paths)
}

func (idx *SourceIndex) Clear() error {
	if idx == nil || idx.documents == nil {
		return nil
	}
	return idx.documents.Clear()
}

func (idx *SourceIndex) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil || idx.documents == nil {
		return nil
	}
	return idx.documents.ClearIn(mutation)
}

func (idx *SourceIndex) Close() error {
	if idx == nil || idx.documents == nil {
		return nil
	}
	if err := idx.documents.Close(); err != nil {
		return fmt.Errorf("close entity schema source index: %w", err)
	}
	return nil
}
