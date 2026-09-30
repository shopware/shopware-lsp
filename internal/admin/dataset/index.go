// Package dataset indexes Administration publishData calls and resolves the
// entities an Admin SDK selector reads.
package dataset

import (
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

// Publication is one literal Administration dataset registration.
type Publication struct {
	ID           string
	PropertyPath string
	File         string
	Line         int
}

// Index stores publishData registrations from Administration sources.
type Index struct {
	publications *indexer.DataIndexer[Publication]
}

type preparedPublications struct {
	items map[string]Publication
}

var (
	_ indexer.Indexer              = (*Index)(nil)
	_ indexer.PreparingIndexer     = (*Index)(nil)
	_ indexer.TransactionalRemover = (*Index)(nil)
	_ indexer.TransactionalClearer = (*Index)(nil)
)

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	repository, err := indexer.NewRepository[Publication](
		filepath.Join(configDir, "admin_datasets.db"),
		"admin.dataset.publications",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	return &Index{publications: repository}, nil
}

func (idx *Index) ID() string { return "admin.dataset" }

func (idx *Index) Index(file *indexer.ParsedFile) error {
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}

func (idx *Index) Prepare(file *indexer.ParsedFile) (any, error) {
	if file == nil || !isDatasetSource(file.Path) {
		return nil, nil
	}
	items := map[string]Publication{}
	if strings.Contains(file.Source, "publishData") {
		items = extractPublications(file)
	}
	return &preparedPublications{items: items}, nil
}

func (idx *Index) IndexPrepared(file *indexer.ParsedFile, value any) error {
	prepared, ok := value.(*preparedPublications)
	if !ok || prepared == nil || file == nil {
		return nil
	}
	return idx.publications.BatchSaveItemsIn(file.Mutation(), map[string]map[string]Publication{
		file.Path: prepared.items,
	})
}

// PropertyPath returns the Vue property path published for a dataset id.
// An empty path with a nil error means the id was not published with a
// literal path in this workspace.
func (idx *Index) PropertyPath(id string) (string, error) {
	if idx == nil || id == "" {
		return "", nil
	}
	values, err := idx.publications.GetValues(id)
	if err != nil || len(values) == 0 {
		return "", err
	}
	bestFile := ""
	bestPath := ""
	for _, value := range values {
		if value.PropertyPath == "" {
			continue
		}
		if bestPath == "" || value.File < bestFile ||
			(value.File == bestFile && value.PropertyPath < bestPath) {
			bestFile = value.File
			bestPath = value.PropertyPath
		}
	}
	return bestPath, nil
}

func (idx *Index) RemovedFiles(paths []string) error {
	return idx.publications.BatchDeleteByFilePaths(paths)
}

func (idx *Index) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return idx.publications.BatchDeleteByFilePathsIn(mutation, paths)
}

func (idx *Index) Clear() error { return idx.publications.Clear() }

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	return idx.publications.ClearIn(mutation)
}

func (idx *Index) Close() error {
	if idx == nil || idx.publications == nil {
		return nil
	}
	return idx.publications.Close()
}

func isDatasetSource(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".ts", ".vue":
		return true
	default:
		return false
	}
}

func extractPublications(file *indexer.ParsedFile) map[string]Publication {
	items := map[string]Publication{}
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return items
	}
	for _, call := range jsquery.Calls(tree.Root) {
		if jsquery.CallMethodName(call) != "publishData" {
			continue
		}
		object := jsquery.ObjectArgument(call, 0)
		if object == nil {
			continue
		}
		idNode := jsquery.PropertyValue(jsquery.Property(object, "id"))
		id, literal := literalJavaScriptString(idNode)
		if !literal || id == "" {
			continue
		}
		propertyPath, _ := literalJavaScriptString(
			jsquery.PropertyValue(jsquery.Property(object, "path")),
		)
		if existing, found := items[id]; found &&
			existing.PropertyPath != "" && propertyPath == "" {
			continue
		}
		publication := Publication{
			ID:           id,
			PropertyPath: propertyPath,
			File:         file.Path,
		}
		if idNode != nil && file.LineIndex() != nil {
			line, _ := file.LineIndex().Position(idNode.RangeTrimmedTrivia().Start)
			publication.Line = int(line) + 1
		}
		items[id] = publication
	}
	return items
}

func literalJavaScriptString(node *cst.Node) (string, bool) {
	if node == nil || node.Kind() != jssyntax.JsString {
		return "", false
	}
	if strings.Contains(node.Text(), "${") {
		return "", false
	}
	return jsquery.StringValue(node), true
}
