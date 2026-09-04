package semantic

import (
	"errors"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/shopware/shopware-lsp/internal/php/types"
)

// Store publishes immutable workspace snapshots. Per-file updates are cheap;
// readers never observe a partially-mutated symbol graph.
type Store struct {
	mu       sync.Mutex
	revision atomic.Uint64
	// documents is shared with the current immutable snapshot after
	// publication. Every mutating operation clones it before the first write.
	documents map[string]*workspaceDocument
	strings   *workspaceStringInterner
	types     map[string]types.Type
	current   atomic.Pointer[Snapshot]
}

// WorkspaceRestoreCapacity reserves the temporary maps used while publishing
// a streamed cache restore. Hints affect allocation only and may be zero or
// underestimate the final cardinality without changing semantics.
type WorkspaceRestoreCapacity struct {
	Documents int
	Strings   int
	Types     int
}

func NewStore() *Store {
	store := &Store{
		documents: make(map[string]*workspaceDocument),
	}
	store.current.Store(NewSnapshot(0, nil))
	return store
}

func (s *Store) Snapshot() *Snapshot {
	if s == nil {
		return NewSnapshot(0, nil)
	}
	return s.current.Load()
}

func (s *Store) Replace(document *Document) *Snapshot {
	if s == nil || document == nil || document.Path == "" {
		return s.Snapshot()
	}
	return s.ReplaceMany(document)
}

// ReplaceMany atomically publishes a set of persisted document replacements.
// Building one immutable generation for a scanner batch avoids quadratic
// workspace rebuilds during cold indexing and cache restoration.
func (s *Store) ReplaceMany(documents ...*Document) *Snapshot {
	return s.replaceMany(true, documents...)
}

// ReplaceManyOwned transfers already compacted, immutable workspace graphs
// into the store without another projection or defensive clone. Callers must
// not mutate a document after this call.
func (s *Store) ReplaceManyOwned(documents ...*Document) *Snapshot {
	return s.replaceMany(false, documents...)
}

// RestoreOwned replaces the store contents from a streaming loader and
// publishes exactly one immutable generation. The loader transfers ownership
// of each document to the store, so it must not mutate accepted documents.
//
// Cache restoration uses this API to intern a decoded graph immediately
// instead of retaining every uninterned graph until the final publication.
func (s *Store) RestoreOwned(
	load func(accept func(*Document)) error,
) (*Snapshot, error) {
	if s == nil {
		return NewSnapshot(0, nil), errors.New("restore semantic store: nil store")
	}
	if load == nil {
		return s.Snapshot(), errors.New("restore semantic store: nil loader")
	}
	return s.restorePackedOwned(WorkspaceRestoreCapacity{}, func(
		accept func(*workspaceDocument),
	) error {
		return load(func(document *Document) {
			accept(packWorkspaceDocument(document))
		})
	})
}

// RestoreWorkspaceGraphsOwned restores already compacted graphs without
// expanding them into public Documents during cache startup.
func (s *Store) RestoreWorkspaceGraphsOwned(
	load func(accept func(*WorkspaceGraph)) error,
) (*Snapshot, error) {
	return s.RestoreWorkspaceGraphsOwnedWithCapacity(
		WorkspaceRestoreCapacity{},
		load,
	)
}

// RestoreWorkspaceGraphsOwnedWithCapacity is the capacity-aware variant used
// by repositories that can cheaply count their rows before streaming them.
func (s *Store) RestoreWorkspaceGraphsOwnedWithCapacity(
	capacity WorkspaceRestoreCapacity,
	load func(accept func(*WorkspaceGraph)) error,
) (*Snapshot, error) {
	if s == nil {
		return NewSnapshot(0, nil), errors.New("restore semantic store: nil store")
	}
	if load == nil {
		return s.Snapshot(), errors.New("restore semantic store: nil loader")
	}
	return s.restorePackedOwned(capacity, func(
		accept func(*workspaceDocument),
	) error {
		return load(func(graph *WorkspaceGraph) {
			if graph == nil {
				return
			}
			document := graph.document
			graph.document = nil
			accept(document)
		})
	})
}

