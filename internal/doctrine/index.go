package doctrine

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
)

type Index struct {
	records           *indexer.DataIndexer[Model]
	dqlUsages         *indexer.DataIndexer[DQLUsageGroup]
	dqlFunctions      *indexer.DataIndexer[DQLFunction]
	typeRegistrations *indexer.DataIndexer[TypeRegistration]

	namespaceProviderMu sync.RWMutex
	namespaceProvider   NamespaceAliasProvider

	pathsMu       sync.RWMutex
	paths         map[string]struct{}
	dqlPaths      map[string]struct{}
	functionPaths map[string]struct{}
	typePaths     map[string]struct{}

	cacheMu         sync.Mutex
	cacheGeneration uint64
	cacheBuiltAt    uint64
	cachedModels    []Model

	typeCacheMu       sync.Mutex
	typeCacheRevision uint64
	typeCacheBuiltAt  uint64
	typeGeneration    uint64
	cachedTypes       []TypeDeclaration

	aliasCacheMu               sync.Mutex
	aliasCacheGeneration       uint64
	aliasCacheProviderRevision uint64
	aliasCacheSet              bool
	cachedModelAliases         []ModelAlias
	cachedAliasTargets         map[string][]string
}

// DQLUsageGroup retains all equal entity or field references from one source
// file under a directly queryable semantic key.
type DQLUsageGroup struct {
	Key    string
	Usages []DQLReference
}

// TypeDeclarations returns custom Doctrine types cached by the immutable PHP
// workspace revision. Mapping completion is latency-sensitive and should not
// rescan every PHP class on each keystroke.
func (idx *Index) TypeDeclarations(
	phpIndex *php.PHPIndex,
) []TypeDeclaration {
	if idx == nil || phpIndex == nil {
		return nil
	}
	revision := phpIndex.SemanticSnapshot().Revision
	idx.typeCacheMu.Lock()
	defer idx.typeCacheMu.Unlock()
	if idx.cachedTypes != nil && idx.typeCacheRevision == revision &&
		idx.typeCacheBuiltAt == idx.typeGeneration {
		return append([]TypeDeclaration(nil), idx.cachedTypes...)
	}
	result := TypeDeclarations(phpIndex)
	registrations, err := idx.typeRegistrations.GetAllValues()
	if err == nil {
		seen := make(map[string]struct{}, len(result)+len(registrations))
		for _, declaration := range result {
			seen[typeDeclarationKey(declaration)] = struct{}{}
		}
		for _, registration := range registrations {
			declaration := TypeDeclaration{
				Name:   registration.Name,
				Class:  registration.Class,
				File:   registration.File,
				Range:  registration.ClassRange,
				Family: DBALTypeFamily,
			}
			if symbol, found := phpIndex.FindClass(registration.Class); found {
				declaration.File = symbol.Path
				declaration.Range = symbol.SelectionRange
			}
			key := typeDeclarationKey(declaration)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, declaration)
		}
		sort.Slice(result, func(left, right int) bool {
			if result[left].Name != result[right].Name {
				return result[left].Name < result[right].Name
			}
			return result[left].Class < result[right].Class
		})
	}
	idx.cachedTypes = append([]TypeDeclaration(nil), result...)
	idx.typeCacheRevision = revision
	idx.typeCacheBuiltAt = idx.typeGeneration
	return result
}

func typeDeclarationKey(declaration TypeDeclaration) string {
	return strings.ToLower(
		strings.TrimSpace(declaration.Name) + "|" +
			normalizeClass(declaration.Class),
	)
}

