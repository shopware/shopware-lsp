package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/phar"
)

const (
	pharCacheFormatVersion       = 1
	maxPHARPHPFiles              = 100_000
	maxPHARPHPFileSize     int64 = 128 << 20
	maxPHARPHPSourceSize   int64 = 1 << 30
)

const pharCacheStateFile = "state.json"

type pharCacheState struct {
	Version       int    `json:"version"`
	ArchivePath   string `json:"archivePath"`
	ArchiveSize   int64  `json:"archiveSize"`
	ArchiveMTime  int64  `json:"archiveMTime"`
	PHPFileCount  int    `json:"phpFileCount"`
	PHPSourceSize int64  `json:"phpSourceSize"`
}

func isPHARArchivePath(filePath string) bool {
	return strings.EqualFold(filepath.Ext(filePath), ".phar")
}

func (scanner *FileScanner) discoverPHARSources(
	ctx context.Context,
	archivePaths []string,
) ([]string, error) {
	if len(archivePaths) == 0 {
		return nil, nil
	}
	slices.Sort(archivePaths)
	seen := make(map[string]struct{}, len(archivePaths))
	var sources []string
	for _, archivePath := range archivePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		canonical, err := canonicalPHARPath(archivePath)
		if err != nil {
			return nil, fmt.Errorf("resolve PHAR %s: %w", archivePath, err)
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		archiveSources, err := scanner.materializePHARSources(ctx, canonical)
		if err != nil {
			if errors.Is(err, phar.ErrNotArchive) {
				// Composer bin proxies can use a .phar suffix while being
				// ordinary PHP scripts that load a different archive.
				continue
			}
			return nil, fmt.Errorf("materialize PHAR %s: %w", canonical, err)
		}
		sources = append(sources, archiveSources...)
	}
	return sources, nil
}

func canonicalPHARPath(filePath string) (string, error) {
	absolute, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func (scanner *FileScanner) materializePHARSources(
	ctx context.Context,
	archivePath string,
) ([]string, error) {
	info, err := os.Stat(archivePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: archive is not a regular file", phar.ErrNotArchive)
	}

	cacheKey := sha256.Sum256([]byte(archivePath))
	cacheDir := filepath.Join(
		scanner.pharCache,
		hex.EncodeToString(cacheKey[:]),
	)
	expected := pharCacheState{
		Version:      pharCacheFormatVersion,
		ArchivePath:  archivePath,
		ArchiveSize:  info.Size(),
		ArchiveMTime: info.ModTime().UnixNano(),
	}
	if state, err := readPHARCacheState(cacheDir); err == nil &&
		state.Version == expected.Version &&
		state.ArchivePath == expected.ArchivePath &&
		state.ArchiveSize == expected.ArchiveSize &&
		state.ArchiveMTime == expected.ArchiveMTime {
		files, fileCount, sourceSize, collectErr := collectPHARCacheFiles(cacheDir)
		if collectErr == nil &&
			fileCount == state.PHPFileCount &&
			sourceSize == state.PHPSourceSize {
			return files, nil
		}
	}

	archive, err := phar.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = archive.Close() }()

	type sourceEntry struct {
		entry    phar.Entry
		relative string
	}
	archiveEntries := archive.Entries()
	entries := make([]sourceEntry, 0, len(archiveEntries))
	paths := make(map[string]string)
	var sourceSize int64
	for _, entry := range archiveEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.EqualFold(pathpkg.Ext(entry.Name), ".php") {
			continue
		}
		if len(entries) >= maxPHARPHPFiles {
			return nil, fmt.Errorf(
				"archive exceeds the PHP source limit of %d files",
				maxPHARPHPFiles,
			)
		}
		if entry.UncompressedSize > maxPHARPHPFileSize {
			return nil, fmt.Errorf(
				"entry %q exceeds the PHP source limit of %d bytes",
				entry.Name,
				maxPHARPHPFileSize,
			)
		}
		if entry.UncompressedSize > maxPHARPHPSourceSize-sourceSize {
			return nil, fmt.Errorf(
				"archive exceeds the PHP source limit of %d bytes",
				maxPHARPHPSourceSize,
			)
		}
		relative, err := safePHAREntryPath(entry.Name)
		if err != nil {
			return nil, err
		}
		collisionKey := strings.ToLower(relative)
		if previous, collision := paths[collisionKey]; collision {
			return nil, fmt.Errorf(
				"entries %q and %q map to the same source path",
				previous,
				entry.Name,
			)
		}
		paths[collisionKey] = entry.Name
		entries = append(entries, sourceEntry{entry: entry, relative: relative})
		sourceSize += entry.UncompressedSize
	}

	if err := os.MkdirAll(scanner.pharCache, 0o755); err != nil {
		return nil, fmt.Errorf("create PHAR source cache: %w", err)
	}
	temporary, err := os.MkdirTemp(scanner.pharCache, ".extract-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary PHAR source cache: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()

	filesRoot := filepath.Join(temporary, "files")
	if err := os.MkdirAll(filesRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create PHAR files cache: %w", err)
	}
	extracted := make([]string, 0, len(entries))
	for _, source := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		destination := filepath.Join(
			filesRoot,
			filepath.FromSlash(source.relative),
		)
		if !pathWithin(filesRoot, destination) {
			return nil, fmt.Errorf("unsafe PHAR entry path %q", source.entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return nil, fmt.Errorf("create parent for %q: %w", source.entry.Name, err)
		}
		file, err := os.OpenFile(
			destination,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			0o644,
		)
		if err != nil {
			return nil, fmt.Errorf("create source for %q: %w", source.entry.Name, err)
		}
		extractErr := archive.Extract(source.entry, file)
		closeErr := file.Close()
		if extractErr != nil || closeErr != nil {
			return nil, errors.Join(extractErr, closeErr)
		}
		archiveTime := info.ModTime()
		if err := os.Chtimes(destination, archiveTime, archiveTime); err != nil {
			return nil, fmt.Errorf("timestamp source for %q: %w", source.entry.Name, err)
		}
		extracted = append(extracted, destination)
	}

	expected.PHPFileCount = len(entries)
	expected.PHPSourceSize = sourceSize
	state, err := json.Marshal(expected)
	if err != nil {
		return nil, fmt.Errorf("encode PHAR cache state: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(temporary, pharCacheStateFile),
		state,
		0o644,
	); err != nil {
		return nil, fmt.Errorf("write PHAR cache state: %w", err)
	}
	if err := replacePHARCacheDirectory(temporary, cacheDir); err != nil {
		return nil, err
	}

	finalFilesRoot := filepath.Join(cacheDir, "files")
	for index, filePath := range extracted {
		relative, err := filepath.Rel(filesRoot, filePath)
		if err != nil {
			return nil, err
		}
		extracted[index] = filepath.Join(finalFilesRoot, relative)
	}
	slices.Sort(extracted)
	return extracted, nil
}

func safePHAREntryPath(name string) (string, error) {
	if name == "" ||
		strings.ContainsRune(name, '\\') ||
		strings.ContainsRune(name, ':') {
		return "", fmt.Errorf("unsafe PHAR entry path %q", name)
	}
	cleaned := pathpkg.Clean(name)
	if cleaned != name ||
		cleaned == "." ||
		cleaned == ".." ||
		pathpkg.IsAbs(cleaned) ||
		strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("unsafe PHAR entry path %q", name)
	}
	return cleaned, nil
}

