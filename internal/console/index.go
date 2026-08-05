package console

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type InputKind uint8

const (
	Argument InputKind = iota
	Option
)

type Input struct {
	Name        string
	Kind        InputKind
	Shortcut    string
	Mode        string
	Description string
	Default     string
	File        string
	Range       cst.TextRange
}

type Command struct {
	Name        string
	Canonical   string
	Description string
	Class       string
	Method      string
	File        string
	Range       cst.TextRange
	Arguments   []Input
	Options     []Input
}

type Index struct {
	commands *indexer.DataIndexer[Command]

	pathsMu sync.RWMutex
	paths   map[string]struct{}
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	commands, err := indexer.NewRepository[Command](
		filepath.Join(configDir, "console.db"),
		"symfony.console.commands",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	paths, err := commands.GetAllFilePaths()
	if err != nil {
		_ = commands.Close()
		return nil, err
	}
	pathSet := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		pathSet[path] = struct{}{}
	}
	return &Index{commands: commands, paths: pathSet}, nil
}

func (idx *Index) ID() string {
	return "symfony.console"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	var commands []Command
	switch file.Extension() {
	case ".php":
		if !isPHPCommandCandidate(file.Content) {
			if !idx.hasPath(file.Path) {
				return nil
			}
		} else {
			commands = parsePHPCommands(file)
		}
	case ".xml":
		if !strings.Contains(file.Source, "console.command") {
			if !idx.hasPath(file.Path) {
				return nil
			}
		} else {
			commands = parseXMLCommands(file)
		}
	default:
		return nil
	}

	write := map[string]map[string]Command{file.Path: {}}
	for _, command := range commands {
		if command.Name != "" {
			write[file.Path][command.Name] = command
		}
	}
	if err := idx.commands.BatchSaveItemsIn(file.Mutation(), write); err != nil {
		return err
	}
	addCommandWorkspaceSymbols(file, commands)
	return idx.publishPath(
		file.Path,
		len(write[file.Path]) != 0,
		file.Mutation(),
	)
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	if err := idx.commands.BatchDeleteByFilePaths(paths); err != nil {
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
	if err := idx.commands.BatchDeleteByFilePathsIn(
		mutation,
		paths,
	); err != nil {
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
	if err := idx.commands.Clear(); err != nil {
		return err
	}
	idx.resetPaths()
	return nil
}

func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	if idx == nil {
		return nil
	}
	if err := idx.commands.ClearIn(mutation); err != nil {
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
	return idx.commands.Close()
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
	clear(idx.paths)
}

func (idx *Index) GetCommand(name string) ([]Command, error) {
	if idx == nil || name == "" {
		return nil, nil
	}
	return idx.commands.GetValues(name)
}

func (idx *Index) GetCommands() ([]Command, error) {
	if idx == nil {
		return nil, nil
	}
	commands, err := idx.commands.GetAllValues()
	if err != nil {
		return nil, err
	}
	sort.Slice(commands, func(left, right int) bool {
		if commands[left].Name == commands[right].Name {
			return commands[left].File < commands[right].File
		}
		return commands[left].Name < commands[right].Name
	})
	return commands, nil
}

func (idx *Index) InputsForTarget(
	class,
	method string,
	kind InputKind,
) ([]Input, error) {
	if idx == nil || class == "" {
		return nil, nil
	}
	commands, err := idx.commands.GetAllValues()
	if err != nil {
		return nil, err
	}
	unique := make(map[string]Input)
	for _, command := range commands {
		if !strings.EqualFold(command.Class, strings.TrimPrefix(class, `\`)) ||
			command.Method != "" && !strings.EqualFold(command.Method, method) {
			continue
		}
		inputs := command.Arguments
		if kind == Option {
			inputs = command.Options
		}
		for _, input := range inputs {
			unique[input.Name] = input
		}
	}
	result := make([]Input, 0, len(unique))
	for _, input := range unique {
		result = append(result, input)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result, nil
}

var _ indexer.Indexer = (*Index)(nil)
var _ indexer.TransactionalRemover = (*Index)(nil)
var _ indexer.TransactionalClearer = (*Index)(nil)
