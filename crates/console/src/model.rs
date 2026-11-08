use serde::{Deserialize, Serialize};
use swlsp_syntax::TextRange;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum InputKind {
    #[default]
    Argument,
    Option,
}

/// A declared command argument or option.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Input {
    pub name: String,
    pub kind: InputKind,
    pub shortcut: String,
    pub mode: String,
    pub description: String,
    pub default: String,
    pub file: String,
    #[serde(with = "range_serde")]
    pub range: TextRange,
}

/// A command name (or alias) with its target and inputs. `method` is set for
/// `#[AsCommand]` on a public method.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Command {
    pub name: String,
    pub canonical: String,
    pub description: String,
    pub class: String,
    pub method: String,
    pub file: String,
    #[serde(with = "range_serde")]
    pub range: TextRange,
    pub arguments: Vec<Input>,
    pub options: Vec<Input>,
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
