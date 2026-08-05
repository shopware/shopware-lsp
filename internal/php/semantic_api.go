package php

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/binder"
	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/phpstormmeta"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// SemanticSnapshot returns the current immutable workspace generation.
func (idx *PHPIndex) SemanticSnapshot() *semantic.Snapshot {
	if idx == nil || idx.semanticStore == nil {
		return semantic.NewSnapshot(0, nil)
	}
	return idx.semanticStore.Snapshot()
}

// AnalyzeDocument creates an unsaved-document overlay and links it against a
// workspace generation containing the overlay's declarations.
func (idx *PHPIndex) AnalyzeDocument(
	path string,
	version int,
	root *phpsyntax.Node,
) *semantic.Document {
	if idx == nil || idx.binder == nil {
		return &semantic.Document{Path: path, Version: version}
	}
	document := idx.binder.Bind(path, version, root)
	if strings.EqualFold(filepath.Base(path), ".phpstorm.meta.php") {
		document.CallContracts = phpstormmeta.Parse(root)
	}
	snapshot := idx.SemanticSnapshot().WithDeclarations(document)
	document.WorkspaceRevision = snapshot.Revision
	document = binder.LinkOwned(document, snapshot)
	document = inference.New(
		snapshot,
		idx.typeExtensions()...,
	).AnalyzeOwned(document, root)
	return inference.LinkMembersOwned(document, snapshot, root)
}

// AnalyzeParsedFile returns the fully linked semantic document shared by
// indexers preparing the same immutable file. FileScanner releases the cached
// value before persistence begins, so semantic documents do not become
// long-lived index state.
func (idx *PHPIndex) AnalyzeParsedFile(
	file *indexer.ParsedFile,
) *semantic.Document {
	if file == nil {
		return &semantic.Document{}
	}
	value := file.Memoized(idx, func() any {
		tree := file.SyntaxTree()
		if tree == nil {
			return &semantic.Document{Path: file.Path}
		}
		return idx.AnalyzeDocument(file.Path, 0, tree.Root)
	})
	document, ok := value.(*semantic.Document)
	if !ok || document == nil {
		return &semantic.Document{Path: file.Path}
	}
	return document
}

// SemanticDocument reconstructs a fully linked document for an indexed path.
// The long-lived index intentionally persists only compact workspace graphs;
// detailed scopes and flow facts are parsed from source on demand.
func (idx *PHPIndex) SemanticDocument(path string) (*semantic.Document, bool) {
	if idx == nil || idx.semanticStore == nil {
		return nil, false
	}
	snapshot := idx.SemanticSnapshot()
	if !snapshot.HasDocument(path) {
		return nil, false
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	tree := phpparser.ParseBytes(source).Tree
	if tree == nil || tree.Root == nil {
		return nil, false
	}
	return idx.AnalyzeDocument(path, 0, tree.Root), true
}

// FindClass returns the preferred class-like declaration for a fully qualified
// PHP name. Source declarations win over internal runtime stubs.
func (idx *PHPIndex) FindClass(name string) (semantic.Symbol, bool) {
	candidates := idx.SemanticSnapshot().Classes(name)
	if len(candidates) == 0 {
		return semantic.Symbol{}, false
	}
	for _, candidate := range candidates {
		if !candidate.Flags.Has(semantic.InternalFlag) {
			return candidate, true
		}
	}
	return candidates[0], true
}

// FindMethods resolves a method on a class, including inherited and trait
// methods, and returns specialized parameter types for generic ancestors.
func (idx *PHPIndex) FindMethods(
	className,
	methodName string,
) []semantic.Symbol {
	if idx == nil || className == "" || methodName == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: idx.SemanticSnapshot(),
	}).Methods(types.Named(strings.TrimPrefix(className, "\\")), methodName)
	result := make([]semantic.Symbol, 0, len(members))
	for _, member := range members {
		result = append(result, member.Symbol)
	}
	return result
}

// FindProperties resolves a property on a class, including inherited and trait
// declarations.
func (idx *PHPIndex) FindProperties(
	className,
	propertyName string,
) []semantic.Symbol {
	if idx == nil || className == "" || propertyName == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: idx.SemanticSnapshot(),
	}).Properties(
		types.Named(strings.TrimPrefix(className, "\\")),
		strings.TrimPrefix(propertyName, "$"),
	)
	result := make([]semantic.Symbol, 0, len(members))
	for _, member := range members {
		result = append(result, member.Symbol)
	}
	return result
}

// FindConstants resolves a class constant or enum case, including inherited
// and trait declarations.
func (idx *PHPIndex) FindConstants(
	className,
	constantName string,
) []semantic.Symbol {
	if idx == nil || className == "" || constantName == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: idx.SemanticSnapshot(),
	}).Constants(
		types.Named(strings.TrimPrefix(className, "\\")),
		constantName,
	)
	result := make([]semantic.Symbol, 0, len(members))
	for _, member := range members {
		result = append(result, member.Symbol)
	}
	return result
}

