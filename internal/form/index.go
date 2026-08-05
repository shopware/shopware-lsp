package form

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	formTypeInterface          = "Symfony\\Component\\Form\\FormTypeInterface"
	formTypeExtensionInterface = "Symfony\\Component\\Form\\FormTypeExtensionInterface"
	abstractTypeClass          = "Symfony\\Component\\Form\\AbstractType"
	coreFormTypeClass          = "Symfony\\Component\\Form\\Extension\\Core\\Type\\FormType"
)

type OptionKind uint8

const (
	DefaultOption OptionKind = iota
	RequiredOption
	DefinedOption
	AllowedValuesOption
	AllowedTypesOption
)

type Option struct {
	Name         string
	Kind         OptionKind
	Default      string
	AllowedTypes []string
	Class        string
	File         string
	Range        cst.TextRange
}

type Field struct {
	Name         string
	Type         string
	PropertyPath string
	Mapped       bool
	Class        string
	File         string
	Range        cst.TextRange
	TypeRange    cst.TextRange
}

type ViewVar struct {
	Name  string
	Type  string
	Value string
	Class string
	File  string
	Range cst.TextRange
}

type DataField struct {
	Name   string
	Type   string
	Class  string
	File   string
	Range  cst.TextRange
	Symbol semantic.Symbol
}

// Type is the complete source contribution of one PHP form class or one
// service registration. Multiple records for the same class are merged at
// query time, allowing autoconfigured PHP types and explicit container aliases
// to complement each other without coupling this index to the DI repository.
type Type struct {
	Class          string
	Aliases        []string
	Parent         string
	DataClass      string
	FormType       bool
	Abstract       bool
	Extension      bool
	ExtendedTypes  []string
	File           string
	Range          cst.TextRange
	NameRange      cst.TextRange
	DataClassRange cst.TextRange
	Options        []Option
	Fields         []Field
	ViewVars       []ViewVar
}

type DataClassRelation struct {
	Class     string
	DataClass string
	File      string
	NameRange cst.TextRange
}

