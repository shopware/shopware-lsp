package event

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type OccurrenceKind uint8

const (
	ListenerOccurrence OccurrenceKind = iota
	DispatchOccurrence
)

type SourceKind uint8

const (
	SubscriberSource SourceKind = iota
	AttributeSource
	ServiceTagSource
	DispatchSource
)

type Occurrence struct {
	Kind        OccurrenceKind
	Source      SourceKind
	File        string
	Range       cst.TextRange
	MethodRange cst.TextRange
	Class       string
	Method      string
	Service     string
	Priority    string
	EventType   string
}

type Record struct {
	Name        string
	Expression  string
	EventType   string
	File        string
	Occurrences []Occurrence
}

type Constant struct {
	Expression string
	Value      string
	File       string
	Range      cst.TextRange
}

type Event struct {
	Name        string
	EventType   string
	Occurrences []Occurrence
}

func (event Event) Listeners() []Occurrence {
	result := make([]Occurrence, 0, len(event.Occurrences))
	for _, occurrence := range event.Occurrences {
		if occurrence.Kind == ListenerOccurrence {
			result = append(result, occurrence)
		}
	}
	return result
}

func (event Event) Dispatches() []Occurrence {
	result := make([]Occurrence, 0, len(event.Occurrences))
	for _, occurrence := range event.Occurrences {
		if occurrence.Kind == DispatchOccurrence {
			result = append(result, occurrence)
		}
	}
	return result
}

type Index struct {
	records     *indexer.DataIndexer[Record]
	phpIndex    *php.PHPIndex
	pathsMu     sync.RWMutex
	recordPaths map[string]struct{}

	constantMu       sync.Mutex
	constantRevision uint64
	constantCache    map[string]string
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	records, err := indexer.NewRepository[Record](
		filepath.Join(configDir, "events.db"),
		"symfony.events.records",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	recordPaths, err := records.GetAllFilePaths()
	if err != nil {
		_ = records.Close()
		return nil, err
	}
	return &Index{
		records:       records,
		recordPaths:   pathSet(recordPaths),
		constantCache: make(map[string]string),
	}, nil
}

func (idx *Index) SetPHPIndex(index *php.PHPIndex) {
	if idx != nil {
		idx.phpIndex = index
	}
}

func (idx *Index) ID() string {
	return "symfony.event"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}

	var records []Record
	switch file.Extension() {
	case ".php":
		if !isPHPEventCandidate(file.Content) {
			if !idx.hasIndexedPath(file.Path) {
				return nil
			}
		}
		records = parsePHP(file)
	case ".yaml", ".yml":
		if !strings.Contains(file.Source, "kernel.event_listener") {
			if !idx.hasIndexedPath(file.Path) {
				return nil
			}
		} else {
			records = parseYAML(file)
		}
	case ".xml":
		if !strings.Contains(file.Source, "kernel.event_listener") {
			if !idx.hasIndexedPath(file.Path) {
				return nil
			}
		} else {
			records = parseXML(file)
		}
	default:
		return nil
	}

	recordWrite := map[string]map[string]Record{file.Path: {}}
	for _, record := range records {
		if key := recordKey(record); key != "" {
			recordWrite[file.Path][key] = record
		}
	}
	if err := idx.records.BatchSaveItemsIn(
		file.Mutation(),
		recordWrite,
	); err != nil {
		return err
	}

	return idx.publishIndexedPath(
		file.Path,
		len(recordWrite[file.Path]) != 0,
		file.Mutation(),
	)
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	err := idx.records.BatchDeleteByFilePaths(paths)
	if err == nil {
		idx.removeIndexedPaths(paths)
	}
	return err
}

func (idx *Index) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil {
		return nil
	}
	err := idx.records.BatchDeleteByFilePathsIn(mutation, paths)
	if err != nil {
		return err
	}
	if mutation == nil {
		idx.removeIndexedPaths(paths)
		return nil
	}
	return mutation.AfterCommit(func() {
		idx.removeIndexedPaths(paths)
	})
}

