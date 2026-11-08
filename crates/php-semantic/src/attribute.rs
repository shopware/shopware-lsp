//! Attribute lookups shared by hover, diagnostics, and framework metadata.

use crate::str::Str;

use crate::symbol::{Attribute, AttributeValue, AttributeValueKind};
use crate::text::equal_fold;

/// The first attribute matching a short or fully qualified class name.
/// Attribute names are case-insensitive in PHP.
pub fn attribute_named<'a>(attributes: &'a [Attribute], expected: &str) -> Option<&'a Attribute> {
    let expected = trim_attribute_name(expected);
    attributes.iter().find(|attribute| {
        let candidate = trim_attribute_name(&attribute.name);
        equal_fold(candidate, expected) || has_short_name(candidate, expected)
    })
}

fn has_short_name(candidate: &str, expected: &str) -> bool {
    if expected.contains('\\') {
        return false;
    }
    candidate
        .rfind('\\')
        .is_some_and(|separator| equal_fold(&candidate[separator + 1..], expected))
}

fn trim_attribute_name(name: &str) -> &str {
    name.trim().trim_matches('\\')
}

impl Attribute {
    /// A named argument when present, otherwise the positional argument at
    /// `index`, mirroring how JetBrains attribute constructors expose stable
    /// parameter names while remaining compatible with positional usage.
    pub fn argument(&self, name: &str, index: usize) -> Option<&AttributeValue> {
        if let Some(argument) = self
            .arguments
            .iter()
            .find(|argument| !argument.name.is_empty() && equal_fold(&argument.name, name))
        {
            return Some(&argument.value);
        }
        self.arguments
            .get(index)
            .filter(|argument| argument.name.is_empty())
            .map(|argument| &argument.value)
    }
}

/// The structured JetBrains/PHP `Deprecated` attribute payload.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Deprecation {
    pub reason: Str,
    pub replacement: Str,
    pub since: Str,
}

/// Extracts both `JetBrains\PhpStorm\Deprecated` and the native
/// `Deprecated` attribute conventions. `Some` reports attribute presence,
/// even without structured details.
pub fn deprecation_of(attributes: &[Attribute]) -> Option<Deprecation> {
    let attribute = attribute_named(attributes, "Deprecated")?;
    let mut result = Deprecation::default();
    if let Some(value) = attribute.argument("reason", 0) {
        result.reason = attribute_string(value);
    }
    if result.reason.is_empty()
        && let Some(value) = attribute.argument("message", 0)
    {
        result.reason = attribute_string(value);
    }
    if let Some(value) = attribute.argument("replacement", 1) {
        result.replacement = attribute_string(value);
    }
    if let Some(value) = attribute.argument("since", 2) {
        result.since = attribute_string(value);
    }
    Some(result)
}

fn attribute_string(value: &AttributeValue) -> Str {
    if value.kind == AttributeValueKind::String {
        value.value.clone()
    } else {
        Str::default()
    }
}
