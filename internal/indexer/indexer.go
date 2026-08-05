package indexer

type Indexer interface {
	ID() string
	Index(file *ParsedFile) error
	RemovedFiles(paths []string) error
	Close() error
	Clear() error
}

// PreparingIndexer moves CPU- and allocation-heavy, read-only work ahead of
// the workspace write transaction. Prepare may run concurrently for different
// files. IndexPrepared must only persist the prepared value and arrange
// post-commit publication.
type PreparingIndexer interface {
	Prepare(file *ParsedFile) (any, error)
	IndexPrepared(file *ParsedFile, prepared any) error
}

// SupplementalPathIndexer lets an indexer opt into files outside the syntax
// registry and selectively reopen otherwise skipped directories. This is used
// for non-code workspace resources such as public assets without making every
// indexer scan generated media, caches, or arbitrary binary files.
type SupplementalPathIndexer interface {
	ShouldEnterDirectory(path string) bool
	ShouldIndexPath(path string) bool
}

// SupplementalSyntaxIndexer lets a supplemental path indexer request the
// normal ahead-of-transaction syntax preparation for selected files. Pure
// resource files can omit this interface so generated JavaScript, JSON, and
// other parseable assets are not needlessly turned into CSTs.
type SupplementalSyntaxIndexer interface {
	ShouldPreparsePath(path string) bool
}

// TransactionalRemover and TransactionalClearer let the coordinator group
// changes across all namespaced repositories in the shared workspace store.
// The legacy methods remain for standalone use and compatibility tests.
type TransactionalRemover interface {
	RemovedFilesIn(paths []string, mutation *Mutation) error
}

type TransactionalClearer interface {
	ClearIn(mutation *Mutation) error
}

// BatchIndexer is notified around one coordinated IndexFiles run. Indexers
// with expensive immutable publication can accumulate committed file updates
// and publish one workspace generation when the batch finishes.
//
// candidateFiles is the filtered, sorted input and may be inspected only for
// allocation hints; implementations must not retain or mutate it.
//
// EndIndexingBatch is always called for every successful begin, including when
// indexing is canceled or one of the files fails.
type BatchIndexer interface {
	BeginIndexingBatch(candidateFiles []string)
	EndIndexingBatch() error
}
