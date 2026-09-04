package php

import (
	"container/list"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/binder"
	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/stubs"
)

const (
	workspaceRestoreStringsPerDocument = 18
	workspaceRestoreTypeNumerator      = 5
	workspaceRestoreTypeDenominator    = 4
)

// PHPIndex owns the persisted compact workspace graphs and publishes immutable
// workspace snapshots. A document update becomes visible only after the shared
// index transaction commits.
type PHPIndex struct {
	workspaceGraphs      *indexer.DataIndexer[persistedWorkspaceGraph]
	twigContextIndexer   *indexer.DataIndexer[TwigTemplateContext]
	semanticStore        *semantic.Store
	binder               *binder.Binder
	ownedStore           *indexer.Store
	extensionMu          sync.RWMutex
	extensions           []inference.Extension
	project              *project.Model
	supplementalRoots    []string
	supplementalFiles    []string
	revision             atomic.Uint64
	batchMu              sync.Mutex
	batchDepth           int
	pending              map[string]*semantic.WorkspaceGraph
	graphDetacher        *semantic.WorkspaceGraphDetacher
	twigContextCacheMu   sync.Mutex
	twigContextCacheAt   uint64
	twigContextCache     map[string][]TwigTemplateVariable
	classCatalogMu       sync.Mutex
	classCatalogSnapshot *semantic.Snapshot
	classCatalogSymbols  []semantic.Symbol
	classCatalogNames    []string
	workspaceGraphLoader *workspaceGraphRepositoryLoader
}

type preparedPHPDocument struct {
	graph        *semantic.WorkspaceGraph
	graphStorage persistedWorkspaceGraph
	twigContexts map[string]map[string]TwigTemplateContext
}

type workspaceGraphRepositoryLoader struct {
	repository *indexer.DataIndexer[persistedWorkspaceGraph]
	mu         sync.Mutex
	entries    map[string]*list.Element
	recent     list.List
	loading    map[string]*workspaceGraphLoad
	generation uint64
}

// Keep recently requested detail graphs hot without allowing a broad catalog
// query to recreate the complete eager warm-start graph in memory.
const workspaceGraphDetailCacheSize = 128

type workspaceGraphCacheEntry struct {
	path  string
	graph *semantic.WorkspaceGraph
}

type workspaceGraphLoad struct {
	done       chan struct{}
	graph      *semantic.WorkspaceGraph
	err        error
	generation uint64
}

func (loader *workspaceGraphRepositoryLoader) LoadWorkspaceGraph(
	path string,
) (*semantic.WorkspaceGraph, error) {
	if loader == nil || loader.repository == nil {
		return nil, fmt.Errorf("load PHP workspace graph %s: repository is unavailable", path)
	}
	loader.mu.Lock()
	if element := loader.entries[path]; element != nil {
		loader.recent.MoveToFront(element)
		graph := element.Value.(*workspaceGraphCacheEntry).graph
		loader.mu.Unlock()
		return graph, nil
	}
	if pending := loader.loading[path]; pending != nil {
		loader.mu.Unlock()
		<-pending.done
		return pending.graph, pending.err
	}
	if loader.loading == nil {
		loader.loading = make(map[string]*workspaceGraphLoad)
	}
	pending := &workspaceGraphLoad{
		done:       make(chan struct{}),
		generation: loader.generation,
	}
	loader.loading[path] = pending
	loader.mu.Unlock()

	result, err := loader.loadWorkspaceGraph(path)
	loader.mu.Lock()
	pending.graph = result
	pending.err = err
	if loader.loading[path] == pending {
		delete(loader.loading, path)
	}
	if err == nil && pending.generation == loader.generation {
		if loader.entries == nil {
			loader.entries = make(map[string]*list.Element)
		}
		entry := &workspaceGraphCacheEntry{path: path, graph: result}
		loader.entries[path] = loader.recent.PushFront(entry)
		if loader.recent.Len() > workspaceGraphDetailCacheSize {
			oldest := loader.recent.Back()
			delete(
				loader.entries,
				oldest.Value.(*workspaceGraphCacheEntry).path,
			)
			loader.recent.Remove(oldest)
		}
	}
	close(pending.done)
	loader.mu.Unlock()
	return result, err
}

