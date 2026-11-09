//! Related-file document links.
//!
//! Exposes the reference plugin's file-navigation targets as native editor
//! links. Ambiguous references are intentionally left to go-to-definition and
//! multi-target code lenses.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{
    Context, DocumentLinkProvider, DocumentLinkRequest, ProviderResult, protocol_range,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::PhpIndex;
use swlsp_symfony::{RouteResourceResolver, route_resource_references};
use swlsp_symfonyconfig as symfonyconfig;
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_twig::{TemplateReference, TwigIndexer};
use swlsp_uriutil::{file_uri, filepath};

#[cfg(test)]
mod tests;

pub struct RelatedProvider {
    twig: Option<Arc<TwigIndexer>>,
    configuration: Option<Arc<symfonyconfig::Index>>,
    routes: RouteResourceResolver,
}

impl RelatedProvider {
    pub fn new(
        twig: Option<Arc<TwigIndexer>>,
        configuration: Option<Arc<symfonyconfig::Index>>,
        php: Option<PhpIndex>,
    ) -> Self {
        Self {
            twig,
            configuration,
            routes: RouteResourceResolver::new(php, None),
        }
    }
}

struct Links<'a> {
    path: &'a str,
    lines: &'a swlsp_syntax::LineIndex,
    seen: HashSet<(TextRange, String)>,
    result: Vec<protocol::DocumentLink>,
}

impl Links<'_> {
    fn add(&mut self, range: TextRange, targets: &[String], tooltip: impl FnOnce() -> String) {
        let Some(target) = unambiguous_document_link_target(self.path, targets) else {
            return;
        };
        if !self.seen.insert((range, target.clone())) {
            return;
        }
        self.result.push(protocol::DocumentLink {
            range: protocol_range(self.lines, range),
            target: file_uri(&target),
            tooltip: tooltip(),
            ..Default::default()
        });
    }
}

impl DocumentLinkProvider for RelatedProvider {
    fn get_document_links(
        &self,
        ctx: &Context,
        request: &DocumentLinkRequest,
    ) -> ProviderResult<Vec<protocol::DocumentLink>> {
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let mut links = Links {
            path: &path,
            lines: document.line_index(),
            seen: HashSet::new(),
            result: Vec::new(),
        };

        if let Some(twig) = &self.twig {
            for reference in document_template_references(&path, document.language, &root) {
                if ctx.is_cancelled() {
                    return Ok(finish(links.result));
                }
                let files = twig.get_twig_files_by_rel_path(&reference.template)?;
                let targets: Vec<String> = files.into_iter().map(|file| file.path).collect();
                links.add(reference.range, &targets, || {
                    format!(
                        "Open {} template {:?}",
                        reference.kind.as_str(),
                        reference.template
                    )
                });
            }
        }

        for reference in route_resource_references(&root) {
            if ctx.is_cancelled() {
                return Ok(finish(links.result));
            }
            let targets = self.routes.files(&path, &reference);
            links.add(reference.range, &targets, || {
                "Open routing resource".to_string()
            });
        }

        let language = document.language;
        if matches!(language, Some(LanguageId::Php | LanguageId::Yaml)) {
            for node in root.descendants() {
                if ctx.is_cancelled() {
                    return Ok(finish(links.result));
                }
                let wanted = if language == Some(LanguageId::Php) {
                    PHP_STRING
                } else {
                    YAML_SCALAR
                };
                if node.kind() != wanted {
                    continue;
                }
                if let Some(reference) = symfonyconfig::resource_reference_at(&node) {
                    let targets = symfonyconfig::resource_files(&path, &reference.path);
                    links.add(reference.range, &targets, || {
                        "Open configuration resource".to_string()
                    });
                }
                let Some(configuration) = &self.configuration else {
                    continue;
                };
                let Some(reference) = symfonyconfig::root_reference_at(&node) else {
                    continue;
                };
                let roots = configuration.roots(&reference.name)?;
                let targets: Vec<String> = roots.into_iter().map(|root| root.file).collect();
                links.add(reference.range, &targets, || {
                    format!("Open {:?} configuration declaration", reference.name)
                });
            }
        }

        Ok(finish(links.result))
    }
}

fn finish(mut result: Vec<protocol::DocumentLink>) -> Vec<protocol::DocumentLink> {
    result.sort_by(|left, right| {
        left.range
            .start
            .line
            .cmp(&right.range.start.line)
            .then(left.range.start.character.cmp(&right.range.start.character))
            .then_with(|| left.target.cmp(&right.target))
    });
    result
}

fn document_template_references(
    path: &str,
    language: Option<LanguageId>,
    root: &SyntaxNode,
) -> Vec<TemplateReference> {
    match language {
        Some(LanguageId::Twig) => swlsp_twig::twig_template_references(path, root),
        Some(LanguageId::Php) => swlsp_twig::php_template_references(path, root),
        _ => Vec::new(),
    }
}

fn unambiguous_document_link_target(current_path: &str, candidates: &[String]) -> Option<String> {
    let current_path = filepath::clean(current_path);
    let mut unique: HashMap<String, &String> = HashMap::new();
    for candidate in candidates {
        if candidate.is_empty() {
            continue;
        }
        let clean = filepath::clean(candidate);
        if clean == current_path {
            continue;
        }
        unique.insert(clean, candidate);
    }
    if unique.len() != 1 {
        return None;
    }
    unique.into_values().next().cloned()
}
