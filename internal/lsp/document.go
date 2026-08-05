package lsp

import (
	"sync"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
)

// TextDocument represents a document open in the editor
type TextDocument struct {
	URI            string
	Text           []byte
	Source         string
	Version        int
	SyntaxTree     *cst.Tree
	SyntaxLanguage language.ID
	ParseErrors    []parsekit.Error
	LineIndex      *cst.LineIndex
}

// DocumentManager manages text documents
type DocumentManager struct {
	documents map[string]*TextDocument
	observers []DocumentObserver
	mu        sync.RWMutex
	languages *language.Registry
}

// DocumentObserver receives immutable open-document snapshots after the
// document manager has published them. Domain indexes can use this narrow
// boundary for request-local overlays without importing LSP state or writing
// editor buffers into their persistent workspace generation.
type DocumentObserver struct {
	DidOpenOrChange func(*TextDocument)
	DidClose        func(string)
}

// NewDocumentManager creates a new document manager
func NewDocumentManager() *DocumentManager {
	return NewDocumentManagerWithRegistry(language.DefaultRegistry())
}

func NewDocumentManagerWithRegistry(registry *language.Registry) *DocumentManager {
	if registry == nil {
		registry = language.DefaultRegistry()
	}
	return &DocumentManager{
		documents: make(map[string]*TextDocument),
		languages: registry,
	}
}

// OpenDocument adds or updates a document
func (m *DocumentManager) OpenDocument(uri string, text string, version int) {
	doc := NewTextDocumentWithRegistry(m.languages, uri, text, version)
	m.publishDocument(doc)
}

// UpdateDocument updates an existing document
func (m *DocumentManager) UpdateDocument(uri string, text string, version int) {
	doc := NewTextDocumentWithRegistry(m.languages, uri, text, version)
	m.publishDocument(doc)
}

func (m *DocumentManager) publishDocument(doc *TextDocument) {
	if doc == nil {
		return
	}
	m.mu.Lock()
	m.documents[doc.URI] = doc
	observers := append([]DocumentObserver(nil), m.observers...)
	m.mu.Unlock()

	for _, observer := range observers {
		if observer.DidOpenOrChange != nil {
			observer.DidOpenOrChange(doc)
		}
	}
}

// RegisterObserver subscribes to document lifecycle changes and immediately
// replays documents which were already open. Replaying makes workspace
// initialization order irrelevant while retaining synchronous change
// delivery before diagnostics and interactive requests are scheduled.
func (m *DocumentManager) RegisterObserver(observer DocumentObserver) {
	if observer.DidOpenOrChange == nil && observer.DidClose == nil {
		return
	}
	m.mu.Lock()
	m.observers = append(m.observers, observer)
	documents := make([]*TextDocument, 0, len(m.documents))
	for _, document := range m.documents {
		documents = append(documents, document)
	}
	m.mu.Unlock()

	if observer.DidOpenOrChange != nil {
		for _, document := range documents {
			observer.DidOpenOrChange(document)
		}
	}
}

func NewTextDocument(uri, source string, version int) *TextDocument {
	return NewTextDocumentWithRegistry(
		language.DefaultRegistry(),
		uri,
		source,
		version,
	)
}

func NewTextDocumentWithRegistry(
	registry *language.Registry,
	uri,
	source string,
	version int,
) *TextDocument {
	if registry == nil {
		registry = language.DefaultRegistry()
	}
	doc := &TextDocument{
		URI:       uri,
		Text:      []byte(source),
		Source:    source,
		Version:   version,
		LineIndex: cst.NewLineIndex(source),
	}

	if id, result, ok := registry.ParsePath(uri, source); ok {
		doc.SyntaxLanguage = id
		doc.SyntaxTree = result.Tree
		doc.ParseErrors = result.Errors
	}
	return doc
}

// CloseDocument removes a document
func (m *DocumentManager) CloseDocument(uri string) {
	m.mu.Lock()
	delete(m.documents, uri)
	observers := append([]DocumentObserver(nil), m.observers...)
	m.mu.Unlock()

	for _, observer := range observers {
		if observer.DidClose != nil {
			observer.DidClose(uri)
		}
	}
}

// GetDocument returns a document by URI
func (m *DocumentManager) GetDocument(uri string) (*TextDocument, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	doc, ok := m.documents[uri]
	return doc, ok
}

func (m *DocumentManager) SyntaxContext(
	uri string,
	line,
	character int,
) (SyntaxContext, bool) {
	document, ok := m.GetDocument(uri)
	if !ok {
		return SyntaxContext{}, false
	}
	return newSyntaxContext(document, line, character), true
}

func (m *DocumentManager) Documents() []*TextDocument {
	m.mu.RLock()
	defer m.mu.RUnlock()

	documents := make([]*TextDocument, 0, len(m.documents))
	for _, document := range m.documents {
		documents = append(documents, document)
	}
	return documents
}

// GetDocumentText returns the text of a document by URI
func (m *DocumentManager) GetDocumentText(uri string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if doc, ok := m.documents[uri]; ok {
		return doc.Text, true
	}
	return nil, false
}

func syntaxAtPosition(tree *cst.Tree, lineIndex *cst.LineIndex, line int, character int) (*cst.Node, *cst.Token, *cst.Node) {
	if tree == nil || tree.Root == nil {
		return nil, nil, nil
	}

	offset := lineIndex.OffsetUTF16(uint32(line), uint32(character))
	token := tree.Root.TokenAtOffset(offset)
	node := tree.Root.NodeAtOffset(offset)

	// At EOF there is no token on the right side of the cursor. Use the final
	// token as editor context so incomplete input still has useful context.
	if token == nil && offset > tree.Root.Range().Start {
		token = tree.Root.TokenAtOffset(offset - 1)
		node = tree.Root.NodeAtOffset(offset - 1)
	}
	for token != nil && token.Kind().IsTrivia() && token.Range().Start > tree.Root.Range().Start {
		previousOffset := token.Range().Start - 1
		previousToken := tree.Root.TokenAtOffset(previousOffset)
		if previousToken == nil || previousToken == token {
			break
		}
		token = previousToken
		node = tree.Root.NodeAtOffset(previousOffset)
	}

	return tree.Root, token, node
}

// Close closes the document manager and frees resources
func (m *DocumentManager) Close() {
	m.mu.Lock()
	observers := append([]DocumentObserver(nil), m.observers...)
	uris := make([]string, 0, len(m.documents))
	for uri := range m.documents {
		uris = append(uris, uri)
	}
	m.documents = make(map[string]*TextDocument)
	m.observers = nil
	m.mu.Unlock()

	for _, uri := range uris {
		for _, observer := range observers {
			if observer.DidClose != nil {
				observer.DidClose(uri)
			}
		}
	}
}
