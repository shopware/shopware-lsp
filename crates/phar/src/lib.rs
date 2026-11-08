//! Reads PHP Archive (PHAR) files without invoking PHP or extracting the
//! whole archive into memory (port of `internal/phar`).

use std::fs::File;
use std::io::{self, Read, Write};
use std::path::{Path, PathBuf};

const MAX_STUB_SIZE: u64 = 8 << 20;
const MAX_MANIFEST_SIZE: u64 = 1 << 20;
const MAX_ENTRY_COUNT: u32 = 1_000_000;
const MAX_HALT_MARKERS: usize = 64;

const COMPRESSION_MASK: u32 = 0x0000_3000;
/// Entry flag for zlib/deflate-compressed content.
pub const COMPRESSION_GZIP: u32 = 0x0000_1000;
/// Entry flag for bzip2-compressed content.
pub const COMPRESSION_BZIP2: u32 = 0x0000_2000;

const HALT_COMPILER_MARKER: &[u8] = b"__HALT_COMPILER();";

/// Errors produced while opening or extracting an archive. `Display` matches
/// the Go error text.
#[derive(Debug)]
pub enum PharError {
    /// The file does not contain a PHAR manifest (Go `ErrNotArchive`).
    /// Composer sometimes installs PHP wrapper scripts with a `.phar`
    /// suffix; callers can ignore these without hiding malformed archives.
    NotArchive(String),
    /// Opening the file failed (Go returns the bare `os.Open` error).
    Io(io::Error),
    /// Any other failure, with its full message.
    Other(String),
}

impl PharError {
    /// Mirrors `errors.Is(err, phar.ErrNotArchive)`.
    pub fn is_not_archive(&self) -> bool {
        matches!(self, PharError::NotArchive(_))
    }
}

impl std::fmt::Display for PharError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            PharError::NotArchive(detail) => write!(formatter, "not a PHAR archive: {detail}"),
            PharError::Io(error) => write!(formatter, "{error}"),
            PharError::Other(message) => formatter.write_str(message),
        }
    }
}

impl std::error::Error for PharError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            PharError::Io(error) => Some(error),
            _ => None,
        }
    }
}

fn other(message: impl Into<String>) -> PharError {
    PharError::Other(message.into())
}

/// One file in a PHAR. The content offset deliberately remains private so
/// callers cannot construct an entry that reads outside the parsed archive.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Entry {
    pub name: String,
    pub uncompressed_size: i64,
    pub compressed_size: i64,
    pub crc32: u32,
    pub flags: u32,
    offset: i64,
}

/// An open PHAR file.
#[derive(Debug)]
pub struct Archive {
    path: PathBuf,
    file: Option<File>,
    entries: Vec<Entry>,
}

#[cfg(unix)]
fn read_at(file: &File, buffer: &mut [u8], offset: u64) -> io::Result<usize> {
    std::os::unix::fs::FileExt::read_at(file, buffer, offset)
}

#[cfg(windows)]
fn read_at(file: &File, buffer: &mut [u8], offset: u64) -> io::Result<usize> {
    std::os::windows::fs::FileExt::seek_read(file, buffer, offset)
}

/// Go `io.ReaderAt.ReadAt` semantics: fill the whole buffer or fail with
/// `UnexpectedEof` (Go reports `EOF`).
fn read_exact_at(file: &File, mut buffer: &mut [u8], mut offset: u64) -> io::Result<()> {
    while !buffer.is_empty() {
        match read_at(file, buffer, offset) {
            Ok(0) => return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "EOF")),
            Ok(read) => {
                buffer = &mut buffer[read..];
                offset += read as u64;
            }
            Err(error) if error.kind() == io::ErrorKind::Interrupted => {}
            Err(error) => return Err(error),
        }
    }
    Ok(())
}

/// A positional reader over `[offset, offset+length)` of a shared file
/// (Go `io.SectionReader`).
struct SectionReader<'a> {
    file: &'a File,
    offset: u64,
    remaining: u64,
}

