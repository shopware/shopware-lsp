package indexer

import (
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// ParsedFile is the immutable input shared by every indexer handling one file.
// Syntax and line data are constructed lazily and at most once, so multiple
// indexers never repeat the same frontend work.
type ParsedFile struct {
	Path    string
	Content []byte
	Source  string

	extension  string
	languageID language.ID
	parser     language.Parser

	syntaxOnce sync.Once
	syntaxTree *cst.Tree

	lineIndexOnce    sync.Once
	lineIndex        *cst.LineIndex
	memoizedMu       sync.Mutex
	memoizedFirst    memoizedValue
	memoizedMore     map[any]*memoizedValue
	mutation         *Mutation
	workspaceSymbols []WorkspaceSymbol
}

// AddWorkspaceSymbols publishes declarations discovered while an indexer is
// already processing this file. The scanner persists them with the same file
// transaction, so domain indexers do not need a second parsing pass.
func (f *ParsedFile) AddWorkspaceSymbols(symbols ...WorkspaceSymbol) {
	if f == nil || len(symbols) == 0 {
		return
	}
	f.workspaceSymbols = append(f.workspaceSymbols, symbols...)
}

func (f *ParsedFile) collectedWorkspaceSymbols() []WorkspaceSymbol {
	if f == nil {
		return nil
	}
	return f.workspaceSymbols
}

func (f *ParsedFile) clearWorkspaceSymbols() {
	if f != nil {
		f.workspaceSymbols = nil
	}
}

type memoizedValue struct {
	key   any
	ready chan struct{}
	value any
}

func NewParsedFile(path string, content []byte) *ParsedFile {
	return NewParsedFileWithRegistry(language.DefaultRegistry(), path, content)
}

func NewParsedFileWithRegistry(
	registry *language.Registry,
	path string,
	content []byte,
) *ParsedFile {
	if registry == nil {
		registry = language.DefaultRegistry()
	}
	file := &ParsedFile{
		Path:    path,
		Content: content,
		// ParsedFile owns immutable input for its lifetime. Reuse the byte
		// backing store for parser-facing text instead of duplicating every
		// indexed source file.
		Source:    unsafe.String(unsafe.SliceData(content), len(content)),
		extension: strings.ToLower(filepath.Ext(path)),
	}
	if definition, ok := registry.ForPath(path); ok {
		file.languageID = definition.ID
		file.parser = definition.Parse
	}
	return file
}

func (f *ParsedFile) Extension() string {
	if f == nil {
		return ""
	}
	return f.extension
}

func (f *ParsedFile) Language() language.ID {
	if f == nil {
		return ""
	}
	return f.languageID
}

// SyntaxTree returns the language frontend result selected from the extension.
// Unsupported files return nil.
func (f *ParsedFile) SyntaxTree() *cst.Tree {
	if f == nil {
		return nil
	}
	f.syntaxOnce.Do(func() {
		if f.parser != nil {
			f.syntaxTree = f.parser(f.Source).Tree
		}
	})
	return f.syntaxTree
}

func (f *ParsedFile) LineIndex() *cst.LineIndex {
	if f == nil {
		return nil
	}
	f.lineIndexOnce.Do(func() {
		f.lineIndex = cst.NewLineIndex(f.Source)
	})
	return f.lineIndex
}

// Memoized returns one file-lifetime derived value for key. Concurrent
// consumers share the same computation, which lets independent indexers reuse
// expensive read-only analysis without coupling their prepared value types.
//
// Keys must be comparable. A component pointer is normally the safest key
// because it also keeps values from separate workspace instances isolated.
func (f *ParsedFile) Memoized(key any, compute func() any) any {
	if f == nil {
		return nil
	}
	if compute == nil {
		return nil
	}
	entry, loaded := f.memoizedEntry(key)
	if loaded {
		<-entry.ready
		return entry.value
	}
	defer close(entry.ready)
	entry.value = compute()
	return entry.value
}

func (f *ParsedFile) memoizedEntry(key any) (*memoizedValue, bool) {
	f.memoizedMu.Lock()
	defer f.memoizedMu.Unlock()

	if f.memoizedFirst.ready == nil {
		f.memoizedFirst = memoizedValue{
			key:   key,
			ready: make(chan struct{}),
		}
		return &f.memoizedFirst, false
	}
	if f.memoizedFirst.key == key {
		return &f.memoizedFirst, true
	}
	if entry, exists := f.memoizedMore[key]; exists {
		return entry, true
	}
	if f.memoizedMore == nil {
		f.memoizedMore = make(map[any]*memoizedValue)
	}
	entry := &memoizedValue{
		key:   key,
		ready: make(chan struct{}),
	}
	f.memoizedMore[key] = entry
	return entry, false
}

// clearMemoized releases heavyweight cross-indexer analysis after the
// read-only preparation phase. Prepared index values must contain everything
// needed by their persistence phase. The caller must wait for all Memoized
// computations to finish before clearing; FileScanner prepares one file
// sequentially and treats this call as that phase boundary.
func (f *ParsedFile) clearMemoized() {
	if f == nil {
		return
	}
	f.memoizedMu.Lock()
	f.memoizedFirst = memoizedValue{}
	f.memoizedMore = nil
	f.memoizedMu.Unlock()
}

func (f *ParsedFile) releaseSyntaxStorage() {
	if f == nil || f.syntaxTree == nil {
		return
	}
	f.syntaxTree.ReleaseTransientStorage()
	f.syntaxTree = nil
}

// Mutation returns the workspace-store transaction assigned by FileScanner.
// It is nil when the file is indexed outside a coordinated scanner run.
func (f *ParsedFile) Mutation() *Mutation {
	if f == nil {
		return nil
	}
	return f.mutation
}

func (f *ParsedFile) setMutation(mutation *Mutation) {
	f.mutation = mutation
}
