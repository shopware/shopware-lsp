use serde::{Deserialize, Serialize};
use swlsp_syntax::TextRange;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum OccurrenceKind {
    #[default]
    Listener,
    Dispatch,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum SourceKind {
    #[default]
    Subscriber,
    Attribute,
    ServiceTag,
    Dispatch,
}

/// One listener declaration or dispatch of an event. Ranges are byte offsets
/// into the source file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Occurrence {
    pub kind: OccurrenceKind,
    pub source: SourceKind,
    pub file: String,
    pub range: TextRange,
    pub method_range: TextRange,
    pub class: String,
    pub method: String,
    pub service: String,
    pub priority: String,
    pub event_type: String,
}

/// The persisted occurrences of one event in one file. An event is named
/// directly (`name`), through a class constant (`expression`, resolved at
/// query time) or inferred from the listener's first parameter type.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Record {
    pub name: String,
    pub expression: String,
    pub event_type: String,
    pub file: String,
    pub occurrences: Vec<Occurrence>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Constant {
    pub expression: String,
    pub value: String,
    pub file: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Event {
    pub name: String,
    pub event_type: String,
    pub occurrences: Vec<Occurrence>,
}

impl Event {
    pub fn listeners(&self) -> Vec<Occurrence> {
        self.of_kind(OccurrenceKind::Listener)
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