impl Read for SectionReader<'_> {
    fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        if self.remaining == 0 {
            return Ok(0);
        }
        let length = buffer
            .len()
            .min(self.remaining.min(usize::MAX as u64) as usize);
        let read = read_at(self.file, &mut buffer[..length], self.offset)?;
        self.offset += read as u64;
        self.remaining -= read as u64;
        Ok(read)
    }
}

fn find(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

impl Archive {
    /// Validates the PHAR manifest and returns the opened archive.
    pub fn open(path: impl AsRef<Path>) -> Result<Archive, PharError> {
        let path = path.as_ref();
        let file = File::open(path).map_err(PharError::Io)?;
        let size = file
            .metadata()
            .map_err(|error| other(format!("stat PHAR: {error}")))?
            .len();
        if size < (HALT_COMPILER_MARKER.len() + 4) as u64 {
            return Err(PharError::NotArchive("halt marker is missing".to_owned()));
        }

        let stub_size = size.min(MAX_STUB_SIZE) as usize;
        let mut stub = vec![0u8; stub_size];
        (&file)
            .read_exact(&mut stub)
            .map_err(|error| other(format!("read PHAR stub: {}", go_io_error(&error))))?;

        let mut parse_errors: Vec<String> = Vec::new();
        let mut search_from = 0;
        let mut marker_count = 0;
        while let Some(relative) = find(&stub[search_from..], HALT_COMPILER_MARKER) {
            marker_count += 1;
            if marker_count > MAX_HALT_MARKERS {
                return Err(other(format!(
                    "parse PHAR manifest: more than {MAX_HALT_MARKERS} halt markers"
                )));
            }
            let marker_end = search_from + relative + HALT_COMPILER_MARKER.len();
            for manifest_offset in manifest_offsets(&stub, marker_end) {
                match parse_manifest(&file, size as i64, manifest_offset as i64) {
                    Ok(entries) => {
                        return Ok(Archive {
                            path: path.to_path_buf(),
                            file: Some(file),
                            entries,
                        });
                    }
                    Err(error) => parse_errors.push(error),
                }
            }
            search_from = marker_end;
        }

        if !parse_errors.is_empty() {
            return Err(other(format!(
                "parse PHAR manifest: {}",
                parse_errors.join("\n")
            )));
        }
        if size > MAX_STUB_SIZE {
            return Err(PharError::NotArchive(format!(
                "halt marker is not within the first {MAX_STUB_SIZE} bytes"
            )));
        }
        Err(PharError::NotArchive("halt marker is missing".to_owned()))
    }

    /// The archive path passed to [`Archive::open`].
    pub fn path(&self) -> &Path {
        &self.path
    }

    /// The immutable entry metadata parsed from the manifest.
    pub fn entries(&self) -> &[Entry] {
        &self.entries
    }

    /// Writes one entry to `destination` while verifying its declared
    /// uncompressed size and CRC32 checksum.
    pub fn extract(&self, entry: &Entry, destination: &mut dyn Write) -> Result<(), PharError> {
        let Some(file) = self.file.as_ref() else {
            return Err(other("PHAR archive is closed"));
        };
        if entry.offset < 0 || entry.compressed_size < 0 {
            return Err(other("invalid PHAR entry"));
        }
        let section = SectionReader {
            file,
            offset: entry.offset as u64,
            remaining: entry.compressed_size as u64,
        };
        let source: Box<dyn Read + '_> = match entry.flags & COMPRESSION_MASK {
            0 => Box::new(section),
            COMPRESSION_GZIP => Box::new(flate2::read::DeflateDecoder::new(section)),
            COMPRESSION_BZIP2 => Box::new(bzip2::read::BzDecoder::new(section)),
            flags => {
                return Err(other(format!(
                    "extract {}: unsupported compression flags {flags:#x}",
                    go_quote(&entry.name)
                )));
            }
        };

        let mut limited = source.take(entry.uncompressed_size as u64 + 1);
        let mut hasher = crc32fast::Hasher::new();
        let mut buffer = [0u8; 32 * 1024];
        let mut written: i64 = 0;
        loop {
            let read = match limited.read(&mut buffer) {
                Ok(0) => break,
                Ok(read) => read,
                Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
                Err(error) => {
                    return Err(other(format!(
                        "extract {}: {}",
                        go_quote(&entry.name),
                        go_io_error(&error)
                    )));
                }
            };
            destination
                .write_all(&buffer[..read])
                .map_err(|error| other(format!("extract {}: {error}", go_quote(&entry.name))))?;
            hasher.update(&buffer[..read]);
            written += read as i64;
        }
        if written != entry.uncompressed_size {
            return Err(other(format!(
                "extract {}: size mismatch: manifest has {} bytes, read {written}",
                go_quote(&entry.name),
                entry.uncompressed_size
            )));
        }
        let checksum = hasher.finalize();
        if checksum != entry.crc32 {
            return Err(other(format!(
                "extract {}: CRC32 mismatch: manifest has {:#010x}, read {checksum:#010x}",
                go_quote(&entry.name),
                entry.crc32
            )));
        }
        Ok(())
    }

    /// Releases the underlying archive file. Later extractions fail.
    pub fn close(&mut self) {
        self.file = None;
    }
}

