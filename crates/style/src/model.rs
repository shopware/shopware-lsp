use serde::{Deserialize, Serialize};
use swlsp_syntax::{TextRange, TextSize};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum ClassOccurrenceKind {
    #[default]
    Declaration = 0,
    Usage = 1,
}

/// A zero-based LSP position (UTF-16 character).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct SourcePosition {
    pub line: u32,
    pub character: u32,
}

/// A byte range into the indexed source snapshot.
#[derive(
    Clone, Copy, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord, Serialize, Deserialize,
)]
pub struct ByteRange {
    pub start: u32,
    pub end: u32,
}

impl ByteRange {
    pub fn new(start: u32, end: u32) -> Self {
        Self { start, end }
    }

    pub fn contains(&self, offset: u32) -> bool {
        offset >= self.start && offset < self.end
    }

    pub fn is_empty(&self) -> bool {
        self.start == self.end
    }
}

impl From<TextRange> for ByteRange {
    fn from(range: TextRange) -> Self {
        Self {
            start: range.start().into(),
            end: range.end().into(),
        }
    }
}

impl From<ByteRange> for TextRange {
    fn from(range: ByteRange) -> Self {
        TextRange::new(TextSize::from(range.start), TextSize::from(range.end))
    }
}

/// One static CSS class declaration or markup usage. `range` serves
/// live-document matching while `start`/`end` answer LSP locations without
/// reopening indexed files.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ClassOccurrence {
    pub name: String,
    pub file: String,
    pub range: ByteRange,
    pub start: SourcePosition,
    pub end: SourcePosition,
    pub kind: ClassOccurrenceKind,
}

/// All occurrences of one class name in one file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ClassCatalog {
    pub name: String,
    pub file: String,
    pub occurrences: Vec<ClassOccurrence>,
}