type Index struct {
	records  *indexer.DataIndexer[Type]
	phpIndex *php.PHPIndex

	pathsMu sync.RWMutex
	paths   map[string]struct{}

	cacheMu                  sync.Mutex
	cacheGeneration          uint64
	cachePHPRevision         uint64
	cacheBuiltAt             uint64
	cachedTypes              []Type
	cachedDataClassRelations []DataClassRelation
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	records, err := indexer.NewRepository[Type](
		filepath.Join(configDir, "forms.db"),
		"symfony.forms.types",
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
	return &Index{
		records: records,
		paths:   formPathSet(paths),
	}, nil
}

func (idx *Index) SetPHPIndex(index *php.PHPIndex) {
	if idx == nil {
		return
	}
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	idx.phpIndex = index
	idx.cacheBuiltAt = 0
}

func (idx *Index) ID() string {
	return "symfony.form"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}

	var records []Type
	switch file.Extension() {
	case ".php":
		if !isPHPFormCandidate(file.Content) && !idx.hasIndexedPath(file.Path) {
			return nil
		}
		records = parsePHP(file)
	case ".yaml", ".yml":
		if !strings.Contains(file.Source, "form.type") &&
			!strings.Contains(file.Source, "form.type_extension") &&
			!idx.hasIndexedPath(file.Path) {
			return nil
		}
		records = parseYAML(file)
	case ".xml":
		if !strings.Contains(file.Source, "form.type") &&
			!strings.Contains(file.Source, "form.type_extension") &&
			!idx.hasIndexedPath(file.Path) {
			return nil
		}
		records = parseXML(file)
	default:
		return nil
	}

	write := map[string]map[string]Type{file.Path: {}}
	for recordIndex, record := range records {
		if record.Class == "" {
			continue
		}
		key := strings.ToLower(record.Class)
		if _, exists := write[file.Path][key]; exists {
			key += "#" + recordKeySuffix(recordIndex, record.Range.Start)
		}
		write[file.Path][key] = record
	}
	if err := idx.records.BatchSaveItemsIn(file.Mutation(), write); err != nil {
		return err
	}
	return idx.publishIndexedPath(
		file.Path,
		len(write[file.Path]) != 0,
		file.Mutation(),
	)
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.BatchDeleteByFilePaths(paths); err != nil {
		return err
	}
	idx.removeIndexedPaths(paths)
	idx.invalidate()
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
		idx.invalidate()
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
	idx.invalidate()
	return nil
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.ClearIn(mutation); err != nil {
		return err
	}
	publish := func() {
		idx.resetIndexedPaths()
		idx.invalidate()
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
	return idx.records.Close()
}

func (idx *Index) GetTypes() ([]Type, error) {
	if idx == nil {
		return nil, nil
	}
	phpRevision := uint64(0)
	if idx.phpIndex != nil {
		phpRevision = idx.phpIndex.Revision()
	}

	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	if idx.cacheBuiltAt == idx.cacheGeneration &&
		idx.cachePHPRevision == phpRevision &&
		idx.cachedTypes != nil {
		return cloneTypes(idx.cachedTypes), nil
	}

	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	merged := make(map[string]*Type)
	for _, record := range records {
		mergeTypeRecord(merged, record)
	}
	idx.attachSemanticTypes(merged)

	result := make([]Type, 0, len(merged))
	dataClassRelations := make([]DataClassRelation, 0)
	for _, current := range merged {
		if current.DataClass != "" {
			dataClassRelations = append(
				dataClassRelations,
				DataClassRelation{
					Class:     normalizePHPName(current.Class),
					DataClass: normalizePHPName(current.DataClass),
					File:      current.File,
					NameRange: current.NameRange,
				},
			)
		}
		if current.Extension || current.Abstract ||
			!idx.isFormTypeRecord(merged, current, make(map[string]struct{})) {
			continue
		}
		normalizeType(current)
		result = append(result, *current)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Class) <
			strings.ToLower(result[right].Class)
	})
	sort.Slice(dataClassRelations, func(left, right int) bool {
		if !strings.EqualFold(
			dataClassRelations[left].DataClass,
			dataClassRelations[right].DataClass,
		) {
			return strings.ToLower(dataClassRelations[left].DataClass) <
				strings.ToLower(dataClassRelations[right].DataClass)
		}
		return strings.ToLower(dataClassRelations[left].Class) <
			strings.ToLower(dataClassRelations[right].Class)
	})
	idx.cachedTypes = cloneTypes(result)
	idx.cachedDataClassRelations = append(
		[]DataClassRelation(nil),
		dataClassRelations...,
	)
	idx.cacheBuiltAt = idx.cacheGeneration
	idx.cachePHPRevision = phpRevision
	return result, nil
}

// GetDataClassRelations returns direct form data_class declarations. Unlike
// GetTypes, this also retains form extensions and configuration-only classes:
// they can declare a useful class relationship even when they are not
// instantiable form types themselves.
func (idx *Index) GetDataClassRelations() ([]DataClassRelation, error) {
	if idx == nil {
		return nil, nil
	}
	if _, err := idx.GetTypes(); err != nil {
		return nil, err
	}
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	return append(
		[]DataClassRelation(nil),
		idx.cachedDataClassRelations...,
	), nil
}

func (idx *Index) GetType(name string) (Type, bool, error) {
	name = normalizePHPName(name)
	if idx == nil || name == "" {
		return Type{}, false, nil
	}
	all, err := idx.GetTypes()
	if err != nil {
		return Type{}, false, err
	}
	for _, current := range all {
		if strings.EqualFold(current.Class, name) ||
			containsFold(current.Aliases, name) {
			return current, true, nil
		}
	}
	return Type{}, false, nil
}

func (idx *Index) EffectiveOptions(name string) ([]Option, error) {
	options, err := idx.EffectiveOptionDeclarations(name)
	if err != nil {
		return nil, err
	}
	return uniqueOptions(options), nil
}

// EffectiveOptionDeclarations returns every option contribution from the
// concrete type, its parent chain, and matching extensions. Unlike
// EffectiveOptions it intentionally retains repeated declarations so
// analytics and navigation clients can expose all option kinds and sources.
func (idx *Index) EffectiveOptionDeclarations(
	name string,
) ([]Option, error) {
	related, extensions, err := idx.relatedRecordsWithOptions(name, true)
	if err != nil {
		return nil, err
	}
	var result []Option
	for _, current := range append(related, extensions...) {
		result = append(result, current.Options...)
	}
	return result, nil
}

