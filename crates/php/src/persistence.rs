//! Persisted compact workspace graphs (port of `persistence.go`).
//!
//! A repository value is a small MessagePack envelope `{format, payload}`
//! whose payload is the raw-DEFLATE compressed binary encoding of one
//! [`WorkspaceGraph`] (`swlsp-php-semantic`'s own cache format). Restoration
//! parses the envelope in place, so the compressed payload is borrowed from
//! the SQLite row instead of copied.
//!
//! Rust caches are never shared with Go (the cache version marker differs),
//! so only the Rust format is accepted.

use std::cell::RefCell;
use std::fmt;
use std::io::Read;

use flate2::{Compress, Compression, Decompress, FlushCompress, FlushDecompress, Status};
use serde::de::{self, Visitor};
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use swlsp_php_semantic::{WorkspaceGraph, WorkspaceGraphDecoder};

/// Raw DEFLATE over the `swlsp-php-semantic` binary graph codec.
pub(crate) const PERSISTED_WORKSPACE_GRAPH_FORMAT: u8 = 1;
/// Upper bound of one decompressed graph.
pub(crate) const MAX_PERSISTED_SEMANTIC_VALUE_SIZE: usize = 128 << 20;
/// Pooled buffers above this size are released after use.
const MAX_POOLED_SEMANTIC_BUFFER: usize = 8 << 20;

/// The repository envelope of one persisted graph.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct PersistedWorkspaceGraph {
    pub format: u8,
    pub payload: Payload,
}

/// Compressed bytes, serialized as MessagePack binary.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Payload(pub Vec<u8>);

impl Serialize for Payload {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_bytes(&self.0)
    }
}

impl<'de> Deserialize<'de> for Payload {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct BytesVisitor;

        impl<'de> Visitor<'de> for BytesVisitor {
            type Value = Payload;

            fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str("binary payload")
            }

            fn visit_bytes<E: de::Error>(self, value: &[u8]) -> Result<Payload, E> {
                Ok(Payload(value.to_vec()))
            }

            fn visit_byte_buf<E: de::Error>(self, value: Vec<u8>) -> Result<Payload, E> {
                Ok(Payload(value))
            }

            fn visit_unit<E: de::Error>(self) -> Result<Payload, E> {
                Ok(Payload::default())
            }

            fn visit_seq<A: de::SeqAccess<'de>>(self, mut seq: A) -> Result<Payload, A::Error> {
                let mut bytes = Vec::with_capacity(seq.size_hint().unwrap_or_default());
                while let Some(byte) = seq.next_element::<u8>()? {
                    bytes.push(byte);
                }
                Ok(Payload(bytes))
            }
        }

        deserializer.deserialize_any(BytesVisitor)
    }
}

/// A persistence failure with operation context.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PersistError(pub String);

impl fmt::Display for PersistError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for PersistError {}

fn persist_error<T>(message: impl Into<String>) -> Result<T, PersistError> {
    Err(PersistError(message.into()))
}

struct Codec {
    compress: Compress,
    decompress: Decompress,
    buffer: Vec<u8>,
}

thread_local! {
    // DEFLATE state and output buffers are reused per worker thread; the
    // buffer is released when one value grows it beyond the pooled bound.
    static CODEC: RefCell<Option<Codec>> = const { RefCell::new(None) };
}

fn with_codec<R>(operation: impl FnOnce(&mut Codec) -> R) -> R {
    CODEC.with(|cell| {
        let Ok(mut slot) = cell.try_borrow_mut() else {
            return operation(&mut new_codec());
        };
        let codec = slot.get_or_insert_with(new_codec);
        let result = operation(codec);
        if codec.buffer.capacity() > MAX_POOLED_SEMANTIC_BUFFER {
            codec.buffer = Vec::new();
        }
        result
    })
}

fn new_codec() -> Codec {
    Codec {
        compress: Compress::new(Compression::fast(), false),
        decompress: Decompress::new(false),
        buffer: Vec::new(),
    }
}

/// Releases the calling thread's pooled DEFLATE state (Go clears its
/// compressor/decompressor pools after a batch or restore).
pub(crate) fn release_codec_pool() {
    CODEC.with(|cell| {
        if let Ok(mut slot) = cell.try_borrow_mut() {
            *slot = None;
        }
    });
}

fn deflate(codec: &mut Codec, input: &[u8]) -> Result<Vec<u8>, PersistError> {
    codec.compress.reset();
    let mut output = Vec::with_capacity(input.len() / 3 + 64);
    loop {
        let consumed = codec.compress.total_in() as usize;
        if output.len() == output.capacity() {
            output.reserve(output.capacity().max(256));
        }
        let status = codec
            .compress
            .compress_vec(&input[consumed..], &mut output, FlushCompress::Finish)
            .map_err(|err| PersistError(format!("compress workspace graph: {err}")))?;
        if status == Status::StreamEnd {
            return Ok(output);
        }
    }
}