fn go_io_error(error: &io::Error) -> String {
    if error.kind() == io::ErrorKind::UnexpectedEof {
        return "unexpected EOF".to_owned();
    }
    error.to_string()
}

fn go_quote(value: &str) -> String {
    format!("{value:?}")
}

fn manifest_offsets(stub: &[u8], marker_end: usize) -> Vec<usize> {
    let mut offsets = vec![marker_end];
    let push_unique = |offsets: &mut Vec<usize>, value: usize| {
        if !offsets.contains(&value) {
            offsets.push(value);
        }
    };
    let mut cursor = marker_end;
    while cursor < stub.len() && (stub[cursor] == b' ' || stub[cursor] == b'\t') {
        cursor += 1;
    }
    if cursor != marker_end {
        push_unique(&mut offsets, cursor);
    }
    if cursor + 2 <= stub.len() && &stub[cursor..cursor + 2] == b"?>" {
        cursor += 2;
        push_unique(&mut offsets, cursor);
    }
    if cursor + 2 <= stub.len() && stub[cursor] == b'\r' && stub[cursor + 1] == b'\n' {
        push_unique(&mut offsets, cursor + 2);
    } else if cursor < stub.len() && (stub[cursor] == b'\n' || stub[cursor] == b'\r') {
        push_unique(&mut offsets, cursor + 1);
    }
    offsets
}

struct ManifestCursor<'a> {
    data: &'a [u8],
    offset: usize,
}

const UNEXPECTED_EOF: &str = "unexpected EOF";

impl<'a> ManifestCursor<'a> {
    fn bytes(&mut self, length: u32) -> Result<&'a [u8], String> {
        if length as u64 > (self.data.len() - self.offset) as u64 {
            return Err(UNEXPECTED_EOF.to_owned());
        }
        let end = self.offset + length as usize;
        let value = &self.data[self.offset..end];
        self.offset = end;
        Ok(value)
    }

    fn uint16(&mut self) -> Result<u16, String> {
        let value = self.bytes(2)?;
        Ok(u16::from_le_bytes([value[0], value[1]]))
    }

    fn uint32(&mut self) -> Result<u32, String> {
        let value = self.bytes(4)?;
        Ok(u32::from_le_bytes([value[0], value[1], value[2], value[3]]))
    }

    fn skip(&mut self, length: u32) -> Result<(), String> {
        self.bytes(length).map(|_| ())
    }

    fn remaining(&self) -> usize {
        self.data.len() - self.offset
    }
}

