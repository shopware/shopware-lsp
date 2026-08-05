package twigcomponent

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
)

const (
	modernServiceTag = "ux.twig_component"
	legacyServiceTag = "twig.component"
)

type Index struct {
	records *indexer.DataIndexer[Record]

	pathsMu sync.RWMutex
	paths   map[string]struct{}

	dependenciesMu sync.RWMutex
	phpIndex       *php.PHPIndex
	serviceIndex   *symfony.ServiceIndex
	twigIndex      *twig.TwigIndexer

	catalogMu    sync.RWMutex
	catalogValid bool
	catalogEpoch uint64
	compiledRev  uint64
	catalog      []Component
	propsCache   map[string][]Prop
}

func NewIndex(
	configDir string,
	stores ...*indexer.Store,
) (*Index, error) {
	records, err := indexer.NewRepository[Record](
		filepath.Join(configDir, "twig_components.db"),
		"symfony.twig_components.records",
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
	return &Index{
		records: records,
		paths:   pathSet,
	}, nil
}

func (idx *Index) SetDependencies(
	phpIndex *php.PHPIndex,
	serviceIndex *symfony.ServiceIndex,
	twigIndex *twig.TwigIndexer,
) {
	if idx == nil {
		return
	}
	idx.dependenciesMu.Lock()
	idx.phpIndex = phpIndex
	idx.serviceIndex = serviceIndex
	idx.twigIndex = twigIndex
	idx.dependenciesMu.Unlock()
	idx.invalidateCatalog()
}

func (idx *Index) ID() string {
	return "symfony.twig_components"
}

func (idx *Index) Index(file *indexer.ParsedFile) error {
	if idx == nil || file == nil {
		return nil
	}
	record := Record{File: file.Path}
	candidate := false
	switch file.Extension() {
	case ".php":
		candidate = bytes.Contains(file.Content, []byte("AsTwigComponent")) ||
			bytes.Contains(file.Content, []byte("AsLiveComponent")) ||
			bytes.Contains(file.Content, []byte("LiveAction")) ||
			bytes.Contains(file.Content, []byte("LiveArg")) ||
			bytes.Contains(file.Content, []byte("LiveListener")) ||
			bytes.Contains(file.Content, []byte("->emit")) ||
			bytes.Contains(file.Content, []byte("twig_component"))
		if candidate {
			tree := file.SyntaxTree()
			if tree != nil {
				record.Declarations = declarationsInPHP(
					file.Path,
					tree.Root,
				)
				record.Props = propsInPHP(file.Path, tree.Root)
				record.LiveActions = liveActionsInPHP(
					file.Path,
					tree.Root,
				)
				record.LiveListeners = liveListenersInPHP(
					file.Path,
					tree.Root,
				)
				record.LiveEventReferences,
					record.LiveEventArguments =
					liveEventReferencesInPHP(
						file.Path,
						tree.Root,
					)
				record.Namespaces,
					record.AnonymousDirectories = configurationInPHP(
					file.Path,
					tree.Root,
				)
			}
		}
	case ".yaml", ".yml":
		candidate = bytes.Contains(file.Content, []byte("twig_component"))
		if candidate {
			tree := file.SyntaxTree()
			if tree != nil {
				record.Namespaces,
					record.AnonymousDirectories = configurationInYAML(
					file.Path,
					tree.Root,
				)
			}
		}
	case ".twig":
		candidate = bytes.Contains(file.Content, []byte("component")) ||
			bytes.Contains(file.Content, []byte("<twig:")) ||
			bytes.Contains(file.Content, []byte("live_action")) ||
			bytes.Contains(
				file.Content,
				[]byte("data-live-action-param"),
			) ||
			bytes.Contains(
				file.Content,
				[]byte("data-live-event-param"),
			) ||
			bytes.Contains(file.Content, []byte(" props ")) ||
			bytes.Contains(file.Content, []byte("{% props")) ||
			bytes.Contains(file.Content, []byte("{%- props")) ||
			bytes.Contains(file.Content, []byte("@prop"))
		if candidate {
			tree := file.SyntaxTree()
			if tree != nil {
				record.Usages = usagesInTwig(file.Path, tree.Root)
				record.Props = propsInTwig(file.Path, tree.Root)
				record.LiveActionReferences =
					liveActionReferencesInTwig(file.Path, tree.Root)
				record.LiveActionArguments =
					LiveActionArgumentReferencesInTwig(
						file.Path,
						tree.Root,
					)
				record.LiveEventReferences,
					record.LiveEventArguments =
					liveEventReferencesInTwig(
						file.Path,
						tree.Root,
					)
			}
		}
	default:
		return nil
	}

	present := len(record.Declarations) != 0 ||
		len(record.Namespaces) != 0 ||
		len(record.AnonymousDirectories) != 0 ||
		len(record.Props) != 0 ||
		len(record.Usages) != 0 ||
		len(record.LiveActions) != 0 ||
		len(record.LiveActionReferences) != 0 ||
		len(record.LiveActionArguments) != 0 ||
		len(record.LiveListeners) != 0 ||
		len(record.LiveEventReferences) != 0 ||
		len(record.LiveEventArguments) != 0
	if !candidate && !idx.hasIndexedPath(file.Path) {
		return idx.invalidateAfter(file.Mutation())
	}
	write := map[string]map[string]Record{file.Path: {}}
	if present {
		write[file.Path]["components"] = record
	}
	if err := idx.records.BatchSaveItemsIn(
		file.Mutation(),
		write,
	); err != nil {
		return err
	}
	if err := idx.publishIndexedPath(
		file.Path,
		present,
		file.Mutation(),
	); err != nil {
		return err
	}
	addComponentWorkspaceSymbols(file, record)
	return idx.invalidateAfter(file.Mutation())
}

func (idx *Index) Components() ([]Component, error) {
	if idx == nil {
		return nil, nil
	}
	compiled, compiledRev := idx.compiledComponents()
	idx.catalogMu.RLock()
	if idx.catalogValid && idx.compiledRev == compiledRev {
		result := append([]Component(nil), idx.catalog...)
		idx.catalogMu.RUnlock()
		return result, nil
	}
	idx.catalogMu.RUnlock()

	idx.catalogMu.Lock()
	if idx.catalogValid && idx.compiledRev != compiledRev {
		idx.catalogValid = false
		idx.catalogEpoch++
		idx.catalog = nil
		clear(idx.propsCache)
	}
	epoch := idx.catalogEpoch
	idx.catalogMu.Unlock()

	result, err := idx.buildComponents(compiled)
	if err != nil {
		return nil, err
	}
	idx.catalogMu.Lock()
	if idx.catalogEpoch == epoch && !idx.catalogValid {
		idx.catalog = append([]Component(nil), result...)
		idx.catalogValid = true
		idx.compiledRev = compiledRev
	}
	if idx.catalogValid && idx.compiledRev == compiledRev {
		result = append([]Component(nil), idx.catalog...)
	}
	idx.catalogMu.Unlock()
	return result, nil
}

func (idx *Index) buildComponents(
	compiled []Declaration,
) ([]Component, error) {
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	namespaces := configuredNamespaces(records)
	var result []Component
	for _, record := range records {
		for _, declaration := range record.Declarations {
			if component, ok := resolveDeclaration(
				declaration,
				namespaces,
			); ok {
				result = append(result, component)
			}
		}
	}
	services, err := idx.serviceDeclarations()
	if err != nil {
		return nil, err
	}
	for _, declaration := range services {
		if component, ok := resolveDeclaration(
			declaration,
			namespaces,
		); ok {
			result = append(result, component)
		}
	}
	for _, declaration := range compiled {
		if component, ok := resolveDeclaration(
			declaration,
			namespaces,
		); ok {
			result = mergeCompiledComponent(result, component)
		}
	}
	anonymous, err := idx.anonymousComponents(
		anonymousDirectories(records),
	)
	if err != nil {
		return nil, err
	}
	result = append(result, anonymous...)
	result = uniqueComponents(result)
	sortComponents(result)
	return result, nil
}

func (idx *Index) Find(name string) ([]Component, error) {
	components, err := idx.Components()
	if err != nil {
		return nil, err
	}
	var result []Component
	for _, component := range components {
		if component.Name == name {
			result = append(result, component)
		}
	}
	return result, nil
}

func (idx *Index) ResolveDeclaration(
	declaration Declaration,
) (Component, bool, error) {
	if idx == nil {
		return Component{}, false, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return Component{}, false, err
	}
	component, found := resolveDeclaration(
		declaration,
		configuredNamespaces(records),
	)
	return component, found, nil
}

func (idx *Index) Names() ([]string, error) {
	components, err := idx.Components()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(components))
	var result []string
	for _, component := range components {
		if component.Name == "" {
			continue
		}
		if _, exists := seen[component.Name]; exists {
			continue
		}
		seen[component.Name] = struct{}{}
		result = append(result, component.Name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result, nil
}

func (idx *Index) Usages(name string) ([]Usage, error) {
	if idx == nil || name == "" {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []Usage
	for _, record := range records {
		for _, usage := range record.Usages {
			if usage.Name == name {
				result = append(result, usage)
			}
		}
	}
	sortUsages(result)
	return result, nil
}

func (idx *Index) TemplateFiles(
	component Component,
) ([]string, error) {
	if component.Template == "" {
		return nil, nil
	}
	_, _, twigIndex := idx.dependencies()
	if twigIndex == nil {
		return nil, nil
	}
	files, err := twigIndex.GetTwigFilesByRelPath(component.Template)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(files))
	var result []string
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		if _, exists := seen[file.Path]; exists {
			continue
		}
		seen[file.Path] = struct{}{}
		result = append(result, file.Path)
	}
	sort.Strings(result)
	return result, nil
}

func (idx *Index) Blocks(name string) ([]Block, error) {
	components, err := idx.Find(name)
	if err != nil {
		return nil, err
	}
	_, _, twigIndex := idx.dependencies()
	if twigIndex == nil {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var result []Block
	for _, component := range components {
		if component.Template == "" {
			continue
		}
		files, fileErr := twigIndex.GetTwigFilesByRelPath(
			component.Template,
		)
		if fileErr != nil {
			return nil, fileErr
		}
		for _, file := range files {
			for _, block := range file.Blocks {
				key := block.Name + "\x00" + file.Path
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				result = append(result, Block{
					Name: block.Name,
					File: file.Path,
					Line: block.Line,
				})
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].File < result[right].File
	})
	return result, nil
}

func (idx *Index) Props(name string) ([]Prop, error) {
	idx.catalogMu.RLock()
	epoch := idx.catalogEpoch
	if cached, exists := idx.propsCache[name]; exists {
		result := append([]Prop(nil), cached...)
		idx.catalogMu.RUnlock()
		return result, nil
	}
	idx.catalogMu.RUnlock()

	components, err := idx.Find(name)
	if err != nil {
		return nil, err
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	templatePaths := make(map[string]struct{})
	componentClasses := make(map[string]struct{})
	for _, component := range components {
		if component.Class != "" {
			componentClasses[strings.ToLower(
				normalizeClass(component.Class),
			)] = struct{}{}
		}
		paths, pathErr := idx.TemplateFiles(component)
		if pathErr != nil {
			return nil, pathErr
		}
		for _, path := range paths {
			templatePaths[path] = struct{}{}
		}
	}
	var result []Prop
	for _, record := range records {
		if _, relevant := templatePaths[record.File]; relevant {
			for _, prop := range record.Props {
				if prop.Class == "" {
					result = append(result, prop)
				}
			}
		}
		for _, prop := range record.Props {
			if prop.Class == "" {
				continue
			}
			if _, relevant := componentClasses[strings.ToLower(
				normalizeClass(prop.Class),
			)]; relevant {
				result = append(result, prop)
			}
		}
	}
	semanticProps := idx.phpProps(components)
	for semanticIndex := range semanticProps {
		for _, indexed := range result {
			if indexed.Class == "" || indexed.Member == "" ||
				!strings.EqualFold(
					normalizeClass(indexed.Class),
					normalizeClass(semanticProps[semanticIndex].Class),
				) ||
				!strings.EqualFold(
					indexed.Member,
					semanticProps[semanticIndex].Member,
				) {
				continue
			}
			semanticProps[semanticIndex].Name = indexed.Name
			semanticProps[semanticIndex].Live =
				semanticProps[semanticIndex].Live || indexed.Live
			semanticProps[semanticIndex].Writable =
				semanticProps[semanticIndex].Writable ||
					indexed.Writable
			break
		}
	}
	result = append(semanticProps, result...)
	result = mergeProps(result)
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	idx.catalogMu.Lock()
	if idx.catalogEpoch == epoch {
		if idx.propsCache == nil {
			idx.propsCache = make(map[string][]Prop)
		}
		idx.propsCache[name] = append([]Prop(nil), result...)
		result = append([]Prop(nil), idx.propsCache[name]...)
	}
	idx.catalogMu.Unlock()
	return result, nil
}

// Computed returns zero-argument public component getters exposed through
// Twig's cached `computed.*` proxy.
func (idx *Index) Computed(name string) ([]Prop, error) {
	components, err := idx.Find(name)
	if err != nil {
		return nil, err
	}
	phpIndex, _, _ := idx.dependencies()
	if phpIndex == nil {
		return nil, nil
	}
	seenClasses := make(map[string]struct{})
	var result []Prop
	for _, component := range components {
		class := normalizeClass(component.Class)
		if class == "" {
			continue
		}
		key := strings.ToLower(class)
		if _, exists := seenClasses[key]; exists {
			continue
		}
		seenClasses[key] = struct{}{}
		for _, method := range phpIndex.Methods(class) {
			computedName, found := computedMethodName(method.Name)
			if !found ||
				method.Visibility != semantic.Public ||
				method.Flags.Has(semantic.StaticFlag) ||
				len(method.Parameters) != 0 {
				continue
			}
			result = append(result, Prop{
				Name:  computedName,
				Type:  method.ReturnType.String(),
				File:  method.Path,
				Class: class,
				Range: method.SelectionRange,
			})
		}
	}
	result = mergeProps(result)
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (idx *Index) ComputedForTemplate(path string) ([]Prop, error) {
	components, err := idx.ComponentsForTemplate(path)
	if err != nil {
		return nil, err
	}
	var result []Prop
	for _, component := range components {
		current, currentErr := idx.Computed(component.Name)
		if currentErr != nil {
			return nil, currentErr
		}
		result = append(result, current...)
	}
	result = mergeProps(result)
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

// LiveActions returns every public, non-static #[LiveAction] method visible
// on the named Live Component, including inherited and trait methods.
func (idx *Index) LiveActions(name string) ([]LiveAction, error) {
	components, err := idx.Find(name)
	if err != nil {
		return nil, err
	}
	phpIndex, _, _ := idx.dependencies()
	if phpIndex == nil {
		return nil, nil
	}
	snapshot := phpIndex.SemanticSnapshot()
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	indexed := make(map[string]LiveAction)
	for _, record := range records {
		for _, action := range record.LiveActions {
			indexed[liveActionDeclarationKey(
				action.File,
				action.Range,
			)] = action
		}
	}
	seen := make(map[string]struct{})
	var result []LiveAction
	for _, component := range components {
		if !component.Live || component.Class == "" {
			continue
		}
		class := normalizeClass(component.Class)
		for _, method := range phpIndex.Methods(class) {
			if method.Visibility != semantic.Public ||
				method.Flags.Has(semantic.StaticFlag) ||
				!hasLiveAction(method) {
				continue
			}
			key := strings.ToLower(method.Path) + "\x00" +
				method.SelectionRange.String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			parameters := make(
				[]LiveActionParameter,
				0,
				len(method.Parameters),
			)
			for _, parameter := range method.Parameters {
				parameterRange := parameter.Range
				if parameterSymbol, found := snapshot.Symbol(
					parameter.ID,
				); found {
					parameterRange = parameterSymbol.SelectionRange
				}
				parameters = append(parameters, LiveActionParameter{
					Name: strings.TrimPrefix(parameter.Name, "$"),
					PHPName: strings.TrimPrefix(
						parameter.Name,
						"$",
					),
					Type: parameter.Type.String(),
					Optional: parameter.Optional ||
						parameter.Flags.Has(semantic.VariadicFlag),
					Range: parameterRange,
				})
			}
			if parsed, exists := indexed[liveActionDeclarationKey(
				method.Path,
				method.SelectionRange,
			)]; exists {
				for index := range parameters {
					for _, parsedParameter := range parsed.Parameters {
						if !strings.EqualFold(
							parsedParameter.PHPName,
							parameters[index].PHPName,
						) {
							continue
						}
						parameters[index].Name = parsedParameter.Name
						parameters[index].LiveArg = parsedParameter.LiveArg
						break
					}
				}
			}
			result = append(result, LiveAction{
				Name:       method.Name,
				Class:      class,
				Method:     method.Name,
				File:       method.Path,
				Range:      method.SelectionRange,
				Parameters: parameters,
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		leftName := strings.ToLower(result[left].Name)
		rightName := strings.ToLower(result[right].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

func liveActionDeclarationKey(
	path string,
	rng cst.TextRange,
) string {
	return strings.ToLower(path) + "\x00" + rng.String()
}

func (idx *Index) LiveActionsForTemplate(path string) ([]LiveAction, error) {
	components, err := idx.ComponentsForTemplate(path)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var result []LiveAction
	for _, component := range components {
		actions, actionErr := idx.LiveActions(component.Name)
		if actionErr != nil {
			return nil, actionErr
		}
		for _, action := range actions {
			key := strings.ToLower(action.File) + "\x00" +
				action.Range.String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, action)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		leftName := strings.ToLower(result[left].Name)
		rightName := strings.ToLower(result[right].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return result[left].File < result[right].File
	})
	return result, nil
}

func (idx *Index) LiveActionReferences(
	componentName,
	actionName string,
) ([]LiveActionReference, error) {
	components, err := idx.Find(componentName)
	if err != nil {
		return nil, err
	}
	templatePaths := make(map[string]struct{})
	for _, component := range components {
		if component.Source == AnonymousTemplateSource &&
			component.File != "" {
			templatePaths[component.File] = struct{}{}
		}
		paths, pathErr := idx.TemplateFiles(component)
		if pathErr != nil {
			return nil, pathErr
		}
		for _, path := range paths {
			templatePaths[path] = struct{}{}
		}
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []LiveActionReference
	for _, record := range records {
		if _, matches := templatePaths[record.File]; !matches {
			continue
		}
		for _, reference := range record.LiveActionReferences {
			if strings.EqualFold(reference.Name, actionName) {
				result = append(result, reference)
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

// LiveListeners returns public, non-static #[LiveListener] methods visible on
// indexed Live Components. Repeatable attributes produce one entry per event.
func (idx *Index) LiveListeners() ([]LiveListener, error) {
	components, err := idx.Components()
	if err != nil {
		return nil, err
	}
	phpIndex, _, _ := idx.dependencies()
	if phpIndex == nil {
		return nil, nil
	}
	snapshot := phpIndex.SemanticSnapshot()
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	indexed := make(map[string][]LiveListener)
	for _, record := range records {
		for _, listener := range record.LiveListeners {
			key := liveActionDeclarationKey(
				listener.File,
				listener.MethodRange,
			)
			indexed[key] = append(indexed[key], listener)
		}
	}
	seen := make(map[string]struct{})
	var result []LiveListener
	for _, component := range components {
		if !component.Live || component.Class == "" {
			continue
		}
		class := normalizeClass(component.Class)
		for _, method := range phpIndex.Methods(class) {
			if method.Visibility != semantic.Public ||
				method.Flags.Has(semantic.StaticFlag) ||
				!hasLiveListener(method) {
				continue
			}
			parsed := indexed[liveActionDeclarationKey(
				method.Path,
				method.SelectionRange,
			)]
			if len(parsed) == 0 {
				continue
			}
			parameters := make(
				[]LiveActionParameter,
				0,
				len(method.Parameters),
			)
			for _, parameter := range method.Parameters {
				parameterRange := parameter.Range
				if parameterSymbol, found := snapshot.Symbol(
					parameter.ID,
				); found {
					parameterRange = parameterSymbol.SelectionRange
				}
				parameters = append(parameters, LiveActionParameter{
					Name: strings.TrimPrefix(parameter.Name, "$"),
					PHPName: strings.TrimPrefix(
						parameter.Name,
						"$",
					),
					Type: parameter.Type.String(),
					Optional: parameter.Optional ||
						parameter.Flags.Has(semantic.VariadicFlag),
					Range: parameterRange,
				})
			}
			for _, declaration := range parsed {
				currentParameters := append(
					[]LiveActionParameter(nil),
					parameters...,
				)
				for parameterIndex := range currentParameters {
					for _, parsedParameter := range declaration.Parameters {
						if !strings.EqualFold(
							parsedParameter.PHPName,
							currentParameters[parameterIndex].PHPName,
						) {
							continue
						}
						currentParameters[parameterIndex].Name =
							parsedParameter.Name
						currentParameters[parameterIndex].LiveArg =
							parsedParameter.LiveArg
						break
					}
				}
				key := strings.ToLower(declaration.Name) + "\x00" +
					strings.ToLower(method.Path) + "\x00" +
					declaration.Range.String()
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				result = append(result, LiveListener{
					Name:        declaration.Name,
					Class:       declaration.Class,
					Method:      method.Name,
					File:        method.Path,
					Range:       declaration.Range,
					MethodRange: method.SelectionRange,
					Parameters:  currentParameters,
				})
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		leftName := strings.ToLower(result[left].Name)
		rightName := strings.ToLower(result[right].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

func (idx *Index) LiveEventReferences(
	eventName string,
) ([]LiveEventReference, error) {
	if idx == nil || eventName == "" {
		return nil, nil
	}
	records, err := idx.records.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []LiveEventReference
	for _, record := range records {
		for _, reference := range record.LiveEventReferences {
			if strings.EqualFold(reference.Name, eventName) {
				result = append(result, reference)
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

func (idx *Index) LiveEventNames() ([]string, error) {
	listeners, err := idx.LiveListeners()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(listeners))
	var result []string
	for _, listener := range listeners {
		key := strings.ToLower(listener.Name)
		if listener.Name == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, listener.Name)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result, nil
}

func (idx *Index) ComponentsForTemplate(
	path string,
) ([]Component, error) {
	components, err := idx.Components()
	if err != nil {
		return nil, err
	}
	templateNames := make(map[string]struct{})
	for _, name := range twig.TemplateNames(path) {
		templateNames[name] = struct{}{}
	}
	var result []Component
	for _, component := range components {
		if component.File == path &&
			component.Source == AnonymousTemplateSource {
			result = append(result, component)
			continue
		}
		if _, matches := templateNames[component.Template]; matches {
			result = append(result, component)
		}
	}
	return result, nil
}

func (idx *Index) ContextForTemplate(
	path string,
	currentRoot *twigsyntax.Node,
) ([]Component, []Prop, error) {
	components, err := idx.ComponentsForTemplate(path)
	if err != nil || len(components) == 0 {
		return components, nil, err
	}
	var props []Prop
	for _, component := range components {
		current, propErr := idx.Props(component.Name)
		if propErr != nil {
			return nil, nil, propErr
		}
		for _, prop := range current {
			if prop.File != path {
				props = append(props, prop)
			}
		}
	}
	if currentRoot != nil {
		props = append(props, PropsInTwig(path, currentRoot)...)
	}
	props = mergeProps(props)
	sort.Slice(props, func(left, right int) bool {
		return strings.ToLower(props[left].Name) <
			strings.ToLower(props[right].Name)
	})
	return components, props, nil
}

func (idx *Index) RemovedFiles(paths []string) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.BatchDeleteByFilePaths(paths); err != nil {
		return err
	}
	idx.removeIndexedPaths(paths)
	idx.invalidateCatalog()
	return nil
}

func (idx *Index) RemovedFilesIn(
	paths []string,
	mutation *indexer.Mutation,
) error {
	if idx == nil {
		return nil
	}
	if err := idx.records.BatchDeleteByFilePathsIn(
		mutation,
		paths,
	); err != nil {
		return err
	}
	publish := func() {
		idx.removeIndexedPaths(paths)
		idx.invalidateCatalog()
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
	idx.invalidateCatalog()
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
		idx.resetIndexedPaths()
		idx.invalidateCatalog()
		return nil
	}
	return mutation.AfterCommit(func() {
		idx.resetIndexedPaths()
		idx.invalidateCatalog()
	})
}

func (idx *Index) Close() error {
	if idx == nil {
		return nil
	}
	return idx.records.Close()
}

func configuredNamespaces(records []Record) []Namespace {
	var result []Namespace
	for _, record := range records {
		result = append(result, record.Namespaces...)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	defaultNamespace := "App\\Twig\\Components"
	hasDefault := false
	for _, namespace := range result {
		if strings.EqualFold(
			normalizeClass(namespace.ClassPrefix),
			defaultNamespace,
		) {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		result = append(result, Namespace{
			ClassPrefix:       defaultNamespace,
			TemplateDirectory: "components/",
		})
	}
	return uniqueNamespaces(result)
}

func anonymousDirectories(records []Record) []string {
	var result []string
	for _, record := range records {
		result = append(result, record.AnonymousDirectories...)
	}
	result = uniqueStrings(result)
	if len(result) == 0 {
		return []string{"components/"}
	}
	return result
}

func resolveDeclaration(
	declaration Declaration,
	namespaces []Namespace,
) (Component, bool) {
	class := normalizeClass(declaration.Class)
	name := normalizeComponentName(declaration.Name)
	template := normalizeTemplate(declaration.Template)
	var matched *Namespace
	for index := range namespaces {
		prefix := normalizeClass(namespaces[index].ClassPrefix)
		if strings.EqualFold(class, prefix) ||
			strings.HasPrefix(
				strings.ToLower(class),
				strings.ToLower(prefix)+`\`,
			) {
			matched = &namespaces[index]
			break
		}
	}
	if name == "" && matched != nil {
		relative := strings.TrimPrefix(
			class[len(normalizeClass(matched.ClassPrefix)):],
			`\`,
		)
		name = strings.ReplaceAll(relative, `\`, ":")
		if matched.NamePrefix != "" && name != "" {
			name = matched.NamePrefix + ":" + name
		}
	}
	if name == "" {
		return Component{}, false
	}
	if template == "" && declaration.TemplateFromMethod == "" {
		directory := "components/"
		templateName := name
		if matched != nil {
			directory = matched.TemplateDirectory
			if matched.NamePrefix != "" {
				prefix := matched.NamePrefix + ":"
				templateName = strings.TrimPrefix(templateName, prefix)
			}
		}
		template = normalizeDirectory(directory) +
			strings.ReplaceAll(templateName, ":", "/") +
			".html.twig"
	}
	nameRange := declaration.NameRange
	if nameRange.Len() == 0 {
		nameRange = declaration.ClassRange
	}
	return Component{
		Name:               name,
		Class:              class,
		Template:           template,
		TemplateFromMethod: declaration.TemplateFromMethod,
		File:               declaration.File,
		NameRange:          nameRange,
		ClassRange:         declaration.ClassRange,
		TemplateRange:      declaration.TemplateRange,
		Source:             declaration.Source,
		ExposePublicProps:  declaration.ExposePublicProps,
		Live:               declaration.Live,
	}, true
}

func (idx *Index) compiledComponents() ([]Declaration, uint64) {
	_, serviceIndex, _ := idx.dependencies()
	if serviceIndex == nil {
		return nil, 0
	}
	components, revision :=
		serviceIndex.GetCompiledTwigComponentsState()
	result := make([]Declaration, 0, len(components))
	for _, component := range components {
		if component.Name == "" {
			continue
		}
		result = append(result, Declaration{
			Name:               component.Name,
			Class:              component.Class,
			Template:           component.Template,
			TemplateFromMethod: component.TemplateFromMethod,
			File:               component.Path,
			NameRange:          component.NameRange,
			ClassRange:         component.ClassRange,
			TemplateRange:      component.TemplateRange,
			Source:             CompiledContainerSource,
			ExposePublicProps:  true,
		})
	}
	return result, revision
}

func mergeCompiledComponent(
	components []Component,
	compiled Component,
) []Component {
	for index := range components {
		if !strings.EqualFold(components[index].Name, compiled.Name) {
			continue
		}
		merged := components[index]
		if compiled.Class != "" {
			merged.Class = compiled.Class
			merged.ClassRange = compiled.ClassRange
		}
		if compiled.Template != "" {
			merged.Template = compiled.Template
			merged.TemplateRange = compiled.TemplateRange
		}
		if compiled.TemplateFromMethod != "" {
			merged.Template = ""
			merged.TemplateFromMethod = compiled.TemplateFromMethod
		}
		merged.File = compiled.File
		merged.Source = CompiledContainerSource
		merged.NameRange = compiled.NameRange
		merged.ExposePublicProps = compiled.ExposePublicProps
		components[index] = merged
		return components
	}
	return append(components, compiled)
}

func (idx *Index) serviceDeclarations() ([]Declaration, error) {
	_, serviceIndex, _ := idx.dependencies()
	if serviceIndex == nil {
		return nil, nil
	}
	var result []Declaration
	seen := make(map[string]struct{})
	for _, tag := range []string{modernServiceTag, legacyServiceTag} {
		ids, err := serviceIndex.GetServicesByTag(tag)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			service, found, serviceErr := serviceIndex.GetServiceByID(id)
			if serviceErr != nil {
				return nil, serviceErr
			}
			if !found {
				continue
			}
			class := normalizeClass(service.Class)
			if class == "" {
				class = normalizeClass(service.ID)
			}
			key := strings.ToLower(class) + "\x00" + service.Path
			if class == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, Declaration{
				Class:             class,
				File:              service.Path,
				Source:            ServiceSource,
				ExposePublicProps: true,
			})
		}
	}
	return result, nil
}

func (idx *Index) anonymousComponents(
	directories []string,
) ([]Component, error) {
	_, _, twigIndex := idx.dependencies()
	if twigIndex == nil {
		return nil, nil
	}
	templates, err := twigIndex.GetAllTemplateFiles()
	if err != nil {
		return nil, err
	}
	var result []Component
	for _, template := range templates {
		name, matches := anonymousComponentName(
			template,
			directories,
		)
		if !matches {
			continue
		}
		files, fileErr := twigIndex.GetTwigFilesByRelPath(template)
		if fileErr != nil {
			return nil, fileErr
		}
		for _, file := range files {
			result = append(result, Component{
				Name:              name,
				Template:          template,
				File:              file.Path,
				Source:            AnonymousTemplateSource,
				ExposePublicProps: true,
			})
		}
	}
	return result, nil
}

func anonymousComponentName(
	template string,
	directories []string,
) (string, bool) {
	template = strings.TrimPrefix(
		strings.ReplaceAll(template, `\`, "/"),
		"/",
	)
	namespace := ""
	relative := template
	if strings.HasPrefix(relative, "@") {
		slash := strings.IndexByte(relative, '/')
		if slash <= 1 {
			return "", false
		}
		namespace = relative[1:slash]
		relative = relative[slash+1:]
	}
	for _, directory := range directories {
		directory = normalizeDirectory(directory)
		directoryNamespace := ""
		if strings.HasPrefix(directory, "@") {
			slash := strings.IndexByte(directory, '/')
			if slash <= 1 {
				continue
			}
			directoryNamespace = directory[1:slash]
			directory = directory[slash+1:]
			if namespace != directoryNamespace {
				continue
			}
		}
		if !strings.HasPrefix(relative, directory) {
			continue
		}
		name := strings.TrimPrefix(relative, directory)
		name = strings.TrimSuffix(name, ".html.twig")
		name = strings.TrimSuffix(name, ".twig")
		name = strings.Trim(name, "/")
		if name == "" || name == "index" {
			return "", false
		}
		name = strings.ReplaceAll(name, "/", ":")
		name = strings.TrimSuffix(name, ":index")
		if name == "" {
			return "", false
		}
		if namespace != "" && directoryNamespace == "" {
			name = namespace + ":" + name
		}
		return name, true
	}
	return "", false
}

func (idx *Index) phpProps(components []Component) []Prop {
	phpIndex, _, _ := idx.dependencies()
	if phpIndex == nil {
		return nil
	}
	seenClasses := make(map[string]struct{})
	var result []Prop
	for _, component := range components {
		class := normalizeClass(component.Class)
		if class == "" {
			continue
		}
		key := strings.ToLower(class)
		if _, exists := seenClasses[key]; exists {
			continue
		}
		seenClasses[key] = struct{}{}
		for _, property := range phpIndex.Properties(class) {
			exposed := component.ExposePublicProps &&
				property.Visibility == semantic.Public
			if hasExposeInTemplate(property) {
				exposed = true
			}
			if !exposed {
				continue
			}
			result = append(result, Prop{
				Name:   strings.TrimPrefix(property.Name, "$"),
				Type:   property.Type.String(),
				File:   property.Path,
				Class:  class,
				Member: strings.TrimPrefix(property.Name, "$"),
				Range:  property.SelectionRange,
			})
		}
		for _, method := range phpIndex.Methods(class) {
			if method.Visibility != semantic.Public ||
				!hasExposeInTemplate(method) {
				continue
			}
			result = append(result, Prop{
				Name:   exposedMethodName(method.Name),
				Type:   method.ReturnType.String(),
				File:   method.Path,
				Class:  class,
				Member: method.Name,
				Range:  method.SelectionRange,
			})
		}
	}
	return result
}

func hasExposeInTemplate(symbol semantic.Symbol) bool {
	for _, attribute := range symbol.Attributes {
		if strings.EqualFold(
			normalizeClass(attribute.Name),
			"Symfony\\UX\\TwigComponent\\Attribute\\ExposeInTemplate",
		) {
			return true
		}
	}
	return false
}

func hasLiveAction(symbol semantic.Symbol) bool {
	for _, attribute := range symbol.Attributes {
		if strings.EqualFold(
			normalizeClass(attribute.Name),
			"Symfony\\UX\\LiveComponent\\Attribute\\LiveAction",
		) {
			return true
		}
	}
	return false
}

func hasLiveListener(symbol semantic.Symbol) bool {
	for _, attribute := range symbol.Attributes {
		if strings.EqualFold(
			normalizeClass(attribute.Name),
			"Symfony\\UX\\LiveComponent\\Attribute\\LiveListener",
		) {
			return true
		}
	}
	return false
}

func exposedMethodName(name string) string {
	for _, prefix := range []string{"get", "is", "has"} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			value := name[len(prefix):]
			return strings.ToLower(value[:1]) + value[1:]
		}
	}
	return name
}

func computedMethodName(name string) (string, bool) {
	for _, prefix := range []string{"get", "is", "has"} {
		if !strings.HasPrefix(name, prefix) ||
			len(name) <= len(prefix) {
			continue
		}
		value := name[len(prefix):]
		return strings.ToLower(value[:1]) + value[1:], true
	}
	return "", false
}

func mergeProps(props []Prop) []Prop {
	byName := make(map[string]Prop, len(props))
	var order []string
	for _, prop := range props {
		key := strings.ToLower(prop.Name)
		if key == "" {
			continue
		}
		existing, exists := byName[key]
		if !exists {
			order = append(order, key)
			byName[key] = prop
			continue
		}
		if existing.Type == "" {
			existing.Type = prop.Type
		}
		if existing.DefaultValue == "" {
			existing.DefaultValue = prop.DefaultValue
		}
		if existing.Description == "" {
			existing.Description = prop.Description
		}
		if existing.Class == "" {
			existing.Class = prop.Class
		}
		if existing.Member == "" {
			existing.Member = prop.Member
		}
		existing.Live = existing.Live || prop.Live
		existing.Writable = existing.Writable || prop.Writable
		byName[key] = existing
	}
	result := make([]Prop, 0, len(order))
	for _, key := range order {
		result = append(result, byName[key])
	}
	return result
}

func uniqueComponents(components []Component) []Component {
	seen := make(map[string]struct{}, len(components))
	result := make([]Component, 0, len(components))
	for _, component := range components {
		if component.Name == "" {
			continue
		}
		key := componentKey(component)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, component)
	}
	return result
}

func (idx *Index) dependencies() (
	*php.PHPIndex,
	*symfony.ServiceIndex,
	*twig.TwigIndexer,
) {
	idx.dependenciesMu.RLock()
	defer idx.dependenciesMu.RUnlock()
	return idx.phpIndex, idx.serviceIndex, idx.twigIndex
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

func (idx *Index) invalidateAfter(
	mutation *indexer.Mutation,
) error {
	if mutation == nil {
		idx.invalidateCatalog()
		return nil
	}
	return mutation.AfterCommit(idx.invalidateCatalog)
}

func (idx *Index) invalidateCatalog() {
	idx.catalogMu.Lock()
	defer idx.catalogMu.Unlock()
	idx.catalogValid = false
	idx.catalogEpoch++
	idx.compiledRev = 0
	idx.catalog = nil
	clear(idx.propsCache)
}

var _ indexer.Indexer = (*Index)(nil)