/// Inflates with `FlushDecompress::None`: miniz's `Finish` mode decodes
/// straight into the caller's (possibly too small) output slice and fails on
/// back-references into earlier chunks.
fn inflate<'a>(codec: &'a mut Codec, payload: &[u8]) -> Result<&'a [u8], PersistError> {
    codec.decompress.reset(false);
    codec.buffer.clear();
    loop {
        let consumed = codec.decompress.total_in() as usize;
        if codec.buffer.len() == codec.buffer.capacity() {
            if codec.buffer.len() > MAX_PERSISTED_SEMANTIC_VALUE_SIZE {
                return persist_error(format!(
                    "workspace graph exceeds {MAX_PERSISTED_SEMANTIC_VALUE_SIZE} bytes"
                ));
            }
            let grow = (payload.len() * 4).max(codec.buffer.capacity()).max(4096);
            codec.buffer.reserve(grow);
        }
        let before = (codec.decompress.total_in(), codec.decompress.total_out());
        let status = codec
            .decompress
            .decompress_vec(
                &payload[consumed..],
                &mut codec.buffer,
                FlushDecompress::None,
            )
            .map_err(|err| PersistError(format!("open workspace graph: {err}")))?;
        if status == Status::StreamEnd {
            break;
        }
        let progressed = (codec.decompress.total_in(), codec.decompress.total_out()) != before;
        if !progressed && codec.buffer.len() < codec.buffer.capacity() {
            return persist_error("open workspace graph: unexpected end of compressed data");
        }
    }
    if codec.buffer.len() > MAX_PERSISTED_SEMANTIC_VALUE_SIZE {
        return persist_error(format!(
            "workspace graph exceeds {MAX_PERSISTED_SEMANTIC_VALUE_SIZE} bytes"
        ));
    }
    Ok(&codec.buffer)
}

/// Compresses the binary encoding of `graph`.
pub fn encode_workspace_graph(
    graph: &WorkspaceGraph,
) -> Result<PersistedWorkspaceGraph, PersistError> {
    if graph.path().is_empty() {
        return persist_error("workspace graph is required");
    }
    let encoded = graph.encode();
    let payload = with_codec(|codec| deflate(codec, &encoded))?;
    Ok(PersistedWorkspaceGraph {
        format: PERSISTED_WORKSPACE_GRAPH_FORMAT,
        payload: Payload(payload),
    })
}

/// A persisted envelope whose payload borrows the repository row.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct BorrowedWorkspaceGraph<'a> {
    pub format: u8,
    pub payload: &'a [u8],
}

impl BorrowedWorkspaceGraph<'_> {
    /// Decodes the complete graph.
    pub fn decode(
        &self,
        decoder: &mut WorkspaceGraphDecoder,
    ) -> Result<WorkspaceGraph, PersistError> {
        self.decode_with(decoder, None)
    }

    /// Decodes declaration cores, hierarchy edges and references, deferring
    /// signatures and metadata to `loader`.
    pub fn decode_summary(
        &self,
        decoder: &mut WorkspaceGraphDecoder,
        loader: std::sync::Arc<dyn swlsp_php_semantic::WorkspaceGraphLoader>,
    ) -> Result<WorkspaceGraph, PersistError> {
        self.decode_with(decoder, Some(loader))
    }

    fn decode_with(
        &self,
        decoder: &mut WorkspaceGraphDecoder,
        loader: Option<std::sync::Arc<dyn swlsp_php_semantic::WorkspaceGraphLoader>>,
    ) -> Result<WorkspaceGraph, PersistError> {
        if self.format != PERSISTED_WORKSPACE_GRAPH_FORMAT {
            return persist_error(format!(
                "unsupported workspace graph format {}",
                self.format
            ));
        }
        with_codec(|codec| {
            let bytes = inflate(codec, self.payload)?;
            let result = match loader {
                Some(loader) => decoder.decode_summary(bytes, loader),
                None => decoder.decode(bytes),
            };
            result.map_err(|err| PersistError(err.to_string()))
        })
    }
}

impl PersistedWorkspaceGraph {
    pub fn borrowed(&self) -> BorrowedWorkspaceGraph<'_> {
        BorrowedWorkspaceGraph {
            format: self.format,
            payload: &self.payload.0,
        }
    }

    /// Decodes the complete graph with a one-off decoder.
    pub fn decode(&self) -> Result<WorkspaceGraph, PersistError> {
        self.borrowed().decode(&mut WorkspaceGraphDecoder::new())
    }
}