func readPHARCacheState(cacheDir string) (pharCacheState, error) {
	content, err := os.ReadFile(filepath.Join(cacheDir, pharCacheStateFile))
	if err != nil {
		return pharCacheState{}, err
	}
	var state pharCacheState
	if err := json.Unmarshal(content, &state); err != nil {
		return pharCacheState{}, err
	}
	return state, nil
}

func collectPHARCacheFiles(
	cacheDir string,
) ([]string, int, int64, error) {
	filesRoot := filepath.Join(cacheDir, "files")
	var files []string
	var sourceSize int64
	err := filepath.WalkDir(filesRoot, func(
		filePath string,
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
		if !info.Mode().IsRegular() ||
			!strings.EqualFold(filepath.Ext(filePath), ".php") {
			return fmt.Errorf("unexpected file in PHAR source cache: %s", filePath)
		}
		files = append(files, filePath)
		sourceSize += info.Size()
		return nil
	})
	if err != nil {
		return nil, 0, 0, err
	}
	slices.Sort(files)
	return files, len(files), sourceSize, nil
}

func replacePHARCacheDirectory(temporary, destination string) error {
	parent := filepath.Dir(destination)
	var previous string
	if _, err := os.Stat(destination); err == nil {
		placeholder, err := os.MkdirTemp(parent, ".previous-*")
		if err != nil {
			return fmt.Errorf("reserve previous PHAR cache path: %w", err)
		}
		if err := os.Remove(placeholder); err != nil {
			return fmt.Errorf("release previous PHAR cache path: %w", err)
		}
		if err := os.Rename(destination, placeholder); err != nil {
			return fmt.Errorf("preserve previous PHAR cache: %w", err)
		}
		previous = placeholder
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing PHAR cache: %w", err)
	}

	if err := os.Rename(temporary, destination); err != nil {
		if previous != "" {
			_ = os.Rename(previous, destination)
		}
		return fmt.Errorf("publish PHAR source cache: %w", err)
	}
	if previous != "" {
		if err := os.RemoveAll(previous); err != nil {
			return fmt.Errorf("remove previous PHAR source cache: %w", err)
		}
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	_, within := relativePathWithin(root, candidate)
	return within
}

func relativePathWithin(root, candidate string) (string, bool) {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if root == candidate {
		return ".", true
	}
	if len(candidate) > len(root) && strings.HasPrefix(candidate, root) &&
		(root[len(root)-1] == os.PathSeparator ||
			candidate[len(root)] == os.PathSeparator) {
		relative := candidate[len(root):]
		if len(relative) > 0 && relative[0] == os.PathSeparator {
			relative = relative[1:]
		}
		return relative, true
	}
	// On Unix, two clean absolute paths that do not share the root prefix
	// cannot overlap. Avoid filepath.Rel constructing a potentially long chain
	// of ".." components for every discovered workspace path. Windows needs
	// the fallback for its case-insensitive path comparison.
	if os.PathSeparator == '/' && filepath.IsAbs(root) && filepath.IsAbs(candidate) {
		return "", false
	}
	relative, err := filepath.Rel(root, candidate)
	within := err == nil &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(os.PathSeparator))
	return relative, within
}