fn parse_manifest(
    file: &File,
    archive_size: i64,
    manifest_offset: i64,
) -> Result<Vec<Entry>, String> {
    let mut length_bytes = [0u8; 4];
    read_exact_at(file, &mut length_bytes, manifest_offset as u64).map_err(|error| {
        format!(
            "read manifest length at {manifest_offset}: {}",
            go_read_at_error(&error)
        )
    })?;
    let manifest_length = u32::from_le_bytes(length_bytes) as i64;
    if !(18..=MAX_MANIFEST_SIZE as i64).contains(&manifest_length) {
        return Err(format!(
            "invalid manifest length {manifest_length} at {manifest_offset}"
        ));
    }
    let manifest_end = manifest_offset + 4 + manifest_length;
    if manifest_end < manifest_offset || manifest_end > archive_size {
        return Err("manifest extends beyond the archive".to_owned());
    }

    let mut manifest = vec![0u8; manifest_length as usize];
    read_exact_at(file, &mut manifest, manifest_offset as u64 + 4).map_err(|error| {
        format!(
            "read manifest at {manifest_offset}: {}",
            go_read_at_error(&error)
        )
    })?;
    let mut cursor = ManifestCursor {
        data: &manifest,
        offset: 0,
    };
    let entry_count = cursor.uint32()?;
    if entry_count > MAX_ENTRY_COUNT {
        return Err(format!("manifest declares too many entries: {entry_count}"));
    }
    cursor.uint16()?; // minimum supported API version
    cursor.uint32()?; // global flags
    let alias_length = cursor.uint32()?;
    cursor
        .skip(alias_length)
        .map_err(|error| format!("read archive alias: {error}"))?;
    let metadata_length = cursor.uint32()?;
    cursor
        .skip(metadata_length)
        .map_err(|error| format!("read archive metadata: {error}"))?;

    let mut entries = Vec::with_capacity(entry_count as usize);
    let mut content_offset = manifest_end;
    for index in 0..entry_count {
        let name_length = cursor
            .uint32()
            .map_err(|error| format!("entry {index} filename length: {error}"))?;
        let name = cursor
            .bytes(name_length)
            .map_err(|error| format!("entry {index} filename: {error}"))?;
        if name.is_empty() || name.contains(&0) {
            return Err(format!("entry {index} has an invalid filename"));
        }
        let name = String::from_utf8_lossy(name).into_owned();
        let quoted = go_quote(&name);
        let uncompressed_size = cursor
            .uint32()
            .map_err(|error| format!("entry {quoted} size: {error}"))?;
        cursor
            .uint32()
            .map_err(|error| format!("entry {quoted} timestamp: {error}"))?;
        let compressed_size = cursor
            .uint32()
            .map_err(|error| format!("entry {quoted} compressed size: {error}"))?;
        let checksum = cursor
            .uint32()
            .map_err(|error| format!("entry {quoted} checksum: {error}"))?;
        let flags = cursor
            .uint32()
            .map_err(|error| format!("entry {quoted} flags: {error}"))?;
        match flags & COMPRESSION_MASK {
            0 | COMPRESSION_GZIP | COMPRESSION_BZIP2 => {}
            unsupported => {
                return Err(format!(
                    "entry {quoted} uses unsupported compression flags {unsupported:#x}"
                ));
            }
        }
        let entry_metadata_length = cursor
            .uint32()
            .map_err(|error| format!("entry {quoted} metadata size: {error}"))?;
        cursor
            .skip(entry_metadata_length)
            .map_err(|error| format!("entry {quoted} metadata: {error}"))?;

        let next_offset = content_offset + compressed_size as i64;
        if next_offset < content_offset || next_offset > archive_size {
            return Err(format!("entry {quoted} content extends beyond the archive"));
        }
        entries.push(Entry {
            name,
            uncompressed_size: uncompressed_size as i64,
            compressed_size: compressed_size as i64,
            crc32: checksum,
            flags,
            offset: content_offset,
        });
        content_offset = next_offset;
    }
    if cursor.remaining() != 0 {
        return Err(format!(
            "manifest has {} trailing bytes after {entry_count} entries",
            cursor.remaining()
        ));
    }
    Ok(entries)
}

