// Package phar reads PHP Archive (PHAR) files without invoking PHP or
// extracting the whole archive into memory.
package phar

import (
	"bytes"
	"compress/bzip2"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

const (
	maxStubSize     = 8 << 20
	maxManifestSize = 1 << 20
	maxEntryCount   = 1_000_000
	maxHaltMarkers  = 64

	compressionMask  = 0x00003000
	compressionGzip  = 0x00001000
	compressionBzip2 = 0x00002000
)

var (
	// ErrNotArchive identifies files that do not contain a PHAR manifest.
	// Composer sometimes installs PHP wrapper scripts with a .phar suffix;
	// callers can use errors.Is to ignore those without hiding malformed
	// archives.
	ErrNotArchive = errors.New("not a PHAR archive")

	haltCompilerMarker = []byte("__HALT_COMPILER();")
)

// Entry describes one file in a PHAR. The content offset deliberately remains
// private so callers cannot construct an entry that reads outside the parsed
// archive.
type Entry struct {
	Name             string
	UncompressedSize int64
	CompressedSize   int64
	CRC32            uint32
	Flags            uint32

	offset int64
}

// Archive is an open PHAR file. Close must be called when it is no longer
// needed.
type Archive struct {
	path    string
	file    *os.File
	entries []Entry
}

// Open validates the PHAR manifest and returns its entries.
func Open(path string) (*Archive, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat PHAR: %w", err)
	}
	if info.Size() < int64(len(haltCompilerMarker)+4) {
		return nil, fmt.Errorf("%w: halt marker is missing", ErrNotArchive)
	}

	stubSize := min(info.Size(), int64(maxStubSize))
	stub := make([]byte, int(stubSize))
	if _, err := io.ReadFull(file, stub); err != nil {
		return nil, fmt.Errorf("read PHAR stub: %w", err)
	}

	var parseErrors []error
	searchFrom := 0
	markerCount := 0
	for {
		relative := bytes.Index(stub[searchFrom:], haltCompilerMarker)
		if relative < 0 {
			break
		}
		markerCount++
		if markerCount > maxHaltMarkers {
			return nil, fmt.Errorf(
				"parse PHAR manifest: more than %d halt markers",
				maxHaltMarkers,
			)
		}
		markerEnd := searchFrom + relative + len(haltCompilerMarker)
		for _, manifestOffset := range manifestOffsets(stub, markerEnd) {
			entries, err := parseManifest(file, info.Size(), int64(manifestOffset))
			if err == nil {
				closeOnError = false
				return &Archive{
					path:    path,
					file:    file,
					entries: entries,
				}, nil
			}
			parseErrors = append(parseErrors, err)
		}
		searchFrom = markerEnd
	}

	if len(parseErrors) > 0 {
		return nil, fmt.Errorf("parse PHAR manifest: %w", errors.Join(parseErrors...))
	}
	if info.Size() > int64(maxStubSize) {
		return nil, fmt.Errorf(
			"%w: halt marker is not within the first %d bytes",
			ErrNotArchive,
			maxStubSize,
		)
	}
	return nil, fmt.Errorf("%w: halt marker is missing", ErrNotArchive)
}

// Path returns the archive path passed to Open.
func (a *Archive) Path() string {
	return a.path
}

// Entries returns the immutable entry metadata parsed from the manifest.
func (a *Archive) Entries() []Entry {
	return append([]Entry(nil), a.entries...)
}