// TypeUsages returns persisted PHP-attribute/annotation and XML/YAML mapping
// references for one Doctrine type name.
func (idx *Index) TypeUsages(
	name string,
) ([]MappingReference, error) {
	if idx == nil || strings.TrimSpace(name) == "" {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []MappingReference
	for _, model := range records {
		for _, field := range model.Fields {
			if !strings.EqualFold(field.Type, name) ||
				field.TypeRange.Len() == 0 {
				continue
			}
			result = append(result, MappingReference{
				Role:  MappingType,
				Name:  field.Type,
				Owner: model.Class,
				Field: field.Name,
				Range: field.TypeRange,
			})
			result[len(result)-1].File = field.File
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

// TypeRegistrations returns static DoctrineBundle declarations for a custom
// DBAL type name.
func (idx *Index) TypeRegistrations(
	name string,
) ([]TypeRegistration, error) {
	if idx == nil || strings.TrimSpace(name) == "" {
		return nil, nil
	}
	values, err := idx.typeRegistrations.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []TypeRegistration
	for _, registration := range values {
		if strings.EqualFold(registration.Name, name) {
			result = append(result, registration)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].NameRange.Start <
			result[right].NameRange.Start
	})
	return result, nil
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	records, err := indexer.NewRepository[Model](
		filepath.Join(configDir, "doctrine.db"),
		"symfony.doctrine.models",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	dqlUsages, err := indexer.NewRepository[DQLUsageGroup](
		filepath.Join(configDir, "doctrine_dql.db"),
		"symfony.doctrine.dql_usages",
		stores...,
	)
	if err != nil {
		_ = records.Close()
		return nil, err
	}
	dqlFunctions, err := indexer.NewRepository[DQLFunction](
		filepath.Join(configDir, "doctrine_dql_functions.db"),
		"symfony.doctrine.dql_functions",
		stores...,
	)
	if err != nil {
		_ = errors.Join(records.Close(), dqlUsages.Close())
		return nil, err
	}
	typeRegistrations, err := indexer.NewRepository[TypeRegistration](
		filepath.Join(configDir, "doctrine_types.db"),
		"symfony.doctrine.type_registrations",
		stores...,
	)
	if err != nil {
		_ = errors.Join(
			records.Close(),
			dqlUsages.Close(),
			dqlFunctions.Close(),
		)
		return nil, err
	}
	paths, err := records.GetAllFilePaths()
	if err != nil {
		_ = errors.Join(
			records.Close(),
			dqlUsages.Close(),
			dqlFunctions.Close(),
			typeRegistrations.Close(),
		)
		return nil, err
	}
	dqlPaths, err := dqlUsages.GetAllFilePaths()
	if err != nil {
		_ = errors.Join(
			records.Close(),
			dqlUsages.Close(),
			dqlFunctions.Close(),
			typeRegistrations.Close(),
		)
		return nil, err
	}
	functionPaths, err := dqlFunctions.GetAllFilePaths()
	if err != nil {
		_ = errors.Join(
			records.Close(),
			dqlUsages.Close(),
			dqlFunctions.Close(),
			typeRegistrations.Close(),
		)
		return nil, err
	}
	typePaths, err := typeRegistrations.GetAllFilePaths()
	if err != nil {
		_ = errors.Join(
			records.Close(),
			dqlUsages.Close(),
			dqlFunctions.Close(),
			typeRegistrations.Close(),
		)
		return nil, err
	}
	pathSet := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		pathSet[path] = struct{}{}
	}
	dqlPathSet := make(map[string]struct{}, len(dqlPaths))
	for _, path := range dqlPaths {
		dqlPathSet[path] = struct{}{}
	}
	functionPathSet := make(map[string]struct{}, len(functionPaths))
	for _, path := range functionPaths {
		functionPathSet[path] = struct{}{}
	}
	typePathSet := make(map[string]struct{}, len(typePaths))
	for _, path := range typePaths {
		typePathSet[path] = struct{}{}
	}
	return &Index{
		records:           records,
		dqlUsages:         dqlUsages,
		dqlFunctions:      dqlFunctions,
		typeRegistrations: typeRegistrations,
		paths:             pathSet,
		dqlPaths:          dqlPathSet,
		functionPaths:     functionPathSet,
		typePaths:         typePathSet,
	}, nil
}

func (idx *Index) ID() string {
	return "symfony.doctrine"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	candidates := doctrineCandidates(file)
	mappingCandidate := candidates&doctrineMappingCandidate != 0
	dqlCandidate := candidates&doctrineDQLCandidate != 0
	functionCandidate := candidates&doctrineDQLFunctionCandidate != 0
	typeCandidate := candidates&doctrineTypeCandidate != 0
	hadMapping := idx.hasIndexedPath(file.Path)
	hadDQL := idx.hasDQLIndexedPath(file.Path)
	hadFunctions := idx.hasDQLFunctionIndexedPath(file.Path)
	hadTypes := idx.hasTypeIndexedPath(file.Path)
	processTypes := typeCandidate || hadTypes
	if !mappingCandidate && !dqlCandidate && !functionCandidate &&
		!typeCandidate && !hadMapping && !hadDQL && !hadFunctions &&
		!hadTypes {
		return nil
	}

	var models []Model
	var dqlReferences []DQLReference
	var dqlFunctions []DQLFunction
	var typeRegistrations []TypeRegistration
	tree := file.SyntaxTree()
	if tree != nil && tree.Root != nil {
		if mappingCandidate || hadMapping {
			models = ModelsInDocument(file.Path, tree.Root, file.Source)
		}
		if file.Extension() == ".php" && (dqlCandidate || hadDQL) {
			dqlReferences = DQLReferencesInDocument(
				idx,
				context.Background(),
				tree.Root,
				file.Path,
			)
		}
		if file.Extension() == ".php" &&
			(functionCandidate || hadFunctions) {
			dqlFunctions = DQLFunctionsInDocument(file.Path, tree.Root)
		}
		if processTypes {
			typeRegistrations = TypeRegistrationsInDocument(
				file.Path,
				tree.Root,
			)
		}
	}
	write := map[string]map[string]Model{file.Path: {}}
	for position, model := range models {
		if model.Class == "" {
			continue
		}
		key := strings.ToLower(normalizeClass(model.Class))
		if _, exists := write[file.Path][key]; exists {
			key += "#" + strconv.Itoa(position)
		}
		write[file.Path][key] = normalizeModel(model)
	}
	if err := idx.records.BatchSaveItemsIn(file.Mutation(), write); err != nil {
		return err
	}
	groups := make(map[string]DQLUsageGroup)
	for _, reference := range dqlReferences {
		key := DQLReferenceKey(reference)
		if key == "" {
			continue
		}
		group := groups[key]
		group.Key = key
		group.Usages = append(group.Usages, reference)
		groups[key] = group
	}
	if err := idx.dqlUsages.BatchSaveItemsIn(
		file.Mutation(),
		map[string]map[string]DQLUsageGroup{file.Path: groups},
	); err != nil {
		return err
	}
	functionWrite := map[string]map[string]DQLFunction{file.Path: {}}
	for position, function := range dqlFunctions {
		key := strings.ToLower(function.Name)
		if key == "" {
			continue
		}
		if _, duplicate := functionWrite[file.Path][key]; duplicate {
			key += "#" + strconv.Itoa(position)
		}
		functionWrite[file.Path][key] = function
	}
	if err := idx.dqlFunctions.BatchSaveItemsIn(
		file.Mutation(),
		functionWrite,
	); err != nil {
		return err
	}
	typeWrite := map[string]map[string]TypeRegistration{file.Path: {}}
	for position, registration := range typeRegistrations {
		key := strings.ToLower(registration.Name)
		if key == "" || registration.Class == "" {
			continue
		}
		if _, duplicate := typeWrite[file.Path][key]; duplicate {
			key += "#" + strconv.Itoa(position)
		}
		typeWrite[file.Path][key] = registration
	}
	if processTypes {
		if err := idx.typeRegistrations.BatchSaveItemsIn(
			file.Mutation(),
			typeWrite,
		); err != nil {
			return err
		}
	}
	if err := idx.publishIndexedPath(
		file.Path,
		len(write[file.Path]) != 0,
		file.Mutation(),
	); err != nil {
		return err
	}
	if err := idx.publishDQLIndexedPath(
		file.Path,
		len(groups) != 0,
		file.Mutation(),
	); err != nil {
		return err
	}
	if err := idx.publishDQLFunctionIndexedPath(
		file.Path,
		len(functionWrite[file.Path]) != 0,
		file.Mutation(),
	); err != nil {
		return err
	}
	addModelWorkspaceSymbols(file, models)
	if !processTypes {
		return nil
	}
	return idx.publishTypeIndexedPath(
		file.Path,
		len(typeWrite[file.Path]) != 0,
		file.Mutation(),
	)
}

// ModelsInDocument parses Doctrine metadata from a document snapshot without
// persisting it. LSP providers use this for unsaved editor contents so mapping
// navigation and diagnostics do not lag behind the workspace index.
func ModelsInDocument(
	path string,
	root *cst.Node,
	source string,
) []Model {
	if root == nil {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		return parsePHPModels(path, root, source)
	case ".xml":
		return parseXMLModels(path, root)
	case ".yaml", ".yml":
		return parseYAMLModels(path, root)
	default:
		return nil
	}
}

func (idx *Index) DQLUsages(
	reference DQLReference,
) ([]DQLReference, error) {
	if idx == nil || idx.dqlUsages == nil {
		return nil, nil
	}
	key := DQLReferenceKey(reference)
	if key == "" {
		return nil, nil
	}
	groups, err := idx.dqlUsages.GetValues(key)
	if err != nil {
		return nil, err
	}
	var result []DQLReference
	for _, group := range groups {
		result = append(result, group.Usages...)
	}
	if reference.Role == DQLFieldReference {
		models, modelErr := idx.Models()
		if modelErr != nil {
			return nil, modelErr
		}
		for _, model := range models {
			if !sameClass(model.Class, reference.Entity) {
				continue
			}
			for _, constraint := range model.TableConstraints {
				for _, field := range constraint.Fields {
					if strings.EqualFold(
						field.Name,
						reference.Field,
					) {
						result = append(result, DQLReference{
							Role:   DQLFieldReference,
							Entity: model.Class,
							Field:  reference.Field,
							File:   constraint.File,
							Range:  field.Range,
						})
					}
				}
				for _, column := range constraint.Columns {
					if strings.EqualFold(
						modelConstraintColumnField(
							model,
							column.Name,
						),
						reference.Field,
					) {
						result = append(result, DQLReference{
							Role:   DQLFieldReference,
							Entity: model.Class,
							Field:  reference.Field,
							File:   constraint.File,
							Range:  column.Range,
						})
					}
				}
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

func (idx *Index) Models() ([]Model, error) {
	if idx == nil {
		return nil, nil
	}
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	if idx.cachedModels != nil && idx.cacheBuiltAt == idx.cacheGeneration {
		return cloneModels(idx.cachedModels), nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	merged := make(map[string]*Model)
	for _, record := range records {
		key := strings.ToLower(normalizeClass(record.Class))
		if key == "" {
			continue
		}
		current := merged[key]
		if current == nil {
			copy := normalizeModel(record)
			copy.Fields = nil
			copy.Callbacks = nil
			current = &copy
			merged[key] = current
		}
		mergeModel(current, record)
	}
	result := make([]Model, 0, len(merged))
	for _, model := range merged {
		sortModel(model)
		result = append(result, *model)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Class) <
			strings.ToLower(result[right].Class)
	})
	idx.cachedModels = cloneModels(result)
	idx.cacheBuiltAt = idx.cacheGeneration
	return result, nil
}

func (idx *Index) Model(className string) (Model, bool, error) {
	if strings.Contains(className, ":") {
		resolved, found, err := idx.ResolveModelName(className)
		if err != nil || !found {
			return Model{}, false, err
		}
		className = resolved
	}
	for _, model := range mustModels(idx) {
		if sameClass(model.Class, className) {
			return model, true, nil
		}
	}
	if idx == nil {
		return Model{}, false, nil
	}
	models, err := idx.Models()
	if err != nil {
		return Model{}, false, err
	}
	for _, model := range models {
		if sameClass(model.Class, className) {
			return model, true, nil
		}
	}
	return Model{}, false, nil
}

func (idx *Index) ModelDeclarations(className string) ([]Model, error) {
	if idx == nil || className == "" {
		return nil, nil
	}
	if strings.Contains(className, ":") {
		resolved, found, err := idx.ResolveModelName(className)
		if err != nil || !found {
			return nil, err
		}
		className = resolved
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []Model
	for _, record := range records {
		if sameClass(record.Class, className) {
			result = append(result, normalizeModel(record))
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].NameRange.Start < result[right].NameRange.Start
	})
	return result, nil
}

// Fields returns mapped fields including inherited mapped-superclass fields
// and flattened embeddable paths such as address.city.
func (idx *Index) Fields(className string) ([]Field, error) {
	if strings.Contains(className, ":") {
		resolved, found, err := idx.ResolveModelName(className)
		if err != nil || !found {
			return nil, err
		}
		className = resolved
	}
	models, err := idx.Models()
	if err != nil {
		return nil, err
	}
	lookup := make(map[string]Model, len(models))
	for _, model := range models {
		lookup[strings.ToLower(normalizeClass(model.Class))] = model
	}
	var result []Field
	visited := make(map[string]struct{})
	var collect func(string, string, string)
	collect = func(target, pathPrefix, columnPrefix string) {
		key := strings.ToLower(normalizeClass(target))
		if key == "" {
			return
		}
		visitKey := key + "|" + strings.ToLower(pathPrefix) + "|" +
			strings.ToLower(columnPrefix)
		if _, exists := visited[visitKey]; exists {
			return
		}
		visited[visitKey] = struct{}{}
		model, exists := lookup[key]
		if !exists {
			return
		}
		if model.Parent != "" {
			collect(model.Parent, pathPrefix, columnPrefix)
		}
		for _, field := range model.Fields {
			current := field
			current.Name = pathPrefix + field.Name
			if columnPrefix != "" && !field.IsAssociation() &&
				!field.IsEmbedded() {
				if current.Column == "" {
					current.Column = columnPrefix + field.Name
				} else {
					current.Column = columnPrefix + current.Column
				}
			}
			result = append(result, current)
			if field.EmbeddedClass != "" {
				collect(
					field.EmbeddedClass,
					current.Name+".",
					columnPrefix+field.ColumnPrefix,
				)
			}
		}
	}
	collect(className, "", "")
	return uniqueFields(result), nil
}

func (idx *Index) ModelForRepository(
	repository string,
) (Model, bool, error) {
	models, err := idx.Models()
	if err != nil {
		return Model{}, false, err
	}
	for _, model := range models {
		if sameClass(model.Repository, repository) {
			return model, true, nil
		}
	}
	return Model{}, false, nil
}

func (idx *Index) ClassNames() ([]string, error) {
	models, err := idx.Models()
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(models))
	for _, model := range models {
		if model.Kind == MappedSuperclassModel ||
			model.Kind == EmbeddableModel {
			continue
		}
		result = append(result, model.Class)
	}
	return result, nil
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	if err := errors.Join(
		idx.records.BatchDeleteByFilePaths(paths),
		idx.dqlUsages.BatchDeleteByFilePaths(paths),
		idx.dqlFunctions.BatchDeleteByFilePaths(paths),
		idx.typeRegistrations.BatchDeleteByFilePaths(paths),
	); err != nil {
		return err
	}
	idx.removeIndexedPaths(paths)
	idx.removeDQLIndexedPaths(paths)
	idx.removeDQLFunctionIndexedPaths(paths)
	idx.removeTypeIndexedPaths(paths)
	idx.invalidate()
	idx.invalidateTypes()
	return nil
}

func (idx *Index) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil {
		return nil
	}
	if err := errors.Join(
		idx.records.BatchDeleteByFilePathsIn(mutation, paths),
		idx.dqlUsages.BatchDeleteByFilePathsIn(mutation, paths),
		idx.dqlFunctions.BatchDeleteByFilePathsIn(mutation, paths),
		idx.typeRegistrations.BatchDeleteByFilePathsIn(mutation, paths),
	); err != nil {
		return err
	}
	publish := func() {
		idx.removeIndexedPaths(paths)
		idx.removeDQLIndexedPaths(paths)
		idx.removeDQLFunctionIndexedPaths(paths)
		idx.removeTypeIndexedPaths(paths)
		idx.invalidate()
		idx.invalidateTypes()
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
	if err := errors.Join(
		idx.records.Clear(),
		idx.dqlUsages.Clear(),
		idx.dqlFunctions.Clear(),
		idx.typeRegistrations.Clear(),
	); err != nil {
		return err
	}
	idx.resetIndexedPaths()
	idx.resetDQLIndexedPaths()
	idx.resetDQLFunctionIndexedPaths()
	idx.resetTypeIndexedPaths()
	idx.invalidate()
	idx.invalidateTypes()
	return nil
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	if err := errors.Join(
		idx.records.ClearIn(mutation),
		idx.dqlUsages.ClearIn(mutation),
		idx.dqlFunctions.ClearIn(mutation),
		idx.typeRegistrations.ClearIn(mutation),
	); err != nil {
		return err
	}
	publish := func() {
		idx.resetIndexedPaths()
		idx.resetDQLIndexedPaths()
		idx.resetDQLFunctionIndexedPaths()
		idx.resetTypeIndexedPaths()
		idx.invalidate()
		idx.invalidateTypes()
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *Index) Close() error {
	if idx == nil {
		return nil
	}
	return errors.Join(
		idx.records.Close(),
		idx.dqlUsages.Close(),
		idx.dqlFunctions.Close(),
		idx.typeRegistrations.Close(),
	)
}

func normalizeModel(model Model) Model {
	model.Class = normalizeClass(model.Class)
	model.Parent = normalizeClass(model.Parent)
	model.Repository = normalizeClass(model.Repository)
	for position := range model.Fields {
		model.Fields[position].Class = model.Class
		model.Fields[position].Relation = normalizeClass(
			model.Fields[position].Relation,
		)
		model.Fields[position].EmbeddedClass = normalizeClass(
			model.Fields[position].EmbeddedClass,
		)
		model.Fields[position].EnumType = normalizeClass(
			model.Fields[position].EnumType,
		)
	}
	for position := range model.DiscriminatorMap {
		model.DiscriminatorMap[position].Class = normalizeClass(
			model.DiscriminatorMap[position].Class,
		)
	}
	return model
}

func mergeModel(target *Model, source Model) {
	source = normalizeModel(source)
	if target.Parent == "" {
		target.Parent = source.Parent
	}
	if target.Repository == "" {
		target.Repository = source.Repository
		target.RepositoryRange = source.RepositoryRange
	}
	if target.Table == "" {
		target.Table = source.Table
	}
	if target.InheritanceType == "" {
		target.InheritanceType = source.InheritanceType
	}
	if target.DiscriminatorColumn == "" {
		target.DiscriminatorColumn = source.DiscriminatorColumn
	}
	if target.DiscriminatorType == "" {
		target.DiscriminatorType = source.DiscriminatorType
	}
	if target.File == "" || source.Source == PHPAttributeSource {
		target.File = source.File
		target.Range = source.Range
		target.NameRange = source.NameRange
		target.Source = source.Source
	}
	target.Fields = append(target.Fields, source.Fields...)
	target.Callbacks = append(target.Callbacks, source.Callbacks...)
	target.DiscriminatorMap = append(
		target.DiscriminatorMap,
		source.DiscriminatorMap...,
	)
	target.TableConstraints = append(
		target.TableConstraints,
		source.TableConstraints...,
	)
}

func sortModel(model *Model) {
	model.Fields = uniqueFields(model.Fields)
	sort.Slice(model.Callbacks, func(left, right int) bool {
		if model.Callbacks[left].Event != model.Callbacks[right].Event {
			return model.Callbacks[left].Event < model.Callbacks[right].Event
		}
		return model.Callbacks[left].Method < model.Callbacks[right].Method
	})
	model.DiscriminatorMap = uniqueDiscriminatorMappings(
		model.DiscriminatorMap,
	)
	model.TableConstraints = uniqueTableConstraints(
		model.TableConstraints,
	)
}

func uniqueTableConstraints(
	constraints []TableConstraint,
) []TableConstraint {
	seen := make(map[string]struct{}, len(constraints))
	result := make([]TableConstraint, 0, len(constraints))
	for _, constraint := range constraints {
		if len(constraint.Fields) == 0 &&
			len(constraint.Columns) == 0 {
			continue
		}
		position := constraint.NameRange.Start
		if position == 0 && len(constraint.Fields) != 0 {
			position = constraint.Fields[0].Range.Start
		}
		if position == 0 && len(constraint.Columns) != 0 {
			position = constraint.Columns[0].Range.Start
		}
		key := strconv.Itoa(int(constraint.Kind)) + "|" +
			strings.ToLower(constraint.Name) + "|" +
			constraint.File + "|" +
			strconv.FormatUint(
				uint64(position),
				10,
			)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, constraint)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Kind != result[right].Kind {
			return result[left].Kind < result[right].Kind
		}
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].NameRange.Start <
			result[right].NameRange.Start
	})
	return result
}

func uniqueDiscriminatorMappings(
	mappings []DiscriminatorMapping,
) []DiscriminatorMapping {
	seen := make(map[string]struct{}, len(mappings))
	result := make([]DiscriminatorMapping, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping.Value == "" && mapping.Class == "" {
			continue
		}
		key := strings.ToLower(mapping.Value) + "|" +
			strings.ToLower(mapping.Class) + "|" + mapping.File + "|" +
			strconv.FormatUint(uint64(mapping.ClassRange.Start), 10)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, mapping)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Value != result[right].Value {
			return result[left].Value < result[right].Value
		}
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].ClassRange.Start <
			result[right].ClassRange.Start
	})
	return result
}

