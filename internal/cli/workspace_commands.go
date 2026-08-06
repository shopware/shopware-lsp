package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shopware/shopware-lsp/internal/app"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func (r *Runner) runIndex(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("index", flag.ContinueOnError)
	flags.SetOutput(r.errOut)
	force := flags.Bool("force", false, "clear file hashes and rebuild all indexes")
	if err := flags.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() != 0 {
		return usageError("index takes no positional arguments; use -root before the command")
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	result := session.initialIndex
	if *force {
		var response interface{}
		if err := session.call(ctx, "shopware/forceReindex", struct{}{}, &response); err != nil {
			return err
		}
		result, err = session.waitForIndex(ctx)
		if err != nil {
			return err
		}
	}
	var scanner indexer.FileScannerStats
	if err := session.call(ctx, "shopware/index/stats", struct{}{}, &scanner); err != nil {
		return err
	}
	output := map[string]interface{}{
		"root": session.root, "forced": *force,
		"timeInSeconds": result.TimeInSeconds,
		"index":         scanner,
	}
	if r.json {
		return writeJSON(r.out, output)
	}
	return writeFormatted(
		r.out,
		"Indexed %d files (%s) with %d indexers in %s\n",
		scanner.TrackedFiles,
		formatBytes(scanner.TrackedBytes),
		scanner.Indexers,
		time.Duration(result.TimeInSeconds*float64(time.Second)).Round(time.Millisecond),
	)
}

type cacheStats struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

type memoryStats struct {
	AllocBytes      uint64 `json:"allocBytes"`
	HeapAllocBytes  uint64 `json:"heapAllocBytes"`
	HeapInUseBytes  uint64 `json:"heapInUseBytes"`
	HeapObjects     uint64 `json:"heapObjects"`
	SystemBytes     uint64 `json:"systemBytes"`
	TotalAllocBytes uint64 `json:"totalAllocBytes"`
	Collections     uint32 `json:"collections"`
}

func (r *Runner) runStats(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return usageError("stats takes no arguments")
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	var scanner indexer.FileScannerStats
	if err := session.call(ctx, "shopware/index/stats", struct{}{}, &scanner); err != nil {
		return err
	}
	cacheDir, err := app.ProjectCacheFolder(session.root)
	if err != nil {
		return err
	}
	cache, err := inspectCache(cacheDir)
	if err != nil {
		return err
	}
	var runtimeMemory runtime.MemStats
	runtime.ReadMemStats(&runtimeMemory)
	memory := memoryStats{
		AllocBytes: runtimeMemory.Alloc, HeapAllocBytes: runtimeMemory.HeapAlloc,
		HeapInUseBytes: runtimeMemory.HeapInuse, HeapObjects: runtimeMemory.HeapObjects,
		SystemBytes: runtimeMemory.Sys, TotalAllocBytes: runtimeMemory.TotalAlloc,
		Collections: runtimeMemory.NumGC,
	}
	result := map[string]interface{}{
		"version":               r.options.Version,
		"root":                  session.root,
		"initialIndexInSeconds": session.initialIndex.TimeInSeconds,
		"index":                 scanner,
		"cache":                 cache,
		"memory":                memory,
	}
	if r.json {
		return writeJSON(r.out, result)
	}
	return writeFormatted(
		r.out,
		"Workspace:       %s\n"+
			"Index duration:  %s\n"+
			"Tracked files:   %d (%s)\n"+
			"Indexers:        %d\n"+
			"Index workers:   %d\n"+
			"Cache:           %s in %d files\n"+
			"Heap allocated:  %s (%d objects)\n"+
			"Heap in use:     %s\n"+
			"Runtime system:  %s\n"+
			"Total allocated: %s\n"+
			"GC collections:  %d\n",
		session.root,
		time.Duration(session.initialIndex.TimeInSeconds*float64(time.Second)).Round(time.Millisecond),
		scanner.TrackedFiles, formatBytes(scanner.TrackedBytes),
		scanner.Indexers, scanner.Workers,
		formatBytes(cache.Bytes), cache.Files,
		formatBytes(int64(memory.HeapAllocBytes)), memory.HeapObjects,
		formatBytes(int64(memory.HeapInUseBytes)),
		formatBytes(int64(memory.SystemBytes)),
		formatBytes(int64(memory.TotalAllocBytes)),
		memory.Collections,
	)
}

