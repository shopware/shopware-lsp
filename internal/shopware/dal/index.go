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
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
)

var entityNameConstantPattern = regexp.MustCompile(`(?m)\bENTITY_NAME\s*=\s*['"]([^'"]+)['"]`)

type Definition struct {
	Name                string
	Class               string
	FullyQualifiedClass string
	EntityClass         string
	CollectionClass     string
	File                string
	Line                int
	NameRange           cst.TextRange
	ClassRange          cst.TextRange
	Fields              []Field
	VersionAware        bool
}

type Field struct {
	Name        string
	StorageName string
	Type        string
	Association bool
	TargetClass string
	Primary     bool
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
		definition, found := parseDefinition(class, root, file.Path, file.LineIndex())
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

func parseDefinition(class, root *phpsyntax.Node, file string, lines *cst.LineIndex) (Definition, bool) {
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
	resolveClass := definitionClassResolver(root)
	entityClass := ""
	collectionClass := ""
	var fields []Field
	versionAware := false
	for _, method := range phpquery.Methods(class) {
		switch phpquery.MethodName(method) {
		case "getEntityName":
			stringsInMethod := phpquery.Nodes(method, phpsyntax.PhpString)
			if len(stringsInMethod) > 0 {
				name = phpquery.StringValue(stringsInMethod[0])
				nameRange = phpquery.StringContentRange(stringsInMethod[0])
			}
		case "defineFields":
			fields = parseFields(method, lines, resolveClass)
			for _, field := range fields {
				if field.Type == "VersionField" {
					versionAware = true
				}
			}
		case "getEntityClass":
			entityClass = returnedClassName(method, resolveClass)
		case "getCollectionClass":
			collectionClass = returnedClassName(method, resolveClass)
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
	fullyQualifiedClass := resolveClass(className)
	if strings.HasSuffix(fullyQualifiedClass, "Definition") {
		base := strings.TrimSuffix(fullyQualifiedClass, "Definition")
		if entityClass == "" {
			entityClass = base + "Entity"
		}
		if collectionClass == "" {
			collectionClass = base + "Collection"
		}
	}
	line, _ := lines.Position(class.RangeTrimmedTrivia().Start)
	return Definition{
		Name:                name,
		Class:               className,
		FullyQualifiedClass: fullyQualifiedClass,
		EntityClass:         entityClass,
		CollectionClass:     collectionClass,
		File:                file,
		Line:                int(line) + 1,
		NameRange:           nameRange,
		ClassRange:          class.RangeTrimmedTrivia(),
		Fields:              fields,
		VersionAware:        versionAware,
	}, true
}

func parseFields(method *phpsyntax.Node, lines *cst.LineIndex, resolveClass func(string) string) []Field {
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
		storageName := phpquery.StringValue(phpquery.StringArgument(creation, 0))
		if shortType == "VersionField" {
			name = "versionId"
			storageName = "version_id"
		}
		if shortType == "AutoIncrementField" {
			name = "autoIncrement"
			storageName = "auto_increment"
		}
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		fieldRange := creation.RangeTrimmedTrivia()
		if nameNode != nil {
			fieldRange = phpquery.StringContentRange(nameNode)
		}
		line, _ := lines.Position(fieldRange.Start)
		primary := shortType == "IdField" || shortType == "VersionField"
		if item := phpquery.ArrayItemAt(creation); item != nil {
			for _, flag := range phpquery.ObjectCreations(item, "PrimaryKey") {
				if strings.HasSuffix(phpquery.ObjectClassName(flag), "PrimaryKey") {
					primary = true
				}
			}
		}
		fields = append(fields, Field{
			Name:        name,
			StorageName: storageName,
			Type:        shortType,
			Association: association,
			TargetClass: resolveClass(phpquery.ClassConstantName(creation)),
			Primary:     primary,
			Line:        int(line) + 1,
			Range:       fieldRange,
		})
	}
	return fields
}

func returnedClassName(method *phpsyntax.Node, resolve func(string) string) string {
	for _, access := range phpquery.Nodes(method, phpsyntax.PhpScopedAccess, phpsyntax.PhpMemberAccess) {
		if className := phpquery.ClassConstantName(access); className != "" {
			return resolve(className)
		}
	}
	return ""
}

func definitionClassResolver(root *phpsyntax.Node) func(string) string {
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	aliases := make(map[string]string)
	for _, declaration := range phpquery.UseDeclarations(root) {
		for _, imported := range phpresolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind == phpresolver.ClassImport {
				aliases[strings.ToLower(imported.Alias)] = strings.Trim(imported.Target, `\`)
			}
		}
	}
	return func(name string) string {
		name = strings.Trim(strings.TrimSpace(name), `\`)
		if name == "" {
			return ""
		}
		parts := strings.SplitN(name, `\`, 2)
		if target, found := aliases[strings.ToLower(parts[0])]; found {
			if len(parts) == 2 {
				return target + `\` + parts[1]
			}
			return target
		}
		if namespace != "" {
			return namespace + `\` + name
		}
		return name
	}
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
