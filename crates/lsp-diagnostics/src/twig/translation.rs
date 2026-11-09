//! Missing translation keys and domains.

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_php::{PhpContext, PhpIndex, assistant_argument_tags, assistant_sibling_string_argument};
use swlsp_snippet::SnippetIndexer;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};
use swlsp_translation::{Index, Reference, ReferenceRole};

use super::{lower_ext, strings_value};
use crate::LookupError;
use crate::ProblemAnalyzer;
use crate::common::value_node_text_range;

const MISSING_TRANSLATION_KEY_CODE: &str = "symfony.translation.key.missing";
const MISSING_TRANSLATION_DOMAIN_CODE: &str = "symfony.translation.domain.missing";

pub struct TranslationAnalyzer {
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
    snippet_index: Option<Arc<SnippetIndexer>>,
}

impl TranslationAnalyzer {
    pub fn new(
        index: Option<Arc<Index>>,
        php_index: Option<PhpIndex>,
        snippet_index: Option<Arc<SnippetIndexer>>,
    ) -> Self {
        Self {
            index,
            php_index,
            snippet_index,
        }
    }

    fn translation_exists(&self, index: &Index, domain: &str, key: &str) -> Result<bool, BoxError> {
        if index.has_message(domain, key)? {
            return Ok(true);
        }
        let Some(snippets) = &self.snippet_index else {
            return Ok(false);
        };
        if !eq_ignore_case(domain, "messages") {
            return Ok(false);
        }
        Ok(!snippets.frontend_snippet(key)?.is_empty())
    }
}

fn static_translation_key(extension: &str, node: &SyntaxNode, key: &str) -> bool {
    if key.is_empty() || key.contains('$') {
        return false;
    }
    if extension == ".twig" {
        return swlsp_parser_twig::query::string_is_static(node);
    }
    true
}

impl ProblemAnalyzer for TranslationAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(
            crate::inputs(&self.index)
                + crate::inputs(&self.php_index)
                + crate::inputs(&self.snippet_index),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(root)) = (&self.index, document.root()) else {
            return Ok(Vec::new());
        };
        let extension = lower_ext(&document.uri);
        if extension != ".php" && extension != ".twig" {
            return Ok(Vec::new());
        }
        let mut validation = None;
        if extension == ".php"
            && let Some(php_index) = &self.php_index
        {
            validation = swlsp_lsp_phpanalysis::context_for_document(php_index, document)?;
        }
        let mut run = Run {
            analyzer: self,
            index,
            ctx,
            document,
            root: &root,
            domains: None,
            extension,
            validation,
            result: Vec::new(),
            seen: HashSet::new(),
        };
        let analyzed = run.analyze_references();
        if run.domains_empty() {
            // Without an indexed domain there is nothing to check against.
            return Ok(Vec::new());
        }
        analyzed?;
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        if run.extension == ".php" && self.php_index.is_some() {
            let analyzed = run.analyze_assistant_arguments();
            if run.domains_empty() {
                return Ok(Vec::new());
            }
            analyzed?;
        }
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        Ok(run.result)
    }
}

struct Run<'a> {
    analyzer: &'a TranslationAnalyzer,
    index: &'a Index,
    ctx: &'a Context,
    document: &'a TextDocument,
    root: &'a SyntaxNode,
    /// Loaded on the first reference: listing domains scans every indexed
    /// message, and most documents have no translation reference.
    domains: Option<Domains>,
    extension: String,
    validation: Option<PhpContext>,
    result: Vec<Problem>,
    seen: HashSet<String>,
}

fn role_number(role: ReferenceRole) -> u8 {
    match role {
        ReferenceRole::None => 0,
        ReferenceRole::Key => 1,
        ReferenceRole::Domain => 2,
        ReferenceRole::Placeholder => 3,
    }
}

struct Domains {
    names: Vec<String>,
    lowered: HashSet<String>,
}

