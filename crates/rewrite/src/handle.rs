//! Compact, serializable references to immutable syntax elements.

use serde::{Deserialize, Deserializer, Serialize, Serializer};
use swlsp_language::LanguageId;
use swlsp_syntax::{Kind, SyntaxElement, TextRange, TextSize, Tree};

#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum HandleError {
    /// Go `ErrStaleHandle`: the handle belongs to another document snapshot.
    #[error("syntax element handle is stale")]
    Stale,
    /// Go `ErrMissingHandle`: the handle cannot be resolved in the tree.
    #[error("syntax element handle cannot be resolved")]
    Missing,
    /// The recorded element no longer exists at its range.
    #[error("syntax element handle is stale: {range:?} kind {kind}")]
    StaleElement { range: TextRange, kind: String },
}

impl HandleError {
    /// Mirrors `errors.Is(err, ErrStaleHandle)`.
    pub fn is_stale(&self) -> bool {
        matches!(self, HandleError::Stale | HandleError::StaleElement { .. })
    }
}

/// A reference to an immutable CST element, valid only for the recorded
/// document version.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ElementHandle {
    pub uri: String,
    pub version: i32,
    #[serde(with = "language_serde")]
    pub language: LanguageId,
    #[serde(with = "kind_serde")]
    pub kind: Kind,
    #[serde(with = "range_serde")]
    pub range: TextRange,
    /// Encoded as a string because code-action data commonly passes through
    /// JavaScript clients, whose numbers cannot hold every `u64`.
    #[serde(with = "hash_serde")]
    pub text_hash: u64,
}

impl ElementHandle {
    pub fn new(
        uri: impl Into<String>,
        version: i32,
        language: LanguageId,
        element: impl Into<SyntaxElement>,
    ) -> Self {
        let element: SyntaxElement = element.into();
        let mut hash = Fnv64::new();
        match &element {
            SyntaxElement::Node(node) => node
                .text()
                .for_each_chunk(|chunk| hash.write(chunk.as_bytes())),
            SyntaxElement::Token(token) => hash.write(token.text().as_bytes()),
        }
        Self {
            uri: uri.into(),
            version,
            language,
            kind: element.kind(),
            range: element.text_range(),
            text_hash: hash.finish(),
        }
    }

    /// Resolves the handle against the current document snapshot.
    pub fn resolve(
        &self,
        uri: &str,
        version: i32,
        language: LanguageId,
        tree: &Tree,
    ) -> Result<SyntaxElement, HandleError> {
        if self.uri != uri || self.version != version || self.language != language {
            return Err(HandleError::Stale);
        }
        let root = tree.root();
        if self.range.end() > root.text_range().end() {
            return Err(HandleError::Missing);
        }
        let mut candidate = Some(root.covering_element(self.range));
        while let Some(element) = candidate {
            if element.kind() == self.kind
                && element.text_range() == self.range
                && hash_text(tree.text(self.range)) == self.text_hash
            {
                return Ok(element);
            }
            candidate = element.parent().map(SyntaxElement::Node);
        }
        Err(HandleError::StaleElement {
            range: self.range,
            kind: self.kind.name().into_owned(),
        })
    }
}

struct Fnv64(u64);

impl Fnv64 {
    fn new() -> Self {
        Self(0xcbf2_9ce4_8422_2325)
    }

    fn write(&mut self, bytes: &[u8]) {
        for byte in bytes {
            self.0 ^= u64::from(*byte);
            self.0 = self.0.wrapping_mul(0x0000_0100_0000_01b3);
        }
    }

    fn finish(&self) -> u64 {
        self.0
    }
}

fn hash_text(value: &str) -> u64 {
    let mut hash = Fnv64::new();
    hash.write(value.as_bytes());
    hash.finish()
}

mod language_serde {
    use super::*;

    pub fn serialize<S: Serializer>(value: &LanguageId, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(value.as_str())
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<LanguageId, D::Error> {
        let id = String::deserialize(deserializer)?;
        LanguageId::from_id(&id)
            .ok_or_else(|| serde::de::Error::custom(format!("unknown language {id:?}")))
    }
}

mod kind_serde {
    use super::*;

    pub fn serialize<S: Serializer>(value: &Kind, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_u16(value.0)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<Kind, D::Error> {
        u16::deserialize(deserializer).map(Kind)
    }
}

mod range_serde {
    use super::*;

    /// Go's `cst.TextRange` has no JSON tags, so the anchor is written as
    /// `{"Start","End"}`; Go's decoder matches keys case-insensitively.
    #[derive(Serialize, Deserialize)]
    struct Wire {
        #[serde(rename = "Start", alias = "start")]
        start: u32,
        #[serde(rename = "End", alias = "end")]
        end: u32,
    }

    pub fn serialize<S: Serializer>(value: &TextRange, serializer: S) -> Result<S::Ok, S::Error> {
        Wire {
            start: value.start().into(),
            end: value.end().into(),
        }
        .serialize(serializer)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<TextRange, D::Error> {
        let wire = Wire::deserialize(deserializer)?;
        if wire.start > wire.end {
            return Err(serde::de::Error::custom("range start exceeds end"));
        }
        Ok(TextRange::new(
            TextSize::from(wire.start),
            TextSize::from(wire.end),
        ))
    }
}

mod hash_serde {
    use super::*;

    pub fn serialize<S: Serializer>(value: &u64, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(&value.to_string())
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(deserializer: D) -> Result<u64, D::Error> {
        String::deserialize(deserializer)?
            .parse()
            .map_err(serde::de::Error::custom)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fnv64a_matches_reference_vectors() {
        assert_eq!(hash_text(""), 0xcbf29ce484222325);
        assert_eq!(hash_text("a"), 0xaf63dc4c8601ec8c);
    }
}
