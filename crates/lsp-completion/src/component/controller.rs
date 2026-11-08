//! Statically resolvable `controller()` targets in Twig (Go
//! `controller_completion.go`).

use std::collections::{BTreeSet, HashSet};
use std::sync::{Arc, Mutex};

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Symbol, SymbolKind, Visibility};
use swlsp_php_types::equal_fold;
use swlsp_symfony::{RouteIndexer, ServiceIndex};

use super::util::kind;
use crate::common::{go_to_lower, protocol_range, text_edit};

#[derive(Clone)]
struct Candidate {
    label: String,
    detail: String,
}

#[derive(Default)]
struct ClassCache {
    revision: u64,
    classes: Option<Vec<Candidate>>,
}

/// Completes statically resolvable `controller()` targets.
pub struct ControllerCompletionProvider {
    php: Option<PhpIndex>,
    services: Option<Arc<ServiceIndex>>,
    routes: Option<Arc<RouteIndexer>>,
    cache: Mutex<ClassCache>,
}

impl ControllerCompletionProvider {
    pub fn new(
        php: Option<PhpIndex>,
        services: Option<Arc<ServiceIndex>>,
        routes: Option<Arc<RouteIndexer>>,
    ) -> Self {
        Self {
            php,
            services,
            routes,
            cache: Mutex::new(ClassCache::default()),
        }
    }

    fn class_candidates(&self, ctx: &Context, php: &PhpIndex) -> Vec<Candidate> {
        let revision = php.revision();
        let mut cache = self
            .cache
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        if let Some(classes) = cache.classes.as_ref()
            && cache.revision == revision
        {
            return classes.clone();
        }
        let mut result = Vec::new();
        for class in php.class_symbols_view().iter() {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if class.kind != SymbolKind::Class
                || !class.name.ends_with("Controller")
                || controller_test_class(class)
            {
                continue;
            }
            for method in php.methods(&class.fully_qualified) {
                if !controller_action_method(&method, true) {
                    continue;
                }
                let mut label = class.fully_qualified.trim_start_matches('\\').to_string();
                if &*method.name != "__invoke" {
                    label.push_str("::");
                    label.push_str(&method.name);
                }
                result.push(Candidate {
                    label,
                    detail: controller_method_detail(class, &method),
                });
                let legacy = legacy_controller_candidate(class, &method);
                if !legacy.is_empty() {
                    result.push(Candidate {
                        label: legacy,
                        detail: controller_method_detail(class, &method),
                    });
                }
            }
        }
        cache.classes = Some(result.clone());
        cache.revision = revision;
        result
    }

    fn service_candidates(&self, ctx: &Context, php: &PhpIndex) -> Vec<Candidate> {
        let (Some(routes), Some(services)) = (self.routes.as_ref(), self.services.as_ref()) else {
            return Vec::new();
        };
        let Ok(routes) = routes.get_routes_view() else {
            return Vec::new();
        };
        // Go iterates a map here; sorted order keeps Rust deterministic.
        let mut service_ids = BTreeSet::new();
        for route in routes.iter() {
            let Some(reference) = swlsp_symfony::parse_controller_reference(&route.controller)
            else {
                continue;
            };
            if reference.target.contains('\\') {
                continue;
            }
            service_ids.insert(reference.target);
        }
        let mut result = Vec::new();
        for service_id in service_ids {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            let Ok(Some(service)) = services.get_service_by_id(&service_id) else {
                continue;
            };
            if service.class.is_empty() {
                continue;
            }
            let Some(class) = php.find_class(&service.class) else {
                continue;
            };
            for method in php.methods(&class.fully_qualified) {
                if !controller_action_method(&method, false) {
                    continue;
                }
                if &*method.name == "__invoke" {
                    result.push(Candidate {
                        label: service_id.clone(),
                        detail: controller_method_detail(&class, &method),
                    });
                    continue;
                }
                for separator in [":", "::"] {
                    result.push(Candidate {
                        label: format!("{service_id}{separator}{}", &*method.name),
                        detail: controller_method_detail(&class, &method),
                    });
                }
            }
        }
        result
    }
}

impl CompletionProvider for ControllerCompletionProvider {
    fn get_completions(
        &self,
        ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let (Some(php), Some(node)) = (self.php.as_ref(), request.node.as_ref()) else {
            return Vec::new();
        };
        if !equal_fold(
            swlsp_uriutil::filepath::ext(&request.params.text_document.uri),
            ".twig",
        ) {
            return Vec::new();
        }
        let Some(value) = swlsp_symfony::twig_controller_value_at(node) else {
            return Vec::new();
        };
        let mut candidates: Vec<(String, Candidate)> = self
            .class_candidates(ctx, php)
            .into_iter()
            .chain(self.service_candidates(ctx, php))
            .map(|candidate| (go_to_lower(&candidate.label), candidate))
            .collect();
        swlsp_gosort::sort_slice(&mut candidates, |left, right| left.0 < right.0);

        let edit_range = request
            .line_index()
            .map(|line_index| protocol_range(value.range, line_index))
            .unwrap_or_default();
        let mut result = Vec::with_capacity(candidates.len());
        let mut seen = HashSet::with_capacity(candidates.len());
        for (key, candidate) in candidates {
            if ctx.is_cancelled() {
                return Vec::new();
            }
            if !seen.insert(key) {
                continue;
            }
            result.push(protocol::CompletionItem {
                text_edit: Some(text_edit(edit_range, candidate.label.replace('\\', "\\\\"))),
                filter_text: candidate.label.clone(),
                label: candidate.label,
                kind: kind(protocol::CompletionItemKind::METHOD),
                detail: candidate.detail,
                ..Default::default()
            });
        }
        result
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec!["\\".to_string(), ":".to_string()]
    }
}

fn legacy_controller_candidate(class: &Symbol, method: &Symbol) -> String {
    let Some(action) = method.name.strip_suffix("Action") else {
        return String::new();
    };
    let name = class.fully_qualified.trim_start_matches('\\');
    let marker = "\\Controller\\";
    let Some(offset) = name.find(marker) else {
        return String::new();
    };
    let prefix = &name[..offset];
    let bundle = prefix.rsplit('\\').next().unwrap_or(prefix);
    if !bundle.ends_with("Bundle") {
        return String::new();
    }
    let rest = &name[offset + marker.len()..];
    let controller = rest.strip_suffix("Controller").unwrap_or(rest);
    if controller.is_empty() {
        return String::new();
    }
    format!("{bundle}:{controller}:{action}")
}

fn controller_action_method(method: &Symbol, class_controller: bool) -> bool {
    if method.kind != SymbolKind::Method || method.visibility != Visibility::Public {
        return false;
    }
    if method.name.starts_with("__") && &*method.name != "__invoke" {
        return false;
    }
    class_controller || !go_to_lower(&method.name).starts_with("set")
}

fn controller_test_class(class: &Symbol) -> bool {
    let name = go_to_lower(&format!(
        "\\{}\\",
        class.fully_qualified.trim_start_matches('\\')
    ));
    name.contains("\\test\\") || name.contains("\\tests\\")
}

fn controller_method_detail(class: &Symbol, method: &Symbol) -> String {
    let parameters: Vec<&str> = method
        .parameters
        .iter()
        .map(|parameter| {
            if parameter.name.is_empty() {
                "$value"
            } else {
                &*parameter.name
            }
        })
        .collect();
    format!(
        "{}::{}({})",
        class.fully_qualified.trim_start_matches('\\'),
        &*method.name,
        parameters.join(", ")
    )
}
