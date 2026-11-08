//! Byte offset <-> line/column mapping. Lines and columns are 0-based.
//! UTF-16 columns are only for the LSP boundary.

use std::sync::Arc;

#[derive(Debug, Clone)]
pub struct LineIndex {
    source: Arc<str>,
    line_starts: Vec<u32>,
}

impl LineIndex {
    /// A line starts after each `\n`; `\r\n` is terminated by its `\n`.
    pub fn new(source: impl Into<Arc<str>>) -> Self {
        let source: Arc<str> = source.into();
        let mut line_starts = Vec::with_capacity((source.len() / 128 + 1).min(256));
        line_starts.push(0);
        line_starts.extend(memchr_newlines(source.as_bytes()).map(|position| position as u32 + 1));
        Self {
            source,
            line_starts,
        }
    }

    pub fn source(&self) -> &str {
        &self.source
    }

    pub fn line_count(&self) -> usize {
        self.line_starts.len()
    }

    fn line_at(&self, offset: u32) -> usize {
        self.line_starts.partition_point(|start| *start <= offset) - 1
    }

    fn clamp(&self, offset: u32) -> u32 {
        offset.min(self.source.len() as u32)
    }

    fn floor_char_boundary(&self, mut offset: u32) -> u32 {
        while offset > 0 && !self.source.is_char_boundary(offset as usize) {
            offset -= 1;
        }
        offset
    }

    /// Line and byte column. Offsets past EOF clamp to EOF.
    pub fn position(&self, offset: u32) -> (u32, u32) {
        let offset = self.clamp(offset);
        let line = self.line_at(offset);
        (line as u32, offset - self.line_starts[line])
    }

    /// Line and UTF-16 code-unit column. A character straddling `offset` is
    /// clamped to its start.
    pub fn position_utf16(&self, offset: u32) -> (u32, u32) {
        let offset = self.floor_char_boundary(self.clamp(offset));
        let line = self.line_at(offset);
        let start = self.line_starts[line] as usize;
        let units = utf16_len(&self.source[start..offset as usize]);
        (line as u32, units)
    }

    /// Byte offset just after the visible content of `line` (excluding LF/CRLF).
    pub fn line_end(&self, line: u32) -> u32 {
        let line = line as usize;
        if line >= self.line_starts.len() {
            return self.source.len() as u32;
        }
        let mut end = match self.line_starts.get(line + 1) {
            Some(next) => next - 1,
            None => self.source.len() as u32,
        };
        if end > self.line_starts[line] && self.source.as_bytes()[end as usize - 1] == b'\r' {
            end -= 1;
        }
        end
    }

    pub fn line_start(&self, line: u32) -> u32 {
        self.line_starts
            .get(line as usize)
            .copied()
            .unwrap_or(self.source.len() as u32)
    }

    pub fn line_utf16_length(&self, line: u32) -> u32 {
        self.position_utf16(self.line_end(line)).1
    }

    fn line_bounds(&self, line: u32) -> Option<(u32, u32)> {
        let line = line as usize;
        let start = *self.line_starts.get(line)?;
        let end = self
            .line_starts
            .get(line + 1)
            .copied()
            .unwrap_or(self.source.len() as u32);
        Some((start, end))
    }

    /// Byte offset of a line and byte column, clamped to the line's end.
    pub fn offset(&self, line: u32, column: u32) -> u32 {
        let Some((start, end)) = self.line_bounds(line) else {
            return self.source.len() as u32;
        };
        match start.checked_add(column) {
            Some(offset) if offset <= end => offset,
            _ => end,
        }
    }

    /// Byte offset of a line and UTF-16 column. A column inside a surrogate
    /// pair advances past the whole character.
    pub fn offset_utf16(&self, line: u32, column: u32) -> u32 {
        let Some((start, end)) = self.line_bounds(line) else {
            return self.source.len() as u32;
        };
        let mut units = 0u32;
        let text = &self.source[start as usize..end as usize];
        for (index, ch) in text.char_indices() {
            if units >= column {
                return start + index as u32;
            }
            units += ch.len_utf16() as u32;
        }
        end
    }
}

/// Number of UTF-16 code units in `text`, with an ASCII fast path.
pub fn utf16_len(text: &str) -> u32 {
    if text.is_ascii() {
        return text.len() as u32;
    }
    text.chars().map(|ch| ch.len_utf16() as u32).sum()
}

fn memchr_newlines(bytes: &[u8]) -> impl Iterator<Item = usize> + '_ {
    bytes
        .iter()
        .enumerate()
        .filter_map(|(index, byte)| (*byte == b'\n').then_some(index))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn positions_round_trip() {
        let index = LineIndex::new("ab\r\nc😀d\n");
        assert_eq!(index.position(5), (1, 1));
        assert_eq!(index.position_utf16(9), (1, 3));
        assert_eq!(index.offset_utf16(1, 3), 9);
        assert_eq!(index.offset_utf16(1, 2), 9);
        assert_eq!(index.line_end(0), 2);
        assert_eq!(index.offset(9, 0), index.source().len() as u32);
        assert_eq!(index.offset(0, 99), 4);
    }
}