/// Reads the repository envelope in place. The payload borrows `data`.
pub fn decode_persisted_workspace_graph_borrowed(
    data: &[u8],
) -> Result<BorrowedWorkspaceGraph<'_>, PersistError> {
    let mut cursor = Cursor { data, offset: 0 };
    let context =
        |message: String| PersistError(format!("decode workspace graph envelope: {message}"));
    let field_count = cursor.map_length().map_err(context)?;
    if field_count != 2 {
        return Err(context(format!("expected 2 fields, got {field_count}")));
    }
    let mut format = None;
    let mut payload = None;
    for _ in 0..field_count {
        let key = cursor
            .bytes(false)
            .map_err(|err| PersistError(format!("decode workspace graph envelope key: {err}")))?;
        match key {
            b"format" => {
                if format.is_some() {
                    return Err(context("duplicate format".to_owned()));
                }
                let value = cursor.uint().map_err(|err| {
                    PersistError(format!("decode workspace graph envelope format: {err}"))
                })?;
                let value = u8::try_from(value)
                    .map_err(|_| context(format!("format {value} exceeds uint8")))?;
                format = Some(value);
            }
            b"payload" => {
                if payload.is_some() {
                    return Err(context("duplicate payload".to_owned()));
                }
                payload = Some(cursor.bytes(true).map_err(|err| {
                    PersistError(format!("decode workspace graph envelope payload: {err}"))
                })?);
            }
            other => {
                return Err(context(format!(
                    "unknown field {:?}",
                    String::from_utf8_lossy(other)
                )));
            }
        }
    }
    let (Some(format), Some(payload)) = (format, payload) else {
        return Err(context("missing required field".to_owned()));
    };
    if cursor.offset != data.len() {
        return Err(context(format!(
            "{} trailing bytes",
            data.len() - cursor.offset
        )));
    }
    Ok(BorrowedWorkspaceGraph { format, payload })
}

struct Cursor<'a> {
    data: &'a [u8],
    offset: usize,
}

impl<'a> Cursor<'a> {
    fn byte(&mut self) -> Result<u8, String> {
        let value = *self
            .data
            .get(self.offset)
            .ok_or_else(|| "unexpected EOF".to_owned())?;
        self.offset += 1;
        Ok(value)
    }

    fn take(&mut self, length: u64) -> Result<&'a [u8], String> {
        let remaining = self.data.len() - self.offset;
        if length > remaining as u64 {
            return Err("unexpected EOF".to_owned());
        }
        let start = self.offset;
        self.offset += length as usize;
        Ok(&self.data[start..self.offset])
    }

    fn fixed(&mut self, width: usize) -> Result<u64, String> {
        let bytes = self.take(width as u64)?;
        Ok(bytes
            .iter()
            .fold(0u64, |value, byte| (value << 8) | u64::from(*byte)))
    }

    fn map_length(&mut self) -> Result<u64, String> {
        match self.byte()? {
            code @ 0x80..=0x8f => Ok(u64::from(code & 0x0f)),
            0xde => self.fixed(2),
            0xdf => self.fixed(4),
            code => Err(format!("expected map, got code 0x{code:x}")),
        }
    }

    fn bytes(&mut self, allow_binary: bool) -> Result<&'a [u8], String> {
        let code = self.byte()?;
        let length = match code {
            0xc0 if allow_binary => return Ok(&[]),
            0xa0..=0xbf => u64::from(code & 0x1f),
            0xd9 => self.fixed(1)?,
            0xda => self.fixed(2)?,
            0xdb => self.fixed(4)?,
            0xc4 if allow_binary => self.fixed(1)?,
            0xc5 if allow_binary => self.fixed(2)?,
            0xc6 if allow_binary => self.fixed(4)?,
            _ => return Err(format!("expected string or binary, got code 0x{code:x}")),
        };
        self.take(length)
    }

    fn uint(&mut self) -> Result<u64, String> {
        match self.byte()? {
            code @ 0x00..=0x7f => Ok(u64::from(code)),
            0xcc => self.fixed(1),
            0xcd => self.fixed(2),
            0xce => self.fixed(4),
            0xcf => self.fixed(8),
            code => Err(format!("expected unsigned integer, got code 0x{code:x}")),
        }
    }
}

/// Decompresses a payload with a fresh reader (tests and diagnostics).
pub fn inflate_payload(payload: &[u8]) -> Result<Vec<u8>, PersistError> {
    let mut output = Vec::new();
    flate2::read::DeflateDecoder::new(payload)
        .take(MAX_PERSISTED_SEMANTIC_VALUE_SIZE as u64 + 1)
        .read_to_end(&mut output)
        .map_err(|err| PersistError(format!("open workspace graph: {err}")))?;
    if output.len() > MAX_PERSISTED_SEMANTIC_VALUE_SIZE {
        return persist_error(format!(
            "workspace graph exceeds {MAX_PERSISTED_SEMANTIC_VALUE_SIZE} bytes"
        ));
    }
    Ok(output)
}
