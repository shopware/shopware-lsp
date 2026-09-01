package indexer

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
)

type fileCandidate struct {
	path  string
	state *storedFileState
}

type skippedFileWork struct {
	stats   SkippedFileStats
	tracked bool
}

type fileIndexRun struct {
	scanner      *FileScanner
	ctx          context.Context
	files        []string
	storedStates []storedFileState

	resultMu      sync.Mutex
	resultErrors  []error
	updatedStates []fileState
	skippedFiles  []skippedFileWork
	batchIndexers []BatchIndexer
}

func (fs *FileScanner) indexFiles(
	ctx context.Context,
	files []string,
	filterPaths bool,
	storedStates []storedFileState,
) (returnErr error) {
	if len(files) == 0 {
		return nil
	}
	fs.operationMu.Lock()
	defer fs.operationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if filterPaths {
		files = fs.filteredIndexPaths(files)
	}
	if len(files) == 0 {
		return nil
	}
	fs.clearSkippedFiles(files)

	run := &fileIndexRun{
		scanner:      fs,
		ctx:          ctx,
		files:        files,
		storedStates: storedStates,
		updatedStates: make(
			[]fileState,
			0,
			len(files),
		),
	}
	run.beginIndexerBatches()
	defer func() {
		returnErr = errors.Join(returnErr, run.finishIndexerBatches())
	}()
	if err := run.loadStoredStates(); err != nil {
		return err
	}
	if !run.runWorkers() {
		return errors.Join(run.resultErrors...)
	}
	run.commitSkippedFiles()
	run.commitFileStates()
	return errors.Join(run.resultErrors...)
}

func (fs *FileScanner) filteredIndexPaths(files []string) []string {
	filtered := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, path := range files {
		_, duplicate := seen[path]
		if !fs.shouldIndexPath(path) || duplicate {
			continue
		}
		filtered = append(filtered, path)
		seen[path] = struct{}{}
	}
	slices.Sort(filtered)
	return filtered
}

func (run *fileIndexRun) beginIndexerBatches() {
	for _, idx := range run.scanner.indexer {
		if batchIndexer, ok := idx.(BatchIndexer); ok {
			batchIndexer.BeginIndexingBatch(run.files)
			run.batchIndexers = append(run.batchIndexers, batchIndexer)
		}
	}
}

func (run *fileIndexRun) finishIndexerBatches() error {
	var result error
	for index := len(run.batchIndexers) - 1; index >= 0; index-- {
		result = errors.Join(result, run.batchIndexers[index].EndIndexingBatch())
	}
	cst.ReleaseTransientBuffers()
	parsekit.ReleaseTransientBuffers()
	clearMessagePackBuffers()
	const largeBatchReclaimThreshold = 8192
	if len(run.updatedStates) >= largeBatchReclaimThreshold {
		// Cold workspace indexing leaves large parser, binder, and persistence
		// slabs dead at the batch lifecycle boundary.
		debug.FreeOSMemory()
	}
	return result
}

func (run *fileIndexRun) loadStoredStates() error {
	const bulkStateLookupThreshold = 1024
	if run.storedStates != nil || len(run.files) < bulkStateLookupThreshold {
		return nil
	}
	states, err := run.scanner.loadFileStates(run.ctx, run.files)
	if err != nil {
		return fmt.Errorf("load file states: %w", err)
	}
	run.storedStates = states
	return nil
}

func (run *fileIndexRun) workerCount() int {
	count := defaultIndexWorkerCount(runtime.NumCPU())
	if run.scanner.workerCount > 0 {
		count = run.scanner.workerCount
	}
	return min(count, len(run.files))
}

func (run *fileIndexRun) recordError(err error) {
	if err == nil {
		return
	}
	run.resultMu.Lock()
	run.resultErrors = append(run.resultErrors, err)
	run.resultMu.Unlock()
}

func (run *fileIndexRun) recordSkipped(file SkippedFileStats, tracked bool) {
	run.resultMu.Lock()
	run.skippedFiles = append(run.skippedFiles, skippedFileWork{
		stats: file, tracked: tracked,
	})
	run.resultMu.Unlock()
}

func (run *fileIndexRun) commitSkippedFiles() {
	if len(run.skippedFiles) == 0 {
		return
	}
	stats := make([]SkippedFileStats, 0, len(run.skippedFiles))
	tracked := make([]string, 0, len(run.skippedFiles))
	for _, file := range run.skippedFiles {
		stats = append(stats, file.stats)
		if file.tracked {
			tracked = append(tracked, file.stats.Path)
		}
	}
	if len(tracked) > 0 {
		if err := run.scanner.removeFilesLocked(run.ctx, tracked); err != nil {
			run.recordError(fmt.Errorf("remove oversized index entries: %w", err))
		}
	}
	run.scanner.recordSkippedFiles(stats)
	for _, file := range stats[:min(len(stats), fileStatsLimit)] {
		logSkippedFile(file)
	}
	if len(stats) > fileStatsLimit {
		logSkippedFileCount(len(stats))
	}
}