func (idx *Index) EffectiveFields(name string) ([]Field, error) {
	related, _, err := idx.relatedRecords(name)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]Field)
	for _, current := range related {
		for _, field := range current.Fields {
			key := strings.ToLower(field.Name)
			if _, exists := unique[key]; exists {
				continue
			}
			unique[key] = field
		}
	}
	result := make([]Field, 0, len(unique))
	for _, field := range unique {
		result = append(result, field)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result, nil
}

// EffectiveViewVars returns persisted buildView()/finishView() variables from
// the concrete type hierarchy, matching extensions, and Symfony's core
// FormType. Multiple declarations of the same key are retained for
// multi-target navigation; callers can collapse them for completion.
func (idx *Index) EffectiveViewVars(name string) ([]ViewVar, error) {
	related, extensions, err := idx.relatedRecords(name)
	if err != nil {
		return nil, err
	}
	core, coreExtensions, err := idx.relatedRecords(coreFormTypeClass)
	if err != nil {
		return nil, err
	}
	records := append(related, extensions...)
	records = append(records, core...)
	records = append(records, coreExtensions...)
	seen := make(map[string]struct{})
	var result []ViewVar
	for _, current := range records {
		for _, viewVar := range current.ViewVars {
			key := strings.ToLower(viewVar.Name) + "\x00" +
				filepath.Clean(viewVar.File) + "\x00" +
				viewVar.Range.String()
			if viewVar.Name == "" {
				continue
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, viewVar)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if !strings.EqualFold(result[left].Name, result[right].Name) {
			return strings.ToLower(result[left].Name) <
				strings.ToLower(result[right].Name)
		}
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

func (idx *Index) DataClassFor(name string) (string, error) {
	related, _, err := idx.relatedRecords(name)
	if err != nil {
		return "", err
	}
	for _, current := range related {
		if current.DataClass != "" {
			return current.DataClass, nil
		}
	}
	return "", nil
}

func (idx *Index) DataFieldsFor(name string) ([]DataField, error) {
	if idx == nil || idx.phpIndex == nil {
		return nil, nil
	}
	dataClass, err := idx.DataClassFor(name)
	if err != nil || dataClass == "" {
		return nil, err
	}
	return idx.DataFieldsForClass(dataClass), nil
}

func (idx *Index) DataFieldsForClass(dataClass string) []DataField {
	dataClass = normalizePHPName(dataClass)
	if idx == nil || idx.phpIndex == nil || dataClass == "" {
		return nil
	}
	return DataFieldsForClassInSnapshot(
		idx.phpIndex.SemanticSnapshot(),
		dataClass,
	)
}

// DataFieldsForClassInSnapshot returns writable PropertyAccess fields from a
// specific semantic generation. Generator requests use this with an unsaved
// document overlay so a newly added property or setter is immediately
// available without publishing editor state to the workspace index.
func DataFieldsForClassInSnapshot(
	snapshot *semantic.Snapshot,
	dataClass string,
) []DataField {
	dataClass = normalizePHPName(dataClass)
	if snapshot == nil || dataClass == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: snapshot,
	}).All(types.Named(dataClass))
	unique := make(map[string]DataField)
	for _, member := range members {
		symbol := member.Symbol
		fieldName := ""
		fieldType := member.Type.String()
		switch symbol.Kind {
		case semantic.PropertySymbol:
			if symbol.Visibility != semantic.Public &&
				(!symbol.HasWriteVisibility ||
					symbol.WriteVisibility != semantic.Public) {
				continue
			}
			fieldName = strings.TrimPrefix(symbol.Name, "$")
		case semantic.MethodSymbol:
			if symbol.Visibility != semantic.Public ||
				len(symbol.Name) <= 3 ||
				!strings.HasPrefix(strings.ToLower(symbol.Name), "set") ||
				len(symbol.Parameters) == 0 {
				continue
			}
			fieldName = lowerFirst(symbol.Name[3:])
			fieldType = symbol.Parameters[0].Type.String()
		default:
			continue
		}
		if fieldName == "" {
			continue
		}
		key := strings.ToLower(fieldName)
		if _, exists := unique[key]; exists &&
			symbol.Kind == semantic.PropertySymbol {
			continue
		}
		rng := symbol.SelectionRange
		if rng.Len() == 0 {
			rng = symbol.Range
		}
		unique[key] = DataField{
			Name:   fieldName,
			Type:   fieldType,
			Class:  dataClass,
			File:   symbol.Path,
			Range:  rng,
			Symbol: symbol,
		}
	}
	result := make([]DataField, 0, len(unique))
	for _, field := range unique {
		result = append(result, field)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

// EffectiveOptionsFor overlays a current document form type over persisted
// facts and still includes the indexed parent option chain.
func (idx *Index) EffectiveOptionsFor(current Type) ([]Option, error) {
	result := append([]Option(nil), current.Options...)
	if current.Parent != "" {
		parent, err := idx.EffectiveOptions(current.Parent)
		if err != nil {
			return nil, err
		}
		result = append(result, parent...)
	}
	persisted, err := idx.EffectiveOptions(current.Class)
	if err != nil {
		return nil, err
	}
	result = append(result, persisted...)
	return uniqueOptions(result), nil
}

func (idx *Index) EffectiveFieldsFor(current Type) ([]Field, error) {
	result := append([]Field(nil), current.Fields...)
	if current.Parent != "" {
		parent, err := idx.EffectiveFields(current.Parent)
		if err != nil {
			return nil, err
		}
		result = append(result, parent...)
	}
	persisted, err := idx.EffectiveFields(current.Class)
	if err != nil {
		return nil, err
	}
	result = append(result, persisted...)
	return uniqueFields(result), nil
}

func (idx *Index) relatedRecords(name string) ([]Type, []Type, error) {
	return idx.relatedRecordsWithOptions(name, false)
}

func (idx *Index) relatedRecordsWithOptions(
	name string,
	preserveOptionDeclarations bool,
) ([]Type, []Type, error) {
	name = normalizePHPName(name)
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, nil, err
	}
	merged := make(map[string]*Type)
	for _, record := range records {
		mergeTypeRecord(merged, record)
	}
	idx.attachSemanticTypes(merged)

	var related []Type
	visited := make(map[string]struct{})
	var visit func(string)
	visit = func(target string) {
		current := lookupMergedType(merged, target)
		if current == nil {
			return
		}
		key := strings.ToLower(current.Class)
		if _, exists := visited[key]; exists {
			return
		}
		visited[key] = struct{}{}
		options := current.Options
		normalizeType(current)
		if preserveOptionDeclarations {
			current.Options = append([]Option(nil), options...)
		}
		related = append(related, *current)
		if current.Parent != "" {
			visit(current.Parent)
		}
		if idx.phpIndex != nil {
			if symbol, found := idx.phpIndex.FindClass(current.Class); found {
				for _, parent := range symbol.Extends {
					visit(parent)
				}
			}
		}
	}
	visit(name)
	if len(related) == 0 {
		return nil, nil, nil
	}

	var extensions []Type
	for _, current := range merged {
		if !current.Extension {
			continue
		}
		for _, target := range current.ExtendedTypes {
			if relatedContains(related, target) {
				options := current.Options
				normalizeType(current)
				if preserveOptionDeclarations {
					current.Options = append([]Option(nil), options...)
				}
				extensions = append(extensions, *current)
				break
			}
		}
	}
	sort.Slice(extensions, func(left, right int) bool {
		return strings.ToLower(extensions[left].Class) <
			strings.ToLower(extensions[right].Class)
	})
	return related, extensions, nil
}

func (idx *Index) attachSemanticTypes(merged map[string]*Type) {
	if idx.phpIndex == nil {
		return
	}
	snapshot := idx.phpIndex.SemanticSnapshot()
	for _, symbol := range idx.phpIndex.ClassSymbols() {
		if symbol.Flags.Has(semantic.AbstractFlag) ||
			symbol.Kind != semantic.ClassSymbol {
			if symbol.Flags.Has(semantic.AbstractFlag) {
				if current := merged[strings.ToLower(
					symbol.FullyQualified,
				)]; current != nil {
					current.Abstract = true
				}
			}
			continue
		}
		isForm := snapshot.IsSubtypeOf(symbol.FullyQualified, formTypeInterface) ||
			snapshot.IsSubtypeOf(symbol.FullyQualified, abstractTypeClass)
		isExtension := snapshot.IsSubtypeOf(
			symbol.FullyQualified,
			formTypeExtensionInterface,
		)
		if !isForm && !isExtension {
			continue
		}
		key := strings.ToLower(symbol.FullyQualified)
		current := merged[key]
		if current == nil {
			current = &Type{
				Class:     symbol.FullyQualified,
				File:      symbol.Path,
				Range:     symbol.Range,
				NameRange: symbol.SelectionRange,
			}
			merged[key] = current
		}
		if isExtension {
			current.Extension = true
		}
		if isForm {
			current.FormType = true
		}
	}
}

func (idx *Index) isFormTypeRecord(
	merged map[string]*Type,
	current *Type,
	visited map[string]struct{},
) bool {
	if current == nil {
		return false
	}
	key := strings.ToLower(current.Class)
	if _, exists := visited[key]; exists {
		return false
	}
	visited[key] = struct{}{}
	if current.FormType ||
		strings.EqualFold(current.Class, coreFormTypeClass) {
		return true
	}
	if idx.phpIndex == nil {
		return len(current.Options) != 0 || len(current.Fields) != 0
	}
	snapshot := idx.phpIndex.SemanticSnapshot()
	if snapshot.IsSubtypeOf(current.Class, formTypeInterface) ||
		snapshot.IsSubtypeOf(current.Class, abstractTypeClass) {
		return true
	}
	if symbol, found := idx.phpIndex.FindClass(current.Class); found {
		for _, parent := range symbol.Extends {
			if idx.isFormTypeRecord(
				merged,
				lookupMergedType(merged, parent),
				visited,
			) {
				return true
			}
		}
	}
	return false
}

func mergeTypeRecord(merged map[string]*Type, record Type) {
	record.Class = normalizePHPName(record.Class)
	if record.Class == "" {
		return
	}
	key := strings.ToLower(record.Class)
	current := merged[key]
	if current == nil {
		copy := record
		merged[key] = &copy
		return
	}
	current.Aliases = appendUniqueFold(current.Aliases, record.Aliases...)
	current.ExtendedTypes = appendUniqueFold(
		current.ExtendedTypes,
		record.ExtendedTypes...,
	)
	if current.Parent == "" {
		current.Parent = record.Parent
	}
	if current.DataClass == "" {
		current.DataClass = record.DataClass
		current.DataClassRange = record.DataClassRange
	}
	current.Extension = current.Extension || record.Extension
	current.FormType = current.FormType || record.FormType
	current.Abstract = current.Abstract || record.Abstract
	if current.File == "" || record.Options != nil || record.Fields != nil ||
		record.ViewVars != nil {
		current.File = record.File
		current.Range = record.Range
		current.NameRange = record.NameRange
	}
	current.Options = append(current.Options, record.Options...)
	current.Fields = append(current.Fields, record.Fields...)
	current.ViewVars = append(current.ViewVars, record.ViewVars...)
}

func lookupMergedType(merged map[string]*Type, name string) *Type {
	name = normalizePHPName(name)
	if current := merged[strings.ToLower(name)]; current != nil {
		return current
	}
	for _, current := range merged {
		if containsFold(current.Aliases, name) {
			return current
		}
	}
	return nil
}

func relatedContains(related []Type, target string) bool {
	target = normalizePHPName(target)
	for _, current := range related {
		if strings.EqualFold(current.Class, target) ||
			containsFold(current.Aliases, target) {
			return true
		}
	}
	return false
}

func normalizeType(current *Type) {
	current.Class = normalizePHPName(current.Class)
	current.Parent = normalizePHPName(current.Parent)
	current.DataClass = normalizePHPName(current.DataClass)
	current.Aliases = appendUniqueFold(nil, current.Aliases...)
	current.ExtendedTypes = appendUniqueFold(nil, current.ExtendedTypes...)
	sort.Strings(current.Aliases)
	sort.Strings(current.ExtendedTypes)
	current.Options = uniqueOptions(current.Options)
	current.Fields = uniqueFields(current.Fields)
	current.ViewVars = uniqueViewVars(current.ViewVars)
}

func uniqueOptions(options []Option) []Option {
	unique := make(map[string]Option)
	for _, option := range options {
		if option.Name == "" {
			continue
		}
		key := strings.ToLower(option.Name)
		current, exists := unique[key]
		if !exists {
			unique[key] = option
			continue
		}
		if current.Default == "" {
			current.Default = option.Default
		}
		current.AllowedTypes = appendUniqueFold(
			current.AllowedTypes,
			option.AllowedTypes...,
		)
		unique[key] = current
	}
	result := make([]Option, 0, len(unique))
	for _, option := range unique {
		result = append(result, option)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func uniqueFields(fields []Field) []Field {
	unique := make(map[string]Field)
	for _, field := range fields {
		if field.Name == "" {
			continue
		}
		key := strings.ToLower(field.Name)
		if _, exists := unique[key]; !exists {
			unique[key] = field
		}
	}
	result := make([]Field, 0, len(unique))
	for _, field := range unique {
		result = append(result, field)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func uniqueViewVars(viewVars []ViewVar) []ViewVar {
	unique := make(map[string]ViewVar)
	for _, viewVar := range viewVars {
		if viewVar.Name == "" {
			continue
		}
		key := strings.ToLower(viewVar.Name) + "\x00" +
			filepath.Clean(viewVar.File) + "\x00" +
			viewVar.Range.String()
		if _, exists := unique[key]; !exists {
			unique[key] = viewVar
		}
	}
	result := make([]ViewVar, 0, len(unique))
	for _, viewVar := range unique {
		result = append(result, viewVar)
	}
	sort.Slice(result, func(left, right int) bool {
		if !strings.EqualFold(result[left].Name, result[right].Name) {
			return strings.ToLower(result[left].Name) <
				strings.ToLower(result[right].Name)
		}
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result
}

func cloneTypes(source []Type) []Type {
	result := make([]Type, len(source))
	copy(result, source)
	for index := range result {
		result[index].Aliases = append([]string(nil), source[index].Aliases...)
		result[index].ExtendedTypes = append(
			[]string(nil),
			source[index].ExtendedTypes...,
		)
		result[index].Options = append([]Option(nil), source[index].Options...)
		result[index].Fields = append([]Field(nil), source[index].Fields...)
		result[index].ViewVars = append(
			[]ViewVar(nil),
			source[index].ViewVars...,
		)
	}
	return result
}

func normalizePHPName(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), `\`)
}

func appendUniqueFold(target []string, values ...string) []string {
	for _, value := range values {
		value = normalizePHPName(value)
		if value == "" || containsFold(target, value) {
			continue
		}
		target = append(target, value)
	}
	return target
}

func containsFold(values []string, value string) bool {
	for _, current := range values {
		if strings.EqualFold(normalizePHPName(current), normalizePHPName(value)) {
			return true
		}
	}
	return false
}

func lowerFirst(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}

func recordKeySuffix(index int, offset uint32) string {
	return strings.Join([]string{
		itoa(index),
		itoa(int(offset)),
	}, ":")
}

func itoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = digits[value%10]
		value /= 10
	}
	return string(buffer[position:])
}

func (idx *Index) invalidate() {
	idx.cacheMu.Lock()
	defer idx.cacheMu.Unlock()
	idx.cacheGeneration++
	idx.cachedTypes = nil
	idx.cachedDataClassRelations = nil
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
	idx.paths = make(map[string]struct{})
}

func formPathSet(paths []string) map[string]struct{} {
	result := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		result[path] = struct{}{}
	}
	return result
}

var _ indexer.Indexer = (*Index)(nil)
var _ indexer.TransactionalRemover = (*Index)(nil)
var _ indexer.TransactionalClearer = (*Index)(nil)