func (loader *workspaceGraphRepositoryLoader) loadWorkspaceGraph(
	path string,
) (*semantic.WorkspaceGraph, error) {
	var result *semantic.WorkspaceGraph
	err := loader.repository.VisitEncodedValuesByPath(path, func(encoded []byte) error {
		if result != nil {
			return fmt.Errorf("multiple persisted workspace graphs")
		}
		persisted, err := decodePersistedWorkspaceGraphBorrowed(encoded)
		if err != nil {
			return err
		}
		decoder := semantic.NewWorkspaceGraphDecoder()
		defer decoder.Clear()
		result, err = persisted.decodeWith(decoder)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("load PHP workspace graph %s: %w", path, err)
	}
	if result == nil {
		return nil, fmt.Errorf("load PHP workspace graph %s: not found", path)
	}
	return result, nil
}

func (loader *workspaceGraphRepositoryLoader) forget(path string) {
	if loader == nil {
		return
	}
	loader.mu.Lock()
	if element := loader.entries[path]; element != nil {
		delete(loader.entries, path)
		loader.recent.Remove(element)
	}
	loader.mu.Unlock()
}

func (loader *workspaceGraphRepositoryLoader) clear() {
	if loader == nil {
		return
	}
	loader.mu.Lock()
	loader.generation++
	loader.entries = nil
	loader.recent.Init()
	loader.mu.Unlock()
}

func NewPHPIndex(configDir string, stores ...*indexer.Store) (*PHPIndex, error) {
	repositoryStores := stores
	var ownedStore *indexer.Store
	if len(repositoryStores) == 0 || repositoryStores[0] == nil {
		var err error
		ownedStore, err = indexer.NewStore(filepath.Join(configDir, "php.db"))
		if err != nil {
			return nil, fmt.Errorf("create PHP index store: %w", err)
		}
		repositoryStores = []*indexer.Store{ownedStore}
	}

	workspaceGraphs, err := indexer.NewRepository[persistedWorkspaceGraph](
		filepath.Join(configDir, "php.db"),
		"php.semantic.workspace_graphs",
		repositoryStores...,
	)
	if err != nil {
		if ownedStore != nil {
			_ = ownedStore.Close()
		}
		return nil, fmt.Errorf("create PHP workspace graph repository: %w", err)
	}
	twigContextIndexer, err := indexer.NewRepository[TwigTemplateContext](
		filepath.Join(configDir, "php.db"),
		"php.twig.template_contexts",
		repositoryStores...,
	)
	if err != nil {
		_ = workspaceGraphs.Close()
		if ownedStore != nil {
			_ = ownedStore.Close()
		}
		return nil, fmt.Errorf("create PHP Twig context repository: %w", err)
	}

	idx := &PHPIndex{
		workspaceGraphs:    workspaceGraphs,
		twigContextIndexer: twigContextIndexer,
		semanticStore:      semantic.NewStore(),
		binder:             binder.New(),
		ownedStore:         ownedStore,
		extensions:         []inference.Extension{inference.Builtins},
	}
	idx.workspaceGraphLoader = &workspaceGraphRepositoryLoader{
		repository: workspaceGraphs,
	}
	workspaceGraphCount, err := workspaceGraphs.CountAllValues()
	if err != nil {
		_ = workspaceGraphs.Close()
		_ = twigContextIndexer.Close()
		if ownedStore != nil {
			_ = ownedStore.Close()
		}
		return nil, fmt.Errorf(
			"count PHP workspace graph repository: %w",
			err,
		)
	}
	restoreCapacity := semantic.WorkspaceRestoreCapacity{
		Documents: workspaceGraphCount,
		Strings: workspaceGraphCount *
			workspaceRestoreStringsPerDocument,
		Types: (workspaceGraphCount*workspaceRestoreTypeNumerator +
			workspaceRestoreTypeDenominator - 1) /
			workspaceRestoreTypeDenominator,
	}
	_, err = idx.semanticStore.RestoreWorkspaceGraphsDecodedWithCapacity(
		restoreCapacity,
		func(
			decoder *semantic.WorkspaceGraphDecoder,
			accept func(*semantic.WorkspaceGraph),
		) error {
			return workspaceGraphs.VisitAllEncodedValues(func(
				encoded []byte,
			) error {
				persisted, err :=
					decodePersistedWorkspaceGraphBorrowed(encoded)
				if err != nil {
					return err
				}
				graph, err := persisted.decodeSummaryWith(
					decoder,
					idx.workspaceGraphLoader,
				)
				if err != nil {
					return err
				}
				accept(graph)
				return nil
			})
		},
	)
	// Workspace restoration is the only high-volume decode phase. Release the
	// reusable DEFLATE dictionary and MessagePack buffer while the LSP is idle.
	semanticValueDecompressorPool.clear()
	if err != nil {
		_ = workspaceGraphs.Close()
		_ = twigContextIndexer.Close()
		if ownedStore != nil {
			_ = ownedStore.Close()
		}
		return nil, fmt.Errorf("load PHP workspace graph repository: %w", err)
	}
	return idx, nil
}

// ConfigureProject loads Composer roots/version information and publishes the
// matching runtime stub document.
func (idx *PHPIndex) ConfigureProject(root string) error {
	return idx.ConfigureProjectWithExtensions(root, nil, nil)
}

func (idx *PHPIndex) ConfigureProjectWithExtensions(
	root string,
	enabled,
	disabled []string,
) error {
	model, err := project.Load(root)
	if err != nil {
		return err
	}
	model.ConfigureExtensions(enabled, disabled)
	model.LoadedExtensions = stubs.SelectedExtensions(
		model.StubExtensions(),
		model.DisabledExtensions,
	)
	supplementalRoots := model.SourceRoots()
	for index := range supplementalRoots {
		supplementalRoots[index] = filepath.Clean(supplementalRoots[index])
	}
	supplementalFiles := make([]string, len(model.Files))
	for index, file := range model.Files {
		supplementalFiles[index] = filepath.Clean(file)
	}
	idx.extensionMu.Lock()
	idx.project = model
	idx.supplementalRoots = supplementalRoots
	idx.supplementalFiles = supplementalFiles
	idx.extensionMu.Unlock()
	idx.semanticStore.Replace(stubs.DocumentForExtensions(
		model.PHPVersion,
		model.StubExtensions(),
		model.DisabledExtensions,
	))
	idx.revision.Add(1)
	return nil
}

// ShouldEnterDirectory selectively reopens Composer source roots that live in
// globally skipped directories such as tests/. Only the PHP index opts into
// these paths; generated assets and unrelated test trees remain excluded.
func (idx *PHPIndex) ShouldEnterDirectory(path string) bool {
	idx.extensionMu.RLock()
	configured := idx.project != nil
	roots := idx.supplementalRoots
	files := idx.supplementalFiles
	idx.extensionMu.RUnlock()
	if !configured {
		return false
	}
	path = filepath.Clean(path)
	for _, root := range roots {
		if pathsOverlap(root, path) {
			return true
		}
	}
	for _, file := range files {
		if pathsOverlap(filepath.Dir(file), path) {
			return true
		}
	}
	return false
}

func (idx *PHPIndex) ShouldIndexPath(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".php") {
		return false
	}
	idx.extensionMu.RLock()
	configured := idx.project != nil
	roots := idx.supplementalRoots
	files := idx.supplementalFiles
	idx.extensionMu.RUnlock()
	if !configured {
		return false
	}
	path = filepath.Clean(path)
	for _, root := range roots {
		if pathWithinRoot(root, path) {
			return true
		}
	}
	for _, file := range files {
		if path == file {
			return true
		}
	}
	return false
}

