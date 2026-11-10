//! Labels values yielded by a PHPUnit data-provider method with the matching
//! test method's parameter names.

use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

use regex::Regex;
use swlsp_language::LanguageId;
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_ARRAY, PHP_YIELD_EXPRESSION};
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::inlay::{inlay_hint_byte_range, line_position, position};

const PHPUNIT_TEST_CASE_CLASS: &str = "PHPUnit\\Framework\\TestCase";

static DATA_PROVIDER_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?m)@dataProvider[\t\n\x0C\r ]+([A-Za-z_][A-Za-z0-9_]*)").expect("valid pattern")
});

pub struct PhpUnitProviderProvider {
    php: Option<PhpIndex>,
}

impl PhpUnitProviderProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

#[derive(Clone)]
struct Contract {
    test_method: String,
    parameters: Vec<String>,
    position: u32,
}

impl InlayHintProvider for PhpUnitProviderProvider {
    fn get_inlay_hints(
        &self,
        ctx: &Context,
        request: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        let Some(php) = self.php.as_ref() else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        if ctx.is_cancelled() || document.language != Some(LanguageId::Php) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let snapshot = match swlsp_lsp_phpanalysis::for_document(php, document) {
            Ok(Some(state)) => state.snapshot.clone(),
            _ => {
                let path =
                    swlsp_uriutil::path(&document.uri).unwrap_or_else(|_| document.uri.clone());
                let graph = php.analyze_document(
                    &path,
                    i64::from(document.version),
                    &root,
                    document.source(),
                );
                php.semantic_snapshot().with_document(&graph)
            }
        };
        let (range_start, range_end) = inlay_hint_byte_range(document, request.params.range);
        let mut result = Vec::new();
        for class in phpquery::classes(&root) {
            let class_name = phpunit_class_name(&root, &class);
            if class_name.is_empty()
                || !snapshot.is_subtype_of(&class_name, PHPUNIT_TEST_CASE_CLASS)
            {
                continue;
            }
            let providers = phpunit_provider_contracts(&class);
            if providers.is_empty() {
                continue;
            }
            for method in phpquery::methods(&class) {
                let Some(contract) = providers.get(&lowercase(&phpquery::method_name(&method)))
                else {
                    continue;
                };
                if contract.parameters.is_empty() {
                    continue;
                }
                for yield_node in phpquery::nodes(&method, &[PHP_YIELD_EXPRESSION]) {
                    if ctx.is_cancelled() {
                        return Ok(result);
                    }
                    if phpquery::function_like_at(&yield_node).as_ref() != Some(&method) {
                        continue;
                    }
                    let arrays = phpquery::nodes(&yield_node, &[PHP_ARRAY]);
                    let Some(array) = arrays.first() else {
                        continue;
                    };
                    if phpquery::function_like_at(array).as_ref() != Some(&method) {
                        continue;
                    }
                    for (index, item) in phpquery::array_items(array).into_iter().enumerate() {
                        let Some(parameter) = contract.parameters.get(index) else {
                            break;
                        };
                        let value = phpquery::array_item_value(&item).unwrap_or(item);
                        let offset = u32::from(value.range_trimmed_trivia().start());
                        if offset < range_start || offset > range_end {
                            continue;
                        }
                        let (test_line, _) =
                            document.line_index().position_utf16(contract.position);
                        let part = protocol::InlayHintLabelPart {
                            value: format!("{parameter}:"),
                            tooltip: format!("Parameter of {}()", contract.test_method),
                            location: Some(protocol::Location {
                                uri: document.uri.clone(),
                                range: protocol::Range {
                                    start: line_position(test_line),
                                    end: line_position(test_line),
                                },
                            }),
                            command: None,
                        };
                        result.push(protocol::InlayHint {
                            position: position(document, offset),
                            label: protocol::InlayHintLabel::Parts(vec![part]),
                            kind: Some(InlayHintKind::PARAMETER),
                            tooltip: format!(
                                "PHPUnit data provider for {}()",
                                contract.test_method
                            ),
                            padding_right: true,
                            ..Default::default()
                        });
                    }
                }
            }
        }
        Ok(result)
    }
}

fn phpunit_provider_contracts(class: &SyntaxNode) -> HashMap<String, Contract> {
    let mut result: HashMap<String, Contract> = HashMap::new();
    let mut conflicts = HashSet::new();
    for method in phpquery::methods(class) {
        let provider_names = phpunit_method_providers(&method);
        if provider_names.is_empty() {
            continue;
        }
        let names: Vec<String> = phpquery::parameters(&method)
            .map(|parameter| phpquery::parameter_name(&parameter))
            .filter(|name| !name.is_empty())
            .collect();
        if names.is_empty() {
            continue;
        }
        let contract = Contract {
            test_method: phpquery::method_name(&method),
            parameters: names,
            position: method.range_trimmed_trivia().start().into(),
        };
        for provider_name in provider_names {
            let key = lowercase(&provider_name);
            if let Some(existing) = result.get(&key)
                && existing.parameters != contract.parameters
            {
                result.remove(&key);
                conflicts.insert(key);
                continue;
            }
            if !conflicts.contains(&key) {
                result.insert(key, contract.clone());
            }
        }
    }
    result
}

fn phpunit_method_providers(method: &SyntaxNode) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for captures in DATA_PROVIDER_PATTERN.captures_iter(&leading_phpunit_doc_comment(method)) {
        if let Some(name) = captures.get(1) {
            seen.insert(lowercase(name.as_str()));
            result.push(name.as_str().to_string());
        }
    }
    for attribute in phpquery::attributes(method) {
        let name = phpquery::attribute_name(&attribute);
        let name = name.strip_prefix('\\').unwrap_or(&name);
        if !eq_ignore_case(name, "DataProvider") && !lowercase(name).ends_with("\\dataprovider") {
            continue;
        }
        let provider_name = phpquery::argument_expression(&attribute, 0)
            .map(|expression| phpquery::string_value(&expression))
            .unwrap_or_default();
        if provider_name.is_empty() {
            continue;
        }
        if !seen.insert(lowercase(&provider_name)) {
            continue;
        }
        result.push(provider_name);
    }
    result
}

fn leading_phpunit_doc_comment(node: &SyntaxNode) -> String {
    let range = node.text_range();
    let trimmed = node.range_trimmed_trivia();
    if trimmed.start() <= range.start() {
        return String::new();
    }
    let text = node.text().to_string();
    let prefix_length = usize::from(trimmed.start() - range.start()).min(text.len());
    let prefix = &text[..prefix_length];
    let Some(start) = prefix.rfind("/**") else {
        return String::new();
    };
    let Some(end) = prefix[start..].find("*/") else {
        return String::new();
    };
    let end = end + start + 2;
    if !prefix[end..].trim().is_empty() {
        return String::new();
    }
    prefix[start..end].to_string()
}

fn phpunit_class_name(root: &SyntaxNode, class: &SyntaxNode) -> String {
    let name = phpquery::class_name(class);
    let namespace = phpquery::namespace(root);
    let namespace = namespace.trim_matches('\\');
    if namespace.is_empty() {
        name
    } else {
        format!("{namespace}\\{name}")
    }
}