// RestoreWorkspaceGraphsDecodedWithCapacity lets a streaming loader decode
// graphs through a restore-scoped decoder. String and type values are interned
// directly into the temporary publication maps, avoiding throwaway decoded
// copies before the graph is accepted.
func (s *Store) RestoreWorkspaceGraphsDecodedWithCapacity(
	capacity WorkspaceRestoreCapacity,
	load func(
		decoder *WorkspaceGraphDecoder,
		accept func(*WorkspaceGraph),
	) error,
) (*Snapshot, error) {
	if s == nil {
		return NewSnapshot(0, nil), errors.New("restore semantic store: nil store")
	}
	if load == nil {
		return s.Snapshot(), errors.New("restore semantic store: nil loader")
	}
	return s.restorePackedOwned(capacity, func(
		accept func(*workspaceDocument),
	) error {
		decoder := NewWorkspaceGraphDecoder()
		decoder.stringCache = s.strings
		decoder.typeCache = s.types
		defer decoder.Clear()
		return load(decoder, func(graph *WorkspaceGraph) {
			if graph == nil {
				return
			}
			document := graph.document
			graph.document = nil
			accept(document)
		})
	})
}

func (s *Store) restorePackedOwned(
	capacity WorkspaceRestoreCapacity,
	load func(accept func(*workspaceDocument)) error,
) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	previousDocuments := s.documents
	previousStrings := s.strings
	previousTypes := s.types
	s.documents = make(
		map[string]*workspaceDocument,
		nonNegativeCapacity(capacity.Documents),
	)
	s.strings = newWorkspaceStringInterner(
		nonNegativeCapacity(capacity.Strings),
	)
	s.types = make(
		map[string]types.Type,
		nonNegativeCapacity(capacity.Types),
	)

	accepted := 0
	err := load(func(document *workspaceDocument) {
		if document == nil || document.Path == "" {
			return
		}
		internPackedWorkspaceGraphOwned(
			document,
			s.internString,
			s.internType,
		)
		s.documents[document.Path] = document
		accepted++
	})
	if err != nil {
		s.documents = previousDocuments
		s.strings = previousStrings
		s.types = previousTypes
		return s.current.Load(), err
	}
	if accepted == 0 && len(previousDocuments) == 0 {
		s.strings = nil
		s.types = nil
		return s.current.Load(), nil
	}
	return s.publishLocked(), nil
}