func uniqueFields(fields []Field) []Field {
	seen := make(map[string]struct{}, len(fields))
	result := make([]Field, 0, len(fields))
	for _, field := range fields {
		if field.Name == "" {
			continue
		}
		key := strings.ToLower(field.Name) + "|" + field.File + "|" +
			strconv.FormatUint(uint64(field.Range.Start), 10)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, field)
	}
	sort.Slice(result, func(left, right int) bool {
		if strings.EqualFold(result[left].Name, result[right].Name) {
			if result[left].File != result[right].File {
				return result[left].File < result[right].File
			}
			return result[left].Range.Start < result[right].Range.Start
		}
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result
}

func cloneModels(models []Model) []Model {
	result := make([]Model, len(models))
	for position, model := range models {
		result[position] = model
		if model.Fields != nil {
			result[position].Fields = append(
				make([]Field, 0, len(model.Fields)),
				model.Fields...,
			)
		}
		if model.Callbacks != nil {
			result[position].Callbacks = append(
				make([]LifecycleCallback, 0, len(model.Callbacks)),
				model.Callbacks...,
			)
		}
		if model.DiscriminatorMap != nil {
			result[position].DiscriminatorMap = append(
				make([]DiscriminatorMapping, 0, len(model.DiscriminatorMap)),
				model.DiscriminatorMap...,
			)
		}
		if model.TableConstraints != nil {
			result[position].TableConstraints = append(
				make([]TableConstraint, 0, len(model.TableConstraints)),
				model.TableConstraints...,
			)
			for constraint := range result[position].TableConstraints {
				current := &result[position].TableConstraints[constraint]
				current.Fields = append(
					[]TableConstraintReference(nil),
					current.Fields...,
				)
				current.Columns = append(
					[]TableConstraintReference(nil),
					current.Columns...,
				)
			}
		}
	}
	return result
}

// mustModels only serves the hot successful cache path without swallowing a
// database error. A nil result falls through to the normal error-returning
// lookup in Model.
func mustModels(idx *Index) []Model {
	if idx == nil {
		return nil
	}
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	if idx.cachedModels == nil || idx.cacheBuiltAt != idx.cacheGeneration {
		return nil
	}
	return cloneModels(idx.cachedModels)
}

func (idx *Index) invalidate() {
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	idx.cacheGeneration++
	idx.cachedModels = nil
}

func (idx *Index) currentModelGeneration() uint64 {
	if idx == nil {
		return 0
	}
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	return idx.cacheGeneration
}

func (idx *Index) invalidateTypes() {
	idx.typeCacheMu.Lock()
	defer idx.typeCacheMu.Unlock()
	idx.typeGeneration++
	idx.cachedTypes = nil
}

func (idx *Index) hasIndexedPath(path string) bool {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, exists := idx.paths[path]
	return exists
}

func (idx *Index) hasDQLIndexedPath(path string) bool {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, exists := idx.dqlPaths[path]
	return exists
}

func (idx *Index) hasDQLFunctionIndexedPath(path string) bool {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, exists := idx.functionPaths[path]
	return exists
}

func (idx *Index) hasTypeIndexedPath(path string) bool {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, exists := idx.typePaths[path]
	return exists
}

func (idx *Index) publishIndexedPath(
	path string,
	present bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.pathsMu.Lock()
		if present {
			idx.paths[path] = struct{}{}
		} else {
			delete(idx.paths, path)
		}
		idx.pathsMu.Unlock()
		idx.invalidate()
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *Index) publishDQLIndexedPath(
	path string,
	present bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.pathsMu.Lock()
		if present {
			idx.dqlPaths[path] = struct{}{}
		} else {
			delete(idx.dqlPaths, path)
		}
		idx.pathsMu.Unlock()
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *Index) publishDQLFunctionIndexedPath(
	path string,
	present bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.pathsMu.Lock()
		if present {
			idx.functionPaths[path] = struct{}{}
		} else {
			delete(idx.functionPaths, path)
		}
		idx.pathsMu.Unlock()
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *Index) publishTypeIndexedPath(
	path string,
	present bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.pathsMu.Lock()
		if present {
			idx.typePaths[path] = struct{}{}
		} else {
			delete(idx.typePaths, path)
		}
		idx.pathsMu.Unlock()
		idx.invalidateTypes()
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

func (idx *Index) removeDQLIndexedPaths(paths []string) {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	for _, path := range paths {
		delete(idx.dqlPaths, path)
	}
}

func (idx *Index) removeDQLFunctionIndexedPaths(paths []string) {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	for _, path := range paths {
		delete(idx.functionPaths, path)
	}
}

func (idx *Index) removeTypeIndexedPaths(paths []string) {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	for _, path := range paths {
		delete(idx.typePaths, path)
	}
}

func (idx *Index) resetIndexedPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	clear(idx.paths)
}

func (idx *Index) resetDQLIndexedPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	clear(idx.dqlPaths)
}

func (idx *Index) resetDQLFunctionIndexedPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	clear(idx.functionPaths)
}

func (idx *Index) resetTypeIndexedPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	clear(idx.typePaths)
}