impl Run<'_> {
    /// Loads the indexed domains once per run.
    fn domains(&mut self) -> Result<&Domains, BoxError> {
        if self.domains.is_none() {
            let names = self
                .index
                .domains()
                .map_err(|error| LookupError::new("query translation domains", error))?;
            let lowered = names.iter().map(|domain| lowercase(domain)).collect();
            self.domains = Some(Domains { names, lowered });
        }
        Ok(self.domains.as_ref().expect("domains loaded"))
    }

    /// Whether domains were loaded and none is indexed.
    fn domains_empty(&self) -> bool {
        self.domains
            .as_ref()
            .is_some_and(|domains| domains.names.is_empty())
    }

    fn analyze_references(&mut self) -> Result<(), BoxError> {
        for reference in
            swlsp_translation::references(&self.document.uri, self.root, self.document.source())
        {
            if self.ctx.is_cancelled() {
                return Ok(());
            }
            let Some(node) = reference.node.clone() else {
                continue;
            };
            if !self.valid_reference(&reference) || !self.mark_reference_seen(&reference, &node) {
                continue;
            }
            match reference.role {
                ReferenceRole::Domain => self.add_missing_domain(&node, &reference.domain)?,
                ReferenceRole::Key => {
                    self.add_missing_key(&node, &reference.domain, &reference.key)?;
                }
                _ => {}
            }
        }
        Ok(())
    }

    fn valid_reference(&self, reference: &Reference) -> bool {
        self.extension != ".php"
            || swlsp_translation::validate_php_reference(
                self.validation.as_ref(),
                reference,
                self.analyzer.php_index.as_ref(),
            )
    }

    fn mark_reference_seen(&mut self, reference: &Reference, node: &SyntaxNode) -> bool {
        let range = node.range_trimmed_trivia();
        let identity = format!(
            "{}:{}:{}",
            role_number(reference.role),
            u32::from(range.start()),
            u32::from(range.end())
        );
        self.seen.insert(identity)
    }

    fn has_domain(&mut self, domain: &str) -> Result<bool, BoxError> {
        let domains = self.domains()?;
        // An empty catalog disables the analysis (see `domains_empty`).
        Ok(domains.names.is_empty() || domains.lowered.contains(&lowercase(domain)))
    }

    fn add_missing_domain(&mut self, node: &SyntaxNode, domain: &str) -> Result<(), BoxError> {
        if domain.is_empty() || self.has_domain(domain)? {
            return Ok(());
        }
        let mut payload = Map::new();
        payload.insert("domain".into(), Value::String(domain.to_owned()));
        payload.insert(
            "suggestions".into(),
            strings_value(swlsp_suggestion::similar(domain, &self.domains()?.names)),
        );
        self.result.push(Problem {
            id: MISSING_TRANSLATION_DOMAIN_CODE.into(),
            range: value_node_text_range(Some(node), domain),
            message: format!("Translation domain '{domain}' not found"),
            source: "symfony".into(),
            severity: Some(protocol::DiagnosticSeverity::WARNING),
            payload: Some(Value::Object(payload)),
            ..Problem::default()
        });
        Ok(())
    }

    fn add_missing_key(
        &mut self,
        node: &SyntaxNode,
        domain: &str,
        key: &str,
    ) -> Result<(), BoxError> {
        if !static_translation_key(&self.extension, node, key)
            || self.domains()?.names.is_empty()
            || !self.has_domain(domain)?
        {
            return Ok(());
        }
        if self.analyzer.translation_exists(self.index, domain, key)? {
            return Ok(());
        }
        let keys = self.index.keys(domain).map_err(|error| {
            LookupError::new(
                format!("query translation keys for domain {:?}", domain),
                error,
            )
        })?;
        let mut payload = Map::new();
        payload.insert("domain".into(), Value::String(domain.to_owned()));
        payload.insert("key".into(), Value::String(key.to_owned()));
        payload.insert(
            "suggestions".into(),
            strings_value(swlsp_suggestion::similar(key, &keys)),
        );
        self.result.push(Problem {
            id: MISSING_TRANSLATION_KEY_CODE.into(),
            range: value_node_text_range(Some(node), key),
            message: format!("Translation key '{key}' not found in domain '{domain}'"),
            source: "symfony".into(),
            severity: Some(protocol::DiagnosticSeverity::WARNING),
            payload: Some(Value::Object(payload)),
            ..Problem::default()
        });
        Ok(())
    }

    fn analyze_assistant_arguments(&mut self) -> Result<(), BoxError> {
        for literal in phpquery::nodes(self.root, &[PHP_STRING]) {
            if self.ctx.is_cancelled() {
                return Ok(());
            }
            self.analyze_assistant_literal(&literal)?;
        }
        Ok(())
    }

    fn analyze_assistant_literal(&mut self, literal: &SyntaxNode) -> Result<(), BoxError> {
        let tags = match &self.validation {
            Some(context) => {
                assistant_argument_tags(context, literal, &["TranslationDomain", "TranslationKey"])
                    .map(|(_, tags)| tags)
                    .unwrap_or_default()
            }
            None => Vec::new(),
        };
        let value = phpquery::string_value(literal);
        if value.is_empty() {
            return Ok(());
        }
        for tag in tags {
            if !self.mark_assistant_seen(literal, &tag) {
                continue;
            }
            match tag.as_str() {
                "TranslationDomain" => self.add_missing_domain(literal, &value)?,
                "TranslationKey" => {
                    let domain = self.assistant_domain(literal);
                    self.add_missing_key(literal, &domain, &value)?;
                }
                _ => {}
            }
        }
        Ok(())
    }

    fn mark_assistant_seen(&mut self, literal: &SyntaxNode, tag: &str) -> bool {
        let range = literal.range_trimmed_trivia();
        let identity = format!(
            "assistant:{tag}:{}:{}",
            u32::from(range.start()),
            u32::from(range.end())
        );
        self.seen.insert(identity)
    }

    fn assistant_domain(&self, literal: &SyntaxNode) -> String {
        self.validation
            .as_ref()
            .and_then(|context| {
                assistant_sibling_string_argument(context, literal, "TranslationDomain")
            })
            .unwrap_or_else(|| "messages".to_owned())
    }
}
