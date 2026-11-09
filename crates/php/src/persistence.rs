//! Persisted compact workspace graphs.
//!
//! A repository value is a small MessagePack envelope `{format, payload}`
//! whose payload is the LZ4-compressed binary encoding of one
//! [`WorkspaceGraph`] (`swlsp-php-semantic`'s own cache format). Restoration
//! parses the envelope in place, so the compressed payload is borrowed from
//! the SQLite row instead of copied.

use serde::de::{self, Visitor};
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use std::cell::RefCell;
use std::fmt;
use swlsp_php_semantic::{WorkspaceGraph, WorkspaceGraphDecoder};

/// Size-prefixed LZ4 blocks over the `swlsp-php-semantic` binary graph
/// codec. LZ4 decompresses about 40 times faster than the former raw
/// DEFLATE (format 1), which dominated warm-start restore, for about 20%
/// more disk.
pub(crate) const PERSISTED_WORKSPACE_GRAPH_FORMAT: u8 = 2;
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

/// A failure persisting or restoring a workspace graph.
#[derive(Debug, thiserror::Error)]
pub enum PersistError {
    #[error("workspace graph is required")]
    MissingGraph,
    #[error("unsupported workspace graph format {0}")]
    UnsupportedFormat(u8),
    #[error("workspace graph exceeds {MAX_PERSISTED_SEMANTIC_VALUE_SIZE} bytes")]
    TooLarge,
    #[error("open workspace graph: {0}")]
    Decompress(#[from] lz4_flex::block::DecompressError),
    #[error("open workspace graph: unexpected end of compressed data")]
    Truncated,
    #[error(transparent)]
    Decode(#[from] swlsp_php_semantic::DecodeError),
    #[error("decode workspace graph envelope: {0}")]
    Envelope(#[from] EnvelopeError),
}

/// A malformed `{format, payload}` repository envelope.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
pub enum EnvelopeError {
    #[error("unexpected end of data")]
    UnexpectedEnd,
    #[error("expected {expected}, got code 0x{code:x}")]
    UnexpectedCode { expected: &'static str, code: u8 },
    #[error("expected 2 fields, got {0}")]
    FieldCount(u64),
    #[error("duplicate {0}")]
    DuplicateField(&'static str),
    #[error("format {0} exceeds uint8")]
    FormatOutOfRange(u64),
    #[error("unknown field {0:?}")]
    UnknownField(String),
    #[error("missing required field")]
    MissingField,
    #[error("{0} trailing bytes")]
    TrailingBytes(usize),
}

thread_local! {
    // Decompressed graphs land in a per-thread buffer, released when one
    // value grows it beyond the pooled bound.
    static BUFFER: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
}

fn with_buffer<R>(operation: impl FnOnce(&mut Vec<u8>) -> R) -> R {
    BUFFER.with(|cell| {
        let Ok(mut buffer) = cell.try_borrow_mut() else {
            return operation(&mut Vec::new());
        };
        let result = operation(&mut buffer);
        if buffer.capacity() > MAX_POOLED_SEMANTIC_BUFFER {
            *buffer = Vec::new();
        }
        result
    })
}

/// Releases the calling thread's pooled decompression buffer after a batch
/// or restore.
pub(crate) fn release_codec_pool() {
    BUFFER.with(|cell| {
        if let Ok(mut buffer) = cell.try_borrow_mut() {
            *buffer = Vec::new();
        }
    });
}

/// LZ4 block with a little-endian `u32` uncompressed-size prefix.
fn compress(input: &[u8]) -> Vec<u8> {
    lz4_flex::block::compress_prepend_size(input)
}

/// Decompresses into `buffer`, rejecting payloads whose declared size
/// exceeds [`MAX_PERSISTED_SEMANTIC_VALUE_SIZE`] before allocating.
fn decompress<'a>(buffer: &'a mut Vec<u8>, payload: &[u8]) -> Result<&'a [u8], PersistError> {
    let (size, compressed) = lz4_flex::block::uncompressed_size(payload)?;
    if size > MAX_PERSISTED_SEMANTIC_VALUE_SIZE {
        return Err(PersistError::TooLarge);
    }
    buffer.clear();
    buffer.resize(size, 0);
    let written = lz4_flex::block::decompress_into(compressed, buffer)?;
    if written != size {
        return Err(PersistError::Truncated);
    }
    Ok(buffer)
}

/// Compresses the binary encoding of `graph`.
pub fn encode_workspace_graph(
    graph: &WorkspaceGraph,
) -> Result<PersistedWorkspaceGraph, PersistError> {
    if graph.path().is_empty() {
        return Err(PersistError::MissingGraph);
    }
    let encoded = graph.encode();
    let payload = compress(&encoded);
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
            return Err(PersistError::UnsupportedFormat(self.format));
        }
        with_buffer(|buffer| {
            let bytes = decompress(buffer, self.payload)?;
            let result = match loader {
                Some(loader) => decoder.decode_summary(bytes, loader),
                None => decoder.decode(bytes),
            };
            Ok(result?)
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
    Ok(read_envelope(data)?)
}

fn read_envelope(data: &[u8]) -> Result<BorrowedWorkspaceGraph<'_>, EnvelopeError> {
    let mut cursor = Cursor { data, offset: 0 };
    let field_count = cursor.map_length()?;
    if field_count != 2 {
        return Err(EnvelopeError::FieldCount(field_count));
    }
    let mut format = None;
    let mut payload = None;
    for _ in 0..field_count {
        match cursor.bytes(false)? {
            b"format" => {
                if format.is_some() {
                    return Err(EnvelopeError::DuplicateField("format"));
                }
                let value = cursor.uint()?;
                let value =
                    u8::try_from(value).map_err(|_| EnvelopeError::FormatOutOfRange(value))?;
                format = Some(value);
            }
            b"payload" => {
                if payload.is_some() {
                    return Err(EnvelopeError::DuplicateField("payload"));
                }
                payload = Some(cursor.bytes(true)?);
            }
            other => {
                return Err(EnvelopeError::UnknownField(
                    String::from_utf8_lossy(other).into_owned(),
                ));
            }
        }
    }
    let (Some(format), Some(payload)) = (format, payload) else {
        return Err(EnvelopeError::MissingField);
    };
    if cursor.offset != data.len() {
        return Err(EnvelopeError::TrailingBytes(data.len() - cursor.offset));
    }
    Ok(BorrowedWorkspaceGraph { format, payload })
}

struct Cursor<'a> {
    data: &'a [u8],
    offset: usize,
}

impl<'a> Cursor<'a> {
    fn byte(&mut self) -> Result<u8, EnvelopeError> {
        let value = *self
            .data
            .get(self.offset)
            .ok_or(EnvelopeError::UnexpectedEnd)?;
        self.offset += 1;
        Ok(value)
    }

    fn take(&mut self, length: u64) -> Result<&'a [u8], EnvelopeError> {
        let remaining = self.data.len() - self.offset;
        if length > remaining as u64 {
            return Err(EnvelopeError::UnexpectedEnd);
        }
        let start = self.offset;
        self.offset += length as usize;
        Ok(&self.data[start..self.offset])
    }

    fn fixed(&mut self, width: usize) -> Result<u64, EnvelopeError> {
        let bytes = self.take(width as u64)?;
        Ok(bytes
            .iter()
            .fold(0u64, |value, byte| (value << 8) | u64::from(*byte)))
    }

    fn map_length(&mut self) -> Result<u64, EnvelopeError> {
        match self.byte()? {
            code @ 0x80..=0x8f => Ok(u64::from(code & 0x0f)),
            0xde => self.fixed(2),
            0xdf => self.fixed(4),
            code => Err(EnvelopeError::UnexpectedCode {
                expected: "map",
                code,
            }),
        }
    }

    fn bytes(&mut self, allow_binary: bool) -> Result<&'a [u8], EnvelopeError> {
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
            _ => {
                return Err(EnvelopeError::UnexpectedCode {
                    expected: "string or binary",
                    code,
                });
            }
        };
        self.take(length)
    }

    fn uint(&mut self) -> Result<u64, EnvelopeError> {
        match self.byte()? {
            code @ 0x00..=0x7f => Ok(u64::from(code)),
            0xcc => self.fixed(1),
            0xcd => self.fixed(2),
            0xce => self.fixed(4),
            0xcf => self.fixed(8),
            code => Err(EnvelopeError::UnexpectedCode {
                expected: "unsigned integer",
                code,
            }),
        }
    }
}

/// Decompresses a payload into a fresh buffer (tests and diagnostics).
pub fn inflate_payload(payload: &[u8]) -> Result<Vec<u8>, PersistError> {
    let mut output = Vec::new();
    decompress(&mut output, payload)?;
    Ok(output)
}