func nonNegativeCapacity(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) replaceMany(clone bool, documents ...*Document) *Snapshot {
	if s == nil {
		return NewSnapshot(0, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginInterningLocked()
	changed := false
	for _, document := range documents {
		if document == nil || document.Path == "" {
			continue
		}
		var packed *workspaceDocument
		if clone {
			graph := ProjectWorkspaceGraph(document)
			if graph == nil {
				continue
			}
			packed = graph.document
		} else {
			packed = packWorkspaceDocument(document)
		}
		internPackedWorkspaceGraphOwned(
			packed,
			s.internString,
			s.internType,
		)
		if !changed {
			s.cloneDocumentsLocked(len(documents))
		}
		s.documents[packed.Path] = packed
		changed = true
	}
	if !changed {
		s.strings = nil
		s.types = nil
		return s.current.Load()
	}
	return s.publishLocked()
}

// ReplaceWorkspaceGraphsOwned atomically publishes graphs that were compacted
// before the scanner batch ended. Ownership of every accepted graph transfers
// to the store.
func (s *Store) ReplaceWorkspaceGraphsOwned(
	graphs ...*WorkspaceGraph,
) *Snapshot {
	if s == nil {
		return NewSnapshot(0, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginInterningLocked()
	changed := false
	for _, graph := range graphs {
		if graph == nil || graph.document == nil || graph.document.Path == "" {
			continue
		}
		document := graph.document
		graph.document = nil
		internPackedWorkspaceGraphOwned(
			document,
			s.internString,
			s.internType,
		)
		if !changed {
			s.cloneDocumentsLocked(len(graphs))
		}
		s.documents[document.Path] = document
		changed = true
	}
	if !changed {
		s.strings = nil
		s.types = nil
		return s.current.Load()
	}
	return s.publishLocked()
}

// ReplaceCanonicalWorkspaceGraphsOwned publishes graphs that were already
// detached and canonicalized together by a WorkspaceGraphDetacher. Ownership
// of every accepted graph transfers to the store. This avoids rebuilding
// temporary interning maps at the end of a large scanner batch.
func (s *Store) ReplaceCanonicalWorkspaceGraphsOwned(
	graphs ...*WorkspaceGraph,
) *Snapshot {
	if s == nil {
		return NewSnapshot(0, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, graph := range graphs {
		if graph == nil || graph.document == nil || graph.document.Path == "" {
			continue
		}
		document := graph.document
		graph.document = nil
		if !changed {
			s.cloneDocumentsLocked(len(graphs))
		}
		s.documents[document.Path] = document
		changed = true
	}
	if !changed {
		return s.current.Load()
	}
	return s.publishLocked()
}

func (s *Store) Remove(paths ...string) *Snapshot {
	if s == nil {
		return NewSnapshot(0, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := false
	for _, path := range paths {
		if _, exists := s.documents[path]; !exists {
			continue
		}
		if !cloned {
			s.cloneDocumentsLocked(0)
			cloned = true
		}
		delete(s.documents, path)
	}
	return s.publishLocked()
}

func (s *Store) Clear() *Snapshot {
	if s == nil {
		return NewSnapshot(0, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.documents = make(map[string]*workspaceDocument)
	s.strings = nil
	s.types = nil
	return s.publishLocked()
}

func (s *Store) beginInterningLocked() {
	s.strings = newWorkspaceStringInterner(0)
	s.types = make(map[string]types.Type)
}

func (s *Store) cloneDocumentsLocked(additionalCapacity int) {
	if additionalCapacity <= 1 {
		s.documents = maps.Clone(s.documents)
		if s.documents == nil {
			s.documents = make(map[string]*workspaceDocument)
		}
		return
	}
	capacity := len(s.documents) + max(0, additionalCapacity)
	cloned := make(map[string]*workspaceDocument, capacity)
	maps.Copy(cloned, s.documents)
	s.documents = cloned
}

func (s *Store) internString(value string) string {
	if value == "" {
		return ""
	}
	if s.strings == nil {
		s.strings = newWorkspaceStringInterner(0)
	}
	return s.strings.Intern(value)
}

func (s *Store) internType(value types.Type) types.Type {
	if s.types == nil {
		s.types = make(map[string]types.Type)
	}
	key := value.Key()
	if interned, ok := s.types[key]; ok {
		return interned
	}
	s.types[key] = value
	return value
}

func (s *Store) Document(path string) (*Document, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	document, ok := s.documents[path]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	return document.materialize().Clone(), true
}

// EnsureDocumentDetails pins the complete persisted payload for path before a
// repository mutation can replace or remove it. Existing immutable snapshots
// can then continue serving their original generation after publication.
func (s *Store) EnsureDocumentDetails(path string) error {
	if s == nil || path == "" {
		return nil
	}
	s.mu.Lock()
	document := s.documents[path]
	s.mu.Unlock()
	if document == nil || document.lazyDetails == nil {
		return nil
	}
	return document.pinFullDocument()
}

func (s *Store) publishLocked() *Snapshot {
	revision := s.revision.Add(1)
	documents := make([]*workspaceDocument, 0, len(s.documents))
	paths := make([]string, 0, len(s.documents))
	for path := range s.documents {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		documents = append(documents, s.documents[path])
	}
	previous := s.current.Load()
	snapshot := newSnapshotWithPathRefs(revision, documents, s.documents)
	s.current.Store(snapshot)
	if previous != nil && previous.dynamicNames != snapshot.dynamicNames {
		previous.dynamicNames.clear()
	}
	// The immutable documents now own every canonical value. Retaining the
	// build-time lookup tables would duplicate tens of MiB of map metadata for
	// the lifetime of the workspace. A later batch creates its own short-lived
	// interning scope.
	s.strings = nil
	s.types = nil
	return snapshot
}