// runWorkers returns false when the producer observed cancellation. That path
// intentionally skips file-state publication, matching the pre-refactor
// lifecycle even if a worker had already prepared a partial batch.
func (run *fileIndexRun) runWorkers() bool {
	fileChan := make(chan fileCandidate, 100)
	var workers sync.WaitGroup
	for range run.workerCount() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			run.worker(fileChan)
		}()
	}
	for index, path := range run.files {
		candidate := fileCandidate{path: path}
		if run.storedStates != nil {
			candidate.state = &run.storedStates[index]
		}
		select {
		case <-run.ctx.Done():
			run.recordError(run.ctx.Err())
			close(fileChan)
			workers.Wait()
			return false
		case fileChan <- candidate:
		}
	}
	close(fileChan)
	workers.Wait()
	return true
}

func (run *fileIndexRun) worker(fileChan <-chan fileCandidate) {
	batch := make([]fileWork, 0, maxPreparationBatchFiles)
	batchBytes := 0
	flush := func() {
		run.processBatch(batch)
		batch = batch[:0]
		batchBytes = 0
	}
	for {
		var candidate fileCandidate
		var ok bool
		select {
		case <-run.ctx.Done():
			run.recordError(run.ctx.Err())
			flush()
			return
		case candidate, ok = <-fileChan:
			if !ok {
				flush()
				return
			}
		}
		needsIndexing, content, info, skipped, tracked, err :=
			run.scanner.fileNeedsIndexing(
				run.ctx,
				candidate.path,
				candidate.state,
			)
		if err != nil {
			run.recordError(fmt.Errorf("read %s: %w", candidate.path, err))
			continue
		}
		if skipped != nil {
			run.recordSkipped(*skipped, tracked)
			continue
		}
		if !needsIndexing {
			continue
		}
		if preparationBatchWouldOverflow(len(batch), batchBytes, len(content)) {
			flush()
		}
		batch = append(batch, fileWork{
			path: candidate.path, content: content, info: info,
		})
		batchBytes += len(content)
		if preparationBatchReady(len(batch), batchBytes) {
			flush()
		}
	}
}

func (run *fileIndexRun) processBatch(items []fileWork) {
	if len(items) == 0 {
		return
	}
	prepared := run.prepareBatch(items)
	if len(prepared) == 0 {
		return
	}
	defer releasePreparedFiles(prepared)
	var successful []fileState
	if run.scanner.store == nil {
		successful = run.indexWithoutStore(prepared)
	} else {
		successful = run.indexWithStore(prepared)
	}
	if len(successful) == 0 {
		return
	}
	run.resultMu.Lock()
	run.updatedStates = append(run.updatedStates, successful...)
	run.resultMu.Unlock()
}

func (run *fileIndexRun) prepareBatch(items []fileWork) []preparedFileWork {
	prepared := make([]preparedFileWork, 0, len(items))
	for _, item := range items {
		if err := run.ctx.Err(); err != nil {
			run.recordError(err)
			break
		}
		file := NewParsedFile(item.path, item.content)
		if run.scanner.shouldPreparsePath(item.path) {
			_ = file.SyntaxTree()
		}
		preparedValues, err := run.prepareFile(file)
		file.clearMemoized()
		if err != nil {
			file.releaseSyntaxStorage()
			run.recordError(err)
			continue
		}
		prepared = append(prepared, preparedFileWork{
			file: file, info: item.info, prepared: preparedValues,
		})
	}
	return prepared
}

func (run *fileIndexRun) prepareFile(file *ParsedFile) ([]any, error) {
	values := make([]any, len(run.scanner.indexer))
	var resultErrors []error
	for index, idx := range run.scanner.indexer {
		preparer, ok := idx.(PreparingIndexer)
		if !ok {
			continue
		}
		value, err := preparer.Prepare(file)
		if err != nil {
			resultErrors = append(resultErrors, fmt.Errorf(
				"%s prepare %s: %w",
				idx.ID(),
				file.Path,
				err,
			))
			continue
		}
		values[index] = value
	}
	return values, errors.Join(resultErrors...)
}

func releasePreparedFiles(prepared []preparedFileWork) {
	for _, item := range prepared {
		item.file.releaseSyntaxStorage()
	}
}

func (run *fileIndexRun) indexWithoutStore(prepared []preparedFileWork) []fileState {
	successful := make([]fileState, 0, len(prepared))
	for _, item := range prepared {
		if err := errors.Join(run.indexFile(item)...); err != nil {
			run.recordError(err)
			continue
		}
		successful = append(successful, preparedFileState(item))
	}
	return successful
}

