package translation

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

type Index struct {
	messages *indexer.DataIndexer[Message]
}

func NewIndex(
	configDir string,
	stores ...*indexer.Store,
) (*Index, error) {
	messages, err := indexer.NewRepository[Message](
		filepath.Join(configDir, "translations.db"),
		"symfony.translations",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	return &Index{messages: messages}, nil
}

func (idx *Index) ID() string {
	return "translation.indexer"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	metadata, ok := catalogueMetadata(file.Path)
	if !ok {
		return nil
	}

	var messages []Message
	switch {
	case metadata.compiled:
		messages = parseCompiledPHPCatalogue(file, metadata)
	case file.Extension() == ".php":
		messages = parsePHPResource(file, metadata)
	case file.Extension() == ".yaml" || file.Extension() == ".yml":
		messages = parseYAMLResource(file, metadata)
	case file.Extension() == ".xml" ||
		file.Extension() == ".xlf" ||
		file.Extension() == ".xliff":
		messages = parseXMLResource(file, metadata)
	}

	items := map[string]map[string]Message{
		file.Path: {},
	}
	for _, message := range messages {
		if message.Domain == "" || message.Key == "" {
			continue
		}
		items[file.Path][storageKey(message.Domain, message.Key)] = message
	}
	if err := idx.messages.BatchSaveItemsIn(file.Mutation(), items); err != nil {
		return err
	}
	addMessageWorkspaceSymbols(file, messages)
	return nil
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil || len(paths) == 0 {
		return nil
	}
	return idx.messages.BatchDeleteByFilePaths(paths)
}

func (idx *Index) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil || len(paths) == 0 {
		return nil
	}
	return idx.messages.BatchDeleteByFilePathsIn(mutation, paths)
}

func (idx *Index) Close() error {
	if idx == nil {
		return nil
	}
	return idx.messages.Close()
}

func (idx *Index) Clear() error {
	if idx == nil {
		return nil
	}
	return idx.messages.Clear()
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	return idx.messages.ClearIn(mutation)
}

func (idx *Index) GetMessages(domain, key string) ([]Message, error) {
	if idx == nil || domain == "" || key == "" {
		return nil, nil
	}
	messages, err := idx.messages.GetValues(storageKey(domain, key))
	if err != nil {
		return nil, err
	}
	sortMessages(messages)
	return messages, nil
}

func (idx *Index) HasMessage(domain, key string) (bool, error) {
	messages, err := idx.GetMessages(domain, key)
	return len(messages) != 0, err
}

func (idx *Index) GetDomains() ([]string, error) {
	if idx == nil {
		return nil, nil
	}
	values, err := idx.messages.GetAllValuesView()
	if err != nil {
		return nil, err
	}
	unique := make(map[string]string)
	for _, message := range values {
		if message.Domain == "" {
			continue
		}
		key := strings.ToLower(message.Domain)
		if _, exists := unique[key]; !exists {
			unique[key] = message.Domain
		}
	}
	domains := make([]string, 0, len(unique))
	for _, domain := range unique {
		domains = append(domains, domain)
	}
	sort.Slice(domains, func(left, right int) bool {
		return strings.ToLower(domains[left]) < strings.ToLower(domains[right])
	})
	return domains, nil
}

func (idx *Index) GetKeys(domain string) ([]string, error) {
	if idx == nil || domain == "" {
		return nil, nil
	}
	keys, err := idx.messages.GetAllKeys()
	if err != nil {
		return nil, err
	}
	domain = strings.ToLower(normalizeDomain(domain))
	var result []string
	for _, value := range keys {
		storedDomain, key, ok := splitStorageKey(value)
		if ok && storedDomain == domain {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result, nil
}

func (idx *Index) GetDomainMessages(domain string) ([]Message, error) {
	if idx == nil || domain == "" {
		return nil, nil
	}
	values, err := idx.messages.GetAllValuesView()
	if err != nil {
		return nil, err
	}
	var result []Message
	for _, message := range values {
		if strings.EqualFold(message.Domain, normalizeDomain(domain)) {
			result = append(result, message)
		}
	}
	sortMessages(result)
	return result, nil
}

func (idx *Index) Messages() ([]Message, error) {
	if idx == nil {
		return nil, nil
	}
	values, err := idx.messages.GetAllValues()
	if err != nil {
		return nil, err
	}
	sort.Slice(values, func(left, right int) bool {
		if values[left].Key != values[right].Key {
			return values[left].Key < values[right].Key
		}
		if values[left].Domain != values[right].Domain {
			return values[left].Domain < values[right].Domain
		}
		if values[left].Locale != values[right].Locale {
			return values[left].Locale < values[right].Locale
		}
		if values[left].File != values[right].File {
			return values[left].File < values[right].File
		}
		return values[left].Line < values[right].Line
	})
	return values, nil
}

var _ indexer.Indexer = (*Index)(nil)
var _ indexer.TransactionalRemover = (*Index)(nil)
var _ indexer.TransactionalClearer = (*Index)(nil)