fn go_read_at_error(error: &io::Error) -> String {
    if error.kind() == io::ErrorKind::UnexpectedEof {
        return "EOF".to_owned();
    }
    error.to_string()
}

#[cfg(test)]
mod tests {
    use super::*;
    use flate2::Compression;
    use flate2::write::DeflateEncoder;

    struct TestEntry<'a> {
        name: &'a str,
        content: &'a [u8],
        compression: u32,
    }

    fn entry<'a>(name: &'a str, content: &'a [u8], compression: u32) -> TestEntry<'a> {
        TestEntry {
            name,
            content,
            compression,
        }
    }

    fn write_test_archive(directory: &Path, entries: &[TestEntry<'_>]) -> PathBuf {
        write_test_archive_with_ending(directory, entries, " ?>\r\n")
    }

    fn write_test_archive_with_ending(
        directory: &Path,
        entries: &[TestEntry<'_>],
        stub_ending: &str,
    ) -> PathBuf {
        let mut entry_manifest = Vec::new();
        let mut contents = Vec::new();
        for entry in entries {
            let compressed = match entry.compression {
                COMPRESSION_GZIP => {
                    let mut encoder = DeflateEncoder::new(Vec::new(), Compression::default());
                    encoder.write_all(entry.content).unwrap();
                    encoder.finish().unwrap()
                }
                COMPRESSION_BZIP2 => {
                    let mut encoder =
                        bzip2::write::BzEncoder::new(Vec::new(), bzip2::Compression::default());
                    encoder.write_all(entry.content).unwrap();
                    encoder.finish().unwrap()
                }
                _ => entry.content.to_vec(),
            };
            let mut write_u32 = |value: u32| entry_manifest.extend_from_slice(&value.to_le_bytes());
            write_u32(entry.name.len() as u32);
            entry_manifest.extend_from_slice(entry.name.as_bytes());
            let mut write_u32 = |value: u32| entry_manifest.extend_from_slice(&value.to_le_bytes());
            write_u32(entry.content.len() as u32);
            write_u32(1_700_000_000);
            write_u32(compressed.len() as u32);
            write_u32(crc32fast::hash(entry.content));
            write_u32(0o100644 | entry.compression);
            write_u32(0);
            contents.extend_from_slice(&compressed);
        }

        let mut manifest = Vec::new();
        manifest.extend_from_slice(&(entries.len() as u32).to_le_bytes());
        manifest.extend_from_slice(&0x0011u16.to_le_bytes());
        manifest.extend_from_slice(&0u32.to_le_bytes());
        manifest.extend_from_slice(&0u32.to_le_bytes());
        manifest.extend_from_slice(&0u32.to_le_bytes());
        manifest.extend_from_slice(&entry_manifest);

        let mut archive = Vec::new();
        archive.extend_from_slice(
            format!("<?php // fixture\n__HALT_COMPILER();{stub_ending}").as_bytes(),
        );
        archive.extend_from_slice(&(manifest.len() as u32).to_le_bytes());
        archive.extend_from_slice(&manifest);
        archive.extend_from_slice(&contents);

        let path = directory.join("fixture.phar");
        std::fs::write(&path, archive).unwrap();
        path
    }

    #[test]
    fn open_and_extract() {
        let directory = tempfile::tempdir().unwrap();
        let path = write_test_archive(
            directory.path(),
            &[
                entry("src/Plain.php", b"<?php class Plain {}", 0),
                entry(
                    "src/Compressed.php",
                    b"<?php class Compressed {}",
                    COMPRESSION_GZIP,
                ),
                entry("empty.php", b"", 0),
            ],
        );
        let mut archive = Archive::open(&path).unwrap();
        assert_eq!(archive.path(), path);
        assert_eq!(archive.entries().len(), 3);
        for (index, expected) in ["<?php class Plain {}", "<?php class Compressed {}", ""]
            .iter()
            .enumerate()
        {
            let mut content = Vec::new();
            archive
                .extract(&archive.entries()[index], &mut content)
                .unwrap();
            assert_eq!(String::from_utf8(content).unwrap(), *expected);
        }
        let first = archive.entries()[0].clone();
        archive.close();
        assert_eq!(
            archive
                .extract(&first, &mut io::sink())
                .unwrap_err()
                .to_string(),
            "PHAR archive is closed"
        );
    }

    #[test]
    fn extract_bzip2_entry() {
        let directory = tempfile::tempdir().unwrap();
        let path = write_test_archive(
            directory.path(),
            &[entry("src/Bz.php", b"<?php class Bz {}", COMPRESSION_BZIP2)],
        );
        let archive = Archive::open(&path).unwrap();
        let mut content = Vec::new();
        archive
            .extract(&archive.entries()[0], &mut content)
            .unwrap();
        assert_eq!(content, b"<?php class Bz {}");
    }

    #[test]
    fn open_supports_common_stub_endings() {
        for ending in ["", " ?>", " ?>\n", " ?>\r\n"] {
            let directory = tempfile::tempdir().unwrap();
            let path = write_test_archive_with_ending(
                directory.path(),
                &[entry("test.php", b"<?php", 0)],
                ending,
            );
            Archive::open(&path).unwrap_or_else(|error| panic!("{ending:?}: {error}"));
        }
    }

    #[test]
    fn open_rejects_wrapper_and_malformed_archive() {
        let directory = tempfile::tempdir().unwrap();
        let wrapper = directory.path().join("wrapper.phar");
        std::fs::write(&wrapper, b"<?php echo 'wrapper';").unwrap();
        assert!(Archive::open(&wrapper).unwrap_err().is_not_archive());

        let malformed = directory.path().join("malformed.phar");
        let mut bytes = b"<?php __HALT_COMPILER();".to_vec();
        bytes.extend_from_slice(&[0xff, 0xff, 0xff, 0x7f]);
        std::fs::write(&malformed, bytes).unwrap();
        let error = Archive::open(&malformed).unwrap_err();
        assert!(!error.is_not_archive());
        assert_eq!(
            error.to_string(),
            "parse PHAR manifest: invalid manifest length 2147483647 at 24"
        );
    }

    #[test]
    fn extract_rejects_corrupt_content() {
        let directory = tempfile::tempdir().unwrap();
        let path = write_test_archive(directory.path(), &[entry("test.php", b"<?php echo 1;", 0)]);
        let mut bytes = std::fs::read(&path).unwrap();
        let last = bytes.len() - 1;
        bytes[last] ^= 0xff;
        std::fs::write(&path, bytes).unwrap();

        let archive = Archive::open(&path).unwrap();
        let error = archive
            .extract(&archive.entries()[0], &mut io::sink())
            .unwrap_err()
            .to_string();
        assert!(error.contains("CRC32 mismatch"), "{error}");
    }

    #[test]
    fn external_archive() {
        let Ok(path) = std::env::var("SHOPWARE_LSP_TEST_PHAR") else {
            return; // SHOPWARE_LSP_TEST_PHAR is not set
        };
        let archive = Archive::open(&path).unwrap();
        assert!(!archive.entries().is_empty());
        for entry in archive.entries() {
            if !entry.name.ends_with(".php") {
                continue;
            }
            let mut content = Vec::new();
            archive.extract(entry, &mut content).unwrap();
            assert!(String::from_utf8_lossy(&content).contains("<?php"));
            return;
        }
        panic!("archive does not contain a PHP file");
    }
}
