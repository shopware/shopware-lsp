//! Optional LZ4 compression of persisted repository values.
//!
//! Values are named-field MessagePack, which repeats every field name in
//! every value; LZ4 shrinks the repository values of a Shopware checkout by
//! more than half for a negligible decode cost. A compressed value is
//! `0xc1`, the uncompressed length as little-endian `u32`, then one LZ4
//! block. MessagePack never uses `0xc1` ("never used" in the specification),
//! so stored values are self-describing.

use std::cell::RefCell;

use crate::error::{Error, Result};

const COMPRESSED_VALUE: u8 = 0xc1;
/// Smaller values rarely shrink enough to pay for the header.
const MIN_COMPRESSED_INPUT: usize = 256;
/// Upper bound of one decompressed value, checked before allocating.
const MAX_DECOMPRESSED_VALUE: usize = 1 << 30;
/// Pooled buffers above this capacity are released after use.
const MAX_POOLED_BUFFER: usize = 1 << 20;

thread_local! {
    static COMPRESS_BUFFER: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
    static DECOMPRESS_BUFFER: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
}

/// Runs `use_bytes` with the stored form of `encoded`: compressed when that
/// is smaller, otherwise `encoded` itself.
pub(crate) fn with_stored<R>(
    encoded: &[u8],
    compress: bool,
    use_bytes: impl FnOnce(&[u8]) -> Result<R>,
) -> Result<R> {
    if !compress || encoded.len() < MIN_COMPRESSED_INPUT || encoded.len() > u32::MAX as usize {
        return use_bytes(encoded);
    }
    with_pooled(&COMPRESS_BUFFER, |buffer| {
        if compress_into(encoded, buffer) {
            use_bytes(buffer)
        } else {
            use_bytes(encoded)
        }
    })
}

/// Runs `use_bytes` with the MessagePack bytes of a stored value.
pub(crate) fn with_decoded<R>(
    stored: &[u8],
    use_bytes: impl FnOnce(&[u8]) -> Result<R>,
) -> Result<R> {
    if stored.first() != Some(&COMPRESSED_VALUE) {
        return use_bytes(stored);
    }
    with_pooled(&DECOMPRESS_BUFFER, |buffer| {
        decompress_into(stored, buffer)?;
        use_bytes(buffer)
    })
}

fn compress_into(encoded: &[u8], output: &mut Vec<u8>) -> bool {
    output.clear();
    output.push(COMPRESSED_VALUE);
    output.extend_from_slice(&(encoded.len() as u32).to_le_bytes());
    let start = output.len();
    output.resize(
        start + lz4_flex::block::get_maximum_output_size(encoded.len()),
        0,
    );
    match lz4_flex::block::compress_into(encoded, &mut output[start..]) {
        Ok(written) => {
            output.truncate(start + written);
            output.len() < encoded.len()
        }
        Err(_) => false,
    }
}

fn decompress_into(stored: &[u8], output: &mut Vec<u8>) -> Result<()> {
    let header = stored
        .get(1..5)
        .ok_or_else(|| Error::msg("decompress index value: truncated header"))?;
    let size = u32::from_le_bytes(header.try_into().expect("four header bytes")) as usize;
    if size > MAX_DECOMPRESSED_VALUE {
        return Err(Error::msg(format!(
            "decompress index value: {size} bytes exceeds the limit"
        )));
    }
    output.clear();
    output.resize(size, 0);
    let written = lz4_flex::block::decompress_into(&stored[5..], output)
        .map_err(|error| Error::msg(format!("decompress index value: {error}")))?;
    if written != size {
        return Err(Error::msg("decompress index value: truncated data"));
    }
    Ok(())
}

fn with_pooled<R>(
    pool: &'static std::thread::LocalKey<RefCell<Vec<u8>>>,
    operation: impl FnOnce(&mut Vec<u8>) -> R,
) -> R {
    pool.with(|cell| {
        let Ok(mut buffer) = cell.try_borrow_mut() else {
            // Re-entered from a visitor: use a private buffer.
            return operation(&mut Vec::new());
        };
        let result = operation(&mut buffer);
        if buffer.capacity() > MAX_POOLED_BUFFER {
            *buffer = Vec::new();
        }
        result
    })
}

/// Releases this thread's pooled compression buffers.
pub(crate) fn clear_buffers() {
    for pool in [&COMPRESS_BUFFER, &DECOMPRESS_BUFFER] {
        pool.with(|cell| {
            if let Ok(mut buffer) = cell.try_borrow_mut() {
                *buffer = Vec::new();
            }
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn stored(encoded: &[u8], compress: bool) -> Vec<u8> {
        with_stored(encoded, compress, |bytes| Ok(bytes.to_vec())).unwrap()
    }

    fn decoded(stored: &[u8]) -> Result<Vec<u8>> {
        with_decoded(stored, |bytes| Ok(bytes.to_vec()))
    }

    #[test]
    fn compresses_large_repetitive_values_only() {
        let small = vec![0x92, 1, 2];
        assert_eq!(stored(&small, true), small);
        let large = b"\x81\xa4name\xa5value".repeat(64);
        let compressed = stored(&large, true);
        assert_eq!(compressed[0], COMPRESSED_VALUE);
        assert!(compressed.len() < large.len());
        assert_eq!(decoded(&compressed).unwrap(), large);
        assert_eq!(stored(&large, false), large);
        assert_eq!(decoded(&large).unwrap(), large);
    }

    #[test]
    fn keeps_incompressible_values_and_rejects_corrupt_ones() {
        let noise: Vec<u8> = (0..1024u32)
            .map(|index| (index.wrapping_mul(2_654_435_761) >> 13) as u8)
            .collect();
        let kept = stored(&noise, true);
        assert!(kept == noise || kept[0] == COMPRESSED_VALUE);
        assert!(decoded(&[COMPRESSED_VALUE, 1]).is_err());
        assert!(decoded(&[COMPRESSED_VALUE, 0xff, 0xff, 0xff, 0xff, 0]).is_err());
        let mut truncated = stored(&b"abcdefgh".repeat(64), true);
        truncated.truncate(truncated.len() - 2);
        assert!(decoded(&truncated).is_err());
    }
}
