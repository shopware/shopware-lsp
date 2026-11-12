//! `check --only`: the diagnostic rules a run is narrowed to.
//!
//! A value is a rule ID or a prefix ending in `.*`. The selection decides
//! what is reported, what counts for the exit status and what `--fix`
//! fixes. A selected rule that is off unless configured is turned on for
//! the run; a rule the project configuration mentions stays as configured.

use std::collections::{BTreeMap, BTreeSet};

use serde_json::{Value, json};

use crate::CliError;

/// The rules selected with `--only`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct Selection {
    /// What the user wrote, in order, without duplicates.
    patterns: Vec<String>,
    /// The catalog rules the patterns match.
    rules: BTreeSet<String>,
    /// Selected rules that only report when configured, with their
    /// default severity.
    opt_in: BTreeMap<String, String>,
    /// Selected rules the project configuration turns off everywhere.
    off: Vec<String>,
}

/// The `--only` values, split at commas; `None` without any.
pub(crate) fn patterns(values: &[String]) -> Result<Option<Vec<String>>, CliError> {
    let mut patterns: Vec<String> = Vec::new();
    for value in values.iter().flat_map(|value| value.split(',')) {
        let value = value.trim();
        if value.is_empty() {
            return Err(CliError::usage("--only needs a diagnostic rule"));
        }
        if !patterns.iter().any(|known| known == value) {
            patterns.push(value.to_owned());
        }
    }
    Ok(Some(patterns).filter(|patterns| !patterns.is_empty()))
}

fn pattern_matches(pattern: &str, rule: &str) -> bool {
    match pattern.strip_suffix(".*") {
        Some(prefix) => rule
            .strip_prefix(prefix)
            .is_some_and(|rest| rest.starts_with('.')),
        None => pattern == rule,
    }
}

impl Selection {
    /// Resolves `patterns` against the configuration catalog. A pattern
    /// matching no rule is an error: a typo would otherwise read as a
    /// clean project.
    pub(crate) fn resolve(patterns: Vec<String>, catalog: &Value) -> Result<Self, CliError> {
        // Rule ID to (default severity, reports unless configured).
        let mut known: BTreeMap<&str, (&str, bool)> = BTreeMap::new();
        for inspection in catalog["inspections"].as_array().into_iter().flatten() {
            for rule in inspection["rules"].as_array().into_iter().flatten() {
                if let Some(id) = rule["id"].as_str() {
                    known.insert(
                        id,
                        (
                            rule["defaultSeverity"].as_str().unwrap_or("warning"),
                            rule["defaultEnabled"].as_bool().unwrap_or(true),
                        ),
                    );
                }
            }
        }
        let mut rules = BTreeSet::new();
        let mut unknown = Vec::new();
        for pattern in &patterns {
            let matched: Vec<&str> = known
                .keys()
                .copied()
                .filter(|rule| pattern_matches(pattern, rule))
                .collect();
            if matched.is_empty() {
                unknown.push(pattern.as_str());
            }
            rules.extend(matched.into_iter().map(str::to_owned));
        }
        if !unknown.is_empty() {
            return Err(CliError::usage(format!(
                "--only matches no diagnostic rule: {}",
                unknown.join(", ")
            )));
        }
        let configured = configured_rules(catalog);
        let project = &catalog["effective"]["diagnostics"]["rules"];
        let mut opt_in = BTreeMap::new();
        let mut off = Vec::new();
        for rule in &rules {
            if project[rule.as_str()].as_str() == Some("off") {
                off.push(rule.clone());
            } else if !configured.contains(rule.as_str())
                && let Some((severity, false)) = known.get(rule.as_str())
            {
                opt_in.insert(rule.clone(), (*severity).to_owned());
            }
        }
        Ok(Self {
            patterns,
            rules,
            opt_in,
            off,
        })
    }

    /// Whether a diagnostic with this code belongs to the selection.
    pub(crate) fn contains(&self, code: &str) -> bool {
        self.rules.contains(code)
    }

    /// The editor-level configuration that turns the selected opt-in rules
    /// on, if there are any.
    pub(crate) fn settings(&self) -> Option<Value> {
        (!self.opt_in.is_empty()).then(|| json!({"diagnostics": {"rules": self.opt_in}}))
    }

    /// What the user should know about the run: the narrowing itself, and
    /// selected rules the project configuration keeps off.
    pub(crate) fn notices(&self) -> Vec<String> {
        let mut notices = vec![format!(
            "only {} checked; other findings are not reported",
            self.patterns.join(", ")
        )];
        if !self.off.is_empty() {
            notices.push(format!(
                "{} is off in the project configuration and was not checked",
                self.off.join(", ")
            ));
        }
        notices
    }
}

/// Every rule a configuration layer mentions: the project file, its path
/// overrides and the nested extension files. Their setting is kept.
fn configured_rules(catalog: &Value) -> BTreeSet<&str> {
    fn collect<'a>(diagnostics: &'a Value, rules: &mut BTreeSet<&'a str>) {
        let keys = |value: &'a Value| value.as_object().into_iter().flat_map(|map| map.keys());
        rules.extend(keys(&diagnostics["rules"]).map(String::as_str));
        for entry in diagnostics["overrides"].as_array().into_iter().flatten() {
            rules.extend(keys(&entry["rules"]).map(String::as_str));
        }
    }
    let mut rules = BTreeSet::new();
    collect(&catalog["effective"]["diagnostics"], &mut rules);
    for scope in catalog["scopes"].as_array().into_iter().flatten() {
        collect(&scope["configuration"]["diagnostics"], &mut rules);
    }
    rules
}
