//! Shopware feature-flag declarations.

mod indexer;

pub use indexer::FeatureIndexer;

use serde::{Deserialize, Serialize};
use swlsp_parser_yaml::query;
use swlsp_parser_yaml::syntax::{YAML_PAIR, YAML_SCALAR};
use swlsp_syntax::{LineIndex, SyntaxNodeExt, Tree};

/// One `name:` entry of a feature-flag YAML file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Feature {
    pub name: String,
    pub file: String,
    /// 1-based line of the name value.
    pub line: u32,
}

/// A feature file without any `name:` entry.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
#[error("could not find flags node in file: {file}")]
pub struct MissingFeatures {
    pub file: String,
}

/// Parses `document` and extracts its feature flags.
pub fn parse_feature_file(
    document: &str,
    file_path: &str,
) -> Result<Vec<Feature>, MissingFeatures> {
    let parse = swlsp_parser_yaml::parse(document);
    let line_index = LineIndex::new(parse.tree.source_arc().clone());
    parse_feature_tree(&parse.tree, &line_index, file_path)
}

/// Collects every non-null scalar value of a `name:` pair, in source order.
/// A file without any is an error.
pub fn parse_feature_tree(
    tree: &Tree,
    line_index: &LineIndex,
    file_path: &str,
) -> Result<Vec<Feature>, MissingFeatures> {
    let mut features = Vec::new();
    for pair in tree
        .root()
        .descendants()
        .filter(|node| node.kind() == YAML_PAIR)
    {
        if query::pair_key(&pair).is_none_or(|key| query::scalar_value(&key) != "name") {
            continue;
        }
        let Some(value) = query::pair_value(&pair) else {
            continue;
        };
        if value.kind() != YAML_SCALAR || query::is_null(&value) {
            continue;
        }
        let name = query::scalar_value(&value);
        if name.is_empty() {
            continue;
        }
        let (line, _) = line_index.position(value.range_trimmed_trivia().start().into());
        features.push(Feature {
            name,
            file: file_path.to_owned(),
            line: line + 1,
        });
    }
    if features.is_empty() {
        return Err(MissingFeatures {
            file: file_path.to_owned(),
        });
    }
    Ok(features)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_feature_file_finds_all_flags() {
        let file_path = concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/feature.yaml");
        let content = std::fs::read_to_string(file_path).unwrap();
        let features = parse_feature_file(&content, file_path).unwrap();
        assert_eq!(features.len(), 8);
        let expected = [
            ("v6.5.0.0", 4),
            ("v6.6.0.0", 8),
            ("v6.7.0.0", 12),
            ("v6.8.0.0", 16),
            ("DISABLE_VUE_COMPAT", 20),
            ("ACCESSIBILITY_TWEAKS", 24),
            ("TELEMETRY_METRICS", 29),
            ("FLOW_EXECUTION_AFTER_BUSINESS_PROCESS", 34),
        ];
        for feature in &features {
            let line = expected
                .iter()
                .find(|(name, _)| *name == feature.name)
                .unwrap_or_else(|| panic!("unexpected feature {}", feature.name))
                .1;
            assert_eq!(feature.line, line, "{}", feature.name);
            assert_eq!(feature.file, file_path);
        }
    }

    #[test]
    fn file_without_names_is_an_error() {
        let error = parse_feature_file(
            "shopware:\n  feature: ~\n  other:\n    name: ~\n",
            "/x/feature.yaml",
        )
        .unwrap_err();
        assert_eq!(
            error.to_string(),
            "could not find flags node in file: /x/feature.yaml"
        );
    }
}