func (idx *PHPIndex) ShouldPreparsePath(path string) bool {
	return idx.ShouldIndexPath(path)
}

func pathsOverlap(left, right string) bool {
	return pathWithinRoot(left, right) || pathWithinRoot(right, left)
}

func pathWithinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == path {
		return true
	}
	if len(path) > len(root) && strings.HasPrefix(path, root) &&
		(root[len(root)-1] == os.PathSeparator ||
			path[len(root)] == os.PathSeparator) {
		return true
	}
	if os.PathSeparator == '/' && filepath.IsAbs(root) && filepath.IsAbs(path) {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

var _ indexer.SupplementalPathIndexer = (*PHPIndex)(nil)
var _ indexer.SupplementalSyntaxIndexer = (*PHPIndex)(nil)
var _ indexer.WorkspaceSymbolContributor = (*PHPIndex)(nil)

func (idx *PHPIndex) Project() *project.Model {
	idx.extensionMu.RLock()
	defer idx.extensionMu.RUnlock()
	return idx.project
}

func (idx *PHPIndex) ID() string {
	return "php.index"
}

func (idx *PHPIndex) Index(file *indexer.ParsedFile) error {
	prepared, err := idx.Prepare(file)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(file, prepared)
}

func (idx *PHPIndex) Prepare(file *indexer.ParsedFile) (any, error) {
	if file.Extension() != ".php" {
		return (*preparedPHPDocument)(nil), nil
	}

	path := file.Path
	if err := idx.semanticStore.EnsureDocumentDetails(path); err != nil {
		return nil, fmt.Errorf(
			"pin previous PHP workspace graph for %s: %w",
			path,
			err,
		)
	}
	root := file.SyntaxTree().Root
	document := idx.AnalyzeParsedFile(file)

	workspaceGraph := semantic.ProjectWorkspaceGraphBorrowed(document)
	persistedGraph, err := encodeWorkspaceGraph(workspaceGraph)
	if err != nil {
		return nil, fmt.Errorf(
			"encode PHP workspace graph for %s: %w",
			path,
			err,
		)
	}
	twigContexts := map[string]map[string]TwigTemplateContext{path: {}}
	for _, context := range extractTwigTemplateContexts(path, root, document) {
		twigContexts[path][context.Template] = context
	}
	return &preparedPHPDocument{
		graph:        workspaceGraph,
		graphStorage: persistedGraph,
		twigContexts: twigContexts,
	}, nil
}

func (idx *PHPIndex) IndexPrepared(
	file *indexer.ParsedFile,
	value any,
) error {
	if file.Extension() != ".php" {
		return nil
	}
	prepared, ok := value.(*preparedPHPDocument)
	if !ok || prepared == nil || prepared.graph == nil {
		return fmt.Errorf("prepared PHP document is required for %s", file.Path)
	}
	path := file.Path
	if err := idx.workspaceGraphs.BatchSaveItemsIn(
		file.Mutation(),
		map[string]map[string]persistedWorkspaceGraph{
			path: {path: prepared.graphStorage},
		},
	); err != nil {
		return err
	}
	if err := idx.twigContextIndexer.BatchSaveItemsIn(
		file.Mutation(),
		prepared.twigContexts,
	); err != nil {
		return err
	}
	publish := func() {
		idx.workspaceGraphLoader.forget(path)
		idx.publishWorkspaceGraph(prepared.graph)
		idx.revision.Add(1)
	}
	if mutation := file.Mutation(); mutation != nil {
		return mutation.AfterCommit(publish)
	}
	publish()
	return nil
}

// WorkspaceSymbols reuses the compact semantic graph produced by Prepare.
// It deliberately runs before the graph is released, avoiding a decode from
// SQLite and keeping symbol catalog population part of the file transaction.
func (idx *PHPIndex) WorkspaceSymbols(
	file *indexer.ParsedFile,
	value any,
) ([]indexer.WorkspaceSymbol, error) {
	if file.Extension() != ".php" {
		return nil, nil
	}
	prepared, ok := value.(*preparedPHPDocument)
	if !ok || prepared == nil || prepared.graph == nil {
		return nil, fmt.Errorf("prepared PHP document is required for %s", file.Path)
	}
	containers := make(map[semantic.SymbolID]string)
	prepared.graph.VisitSymbolViews(func(view semantic.SymbolView) bool {
		if phpWorkspaceSymbolIsContainer(view.Kind()) {
			name := strings.TrimPrefix(view.FullyQualified(), `\`)
			if name == "" {
				name = view.Name()
			}
			containers[view.ID()] = name
		}
		return true
	})

	lineIndex := file.LineIndex()
	result := make([]indexer.WorkspaceSymbol, 0, len(containers)*2)
	prepared.graph.VisitSymbolViews(func(view semantic.SymbolView) bool {
		kind, priority, include := phpWorkspaceSymbolKind(view.Kind())
		if !include || view.Name() == "" {
			return true
		}
		rangeValue := view.SelectionRange()
		if rangeValue == (cst.TextRange{}) {
			rangeValue = view.Range()
		}
		startLine, startCharacter := lineIndex.PositionUTF16(rangeValue.Start)
		endLine, endCharacter := lineIndex.PositionUTF16(rangeValue.End)
		fullyQualified := strings.TrimPrefix(view.FullyQualified(), `\`)
		container := containers[view.Container()]
		if container == "" && phpWorkspaceSymbolIsContainer(view.Kind()) {
			if separator := strings.LastIndex(fullyQualified, `\`); separator >= 0 {
				container = fullyQualified[:separator]
			}
		}
		var aliases []string
		if phpWorkspaceSymbolIsContainer(view.Kind()) ||
			view.Kind() == semantic.FunctionSymbol ||
			view.Kind() == semantic.GlobalConstantSymbol {
			aliases = []string{fullyQualified}
		}
		result = append(result, indexer.WorkspaceSymbol{
			Name:          view.Name(),
			ContainerName: container,
			Aliases:       aliases,
			Path:          file.Path,
			Domain:        "php",
			Kind:          kind,
			Priority:      priority,
			Range: indexer.WorkspaceSymbolRange{
				Start: indexer.WorkspaceSymbolPosition{
					Line:      int(startLine),
					Character: int(startCharacter),
				},
				End: indexer.WorkspaceSymbolPosition{
					Line:      int(endLine),
					Character: int(endCharacter),
				},
			},
		})
		return true
	})
	return result, nil
}

func phpWorkspaceSymbolIsContainer(kind semantic.SymbolKind) bool {
	switch kind {
	case semantic.ClassSymbol,
		semantic.InterfaceSymbol,
		semantic.TraitSymbol,
		semantic.EnumSymbol:
		return true
	default:
		return false
	}
}

func phpWorkspaceSymbolKind(
	kind semantic.SymbolKind,
) (indexer.WorkspaceSymbolKind, int, bool) {
	switch kind {
	case semantic.ClassSymbol:
		return indexer.WorkspaceSymbolClass,
			indexer.WorkspaceSymbolPriorityPHPType,
			true
	case semantic.InterfaceSymbol:
		return indexer.WorkspaceSymbolInterface,
			indexer.WorkspaceSymbolPriorityPHPType,
			true
	case semantic.TraitSymbol:
		return indexer.WorkspaceSymbolStruct,
			indexer.WorkspaceSymbolPriorityPHPType,
			true
	case semantic.EnumSymbol:
		return indexer.WorkspaceSymbolEnum,
			indexer.WorkspaceSymbolPriorityPHPType,
			true
	case semantic.FunctionSymbol:
		return indexer.WorkspaceSymbolFunction,
			indexer.WorkspaceSymbolPriorityPHPGlobal,
			true
	case semantic.GlobalConstantSymbol:
		return indexer.WorkspaceSymbolConstant,
			indexer.WorkspaceSymbolPriorityPHPGlobal,
			true
	case semantic.MethodSymbol:
		return indexer.WorkspaceSymbolMethod,
			indexer.WorkspaceSymbolPriorityPHPMember,
			true
	case semantic.PropertySymbol:
		return indexer.WorkspaceSymbolProperty,
			indexer.WorkspaceSymbolPriorityPHPMember,
			true
	case semantic.ClassConstantSymbol:
		return indexer.WorkspaceSymbolConstant,
			indexer.WorkspaceSymbolPriorityPHPMember,
			true
	case semantic.EnumCaseSymbol:
		return indexer.WorkspaceSymbolEnumMember,
			indexer.WorkspaceSymbolPriorityPHPMember,
			true
	case semantic.TypeAliasSymbol:
		return indexer.WorkspaceSymbolTypeParameter,
			indexer.WorkspaceSymbolPriorityPHPMember,
			true
	default:
		return 0, 0, false
	}
}

// BeginIndexingBatch defers immutable workspace publication until all file
// transactions in the scanner run have either committed or rolled back.
func (idx *PHPIndex) BeginIndexingBatch(candidateFiles []string) {
	idx.batchMu.Lock()
	defer idx.batchMu.Unlock()
	if idx.batchDepth == 0 {
		phpFiles := 0
		for _, path := range candidateFiles {
			if strings.EqualFold(filepath.Ext(path), ".php") {
				phpFiles++
			}
		}
		idx.pending = make(map[string]*semantic.WorkspaceGraph)
		idx.graphDetacher = semantic.NewWorkspaceGraphDetacherCapacity(
			phpFiles * workspaceRestoreStringsPerDocument,
		)
	}
	idx.batchDepth++
}

// EndIndexingBatch publishes every successfully committed PHP document in one
// generation. Nested batches are supported for direct coordinator reuse.
func (idx *PHPIndex) EndIndexingBatch() error {
	idx.batchMu.Lock()
	if idx.batchDepth == 0 {
		idx.batchMu.Unlock()
		return fmt.Errorf("PHP indexing batch is not active")
	}
	idx.batchDepth--
	if idx.batchDepth > 0 {
		idx.batchMu.Unlock()
		return nil
	}
	graphs := make([]*semantic.WorkspaceGraph, 0, len(idx.pending))
	for _, graph := range idx.pending {
		graphs = append(graphs, graph)
	}
	idx.pending = nil
	idx.graphDetacher.Finish()
	idx.graphDetacher = nil
	idx.batchMu.Unlock()

	// Compression is frequent during a scanner batch but rare while the LSP is
	// idle. Release the bounded writer pool once the batch is published.
	semanticValueCompressorPool.clear()
	idx.semanticStore.ReplaceCanonicalWorkspaceGraphsOwned(graphs...)
	return nil
}

func (idx *PHPIndex) publishWorkspaceGraph(graph *semantic.WorkspaceGraph) {
	if graph == nil {
		return
	}
	idx.batchMu.Lock()
	if idx.batchDepth > 0 {
		idx.graphDetacher.DetachOwned(graph)
		idx.pending[graph.Path()] = graph
		idx.batchMu.Unlock()
		return
	}
	idx.batchMu.Unlock()
	detacher := semantic.NewWorkspaceGraphDetacher()
	detacher.DetachOwned(graph)
	detacher.Finish()
	idx.semanticStore.ReplaceCanonicalWorkspaceGraphsOwned(graph)
}

func (idx *PHPIndex) RemovedFiles(paths []string) error {
	if err := idx.ensureDocumentDetails(paths); err != nil {
		return err
	}
	if err := errors.Join(
		idx.workspaceGraphs.BatchDeleteByFilePaths(paths),
		idx.twigContextIndexer.BatchDeleteByFilePaths(paths),
	); err != nil {
		return err
	}
	for _, path := range paths {
		idx.workspaceGraphLoader.forget(path)
	}
	idx.semanticStore.Remove(paths...)
	idx.revision.Add(1)
	return nil
}

func (idx *PHPIndex) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	if err := idx.ensureDocumentDetails(paths); err != nil {
		return err
	}
	if err := errors.Join(
		idx.workspaceGraphs.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigContextIndexer.BatchDeleteByFilePathsIn(mutation, paths),
	); err != nil {
		return err
	}
	publish := func() {
		for _, path := range paths {
			idx.workspaceGraphLoader.forget(path)
		}
		idx.semanticStore.Remove(paths...)
		idx.revision.Add(1)
	}
	if mutation != nil {
		return mutation.AfterCommit(publish)
	}
	publish()
	return nil
}

func (idx *PHPIndex) Close() error {
	idx.batchMu.Lock()
	idx.batchDepth = 0
	idx.pending = nil
	idx.graphDetacher = nil
	idx.batchMu.Unlock()
	idx.semanticStore.Clear()
	idx.workspaceGraphLoader.clear()
	var storeErr error
	if idx.ownedStore != nil {
		storeErr = idx.ownedStore.Close()
	}
	return errors.Join(
		idx.workspaceGraphs.Close(),
		idx.twigContextIndexer.Close(),
		storeErr,
	)
}

func (idx *PHPIndex) Clear() error {
	if err := errors.Join(
		idx.workspaceGraphs.Clear(),
		idx.twigContextIndexer.Clear(),
	); err != nil {
		return err
	}
	idx.workspaceGraphLoader.clear()
	idx.resetSemanticStore()
	return nil
}

func (idx *PHPIndex) ClearIn(mutation *indexer.Mutation) error {
	if err := errors.Join(
		idx.workspaceGraphs.ClearIn(mutation),
		idx.twigContextIndexer.ClearIn(mutation),
	); err != nil {
		return err
	}
	reset := func() {
		idx.workspaceGraphLoader.clear()
		idx.resetSemanticStore()
	}
	if mutation != nil {
		return mutation.AfterCommit(reset)
	}
	reset()
	return nil
}

func (idx *PHPIndex) ensureDocumentDetails(paths []string) error {
	if idx == nil || idx.semanticStore == nil {
		return nil
	}
	for _, path := range paths {
		if err := idx.semanticStore.EnsureDocumentDetails(path); err != nil {
			return fmt.Errorf(
				"pin previous PHP workspace graph for %s: %w",
				path,
				err,
			)
		}
	}
	return nil
}

func (idx *PHPIndex) resetSemanticStore() {
	idx.semanticStore.Clear()
	if model := idx.Project(); model != nil {
		idx.semanticStore.Replace(stubs.DocumentForExtensions(
			model.PHPVersion,
			model.StubExtensions(),
			model.DisabledExtensions,
		))
	}
	idx.revision.Add(1)
}

func (idx *PHPIndex) Revision() uint64 {
	return idx.revision.Load()
}

// IsSubtypeOf reports whether className is targetName, extends it, or
// implements it through the workspace hierarchy.
func (idx *PHPIndex) IsSubtypeOf(className, targetName string) (bool, error) {
	if className == "" || targetName == "" {
		return false, nil
	}
	return idx.SemanticSnapshot().IsSubtypeOf(className, targetName), nil
}
