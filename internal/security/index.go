package security

import (
	"bytes"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type OccurrenceRole uint8

const (
	DeclarationOccurrence OccurrenceRole = iota
	ReferenceOccurrence
)

type Origin uint8

const (
	OriginUnknown Origin = iota
	OriginVoter
	OriginRoleHierarchy
	OriginAccessControl
	OriginPHPCall
	OriginPHPAttribute
	OriginPHPExpression
	OriginPHPDoc
	OriginTwig
	OriginBuiltIn
)

type Occurrence struct {
	Name   string
	Role   OccurrenceRole
	Origin Origin
	File   string
	Range  cst.TextRange
	Class  string
}

type Record struct {
	File        string
	Occurrences []Occurrence
}

type Attribute struct {
	Name        string
	Occurrences []Occurrence
}

func (attribute Attribute) Declarations() []Occurrence {
	result := make([]Occurrence, 0, len(attribute.Occurrences))
	for _, occurrence := range attribute.Occurrences {
		if occurrence.Role == DeclarationOccurrence {
			result = append(result, occurrence)
		}
	}
	return result
}

func (attribute Attribute) References() []Occurrence {
	result := make([]Occurrence, 0, len(attribute.Occurrences))
	for _, occurrence := range attribute.Occurrences {
		if occurrence.Role == ReferenceOccurrence {
			result = append(result, occurrence)
		}
	}
	return result
}

var builtInAttributes = []string{
	"PUBLIC_ACCESS",
	"IS_AUTHENTICATED",
	"IS_AUTHENTICATED_FULLY",
	"IS_AUTHENTICATED_REMEMBERED",
	"IS_AUTHENTICATED_ANONYMOUSLY",
	"IS_IMPERSONATOR",
}

type Index struct {
	records       *indexer.DataIndexer[Record]
	configRecords *indexer.DataIndexer[ConfigRecord]

	pathsMu sync.RWMutex
	paths   map[string]struct{}
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	records, err := indexer.NewRepository[Record](
		filepath.Join(configDir, "security.db"),
		"symfony.security.attributes",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	configRecords, err := indexer.NewRepository[ConfigRecord](
		filepath.Join(configDir, "security.db"),
		"symfony.security.configuration",
		stores...,
	)
	if err != nil {
		_ = records.Close()
		return nil, err
	}
	attributePaths, err := records.GetAllFilePaths()
	if err != nil {
		_ = records.Close()
		_ = configRecords.Close()
		return nil, err
	}
	configPaths, err := configRecords.GetAllFilePaths()
	if err != nil {
		_ = records.Close()
		_ = configRecords.Close()
		return nil, err
	}
	pathSet := make(
		map[string]struct{},
		len(attributePaths)+len(configPaths),
	)
	for _, path := range attributePaths {
		pathSet[path] = struct{}{}
	}
	for _, path := range configPaths {
		pathSet[path] = struct{}{}
	}
	return &Index{
		records:       records,
		configRecords: configRecords,
		paths:         pathSet,
	}, nil
}

func (idx *Index) ID() string {
	return "symfony.security"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	if !isSecurityCandidate(file) && !idx.hasIndexedPath(file.Path) {
		return nil
	}

	occurrences := occurrencesInFile(file)
	configOccurrences := configOccurrencesInFile(file)
	write := map[string]map[string]Record{file.Path: {}}
	if len(occurrences) != 0 {
		write[file.Path]["security"] = Record{
			File:        file.Path,
			Occurrences: occurrences,
		}
	}
	if err := idx.records.BatchSaveItemsIn(file.Mutation(), write); err != nil {
		return err
	}
	configWrite := map[string]map[string]ConfigRecord{file.Path: {}}
	if len(configOccurrences) != 0 {
		configWrite[file.Path]["configuration"] = ConfigRecord{
			File:        file.Path,
			Occurrences: configOccurrences,
		}
	}
	if err := idx.configRecords.BatchSaveItemsIn(
		file.Mutation(),
		configWrite,
	); err != nil {
		return err
	}
	return idx.publishIndexedPath(
		file.Path,
		len(occurrences) != 0 || len(configOccurrences) != 0,
		file.Mutation(),
	)
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	if err := errors.Join(
		idx.records.BatchDeleteByFilePaths(paths),
		idx.configRecords.BatchDeleteByFilePaths(paths),
	); err != nil {
		return err
	}
	idx.removeIndexedPaths(paths)
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
		idx.configRecords.BatchDeleteByFilePathsIn(mutation, paths),
	); err != nil {
		return err
	}
	publish := func() {
		idx.removeIndexedPaths(paths)
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
		idx.configRecords.Clear(),
	); err != nil {
		return err
	}
	idx.resetIndexedPaths()
	return nil
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	if err := errors.Join(
		idx.records.ClearIn(mutation),
		idx.configRecords.ClearIn(mutation),
	); err != nil {
		return err
	}
	if mutation == nil {
		idx.resetIndexedPaths()
		return nil
	}
	return mutation.AfterCommit(idx.resetIndexedPaths)
}

func (idx *Index) Close() error {
	if idx == nil {
		return nil
	}
	return errors.Join(idx.records.Close(), idx.configRecords.Close())
}

func (idx *Index) ConfigSymbols() ([]ConfigSymbol, error) {
	if idx == nil {
		return nil, nil
	}
	records, err := idx.configRecords.GetAllValues()
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*ConfigSymbol)
	for _, record := range records {
		for _, occurrence := range record.Occurrences {
			if occurrence.Name == "" {
				continue
			}
			key := configSymbolKey(occurrence.Name, occurrence.Kind)
			symbol := byKey[key]
			if symbol == nil {
				symbol = &ConfigSymbol{
					Name: occurrence.Name,
					Kind: occurrence.Kind,
				}
				byKey[key] = symbol
			}
			symbol.Occurrences = append(
				symbol.Occurrences,
				occurrence,
			)
		}
	}
	result := make([]ConfigSymbol, 0, len(byKey))
	for _, symbol := range byKey {
		sortConfigOccurrences(symbol.Occurrences)
		result = append(result, *symbol)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Kind != result[right].Kind {
			return result[left].Kind < result[right].Kind
		}
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (idx *Index) ConfigSymbol(
	name string,
	kind ConfigKind,
) (ConfigSymbol, bool, error) {
	symbols, err := idx.ConfigSymbols()
	if err != nil {
		return ConfigSymbol{}, false, err
	}
	for _, symbol := range symbols {
		if symbol.Kind == kind && strings.EqualFold(symbol.Name, name) {
			return symbol, true, nil
		}
	}
	return ConfigSymbol{}, false, nil
}

func (idx *Index) ConfigNames(kind ConfigKind) ([]string, error) {
	symbols, err := idx.ConfigSymbols()
	if err != nil {
		return nil, err
	}
	var result []string
	for _, symbol := range symbols {
		if symbol.Kind == kind && len(symbol.Declarations()) != 0 {
			result = append(result, symbol.Name)
		}
	}
	return result, nil
}

func (idx *Index) Attributes() ([]Attribute, error) {
	if idx == nil {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	attributes := make(map[string]*Attribute)
	for _, record := range records {
		for _, occurrence := range record.Occurrences {
			if occurrence.Name == "" {
				continue
			}
			key := strings.ToLower(occurrence.Name)
			attribute := attributes[key]
			if attribute == nil {
				attribute = &Attribute{Name: occurrence.Name}
				attributes[key] = attribute
			}
			attribute.Occurrences = append(
				attribute.Occurrences,
				occurrence,
			)
		}
	}
	for _, name := range builtInAttributes {
		key := strings.ToLower(name)
		attribute := attributes[key]
		if attribute == nil {
			attribute = &Attribute{Name: name}
			attributes[key] = attribute
		}
		hasBuiltIn := false
		for _, occurrence := range attribute.Occurrences {
			if occurrence.Origin == OriginBuiltIn {
				hasBuiltIn = true
				break
			}
		}
		if hasBuiltIn {
			continue
		}
		attribute.Occurrences = append(
			attribute.Occurrences,
			Occurrence{
				Name:   name,
				Role:   DeclarationOccurrence,
				Origin: OriginBuiltIn,
			},
		)
	}

	result := make([]Attribute, 0, len(attributes))
	for _, attribute := range attributes {
		sortOccurrences(attribute.Occurrences)
		result = append(result, *attribute)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (idx *Index) Attribute(name string) (Attribute, bool, error) {
	attributes, err := idx.Attributes()
	if err != nil {
		return Attribute{}, false, err
	}
	for _, attribute := range attributes {
		if strings.EqualFold(attribute.Name, name) {
			return attribute, true, nil
		}
	}
	return Attribute{}, false, nil
}

func (idx *Index) Names() ([]string, error) {
	attributes, err := idx.Attributes()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		if len(attribute.Declarations()) == 0 {
			continue
		}
		names = append(names, attribute.Name)
	}
	return names, nil
}

func (idx *Index) ProjectNames() ([]string, error) {
	attributes, err := idx.Attributes()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, attribute := range attributes {
		for _, declaration := range attribute.Declarations() {
			if declaration.Origin == OriginBuiltIn {
				continue
			}
			names = append(names, attribute.Name)
			break
		}
	}
	return names, nil
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
	clear(idx.paths)
}

func isSecurityCandidate(file *indexer.ParsedFile) bool {
	content := file.Content
	switch file.Extension() {
	case ".php":
		return bytes.Contains(content, []byte("SecurityConfig")) ||
			bytes.Contains(content, []byte("Voter")) ||
			bytes.Contains(content, []byte("isGranted")) ||
			bytes.Contains(content, []byte("IsGranted")) ||
			bytes.Contains(content, []byte("denyAccessUnlessGranted")) ||
			bytes.Contains(content, []byte("@Security")) ||
			bytes.Contains(content, []byte("#[Security"))
	case ".twig":
		return bytes.Contains(content, []byte("is_granted")) ||
			bytes.Contains(content, []byte("access_decision"))
	case ".yaml", ".yml":
		return bytes.Contains(content, []byte("role_hierarchy")) ||
			bytes.Contains(content, []byte("access_control")) ||
			bytes.Contains(content, []byte("providers")) ||
			bytes.Contains(content, []byte("firewalls")) ||
			bytes.Contains(content, []byte("password_hashers"))
	case ".xml":
		return bytes.Contains(
			content,
			[]byte("symfony.com/schema/dic/security"),
		)
	default:
		return false
	}
}

func sortOccurrences(occurrences []Occurrence) {
	sort.Slice(occurrences, func(left, right int) bool {
		if occurrences[left].File != occurrences[right].File {
			return occurrences[left].File < occurrences[right].File
		}
		if occurrences[left].Range.Start != occurrences[right].Range.Start {
			return occurrences[left].Range.Start <
				occurrences[right].Range.Start
		}
		return occurrences[left].Role < occurrences[right].Role
	})
}
