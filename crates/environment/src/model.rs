use serde::{Deserialize, Serialize};
use swlsp_syntax::TextRange;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum OccurrenceKind {
    #[default]
    Declaration,
    Reference,
}

impl OccurrenceKind {
    /// The Go enum value, used in persisted keys.
    pub(crate) fn ordinal(self) -> u8 {
        match self {
            OccurrenceKind::Declaration => 0,
            OccurrenceKind::Reference => 1,
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum SourceKind {
    #[default]
    DotEnv,
    DockerCompose,
    Dockerfile,
    SymfonyEnv,
}

/// One declaration or reference of an environment variable. Ranges are byte
/// offsets into the source file.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Occurrence {
    pub kind: OccurrenceKind,
    pub source: SourceKind,
    pub name: String,
    pub value: String,
    pub file: String,
    #[serde(with = "range_serde")]
    pub range: TextRange,
    #[serde(with = "range_serde")]
    pub name_range: TextRange,
    pub processors: Vec<String>,
}

impl Default for Occurrence {
    fn default() -> Self {
        Self {
            kind: OccurrenceKind::Declaration,
            source: SourceKind::DotEnv,
            name: String::new(),
            value: String::new(),
            file: String::new(),
            range: TextRange::default(),
            name_range: TextRange::default(),
            processors: Vec::new(),
        }
    }
}

/// All occurrences of one variable name, each list sorted by file and start.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Variable {
    pub name: String,
    pub declarations: Vec<Occurrence>,
    pub references: Vec<Occurrence>,
}

/// A Symfony environment reference. `range` covers the whole expression and
/// `name_range` the variable name; `processors` is the `a:b:` chain.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Reference {
    pub name: String,
    pub range: TextRange,
    pub name_range: TextRange,
    pub processors: Vec<String>,
}

impl Reference {
    pub(crate) fn into_occurrence(self) -> Occurrence {
        Occurrence {
            kind: OccurrenceKind::Reference,
            source: SourceKind::SymfonyEnv,
            name: self.name,
            range: self.range,
            name_range: self.name_range,
            processors: self.processors,
            ..Occurrence::default()
        }
    }
}

pub(crate) fn range(start: usize, end: usize) -> TextRange {
    TextRange::new((start as u32).into(), (end as u32).into())
}

mod range_serde {
    use serde::{Deserialize, Deserializer, Serialize, Serializer};
    use swlsp_syntax::TextRange;

    pub fn serialize<S: Serializer>(value: &TextRange, serializer: S) -> Result<S::Ok, S::Error> {
        (u32::from(value.start()), u32::from(value.end())).serialize(serializer)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<TextRange, D::Error> {
        let (start, end) = <(u32, u32)>::deserialize(deserializer)?;
        if start > end {
            return Err(serde::de::Error::custom("invalid text range"));
        }
        Ok(TextRange::new(start.into(), end.into()))
    }
}
