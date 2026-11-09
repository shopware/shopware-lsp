use serde::{Deserialize, Serialize};
use swlsp_syntax::TextRange;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum OccurrenceKind {
    #[default]
    Handler,
    Dispatch,
}

impl OccurrenceKind {
    /// The stable numeric value used in persisted keys.
    pub(crate) fn ordinal(self) -> u8 {
        match self {
            OccurrenceKind::Handler => 0,
            OccurrenceKind::Dispatch => 1,
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum SourceKind {
    #[default]
    Attribute,
    Subscriber,
    ServiceTag,
    Dispatch,
}

/// One handler declaration or dispatch of a message. Ranges are byte offsets
/// into the source file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Occurrence {
    pub kind: OccurrenceKind,
    pub source: SourceKind,
    pub message: String,
    pub file: String,
    pub range: TextRange,
    pub message_range: TextRange,
    pub handler_range: TextRange,
    pub class_range: TextRange,
    pub class: String,
    pub method: String,
    pub service: String,
    pub bus: String,
    pub transport: String,
    pub priority: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Message {
    pub name: String,
    pub occurrences: Vec<Occurrence>,
}

impl Message {
    pub fn handlers(&self) -> Vec<Occurrence> {
        self.of_kind(OccurrenceKind::Handler)
    }

    pub fn dispatches(&self) -> Vec<Occurrence> {
        self.of_kind(OccurrenceKind::Dispatch)
    }

    fn of_kind(&self, kind: OccurrenceKind) -> Vec<Occurrence> {
        self.occurrences
            .iter()
            .filter(|occurrence| occurrence.kind == kind)
            .cloned()
            .collect()
    }
}