// Extract writes one entry to destination while verifying its declared
// uncompressed size and CRC32 checksum.
func (a *Archive) Extract(entry Entry, destination io.Writer) error {
	if a == nil || a.file == nil {
		return errors.New("PHAR archive is closed")
	}
	if entry.offset < 0 || entry.CompressedSize < 0 {
		return errors.New("invalid PHAR entry")
	}

	section := io.NewSectionReader(a.file, entry.offset, entry.CompressedSize)
	var source io.Reader = section
	var closer io.Closer
	switch entry.Flags & compressionMask {
	case 0:
	case compressionGzip:
		reader := flate.NewReader(section)
		source = reader
		closer = reader
	case compressionBzip2:
		source = bzip2.NewReader(section)
	default:
		return fmt.Errorf(
			"extract %q: unsupported compression flags %#x",
			entry.Name,
			entry.Flags&compressionMask,
		)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	checksum := crc32.NewIEEE()
	written, err := io.Copy(
		io.MultiWriter(destination, checksum),
		io.LimitReader(source, entry.UncompressedSize+1),
	)
	if err != nil {
		return fmt.Errorf("extract %q: %w", entry.Name, err)
	}
	if written != entry.UncompressedSize {
		return fmt.Errorf(
			"extract %q: size mismatch: manifest has %d bytes, read %d",
			entry.Name,
			entry.UncompressedSize,
			written,
		)
	}
	if checksum.Sum32() != entry.CRC32 {
		return fmt.Errorf(
			"extract %q: CRC32 mismatch: manifest has %#08x, read %#08x",
			entry.Name,
			entry.CRC32,
			checksum.Sum32(),
		)
	}
	return nil
}

// Close releases the underlying archive file.
func (a *Archive) Close() error {
	if a == nil || a.file == nil {
		return nil
	}
	err := a.file.Close()
	a.file = nil
	return err
}

func manifestOffsets(stub []byte, markerEnd int) []int {
	offsets := []int{markerEnd}
	cursor := markerEnd
	for cursor < len(stub) && (stub[cursor] == ' ' || stub[cursor] == '\t') {
		cursor++
	}
	if cursor != markerEnd {
		offsets = appendUnique(offsets, cursor)
	}
	if cursor+2 <= len(stub) && bytes.Equal(stub[cursor:cursor+2], []byte("?>")) {
		cursor += 2
		offsets = appendUnique(offsets, cursor)
	}
	switch {
	case cursor+2 <= len(stub) && stub[cursor] == '\r' && stub[cursor+1] == '\n':
		offsets = appendUnique(offsets, cursor+2)
	case cursor < len(stub) && (stub[cursor] == '\n' || stub[cursor] == '\r'):
		offsets = appendUnique(offsets, cursor+1)
	}
	return offsets
}

func appendUnique(values []int, value int) []int {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func parseManifest(
	reader io.ReaderAt,
	archiveSize,
	manifestOffset int64,
) ([]Entry, error) {
	var lengthBytes [4]byte
	if _, err := reader.ReadAt(lengthBytes[:], manifestOffset); err != nil {
		return nil, fmt.Errorf("read manifest length at %d: %w", manifestOffset, err)
	}
	manifestLength := int64(binary.LittleEndian.Uint32(lengthBytes[:]))
	if manifestLength < 18 || manifestLength > maxManifestSize {
		return nil, fmt.Errorf(
			"invalid manifest length %d at %d",
			manifestLength,
			manifestOffset,
		)
	}
	manifestEnd := manifestOffset + 4 + manifestLength
	if manifestEnd < manifestOffset || manifestEnd > archiveSize {
		return nil, fmt.Errorf("manifest extends beyond the archive")
	}

	manifest := make([]byte, int(manifestLength))
	if _, err := reader.ReadAt(manifest, manifestOffset+4); err != nil {
		return nil, fmt.Errorf("read manifest at %d: %w", manifestOffset, err)
	}
	cursor := manifestCursor{data: manifest}
	entryCount, err := cursor.uint32()
	if err != nil {
		return nil, err
	}
	if entryCount > maxEntryCount {
		return nil, fmt.Errorf("manifest declares too many entries: %d", entryCount)
	}
	if _, err := cursor.uint16(); err != nil { // minimum supported API version
		return nil, err
	}
	if _, err := cursor.uint32(); err != nil { // global flags
		return nil, err
	}
	aliasLength, err := cursor.uint32()
	if err != nil {
		return nil, err
	}
	if err := cursor.skip(aliasLength); err != nil {
		return nil, fmt.Errorf("read archive alias: %w", err)
	}
	metadataLength, err := cursor.uint32()
	if err != nil {
		return nil, err
	}
	if err := cursor.skip(metadataLength); err != nil {
		return nil, fmt.Errorf("read archive metadata: %w", err)
	}

	entries := make([]Entry, 0, int(entryCount))
	contentOffset := manifestEnd
	for index := uint32(0); index < entryCount; index++ {
		nameLength, err := cursor.uint32()
		if err != nil {
			return nil, fmt.Errorf("entry %d filename length: %w", index, err)
		}
		name, err := cursor.bytes(nameLength)
		if err != nil {
			return nil, fmt.Errorf("entry %d filename: %w", index, err)
		}
		if len(name) == 0 || bytes.IndexByte(name, 0) >= 0 {
			return nil, fmt.Errorf("entry %d has an invalid filename", index)
		}
		uncompressedSize, err := cursor.uint32()
		if err != nil {
			return nil, fmt.Errorf("entry %q size: %w", name, err)
		}
		if _, err := cursor.uint32(); err != nil { // timestamp
			return nil, fmt.Errorf("entry %q timestamp: %w", name, err)
		}
		compressedSize, err := cursor.uint32()
		if err != nil {
			return nil, fmt.Errorf("entry %q compressed size: %w", name, err)
		}
		checksum, err := cursor.uint32()
		if err != nil {
			return nil, fmt.Errorf("entry %q checksum: %w", name, err)
		}
		flags, err := cursor.uint32()
		if err != nil {
			return nil, fmt.Errorf("entry %q flags: %w", name, err)
		}
		switch flags & compressionMask {
		case 0, compressionGzip, compressionBzip2:
		default:
			return nil, fmt.Errorf(
				"entry %q uses unsupported compression flags %#x",
				name,
				flags&compressionMask,
			)
		}
		entryMetadataLength, err := cursor.uint32()
		if err != nil {
			return nil, fmt.Errorf("entry %q metadata size: %w", name, err)
		}
		if err := cursor.skip(entryMetadataLength); err != nil {
			return nil, fmt.Errorf("entry %q metadata: %w", name, err)
		}

		nextOffset := contentOffset + int64(compressedSize)
		if nextOffset < contentOffset || nextOffset > archiveSize {
			return nil, fmt.Errorf("entry %q content extends beyond the archive", name)
		}
		entries = append(entries, Entry{
			Name:             string(name),
			UncompressedSize: int64(uncompressedSize),
			CompressedSize:   int64(compressedSize),
			CRC32:            checksum,
			Flags:            flags,
			offset:           contentOffset,
		})
		contentOffset = nextOffset
	}
	if cursor.remaining() != 0 {
		return nil, fmt.Errorf(
			"manifest has %d trailing bytes after %d entries",
			cursor.remaining(),
			entryCount,
		)
	}
	return entries, nil
}

type manifestCursor struct {
	data   []byte
	offset int
}

func (c *manifestCursor) uint16() (uint16, error) {
	value, err := c.bytes(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(value), nil
}

func (c *manifestCursor) uint32() (uint32, error) {
	value, err := c.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(value), nil
}

func (c *manifestCursor) bytes(length uint32) ([]byte, error) {
	if uint64(length) > uint64(len(c.data)-c.offset) {
		return nil, io.ErrUnexpectedEOF
	}
	end := c.offset + int(length)
	value := c.data[c.offset:end]
	c.offset = end
	return value, nil
}

func (c *manifestCursor) skip(length uint32) error {
	_, err := c.bytes(length)
	return err
}

func (c *manifestCursor) remaining() int {
	return len(c.data) - c.offset
}