func inspectCache(path string) (cacheStats, error) {
	result := cacheStats{Path: path}
	err := filepath.WalkDir(path, func(
		_ string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result.Files++
		result.Bytes += info.Size()
		return nil
	})
	return result, err
}

func formatBytes(bytes int64) string {
	const unit = int64(1024)
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, suffix := range units {
		value /= float64(unit)
		if value < float64(unit) || suffix == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%d B", bytes)
}

type diagnosticOutput struct {
	URI        string                                  `json:"uri"`
	Diagnostic protocol.Diagnostic                     `json:"diagnostic"`
	Related    []protocol.DiagnosticRelatedInformation `json:"related,omitempty"`
}

func (r *Runner) runCheck(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(r.errOut)
	severityName := flags.String("severity", "", "minimum severity: hint, info, warning, error (defaults to project configuration)")
	failOnName := flags.String("fail-on", "", "exit unsuccessfully when this severity or higher is reported (defaults to project configuration)")
	workers := flags.Int("workers", min(runtime.GOMAXPROCS(0), 4), "maximum concurrent diagnostic requests")
	if err := flags.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if flags.NArg() == 0 {
		return usageError("check expects at least one file or directory")
	}
	if *workers < 1 {
		return usageError("check workers must be at least 1")
	}
	paths, err := resolveCheckFiles(ctx, flags.Args())
	if err != nil {
		return err
	}
	session, err := r.connect(ctx)
	if err != nil {
		return err
	}
	defer closeIgnoringError(session)
	effectiveSeverity := *severityName
	if effectiveSeverity == "" {
		effectiveSeverity = string(session.configuration.Effective.Check.Severity)
	}
	cutoff, err := diagnosticSeverity(effectiveSeverity)
	if err != nil {
		return err
	}
	effectiveFailOn := *failOnName
	if effectiveFailOn == "" {
		effectiveFailOn = string(session.configuration.Effective.Check.FailOn)
	}
	var failOn protocol.DiagnosticSeverity
	if effectiveFailOn != "" && !strings.EqualFold(effectiveFailOn, "off") {
		failOn, err = diagnosticSeverity(effectiveFailOn)
		if err != nil {
			return err
		}
	}
	if len(paths) == 0 {
		if r.json {
			return writeJSON(r.out, []diagnosticOutput{})
		}
		return nil
	}
	type fileResult struct {
		findings []diagnosticOutput
		err      error
	}
	results := make([]fileResult, len(paths))
	jobs := make(chan int, len(paths))
	for index := range paths {
		jobs <- index
	}
	close(jobs)
	workerCount := min(*workers, len(paths))
	var wait sync.WaitGroup
	wait.Add(workerCount)
	for range workerCount {
		go func() {
			defer wait.Done()
			for index := range jobs {
				results[index].findings, results[index].err =
					session.checkDocument(ctx, paths[index], cutoff)
			}
		}()
	}
	wait.Wait()
	var findings []diagnosticOutput
	var checkErrors []error
	for _, result := range results {
		findings = append(findings, result.findings...)
		checkErrors = append(checkErrors, result.err)
	}
	if err := errors.Join(checkErrors...); err != nil {
		return err
	}
	if r.json {
		if err := writeJSON(r.out, findings); err != nil {
			return err
		}
		if findingsAtSeverity(findings, failOn) {
			return &exitError{code: 1, err: fmt.Errorf("diagnostics matched fail-on %s", effectiveFailOn)}
		}
		return nil
	}
	for _, finding := range findings {
		path, err := uriutil.Path(finding.URI)
		if err != nil {
			path = finding.URI
		}
		diagnostic := finding.Diagnostic
		if err := writeFormatted(
			r.out,
			"%s:%d:%d: %s: %s%s\n",
			path,
			diagnostic.Range.Start.Line+1,
			diagnostic.Range.Start.Character+1,
			strings.ToLower(severityLabel(diagnostic.Severity)),
			diagnostic.Message,
			diagnosticCodeSuffix(diagnostic.Code),
		); err != nil {
			return err
		}
		for _, related := range finding.Related {
			relatedPath, pathErr := uriutil.Path(related.Location.URI)
			if pathErr != nil {
				relatedPath = related.Location.URI
			}
			if err := writeFormatted(
				r.out, "  %s:%d:%d: %s\n", relatedPath,
				related.Location.Range.Start.Line+1,
				related.Location.Range.Start.Character+1,
				related.Message,
			); err != nil {
				return err
			}
		}
	}
	if findingsAtSeverity(findings, failOn) {
		return &exitError{code: 1, err: fmt.Errorf("diagnostics matched fail-on %s", effectiveFailOn)}
	}
	return nil
}

func resolveCheckFiles(ctx context.Context, targets []string) ([]string, error) {
	seen := make(map[string]struct{})
	files := make([]string, 0, len(targets))
	add := func(path string) {
		path = filepath.Clean(path)
		if _, exists := seen[path]; exists {
			return
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}

	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := target
		if strings.HasPrefix(path, "file://") {
			resolved, err := uriutil.Path(path)
			if err != nil {
				return nil, fmt.Errorf("resolve check target %q: %w", target, err)
			}
			path = resolved
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve check target %q: %w", target, err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return nil, fmt.Errorf("inspect check target %q: %w", target, err)
		}
		if !info.IsDir() {
			add(absolute)
			continue
		}

		if err := filepath.WalkDir(absolute, func(
			path string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			relative, err := filepath.Rel(absolute, path)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path != absolute && indexer.ShouldSkipRelativePath(relative) {
					return fs.SkipDir
				}
				return nil
			}
			if indexer.ShouldSkipRelativePath(relative) ||
				!indexer.IsScannedPath(path) {
				return nil
			}
			add(path)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("walk check target %q: %w", target, err)
		}
	}

	slices.Sort(files)
	return files, nil
}

func findingsAtSeverity(
	findings []diagnosticOutput,
	cutoff protocol.DiagnosticSeverity,
) bool {
	if cutoff == 0 {
		return false
	}
	for _, finding := range findings {
		severity := finding.Diagnostic.Severity
		if severity == 0 {
			severity = protocol.DiagnosticSeverityWarning
		}
		if severity <= cutoff {
			return true
		}
	}
	return false
}

func diagnosticSeverity(value string) (protocol.DiagnosticSeverity, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "hint":
		return protocol.DiagnosticSeverityHint, nil
	case "info", "information":
		return protocol.DiagnosticSeverityInformation, nil
	case "warning", "warn":
		return protocol.DiagnosticSeverityWarning, nil
	case "error":
		return protocol.DiagnosticSeverityError, nil
	default:
		return 0, fmt.Errorf("unknown diagnostic severity %q", value)
	}
}

func severityLabel(severity protocol.DiagnosticSeverity) string {
	switch severity {
	case protocol.DiagnosticSeverityError:
		return "Error"
	case protocol.DiagnosticSeverityInformation:
		return "Information"
	case protocol.DiagnosticSeverityHint:
		return "Hint"
	default:
		return "Warning"
	}
}

func diagnosticCodeSuffix(code interface{}) string {
	if code == nil {
		return ""
	}
	return fmt.Sprintf(" [%v]", code)
}
