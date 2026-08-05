package messenger

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

type Index struct {
	records  *indexer.DataIndexer[Occurrence]
	phpIndex *php.PHPIndex

	pathsMu sync.RWMutex
	paths   map[string]struct{}
}

func NewIndex(
	configDir string,
	stores ...*indexer.Store,
) (*Index, error) {
	records, err := indexer.NewRepository[Occurrence](
		filepath.Join(configDir, "messenger.db"),
		"symfony.messenger.occurrences",
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
	pathSet := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		pathSet[path] = struct{}{}
	}
	return &Index{records: records, paths: pathSet}, nil
}

func (idx *Index) SetPHPIndex(index *php.PHPIndex) {
	if idx != nil {
		idx.phpIndex = index
	}
}

func (idx *Index) ID() string {
	return "symfony.messenger"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	if !messengerCandidate(file) && !idx.hasPath(file.Path) {
		return nil
	}
	var occurrences []Occurrence
	switch file.Extension() {
	case ".php":
		occurrences = parsePHP(file, idx.phpIndex)
	case ".yaml", ".yml":
		occurrences = parseYAML(file)
	case ".xml":
		occurrences = parseXML(file)
	default:
		return nil
	}
	items := map[string]map[string]Occurrence{file.Path: {}}
	for _, occurrence := range occurrences {
		occurrence.File = file.Path
		key := occurrenceKey(occurrence)
		if key != "" {
			items[file.Path][key] = occurrence
		}
	}
	if err := idx.records.BatchSaveItemsIn(file.Mutation(), items); err != nil {
		return err
	}
	return idx.publishPath(
		file.Path,
		len(items[file.Path]) != 0,
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
	if err := idx.records.BatchDeleteByFilePathsIn(mutation, paths); err != nil {
		return err
	}
	if mutation == nil {
		idx.removePaths(paths)
		return nil
	}
	return mutation.AfterCommit(func() { idx.removePaths(paths) })
}

func (idx *Index) Clear() error {
	if idx == nil {
		return nil
	}
	if err := idx.records.Clear(); err != nil {
		return err
	}
	idx.resetPaths()
	return nil
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.ClearIn(mutation); err != nil {
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
	return idx.records.Close()
}

func (idx *Index) Messages() ([]Message, error) {
	if idx == nil {
		return nil, nil
	}
	occurrences, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	grouped := make(map[string]*Message)
	for _, occurrence := range occurrences {
		names := []string{occurrence.Message}
		if occurrence.Message == "" &&
			occurrence.Kind == HandlerOccurrence {
			names = idx.inferredHandlerMessages(occurrence)
		}
		for _, name := range names {
			name = normalizeName(name)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			message := grouped[key]
			if message == nil {
				message = &Message{Name: name}
				grouped[key] = message
			}
			current := occurrence
			current.Message = name
			message.Occurrences = append(message.Occurrences, current)
		}
	}
	result := make([]Message, 0, len(grouped))
	for _, message := range grouped {
		message.Occurrences = uniqueOccurrences(message.Occurrences)
		result = append(result, *message)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (idx *Index) GetMessage(name string) (Message, bool, error) {
	messages, err := idx.Messages()
	if err != nil {
		return Message{}, false, err
	}
	name = normalizeName(name)
	for _, message := range messages {
		if strings.EqualFold(message.Name, name) {
			return message, true, nil
		}
	}
	return Message{}, false, nil
}

func (idx *Index) MessageNames() ([]string, error) {
	messages, err := idx.Messages()
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(messages))
	for _, message := range messages {
		result = append(result, message.Name)
	}
	return result, nil
}

func (idx *Index) MessagesForHandler(
	className,
	methodName string,
) ([]Message, error) {
	messages, err := idx.Messages()
	if err != nil {
		return nil, err
	}
	var result []Message
	for _, message := range messages {
		for _, handler := range message.Handlers() {
			if strings.EqualFold(handler.Class, className) &&
				strings.EqualFold(handler.Method, methodName) {
				result = append(result, message)
				break
			}
		}
	}
	return result, nil
}

func (idx *Index) inferredHandlerMessages(
	occurrence Occurrence,
) []string {
	if idx.phpIndex == nil || occurrence.Class == "" {
		return nil
	}
	method := occurrence.Method
	if method == "" {
		method = "__invoke"
	}
	var result []string
	for _, symbol := range idx.phpIndex.FindMethods(
		occurrence.Class,
		method,
	) {
		if len(symbol.Parameters) == 0 {
			continue
		}
		result = append(
			result,
			objectTypeNames(symbol.Parameters[0].Type)...,
		)
		if len(result) == 0 {
			result = append(
				result,
				objectTypeNames(symbol.Parameters[0].NativeType)...,
			)
		}
	}
	return uniqueNames(result)
}

func objectTypeNames(value types.Type) []string {
	switch value.Kind() {
	case types.UnionKind, types.IntersectionKind:
		var result []string
		for _, part := range value.Arguments() {
			result = append(result, objectTypeNames(part)...)
		}
		return result
	case types.ObjectKind:
		if name := normalizeName(value.Name()); name != "" {
			return []string{name}
		}
	}
	return nil
}

func occurrenceKey(occurrence Occurrence) string {
	return fmt.Sprintf(
		"%s:%d:%d:%d:%s:%s:%s",
		occurrence.File,
		occurrence.Kind,
		occurrence.Range.Start,
		occurrence.Range.End,
		strings.ToLower(occurrence.Message),
		strings.ToLower(occurrence.Class),
		strings.ToLower(occurrence.Method),
	)
}

func uniqueOccurrences(values []Occurrence) []Occurrence {
	seen := make(map[string]struct{}, len(values))
	result := make([]Occurrence, 0, len(values))
	for _, value := range values {
		key := occurrenceKey(value)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].File == result[right].File {
			return result[left].Range.Start < result[right].Range.Start
		}
		return result[left].File < result[right].File
	})
	return result
}

func uniqueNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var result []string
	for _, value := range values {
		value = normalizeName(value)
		key := strings.ToLower(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result
}

func normalizeName(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), `\`)
}

func messengerCandidate(file *indexer.ParsedFile) bool {
	content := file.Source
	switch file.Extension() {
	case ".php":
		return strings.Contains(content, "AsMessageHandler") ||
			strings.Contains(content, "MessageSubscriberInterface") ||
			strings.Contains(content, "getHandledMessages") ||
			strings.Contains(content, "messenger.message_handler") ||
			strings.Contains(content, "->dispatch")
	case ".yaml", ".yml", ".xml":
		return strings.Contains(content, "messenger.message_handler")
	default:
		return false
	}
}

func (idx *Index) hasPath(path string) bool {
	idx.pathsMu.RLock()
	defer idx.pathsMu.RUnlock()
	_, exists := idx.paths[path]
	return exists
}

func (idx *Index) publishPath(
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

func (idx *Index) removePaths(paths []string) {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	for _, path := range paths {
		delete(idx.paths, path)
	}
}

func (idx *Index) resetPaths() {
	idx.pathsMu.Lock()
	defer idx.pathsMu.Unlock()
	idx.paths = make(map[string]struct{})
}