// FindGlobalConstants returns declarations for a fully qualified global
// constant name.
func (idx *PHPIndex) FindGlobalConstants(
	name string,
) []semantic.Symbol {
	if idx == nil || name == "" {
		return nil
	}
	return idx.SemanticSnapshot().Constants(name)
}

// Constants returns every constant visible on a class, including inherited
// constants and enum cases.
func (idx *PHPIndex) Constants(
	className string,
) []semantic.Symbol {
	if idx == nil || className == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: idx.SemanticSnapshot(),
	}).All(types.Named(strings.TrimPrefix(className, "\\")))
	var result []semantic.Symbol
	for _, member := range members {
		switch member.Symbol.Kind {
		case semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
			result = append(result, member.Symbol)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return compareFold(result[left].Name, result[right].Name) < 0
	})
	return result
}

// ConstantSymbols returns all constant declarations in deterministic
// display-name/path order.
func (idx *PHPIndex) ConstantSymbols() []semantic.Symbol {
	if idx == nil {
		return nil
	}
	snapshot := idx.SemanticSnapshot()
	var result []semantic.Symbol
	for _, symbol := range snapshot.AllSymbols() {
		switch symbol.Kind {
		case semantic.GlobalConstantSymbol,
			semantic.ClassConstantSymbol,
			semantic.EnumCaseSymbol:
		default:
			continue
		}
		result = append(result, symbol)
	}
	sort.Slice(result, func(left, right int) bool {
		leftName := constantDisplayName(snapshot, result[left])
		rightName := constantDisplayName(snapshot, result[right])
		if leftName != rightName {
			return compareFold(leftName, rightName) < 0
		}
		return result[left].Path < result[right].Path
	})
	return result
}

// ConstantSymbolName renders a global or class constant as it appears in
// Symfony configuration.
func (idx *PHPIndex) ConstantSymbolName(
	symbol semantic.Symbol,
) string {
	if idx == nil {
		return symbol.Name
	}
	return constantDisplayName(idx.SemanticSnapshot(), symbol)
}

func constantDisplayName(
	snapshot *semantic.Snapshot,
	symbol semantic.Symbol,
) string {
	if symbol.Kind == semantic.GlobalConstantSymbol {
		return strings.TrimPrefix(symbol.FullyQualified, "\\")
	}
	container, found := snapshot.Symbol(symbol.Container)
	if !found {
		return symbol.Name
	}
	return strings.TrimPrefix(container.FullyQualified, "\\") +
		"::" + symbol.Name
}

// Properties returns every property visible on a class, including inherited
// and trait declarations.
func (idx *PHPIndex) Properties(className string) []semantic.Symbol {
	if idx == nil || className == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: idx.SemanticSnapshot(),
	}).All(types.Named(strings.TrimPrefix(className, "\\")))
	var result []semantic.Symbol
	for _, member := range members {
		if member.Symbol.Kind == semantic.PropertySymbol {
			result = append(result, member.Symbol)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return compareFold(result[left].Name, result[right].Name) < 0
	})
	return result
}

// Methods returns every method visible on a class, including inherited and
// trait declarations.
func (idx *PHPIndex) Methods(className string) []semantic.Symbol {
	if idx == nil || className == "" {
		return nil
	}
	members := (resolver.MemberResolver{
		Snapshot: idx.SemanticSnapshot(),
	}).All(types.Named(strings.TrimPrefix(className, "\\")))
	var result []semantic.Symbol
	for _, member := range members {
		if member.Symbol.Kind == semantic.MethodSymbol {
			result = append(result, member.Symbol)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return compareFold(result[left].Name, result[right].Name) < 0
	})
	return result
}

// ClassSymbols returns all source class-like declarations in deterministic
// name/path order. Internal runtime stub classes are omitted.
func (idx *PHPIndex) ClassSymbols() []semantic.Symbol {
	symbols, _ := idx.classCatalog()
	return append([]semantic.Symbol(nil), symbols...)
}

// ClassSymbolsView returns an immutable generation-scoped view of source
// class-like declarations. Callers must not modify the slice or its symbols.
// Repeated diagnostics can share this view without rematerializing the entire
// PHP workspace catalog for every document.
func (idx *PHPIndex) ClassSymbolsView() []semantic.Symbol {
	symbols, _ := idx.classCatalog()
	return symbols
}