func (run *fileIndexRun) indexFile(item preparedFileWork) []error {
	var resultErrors []error
	for index, idx := range run.scanner.indexer {
		if err := run.ctx.Err(); err != nil {
			resultErrors = append(resultErrors, err)
			break
		}
		var err error
		if preparer, ok := idx.(PreparingIndexer); ok {
			err = preparer.IndexPrepared(item.file, item.prepared[index])
		} else {
			err = idx.Index(item.file)
		}
		if err != nil {
			resultErrors = append(resultErrors, fmt.Errorf(
				"%s index %s: %w",
				idx.ID(),
				item.file.Path,
				err,
			))
		}
	}
	return resultErrors
}

func (run *fileIndexRun) indexWithStore(prepared []preparedFileWork) []fileState {
	successful := make([]fileState, 0, len(prepared))
	var indexBatch func([]preparedFileWork)
	indexBatch = func(batch []preparedFileWork) {
		if len(batch) == 0 {
			return
		}
		if err := run.ctx.Err(); err != nil {
			run.recordError(err)
			return
		}
		mutation, err := run.scanner.store.BeginMutation(run.ctx)
		if err != nil {
			run.recordError(fmt.Errorf("begin indexing %s: %w", batch[0].file.Path, err))
			return
		}
		indexErrors := run.indexMutationBatch(mutation, batch)
		if len(indexErrors) > 0 {
			if rollbackErr := mutation.Rollback(); rollbackErr != nil {
				indexErrors = append(indexErrors, rollbackErr)
			}
			for _, item := range batch {
				item.file.setMutation(nil)
			}
			if len(batch) > 1 && run.ctx.Err() == nil {
				middle := len(batch) / 2
				indexBatch(batch[:middle])
				indexBatch(batch[middle:])
				return
			}
			run.recordError(errors.Join(indexErrors...))
			return
		}
		if err := run.ctx.Err(); err != nil {
			run.recordError(errors.Join(err, mutation.Rollback()))
			return
		}
		if err := mutation.Commit(); err != nil {
			run.recordError(fmt.Errorf(
				"commit indexing batch starting at %s: %w",
				batch[0].file.Path,
				err,
			))
			return
		}
		for _, item := range batch {
			successful = append(successful, preparedFileState(item))
		}
	}
	indexBatch(prepared)
	return successful
}

func (run *fileIndexRun) indexMutationBatch(
	mutation *Mutation,
	batch []preparedFileWork,
) []error {
	var indexErrors []error
	symbolDocuments := make([]WorkspaceSymbolDocument, 0, len(batch))
	for _, item := range batch {
		item.file.clearWorkspaceSymbols()
		item.file.setMutation(mutation)
		indexErrors = append(indexErrors, run.indexFile(item)...)
		if len(indexErrors) > 0 {
			break
		}
		if run.scanner.symbols == nil {
			continue
		}
		symbols, err := run.workspaceSymbols(item)
		if err != nil {
			indexErrors = append(indexErrors, err)
			break
		}
		symbolDocuments = append(symbolDocuments, WorkspaceSymbolDocument{
			Path: item.file.Path, Symbols: symbols,
		})
	}
	if len(indexErrors) == 0 && run.scanner.symbols != nil {
		if err := run.scanner.symbols.ReplaceFilesIn(mutation, symbolDocuments); err != nil {
			indexErrors = append(indexErrors, fmt.Errorf(
				"catalog workspace symbol batch starting at %s: %w",
				batch[0].file.Path,
				err,
			))
		}
	}
	return indexErrors
}

func (run *fileIndexRun) workspaceSymbols(
	item preparedFileWork,
) ([]WorkspaceSymbol, error) {
	symbols := append([]WorkspaceSymbol(nil), item.file.collectedWorkspaceSymbols()...)
	for index, idx := range run.scanner.indexer {
		contributor, ok := idx.(WorkspaceSymbolContributor)
		if !ok {
			continue
		}
		current, err := contributor.WorkspaceSymbols(item.file, item.prepared[index])
		if err != nil {
			return nil, fmt.Errorf(
				"%s workspace symbols %s: %w",
				idx.ID(),
				item.file.Path,
				err,
			)
		}
		symbols = append(symbols, current...)
	}
	return symbols, nil
}

func preparedFileState(item preparedFileWork) fileState {
	return fileState{path: item.file.Path, info: item.info}
}

func (run *fileIndexRun) commitFileStates() {
	if len(run.updatedStates) == 0 {
		return
	}
	if err := run.scanner.updateFileStates(run.ctx, run.updatedStates); err != nil {
		run.recordError(fmt.Errorf("commit file state: %w", err))
		return
	}
	if run.scanner.onUpdate != nil {
		run.scanner.onUpdate()
	}
}
