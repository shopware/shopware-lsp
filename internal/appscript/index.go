// Package appscript indexes Shopware script hooks and facade availability.
package appscript

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

var hookNamePattern = regexp.MustCompile(`(?m)\bHOOK_NAME\s*=\s*['"]([^'"]+)['"]`)

type Hook struct {
	Name             string
	Class            string
	Parent           string
	File             string
	Line             int
	ServiceFactories []string
}

type Facade struct {
	Name    string
	Class   string
	File    string
	Line    int
	Default bool
}

type Index struct {
	hooks   *indexer.DataIndexer[Hook]
	facades *indexer.DataIndexer[Facade]
}

func NewIndex(configDir string, stores ...*indexer.Store) (*Index, error) {
	hooks, err := indexer.NewRepository[Hook](
		filepath.Join(configDir, "app_script_hooks.db"),
		"shopware.app_script.hooks",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	facades, err := indexer.NewRepository[Facade](
		filepath.Join(configDir, "app_script_facades.db"),
		"shopware.app_script.facades",
		stores...,
	)
	if err != nil {
		_ = hooks.Close()
		return nil, err
	}
	return &Index{hooks: hooks, facades: facades}, nil
}

func (idx *Index) ID() string { return "shopware.app_script" }

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil || file.Extension() != ".php" {
		return nil
	}
	hookWrite := map[string]map[string]Hook{file.Path: {}}
	facadeWrite := map[string]map[string]Facade{file.Path: {}}
	if !strings.Contains(file.Source, "Hook") &&
		!strings.Contains(file.Source, "Facade") {
		if err := idx.hooks.BatchSaveItemsIn(file.Mutation(), hookWrite); err != nil {
			return err
		}
		return idx.facades.BatchSaveItemsIn(file.Mutation(), facadeWrite)
	}
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	for _, class := range phpquery.Classes(tree.Root) {
		className := phpquery.ClassName(class)
		if className == "" {
			continue
		}
		line, _ := file.LineIndex().Position(class.RangeTrimmedTrivia().Start)
		if strings.HasSuffix(className, "HookFactory") ||
			strings.HasSuffix(className, "FacadeFactory") {
			if name := methodLiteral(class, "getName"); name != "" {
				facadeWrite[file.Path][className] = Facade{
					Name: name, Class: className, File: file.Path,
					Line:    int(line) + 1,
					Default: className == "AclFacadeHookFactory",
				}
			}
		}
		if !strings.HasSuffix(className, "Hook") {
			continue
		}
		hook := Hook{
			Class: className, File: file.Path, Line: int(line) + 1,
		}
		parents := phpquery.ClassExtends(class)
		if len(parents) > 0 {
			hook.Parent = shortClass(parents[0])
		}
		hook.Name = methodLiteral(class, "getName")
		if hook.Name == "" {
			match := hookNamePattern.FindStringSubmatch(class.Text())
			if len(match) > 1 {
				hook.Name = match[1]
			}
		}
		for _, method := range phpquery.Methods(class) {
			if phpquery.MethodName(method) != "getServiceIds" {
				continue
			}
			seen := make(map[string]struct{})
			for _, access := range phpquery.Nodes(
				method,
				phpsyntax.PhpScopedAccess,
				phpsyntax.PhpMemberAccess,
			) {
				factory := shortClass(phpquery.ClassConstantName(access))
				if factory == "" {
					continue
				}
				if _, duplicate := seen[factory]; duplicate {
					continue
				}
				seen[factory] = struct{}{}
				hook.ServiceFactories = append(hook.ServiceFactories, factory)
			}
		}
		hookWrite[file.Path][className] = hook
	}
	if err := idx.hooks.BatchSaveItemsIn(file.Mutation(), hookWrite); err != nil {
		return err
	}
	return idx.facades.BatchSaveItemsIn(file.Mutation(), facadeWrite)
}

func methodLiteral(class *phpsyntax.Node, methodName string) string {
	for _, method := range phpquery.Methods(class) {
		if phpquery.MethodName(method) != methodName {
			continue
		}
		stringsInMethod := phpquery.Nodes(method, phpsyntax.PhpString)
		if len(stringsInMethod) > 0 {
			return phpquery.StringValue(stringsInMethod[0])
		}
	}
	return ""
}

func shortClass(name string) string {
	name = strings.TrimSpace(strings.TrimPrefix(name, `\`))
	if separator := strings.LastIndex(name, `\`); separator >= 0 {
		return name[separator+1:]
	}
	return name
}

// ServicesForHook resolves inherited facade factories and executor defaults.
// found is false for unknown hooks so diagnostics can avoid false positives.
func (idx *Index) ServicesForHook(name string) (services map[string]Facade, found bool, err error) {
	hooks, err := idx.hooks.GetAllValues()
	if err != nil {
		return nil, false, err
	}
	facades, err := idx.facades.GetAllValues()
	if err != nil {
		return nil, false, err
	}
	facadeByClass := make(map[string]Facade, len(facades))
	services = make(map[string]Facade)
	for _, facade := range facades {
		facadeByClass[shortClass(facade.Class)] = facade
		if facade.Default {
			services[facade.Name] = facade
		}
	}
	hookByClass := make(map[string]Hook, len(hooks))
	var matched []Hook
	for _, hook := range hooks {
		hookByClass[shortClass(hook.Class)] = hook
		if hook.Name == name {
			matched = append(matched, hook)
		}
	}
	if len(matched) == 0 {
		return services, false, nil
	}
	found = true
	visited := make(map[string]struct{})
	var addHook func(Hook)
	addHook = func(hook Hook) {
		class := shortClass(hook.Class)
		if _, duplicate := visited[class]; duplicate {
			return
		}
		visited[class] = struct{}{}
		for _, factory := range hook.ServiceFactories {
			if facade, exists := facadeByClass[shortClass(factory)]; exists {
				services[facade.Name] = facade
			}
		}
		if parent, exists := hookByClass[shortClass(hook.Parent)]; exists {
			addHook(parent)
		}
	}
	for _, hook := range matched {
		addHook(hook)
	}
	return services, found, nil
}

func (idx *Index) Hooks() ([]Hook, error)     { return idx.hooks.GetAllValues() }
func (idx *Index) Facades() ([]Facade, error) { return idx.facades.GetAllValues() }

func (idx *Index) RemovedFiles(paths []string) error {
	return errors.Join(
		idx.hooks.BatchDeleteByFilePaths(paths),
		idx.facades.BatchDeleteByFilePaths(paths),
	)
}

func (idx *Index) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	return errors.Join(
		idx.hooks.BatchDeleteByFilePathsIn(mutation, paths),
		idx.facades.BatchDeleteByFilePathsIn(mutation, paths),
	)
}

func (idx *Index) Clear() error { return errors.Join(idx.hooks.Clear(), idx.facades.Clear()) }
func (idx *Index) ClearIn(mutation *indexer.Mutation) error {
	return errors.Join(idx.hooks.ClearIn(mutation), idx.facades.ClearIn(mutation))
}
func (idx *Index) Close() error { return errors.Join(idx.hooks.Close(), idx.facades.Close()) }