func (idx *PHPIndex) classCatalog() ([]semantic.Symbol, []string) {
	if idx == nil || idx.semanticStore == nil {
		return nil, nil
	}
	snapshot := idx.SemanticSnapshot()
	idx.classCatalogMu.Lock()
	defer idx.classCatalogMu.Unlock()
	if idx.classCatalogSnapshot == snapshot {
		return idx.classCatalogSymbols, idx.classCatalogNames
	}
	views := snapshot.GlobalClassViews()
	source := views[:0]
	for _, symbol := range views {
		if symbol.Flags().Has(semantic.InternalFlag) ||
			symbol.Flags().Has(semantic.SyntheticFlag) &&
				!symbol.Flags().Has(semantic.ClassAliasFlag) {
			continue
		}
		source = append(source, symbol)
	}
	sort.Slice(source, func(left, right int) bool {
		if source[left].FullyQualified() == source[right].FullyQualified() {
			return source[left].Path() < source[right].Path()
		}
		return compareFold(
			source[left].FullyQualified(),
			source[right].FullyQualified(),
		) < 0
	})
	symbols := make([]semantic.Symbol, len(source))
	names := make([]string, 0, len(source))
	seenNames := make(map[string]struct{}, len(source))
	for index := range source {
		symbols[index] = source[index].Materialize()
		name := symbols[index].FullyQualified
		key := strings.ToLower(name)
		if _, exists := seenNames[key]; exists {
			continue
		}
		seenNames[key] = struct{}{}
		names = append(names, name)
	}
	idx.classCatalogSnapshot = snapshot
	idx.classCatalogSymbols = symbols
	idx.classCatalogNames = names
	return symbols, names
}

func compareFold(left, right string) int {
	limit := min(len(left), len(right))
	for index := range limit {
		leftByte := left[index]
		rightByte := right[index]
		if leftByte >= 0x80 || rightByte >= 0x80 {
			return strings.Compare(
				strings.ToLower(left),
				strings.ToLower(right),
			)
		}
		if leftByte >= 'A' && leftByte <= 'Z' {
			leftByte += 'a' - 'A'
		}
		if rightByte >= 'A' && rightByte <= 'Z' {
			rightByte += 'a' - 'A'
		}
		if leftByte < rightByte {
			return -1
		}
		if leftByte > rightByte {
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
}

func (idx *PHPIndex) ClassSymbolsIn(path string) []semantic.Symbol {
	var result []semantic.Symbol
	for _, symbol := range idx.SemanticSnapshot().SymbolsIn(path) {
		if symbol.IsClassLike() {
			result = append(result, symbol)
		}
	}
	return result
}

func (idx *PHPIndex) ClassNames() []string {
	_, names := idx.classCatalog()
	return append([]string(nil), names...)
}

// ClassNamesView is the immutable counterpart of ClassNames for request paths
// that only read the workspace class-name catalog.
func (idx *PHPIndex) ClassNamesView() []string {
	_, names := idx.classCatalog()
	return names
}

// SymbolAt resolves a declaration or linked name reference at a byte offset.
func SymbolAt(
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	offset uint32,
) (semantic.Symbol, bool) {
	if document == nil || snapshot == nil {
		return semantic.Symbol{}, false
	}
	for _, reference := range document.References {
		if !rangeContainsCursor(reference.Range, offset) || reference.Resolved == "" {
			continue
		}
		return snapshot.Symbol(reference.Resolved)
	}
	bestWidth := ^uint32(0)
	var best semantic.Symbol
	found := false
	for _, symbol := range document.Symbols {
		rng := symbol.SelectionRange
		if rng.Len() == 0 {
			rng = symbol.Range
		}
		if !rangeContainsCursor(rng, offset) || rng.Len() > bestWidth {
			continue
		}
		best = symbol
		bestWidth = rng.Len()
		found = true
	}
	return best, found
}

func ReferenceAt(document *semantic.Document, offset uint32) (semantic.Reference, bool) {
	if document == nil {
		return semantic.Reference{}, false
	}
	for _, reference := range document.References {
		if rangeContainsCursor(reference.Range, offset) {
			return reference, true
		}
	}
	return semantic.Reference{}, false
}

func rangeContainsCursor(rng cst.TextRange, offset uint32) bool {
	return rng.Contains(offset) || offset == rng.End && rng.End > rng.Start
}

// RegisterTypeExtension adds a framework-specific call inference provider.
func (idx *PHPIndex) RegisterTypeExtension(extension inference.Extension) {
	if idx == nil || extension == nil {
		return
	}
	idx.extensionMu.Lock()
	defer idx.extensionMu.Unlock()
	idx.extensions = append(idx.extensions, extension)
}

func (idx *PHPIndex) typeExtensions() []inference.Extension {
	idx.extensionMu.RLock()
	defer idx.extensionMu.RUnlock()
	result := make([]inference.Extension, 0, len(idx.extensions)+2)
	result = append(result, inference.AttributeContracts)
	result = append(result, idx.extensions...)
	return append(result, inference.CallContracts)
}
