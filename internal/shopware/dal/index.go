// Package dal indexes Shopware entity definitions independently of the PHP
// semantic index so non-PHP frontends can resolve technical entity and field
// names without knowing about PHP internals.
package dal

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

var entityNameConstantPattern = regexp.MustCompile(`(?m)\bENTITY_NAME\s*=\s*['"]([^'"]+)['"]`)

type Definition struct {
	Name       string
	Class      string
	File       string
	Line       int
	NameRange  cst.TextRange
	ClassRange cst.TextRange
	Fields     []Field
}

type Field struct {
	Name        string
	StorageName string
	Type        string
	Association bool
	Line        int
	Range       cst.TextRange
}

type FieldDefinition struct {
	Entity string
	Class  string
	File   string
	Field  Field
}

type Index struct {
	definitions *indexer.DataIndexer[Definition]
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	repository, err := indexer.NewRepository[Definition](
		filepath.Join(configDir, "shopware_dal.db"),
		"shopware.dal.definitions",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	return &Index{definitions: repository}, nil
}

func (idx *Index) ID() string { return "shopware.dal" }

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil || file.Extension() != ".php" {
		return nil
	}
	write := map[string]map[string]Definition{file.Path: {}}
	if !strings.Contains(file.Source, "Definition") ||
		!strings.Contains(file.Source, "defineFields") {
		return idx.definitions.BatchSaveItemsIn(file.Mutation(), write)
	}
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return idx.definitions.BatchSaveItemsIn(file.Mutation(), write)
	}
	root := tree.Root
	for _, class := range phpquery.Classes(root) {
		definition, found := parseDefinition(class, file.Path, file.LineIndex())
		if found {
			write[file.Path][definition.Name] = definition
		}
	}
	if err := idx.definitions.BatchSaveItemsIn(file.Mutation(), write); err != nil {
		return err
	}
	addDefinitionWorkspaceSymbols(file, write[file.Path])
	return nil
}

func parseDefinition(class *phpsyntax.Node, file string, lines *cst.LineIndex) (Definition, bool) {
	if class == nil || phpquery.IsAbstract(class) {
		return Definition{}, false
	}
	isDefinition := false
	for _, parent := range phpquery.ClassExtends(class) {
		short := parent
		if separator := strings.LastIndex(short, `\`); separator >= 0 {
			short = short[separator+1:]
		}
		switch short {
		case "EntityDefinition", "MappingEntityDefinition", "EntityTranslationDefinition":
			isDefinition = true
		}
	}
	if !isDefinition {
		return Definition{}, false
	}
	className := phpquery.ClassName(class)
	if className == "" {
		return Definition{}, false
	}
	name := ""
	nameRange := cst.TextRange{}
	var fields []Field
	for _, method := range phpquery.Methods(class) {
		switch phpquery.MethodName(method) {
		case "getEntityName":
			stringsInMethod := phpquery.Nodes(method, phpsyntax.PhpString)
			if len(stringsInMethod) > 0 {
				name = phpquery.StringValue(stringsInMethod[0])
				nameRange = phpquery.StringContentRange(stringsInMethod[0])
			}
		case "defineFields":
			fields = parseFields(method, lines)
		}
	}
	if name == "" {
		match := entityNameConstantPattern.FindStringSubmatchIndex(class.Text())
		if len(match) >= 4 {
			name = class.Text()[match[2]:match[3]]
			nameRange = cst.TextRange{
				Start: class.Range().Start + uint32(match[2]),
				End:   class.Range().Start + uint32(match[3]),
			}
		}
	}
	if name == "" {
		return Definition{}, false
	}
	line, _ := lines.Position(class.RangeTrimmedTrivia().Start)
	return Definition{
		Name:       name,
		Class:      className,
		File:       file,
		Line:       int(line) + 1,
		NameRange:  nameRange,
		ClassRange: class.RangeTrimmedTrivia(),
		Fields:     fields,
	}, true
}

func parseFields(method *phpsyntax.Node, lines *cst.LineIndex) []Field {
	var fields []Field
	seen := make(map[string]struct{})
	for _, creation := range phpquery.ObjectCreations(method) {
		fieldType := phpquery.ObjectClassName(creation)
		shortType := fieldType
		if separator := strings.LastIndex(shortType, `\`); separator >= 0 {
			shortType = shortType[separator+1:]
		}
		if !strings.HasSuffix(shortType, "Field") {
			continue
		}
		association := strings.Contains(shortType, "AssociationField")
		nameArgument := 1
		if association {
			nameArgument = 0
		}
		nameNode := phpquery.StringArgument(creation, nameArgument)
		if nameNode == nil {
			nameArgument = 0
			nameNode = phpquery.StringArgument(creation, nameArgument)
		}
		name := phpquery.StringValue(nameNode)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		line, _ := lines.Position(nameNode.RangeTrimmedTrivia().Start)
		fields = append(fields, Field{
			Name:        name,
			StorageName: phpquery.StringValue(phpquery.StringArgument(creation, 0)),
			Type:        shortType,
			Association: association,
			Line:        int(line) + 1,
			Range:       phpquery.StringContentRange(nameNode),
		})
	}
	return fields
}

func (idx *Index) Definitions() ([]Definition, error) {
	if idx == nil {
		return nil, nil
	}
	return idx.definitions.GetAllValues()
}

func (idx *Index) Definition(name string) ([]Definition, error) {
	if idx == nil || name == "" {
		return nil, nil
	}
	return idx.definitions.GetValues(name)
}

func (idx *Index) Fields() ([]Field, error) {
	definitions, err := idx.Definitions()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var result []Field
	for _, definition := range definitions {
		for _, field := range definition.Fields {
			if _, duplicate := seen[field.Name]; duplicate {
				continue
			}
			seen[field.Name] = struct{}{}
			result = append(result, field)
		}
	}
	return result, nil
}

func (idx *Index) FieldDefinitions(
	name string,
	associationsOnly bool,
) ([]FieldDefinition, error) {
	if idx == nil || name == "" {
		return nil, nil
	}
	definitions, err := idx.Definitions()
	if err != nil {
		return nil, err
	}
	var result []FieldDefinition
	for _, definition := range definitions {
		for _, field := range definition.Fields {
			if field.Name != name || associationsOnly && !field.Association {
				continue
			}
			result = append(result, FieldDefinition{
				Entity: definition.Name,
				Class:  definition.Class,
				File:   definition.File,
				Field:  field,
			})
		}
	}
	return result, nil
}

func (idx *Index) RemovedFiles(paths []string) error {
	return idx.definitions.BatchDeleteByFilePaths(paths)
}

func (idx *Index) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return idx.definitions.BatchDeleteByFilePathsIn(mutation, paths)
}

func (idx *Index) Clear() error { return idx.definitions.Clear() }
func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	return idx.definitions.ClearIn(mutation)
}
func (idx *Index) Close() error { return errors.Join(idx.definitions.Close()) }
