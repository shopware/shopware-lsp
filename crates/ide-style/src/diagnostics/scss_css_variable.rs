//! Administration SCSS variables that have a CSS custom property.
//!
//! Since Shopware 6.5 the Administration emits every variable of its
//! `variables.scss` as `--<name>` on `:root`. A declaration value can read
//! that property, which follows runtime changes such as themes, where the
//! SCSS variable is fixed at build time.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_scss::query as scssquery;
use swlsp_parser_scss::syntax::{SCSS_DECLARATION, SCSS_VARIABLE, TK_IDENTIFIER, TK_OPERATOR};
use swlsp_shopware::ResolvedVersion;
use swlsp_style::{analyze_variables, normalize_variable_name};
use swlsp_syntax::{SyntaxElement, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};
use swlsp_uriutil::filepath;

use crate::diagnostics::{LookupError, ProblemAnalyzer};

pub const SCSS_PREFER_CSS_VARIABLE_CODE: &str = "scss.variable.prefer-css-variable";

/// The file whose variables the Administration turns into custom properties.
const ADMIN_VARIABLES_FILE: &str =
    "/administration/resources/app/administration/src/app/assets/scss/variables.scss";

/// Where SCSS variables are declared.
pub trait ScssVariableSource: Send + Sync {
    /// The name as declared, per declaring file, of the variable `name`.
    fn declarations(&self, name: &str) -> Result<Vec<(String, String)>, BoxError>;
    /// Advances whenever an answer may change; `None` when unknown.
    fn revision(&self) -> Option<u64> {
        None
    }
}

impl ScssVariableSource for swlsp_style::Index {
    fn revision(&self) -> Option<u64> {
        Some(swlsp_indexer::Revisioned::revision(self))
    }

    fn declarations(&self, name: &str) -> Result<Vec<(String, String)>, BoxError> {
        Ok(self
            .variable_declarations(name)?
            .into_iter()
            .map(|occurrence| (occurrence.file, occurrence.name))
            .collect())
    }
}

pub struct ScssCssVariableAnalyzer {
    variables: Option<Arc<dyn ScssVariableSource>>,
    /// Whether the project's Administration generates the properties.
    generated: bool,
}

impl ScssCssVariableAnalyzer {
    pub fn new(variables: Option<Arc<dyn ScssVariableSource>>, version: ResolvedVersion) -> Self {
        Self {
            variables,
            generated: version.at_least(6, 5, 0),
        }
    }
}

impl ProblemAnalyzer for ScssCssVariableAnalyzer {
    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        if !self.generated || !document.uri.contains("Resources/app/administration") {
            return Some(0);
        }
        match &self.variables {
            Some(variables) => variables.revision(),
            None => Some(0),
        }
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(variables), Some(root)) = (&self.variables, document.root()) else {
            return Ok(Vec::new());
        };
        if !self.generated
            || !document.uri.contains("Resources/app/administration")
            || !eq_ignore_case(filepath::ext(&document.uri), ".scss")
        {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(&document.uri)
            .map_err(|error| LookupError::new("resolve SCSS document path", error))?;
        // A variable the file binds itself (a parameter, a local override)
        // is not the Administration's.
        let analysis = analyze_variables(&path, &root);
        let local: HashSet<String> = analysis
            .bindings
            .iter()
            .chain(&analysis.global_declarations)
            .map(|binding| normalize_variable_name(&binding.name))
            .collect();
        let mut properties: HashMap<String, Option<String>> = HashMap::new();
        let mut result = Vec::new();
        for variable in scssquery::nodes(&root, &[SCSS_VARIABLE]) {
            ctx.check()?;
            if !is_plain_declaration_value(&variable) {
                continue;
            }
            let Some(name) = scssquery::variable_name(&variable) else {
                continue;
            };
            let normalized = normalize_variable_name(&name);
            if local.contains(&normalized) {
                continue;
            }
            let property = match properties.get(&normalized) {
                Some(property) => property.clone(),
                None => {
                    let property = variables
                        .declarations(&normalized)
                        .map_err(|error| {
                            LookupError::new(format!("resolve SCSS variable ${name}"), error)
                        })?
                        .into_iter()
                        .find(|(file, _)| {
                            lowercase(&filepath::to_slash(file)).ends_with(ADMIN_VARIABLES_FILE)
                        })
                        .map(|(_, declared)| {
                            format!("--{}", declared.strip_prefix('$').unwrap_or(&declared))
                        });
                    properties.insert(normalized, property.clone());
                    property
                }
            };
            let Some(property) = property else {
                continue;
            };
            let replacement = format!("var({property})");
            let expected = variable.text().to_string().trim().to_owned();
            let mut payload = Map::new();
            payload.insert("title".into(), Value::String(format!("Use {replacement}")));
            payload.insert("expected".into(), Value::String(expected.clone()));
            payload.insert("replacement".into(), Value::String(replacement.clone()));
            payload.insert("equivalent".into(), Value::Bool(true));
            result.push(Problem {
                id: SCSS_PREFER_CSS_VARIABLE_CODE.into(),
                range: variable.range_trimmed_trivia(),
                element: Some(SyntaxElement::Node(variable)),
                message: format!(
                    "{expected} has the CSS custom property {property}; use {replacement}"
                ),
                severity: Some(DiagnosticSeverity::HINT),
                source: "scss".into(),
                payload: Some(Value::Object(payload)),
                ..Problem::default()
            });
        }
        Ok(result)
    }
}

/// Whether `variable` stands on its own in the value of a CSS declaration:
/// not computed with, not passed to a function or mixin, not interpolated,
/// and not in a query, where Sass needs the value at build time.
fn is_plain_declaration_value(variable: &SyntaxNode) -> bool {
    let Some(declaration) = variable
        .parent()
        .filter(|parent| parent.kind() == SCSS_DECLARATION)
    else {
        return false;
    };
    let mut tokens = declaration
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .filter(|token| !token.kind().is_trivia());
    // A custom property takes its value as written: `--a: $b` is no use.
    let property = tokens.next();
    if !property.is_some_and(|property| {
        property.kind() == TK_IDENTIFIER && !property.text().starts_with("--")
    }) {
        return false;
    }
    // `!important` and `!default` are flags, not arithmetic.
    !tokens.any(|token| token.kind() == TK_OPERATOR && token.text() != "!")
}
