//! Configured method names (`->call()`, `'calls' => [...]`) in PHP service
//! configuration.

use std::collections::HashSet;

use swlsp_parser_php::query as phpquery;
use swlsp_php::NameResolver;
use swlsp_syntax::SyntaxNode;

use crate::model::{Service, ServiceMethodReference};
use crate::php_array_services::{
    php_array_config_roots, php_array_entry_array, php_array_node, php_array_option_value,
    php_array_static_value, php_array_static_value_ok,
};
use crate::php_services::{
    PhpServiceEvaluator, direct_services_set_call, method_calls, php_argument_content_range,
    php_expression_content_range, static_php_value,
};

impl PhpServiceEvaluator<'_> {
    pub(crate) fn service_method_references(
        &self,
        service: &Service,
        calls: &[SyntaxNode],
    ) -> Vec<ServiceMethodReference> {
        let base = ServiceMethodReference {
            owner_service_id: service.id.clone(),
            owner_class: service.class.clone(),
            format: "php".to_owned(),
            ..ServiceMethodReference::default()
        };
        php_service_method_references_for_calls(&base, calls, self.resolver, self.path, |call| {
            self.call_targets_definition(call, &service.id)
        })
    }
}

fn php_service_method_references_for_calls(
    base: &ServiceMethodReference,
    calls: &[SyntaxNode],
    resolver: &NameResolver,
    path: &str,
    accept: impl Fn(&SyntaxNode) -> bool,
) -> Vec<ServiceMethodReference> {
    let mut result = Vec::new();
    for call in method_calls(calls, "call") {
        if !accept(&call) {
            continue;
        }
        let Some(method) =
            static_php_value(&phpquery::argument_value_text(&call, 0), resolver, path)
                .filter(|method| !method.is_empty())
        else {
            continue;
        };
        result.push(ServiceMethodReference {
            method_name: method,
            range: php_argument_content_range(&call, 0),
            ..base.clone()
        });
    }
    result
}

pub(crate) fn direct_php_service_method_references(
    path: &str,
    root: &SyntaxNode,
) -> Vec<ServiceMethodReference> {
    let resolver = NameResolver::new(root);
    let mut result = Vec::new();
    for statement in phpquery::expression_statements(root) {
        let calls = phpquery::calls(&statement, &[]);
        let Some(set_call) = direct_services_set_call(&calls) else {
            continue;
        };
        let Some(owner_id) = static_php_value(
            &phpquery::argument_value_text(&set_call, 0),
            &resolver,
            path,
        )
        .filter(|id| !id.is_empty()) else {
            continue;
        };
        let mut owner_class = owner_id.clone();
        let value = phpquery::argument_value_text(&set_call, 1);
        if !value.is_empty() {
            let Some(class_name) = static_php_value(&value, &resolver, path) else {
                continue;
            };
            owner_class = class_name;
        }
        if !owner_class.contains('\\') {
            continue;
        }
        let base = ServiceMethodReference {
            owner_service_id: owner_id,
            owner_class: owner_class
                .strip_prefix('\\')
                .unwrap_or(&owner_class)
                .to_owned(),
            format: "php".to_owned(),
            ..ServiceMethodReference::default()
        };
        result.extend(php_service_method_references_for_calls(
            &base,
            &calls,
            &resolver,
            path,
            |_| true,
        ));
    }
    result
}

pub(crate) fn php_array_service_method_references(
    path: &str,
    root: &SyntaxNode,
) -> Vec<ServiceMethodReference> {
    let resolver = NameResolver::new(root);
    let mut result = Vec::new();
    for config in php_array_config_roots(root) {
        let Some(services) = php_array_entry_array(Some(&config), "services", &resolver, path)
        else {
            continue;
        };
        for item in phpquery::array_items(&services) {
            let owner_id =
                php_array_static_value(phpquery::array_item_key(&item).as_ref(), &resolver, path);
            if owner_id.is_empty() || owner_id.starts_with('_') {
                continue;
            }
            let Some(definition) = php_array_node(phpquery::array_item_value(&item)) else {
                continue;
            };
            let mut owner_class = if owner_id.contains('\\') {
                owner_id.strip_prefix('\\').unwrap_or(&owner_id).to_owned()
            } else {
                String::new()
            };
            if let Some(class) = php_array_option_value(Some(&definition), "class", &resolver, path)
            {
                owner_class = match php_array_static_value_ok(Some(&class), &resolver, path) {
                    None => String::new(),
                    Some(value) => value.strip_prefix('\\').unwrap_or(&value).to_owned(),
                };
            }
            if owner_class.is_empty() {
                continue;
            }
            let calls = php_array_node(php_array_option_value(
                Some(&definition),
                "calls",
                &resolver,
                path,
            ));
            let Some(calls) = calls else { continue };
            for call in phpquery::array_items(&calls) {
                let method = match phpquery::array_item_key(&call) {
                    Some(key) => Some(key),
                    None => {
                        let tuple = php_array_node(phpquery::array_item_value(&call));
                        let parts = tuple
                            .as_ref()
                            .map(phpquery::array_items)
                            .unwrap_or_default();
                        let Some(first) = parts.first() else { continue };
                        phpquery::array_item_value(first)
                    }
                };
                let Some(method_name) = php_array_static_value_ok(method.as_ref(), &resolver, path)
                    .filter(|name| !name.is_empty())
                else {
                    continue;
                };
                result.push(ServiceMethodReference {
                    owner_service_id: owner_id.clone(),
                    owner_class: owner_class.clone(),
                    method_name,
                    range: php_expression_content_range(method.as_ref()),
                    format: "php".to_owned(),
                    ..ServiceMethodReference::default()
                });
            }
        }
    }
    result
}

pub(crate) fn unique_service_method_references(
    references: Vec<ServiceMethodReference>,
) -> Vec<ServiceMethodReference> {
    let mut seen = HashSet::new();
    references
        .into_iter()
        .filter(|reference| {
            seen.insert((
                reference.owner_service_id.clone(),
                reference.owner_class.clone(),
                reference.method_name.clone(),
                reference.range,
            ))
        })
        .collect()
}