func (idx *Index) Clear() error {
	if idx == nil {
		return nil
	}
	err := idx.records.Clear()
	if err == nil {
		idx.resetIndexedPaths()
	}
	return err
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	err := idx.records.ClearIn(mutation)
	if err != nil {
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
	return idx.records.Close()
}

func (idx *Index) hasIndexedPath(path string) bool {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, exists := idx.recordPaths[path]
	return exists
}

func (idx *Index) publishIndexedPath(
	path string,
	hasRecords bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.pathsMu.Lock()
		defer idx.pathsMu.Unlock()
		setPathPresence(idx.recordPaths, path, hasRecords)
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
		delete(idx.recordPaths, path)
	}
}

func (idx *Index) resetIndexedPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	idx.recordPaths = make(map[string]struct{})
}

func pathSet(paths []string) map[string]struct{} {
	result := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		result[path] = struct{}{}
	}
	return result
}

func setPathPresence(
	paths map[string]struct{},
	path string,
	present bool,
) {
	if present {
		paths[path] = struct{}{}
	} else {
		delete(paths, path)
	}
}

func (idx *Index) GetEvents() ([]Event, error) {
	if idx == nil {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}

	grouped := make(map[string]*Event)
	for _, record := range records {
		eventType := record.EventType
		if eventType == "" {
			eventType = idx.recordEventType(record)
		}
		name := record.Name
		if name == "" {
			name = idx.constantValue(record.Expression)
		}
		if name == "" && record.Expression == "" {
			name = eventType
		}
		name = normalizeName(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		current := grouped[key]
		if current == nil {
			current = &Event{Name: name}
			grouped[key] = current
		}
		if eventType == "" && looksLikeClassName(name) {
			eventType = name
		}
		if current.EventType == "" {
			current.EventType = eventType
		}
		for _, occurrence := range record.Occurrences {
			if occurrence.EventType == "" {
				occurrence.EventType = eventType
			}
			if current.EventType == "" {
				current.EventType = occurrence.EventType
			}
			current.Occurrences = append(current.Occurrences, occurrence)
		}
	}

	result := make([]Event, 0, len(grouped))
	for _, current := range grouped {
		current.Occurrences = uniqueOccurrences(current.Occurrences)
		result = append(result, *current)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (idx *Index) GetEvent(name string) (Event, bool, error) {
	name = normalizeName(name)
	if name == "" {
		return Event{}, false, nil
	}
	events, err := idx.GetEvents()
	if err != nil {
		return Event{}, false, err
	}
	for _, current := range events {
		if strings.EqualFold(current.Name, name) {
			return current, true, nil
		}
	}
	return Event{}, false, nil
}

func (idx *Index) ListenerAt(
	path string,
	offset uint32,
) (Occurrence, bool, error) {
	if idx == nil {
		return Occurrence{}, false, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return Occurrence{}, false, err
	}
	for _, record := range records {
		for _, occurrence := range record.Occurrences {
			if occurrence.Kind != ListenerOccurrence {
				continue
			}
			if occurrence.File != path {
				continue
			}
			if containsOffset(occurrence.Range, offset) ||
				containsOffset(occurrence.MethodRange, offset) {
				return occurrence, true, nil
			}
		}
	}
	return Occurrence{}, false, nil
}

func (idx *Index) EventsForListener(
	class,
	method string,
) ([]Event, error) {
	class = strings.TrimPrefix(strings.TrimSpace(class), `\`)
	if class == "" {
		return nil, nil
	}
	events, err := idx.GetEvents()
	if err != nil {
		return nil, err
	}
	var result []Event
	for _, current := range events {
		for _, listener := range current.Listeners() {
			if strings.EqualFold(listener.Class, class) &&
				(method == "" || strings.EqualFold(listener.Method, method)) {
				result = append(result, current)
				break
			}
		}
	}
	return result, nil
}

func (idx *Index) constantValue(expression string) string {
	expression = normalizeName(expression)
	separator := strings.LastIndex(expression, "::")
	if idx == nil || idx.phpIndex == nil || separator < 1 ||
		separator+2 >= len(expression) {
		return ""
	}
	revision := idx.phpIndex.Revision()
	key := strings.ToLower(expression)

	idx.constantMu.Lock()
	defer idx.constantMu.Unlock()
	if idx.constantRevision != revision {
		idx.constantRevision = revision
		idx.constantCache = make(map[string]string)
	}
	if value, exists := idx.constantCache[key]; exists {
		return value
	}

	className := expression[:separator]
	symbol, found := idx.phpIndex.FindClass(className)
	if !found || symbol.Path == "" ||
		strings.HasPrefix(symbol.Path, "phpstub://") {
		idx.constantCache[key] = ""
		return ""
	}
	source, err := os.ReadFile(symbol.Path)
	if err != nil {
		idx.constantCache[key] = ""
		return ""
	}
	file := indexer.NewParsedFile(symbol.Path, source)
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		idx.constantCache[key] = ""
		return ""
	}
	nameResolver := php.NewNameResolver(tree.Root)
	for _, class := range phpquery.Classes(tree.Root) {
		resolved := resolvedClassName(class, nameResolver)
		if !strings.EqualFold(resolved, className) {
			continue
		}
		for _, constant := range parseClassConstants(
			class,
			resolved,
			symbol.Path,
		) {
			idx.constantCache[strings.ToLower(constant.Expression)] =
				constant.Value
		}
		break
	}
	return idx.constantCache[key]
}

func recordKey(record Record) string {
	if record.Name != "" {
		return "name:" + strings.ToLower(normalizeName(record.Name))
	}
	if record.Expression != "" {
		return "expression:" + strings.ToLower(record.Expression)
	}
	if len(record.Occurrences) != 0 {
		occurrence := record.Occurrences[0]
		target := occurrence.Class
		if target == "" {
			target = occurrence.Service
		}
		if target != "" || occurrence.Method != "" {
			return "inferred:" + strings.ToLower(
				target+"::"+occurrence.Method,
			)
		}
	}
	return ""
}

func normalizeName(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), `\`)
}

func looksLikeClassName(name string) bool {
	return strings.Contains(name, `\`) && !strings.ContainsAny(name, " .:-")
}

func containsOffset(rng cst.TextRange, offset uint32) bool {
	return rng.Len() != 0 && offset >= rng.Start && offset <= rng.End
}

func uniqueOccurrences(occurrences []Occurrence) []Occurrence {
	seen := make(map[string]struct{}, len(occurrences))
	result := make([]Occurrence, 0, len(occurrences))
	for _, occurrence := range occurrences {
		key := occurrence.File + ":" +
			strconv.FormatUint(uint64(occurrence.Range.Start), 10) + ":" +
			strconv.FormatUint(uint64(occurrence.Range.End), 10) + ":" +
			occurrence.Class + ":" + occurrence.Method
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, occurrence)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File == result[right].File {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].File < result[right].File
	})
	return result
}

func (idx *Index) recordEventType(record Record) string {
	if idx == nil || idx.phpIndex == nil {
		return ""
	}
	for _, occurrence := range record.Occurrences {
		if occurrence.EventType != "" {
			return normalizeName(occurrence.EventType)
		}
		if occurrence.Class == "" || occurrence.Method == "" {
			continue
		}
		for _, method := range idx.phpIndex.FindMethods(
			occurrence.Class,
			occurrence.Method,
		) {
			if len(method.Parameters) == 0 {
				continue
			}
			eventType := namedEventType(method.Parameters[0].Type)
			if eventType != "" {
				return eventType
			}
		}
	}
	return ""
}

func namedEventType(value types.Type) string {
	if value.IsUnknown() {
		return ""
	}
	if value.Kind() == types.ObjectKind && value.Name() != "" {
		return normalizeName(value.Name())
	}
	for _, part := range value.Arguments() {
		if name := namedEventType(part); name != "" {
			return name
		}
	}
	return ""
}

var _ indexer.Indexer = (*Index)(nil)
var _ indexer.TransactionalRemover = (*Index)(nil)
var _ indexer.TransactionalClearer = (*Index)(nil)
