//! Symfony's PHP service-configurator format.
//!
//! The parser evaluates only statically knowable configuration. This makes it
//! safe for incomplete editor input and avoids inventing service IDs for
//! variables, interpolated strings, or arbitrary PHP return values.

use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

use regex::Regex;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as phpsyntax;
use swlsp_php::NameResolver;
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::model::{
    Parameter, PhpServiceConfig, Service, ServiceArgumentReference, ServiceMethodReference,
    ServicePrototype, TagMap,
};
use crate::php_array_services::{merge_php_service_configs, parse_php_array_service_config};
use crate::php_service_method_reference::{
    direct_php_service_method_references, php_array_service_method_references,
    unique_service_method_references,
};
use crate::reference::parse_service_reference;
use crate::util::numeric_index;
use crate::util::{clean_path, configured_service_bool, dir_path, join_path, line_at};

const CONTAINER_CONFIGURATOR_CLASS: &str =
    "Symfony\\Component\\DependencyInjection\\Loader\\Configurator\\ContainerConfigurator";

static CONFIGURATOR_CALLBACK_PATTERN: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?i)(?-u:\b)function\s*\(([^)]*)\)").expect("valid regex"));
static TYPED_CALLBACK_PARAMETER_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r"(?i)(\\?[A-Za-z_\x{80}-\x{ff}][A-Za-z0-9_\x{80}-\x{ff}\\]*)\s+(\$[A-Za-z_\x{80}-\x{ff}][A-Za-z0-9_\x{80}-\x{ff}]*)",
    )
    .expect("valid regex")
});

/// Parses Symfony's PHP service-configurator format.
pub fn parse_php_services(path: &str, source: &str) -> (Vec<Service>, Vec<Parameter>) {
    let parse = swlsp_parser_php::parse(source);
    parse_php_services_tree(path, &parse.root(), &LineIndex::new(source))
}

/// Reuses the shared PHP CST and line index owned by the workspace scanner.
pub fn parse_php_services_tree(
    path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> (Vec<Service>, Vec<Parameter>) {
    let config = parse_php_service_config_tree(path, root, line_index);
    (config.services, config.parameters)
}

/// Statically knowable service references supplied through Symfony's PHP
/// configurator API or legacy array definitions.
pub fn php_service_argument_references(
    path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<ServiceArgumentReference> {
    let source = root.text().to_string();
    let has_fluent = source.contains("->arg");
    let has_legacy = source.contains("'arguments'") || source.contains("\"arguments\"");
    if !has_fluent && !has_legacy {
        return Vec::new();
    }
    let mut references = Vec::new();
    if has_fluent {
        let config = parse_php_service_config_tree_with_references(path, root, line_index, true);
        references.extend(config.references);
        references.extend(direct_php_service_argument_references(path, root));
    }
    if has_legacy {
        references.extend(legacy_php_service_argument_references(path, root));
    }
    unique_service_argument_references(references)
}

/// Statically knowable method names from Symfony's PHP configurator API and
/// native array service definitions.
pub fn php_service_method_references(
    path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> Vec<ServiceMethodReference> {
    let source = root.text().to_string();
    let has_fluent = source.contains("->call");
    let has_array = source.contains("'calls'") || source.contains("\"calls\"");
    if !has_fluent && !has_array {
        return Vec::new();
    }
    let mut references = Vec::new();
    if has_fluent {
        let config = parse_php_service_config_tree_with_references(path, root, line_index, true);
        references.extend(config.method_references);
        references.extend(direct_php_service_method_references(path, root));
    }
    if has_array {
        references.extend(php_array_service_method_references(path, root));
    }
    unique_service_method_references(references)
}

/// Services from a PHP configuration file: fluent configurator callbacks
/// merged with native array configuration.
pub(crate) fn parse_php_service_config_tree(
    path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
) -> PhpServiceConfig {
    let config = parse_php_service_config_tree_with_references(path, root, line_index, false);
    merge_php_service_configs(
        config,
        parse_php_array_service_config(path, root, line_index),
    )
}

fn parse_php_service_config_tree_with_references(
    path: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
    collect_references: bool,
) -> PhpServiceConfig {
    let resolver = NameResolver::new(root);
    let callbacks = configurator_callbacks(root, &resolver);
    if callbacks.is_empty() {
        return PhpServiceConfig::default();
    }
    let mut evaluator = PhpServiceEvaluator {
        path,
        line_index,
        resolver: &resolver,
        container_variable: String::new(),
        services: HashMap::new(),
        parameters: HashMap::new(),
        service_variables: Vec::new(),
        parameter_variables: Vec::new(),
        definition_vars: Vec::new(),
        default_tags: TagMap::new(),
        instanceof_tags: TagMap::new(),
        default_autowire: false,
        default_autowire_set: false,
        prototypes: Vec::new(),
        collect_references,
        references: Vec::new(),
        method_references: Vec::new(),
    };
    for (container_variable, body) in callbacks {
        evaluator.container_variable = container_variable;
        evaluator.service_variables.clear();
        evaluator.parameter_variables.clear();
        evaluator.definition_vars.clear();
        evaluator.default_tags.clear();
        evaluator.instanceof_tags.clear();
        evaluator.default_autowire = false;
        evaluator.default_autowire_set = false;
        evaluator.evaluate(&body);
    }
    let mut services: Vec<Service> = evaluator.services.into_values().collect();
    sort_services(&mut services);
    let mut parameters: Vec<Parameter> = evaluator.parameters.into_values().collect();
    sort_parameters(&mut parameters);
    PhpServiceConfig {
        services,
        parameters,
        prototypes: evaluator.prototypes,
        references: evaluator.references,
        method_references: evaluator.method_references,
    }
}

pub(crate) fn sort_services(services: &mut [Service]) {
    services.sort_by(|left, right| {
        left.line
            .cmp(&right.line)
            .then_with(|| left.id.cmp(&right.id))
    });
}

pub(crate) fn sort_parameters(parameters: &mut [Parameter]) {
    parameters.sort_by(|left, right| {
        left.line
            .cmp(&right.line)
            .then_with(|| left.name.cmp(&right.name))
    });
}

fn configurator_callbacks(root: &SyntaxNode, resolver: &NameResolver) -> Vec<(String, SyntaxNode)> {
    let source = root.text().to_string();
    let blocks = phpquery::nodes(root, &[phpsyntax::PHP_BLOCK]);
    let mut callbacks = Vec::new();
    let mut claimed: HashSet<TextRange> = HashSet::new();
    for captures in CONFIGURATOR_CALLBACK_PATTERN.captures_iter(&source) {
        let (Some(whole), Some(parameters)) = (captures.get(0), captures.get(1)) else {
            continue;
        };
        let mut container_variable = String::new();
        for parameter in TYPED_CALLBACK_PARAMETER_PATTERN.captures_iter(parameters.as_str()) {
            if resolver.resolve(&parameter[1]) == CONTAINER_CONFIGURATOR_CLASS {
                container_variable = parameter[2].to_owned();
                break;
            }
        }
        if container_variable.is_empty() {
            continue;
        }
        let end = whole.end();
        let body = blocks
            .iter()
            .filter(|block| usize::from(block.text_range().start()) >= end)
            .min_by_key(|block| block.text_range().start());
        let Some(body) = body else { continue };
        if !claimed.insert(body.text_range()) {
            continue;
        }
        callbacks.push((container_variable, body.clone()));
    }
    callbacks
}

pub(crate) struct PhpServiceEvaluator<'a> {
    pub(crate) path: &'a str,
    pub(crate) line_index: &'a LineIndex,
    pub(crate) resolver: &'a NameResolver,
    pub(crate) container_variable: String,
    pub(crate) services: HashMap<String, Service>,
    pub(crate) parameters: HashMap<String, Parameter>,
    /// Kept in insertion order so lookups are deterministic.
    pub(crate) service_variables: Vec<String>,
    pub(crate) parameter_variables: Vec<String>,
    pub(crate) definition_vars: Vec<(String, String)>,
    pub(crate) default_tags: TagMap,
    pub(crate) instanceof_tags: TagMap,
    pub(crate) default_autowire: bool,
    pub(crate) default_autowire_set: bool,
    pub(crate) prototypes: Vec<ServicePrototype>,
    pub(crate) collect_references: bool,
    pub(crate) references: Vec<ServiceArgumentReference>,
    pub(crate) method_references: Vec<ServiceMethodReference>,
}

impl PhpServiceEvaluator<'_> {
    fn evaluate(&mut self, body: &SyntaxNode) {
        for statement in phpquery::expression_statements(body) {
            self.evaluate_statement(&statement);
        }
    }

    pub(crate) fn service_argument_references(
        &self,
        service: &Service,
        calls: &[SyntaxNode],
    ) -> Vec<ServiceArgumentReference> {
        let base = ServiceArgumentReference {
            owner_service_id: service.id.clone(),
            owner_class: service.class.clone(),
            parameter_index: -1,
            format: "php".to_owned(),
            ..ServiceArgumentReference::default()
        };
        php_service_argument_references_for_calls(base, calls, self.resolver, self.path, |call| {
            self.call_targets_definition(call, &service.id)
        })
    }

    pub(crate) fn invoked_services_call(&self, calls: &[SyntaxNode]) -> Option<SyntaxNode> {
        let services_call = format!("{}->services()", self.container_variable);
        calls
            .iter()
            .find(|call| {
                let name = phpquery::call_name(call);
                self.service_variables.contains(&name) || name == services_call
            })
            .cloned()
    }

    pub(crate) fn call_has_configurator(&self, name: &str, configurator: &str) -> bool {
        if self
            .service_variables
            .iter()
            .any(|variable| name.starts_with(&format!("{variable}->{configurator}(")))
        {
            return true;
        }
        name.starts_with(&format!(
            "{}->services()->{configurator}(",
            self.container_variable
        ))
    }

    pub(crate) fn is_accessor_assignment(&self, calls: &[SyntaxNode], accessor: &str) -> bool {
        let expected = format!("{}->{accessor}", self.container_variable);
        let mut found = false;
        for call in calls {
            if phpquery::call_method_name(call) != accessor {
                return false;
            }
            if phpquery::call_name(call) == expected {
                found = true;
            }
        }
        found
    }

    pub(crate) fn rooted_call(
        &self,
        calls: &[SyntaxNode],
        variables: &[String],
        accessor: &str,
        method: &str,
    ) -> Option<SyntaxNode> {
        let rooted = format!("{}->{accessor}()->", self.container_variable);
        calls
            .iter()
            .find(|call| {
                if phpquery::call_method_name(call) != method {
                    return false;
                }
                let name = phpquery::call_name(call);
                variables
                    .iter()
                    .any(|variable| name.starts_with(&format!("{variable}->")))
                    || name.starts_with(&rooted)
            })
            .cloned()
    }

    pub(crate) fn definition_for_calls(&self, calls: &[SyntaxNode]) -> String {
        let names: Vec<String> = calls.iter().map(phpquery::call_name).collect();
        for (variable, id) in &self.definition_vars {
            let prefix = format!("{variable}->");
            if names.iter().any(|name| name.starts_with(&prefix)) {
                return id.clone();
            }
        }
        String::new()
    }

    pub(crate) fn call_targets_definition(&self, call: &SyntaxNode, definition_id: &str) -> bool {
        let name = phpquery::call_name(call);
        if self.service_variables.iter().any(|variable| {
            name.starts_with(&format!("{variable}->")) || name.starts_with(&format!("{variable}("))
        }) {
            return true;
        }
        if name.starts_with(&format!("{}->services()->", self.container_variable)) {
            return true;
        }
        self.definition_vars
            .iter()
            .any(|(variable, id)| id == definition_id && name.starts_with(&format!("{variable}->")))
    }

    pub(crate) fn set_service(&mut self, call: &SyntaxNode) -> String {
        let Some(id) = self.argument_value(call, 0) else {
            return String::new();
        };
        if id.is_empty() || id == "null" {
            return String::new();
        }
        let mut class_name = id.clone();
        let mut class_range = php_argument_content_range(call, 0);
        if phpquery::arguments(call).count() > 1 {
            class_name = String::new();
            class_range = TextRange::default();
            if let Some(value) = self.argument_value(call, 1) {
                if value == "null" {
                    class_name = id.clone();
                    class_range = php_argument_content_range(call, 0);
                } else if !value.is_empty() {
                    class_name = value;
                    class_range = php_argument_content_range(call, 1);
                }
            }
        }
        let service = Service {
            id: id.clone(),
            class: class_name,
            autowire: self.default_autowire,
            autowire_set: self.default_autowire_set,
            tags: self.default_tags.clone(),
            instanceof_tags: self.instanceof_tags.clone(),
            path: self.path.to_owned(),
            line: self.line(call),
            range: call.range_trimmed_trivia(),
            id_range: php_argument_content_range(call, 0),
            class_range,
            ..Service::default()
        };
        self.services.insert(id.clone(), service);
        id
    }

    pub(crate) fn set_prototype(&mut self, load: &SyntaxNode, calls: &[SyntaxNode]) {
        let (Some(namespace), Some(resource)) =
            (self.argument_value(load, 0), self.argument_value(load, 1))
        else {
            return;
        };
        if namespace.is_empty() || resource.is_empty() {
            return;
        }
        let mut prototype = ServicePrototype {
            namespace: namespace
                .strip_prefix('\\')
                .unwrap_or(&namespace)
                .to_owned(),
            resource: config_path(self.path, &resource),
            autowire: self.default_autowire,
            autowire_set: self.default_autowire_set,
            tags: self.default_tags.clone(),
            instanceof_tags: self.instanceof_tags.clone(),
            path: self.path.to_owned(),
            line: self.line(load),
            range: load.range_trimmed_trivia(),
            namespace_range: php_argument_content_range(load, 0),
            resource_range: php_argument_content_range(load, 1),
            ..ServicePrototype::default()
        };
        for call in method_calls(calls, "autowire") {
            if self.call_targets_service_configurator(&call, "load") {
                (prototype.autowire, prototype.autowire_set) = self.php_autowire_call(&call);
            }
        }
        for exclude in method_calls(calls, "exclude") {
            if !self.call_targets_service_configurator(&exclude, "load") {
                continue;
            }
            for value in self.argument_values(&exclude, 0) {
                prototype.excludes.push(config_path(self.path, &value));
            }
        }
        for call in method_calls(calls, "tag") {
            if !self.call_targets_service_configurator(&call, "load") {
                continue;
            }
            if let Some(tag) = self.argument_value(&call, 0) {
                prototype.tags.insert(tag, String::new());
            }
        }
        self.prototypes.push(prototype);
    }

    pub(crate) fn php_autowire_call(&self, call: &SyntaxNode) -> (bool, bool) {
        if phpquery::arguments(call).next().is_none() {
            return (true, true);
        }
        match self.argument_value(call, 0) {
            None => (false, true),
            Some(value) => (configured_service_bool(&value).0, true),
        }
    }

    fn call_targets_service_configurator(&self, call: &SyntaxNode, method: &str) -> bool {
        self.call_has_configurator(&phpquery::call_name(call), method)
    }

    fn argument_values(&self, call: &SyntaxNode, index: usize) -> Vec<String> {
        let Some(argument) = phpquery::argument(call, index) else {
            return Vec::new();
        };
        let arrays = phpquery::arrays(&argument);
        let Some(array) = arrays.first() else {
            return self.argument_value(call, index).into_iter().collect();
        };
        phpquery::array_items(array)
            .iter()
            .filter_map(|item| static_php_value(&item.text().to_string(), self.resolver, self.path))
            .collect()
    }

    pub(crate) fn set_alias(&mut self, call: &SyntaxNode) -> String {
        let (Some(id), Some(target)) = (self.argument_value(call, 0), self.argument_value(call, 1))
        else {
            return String::new();
        };
        if id.is_empty() || target.is_empty() {
            return String::new();
        }
        let service = Service {
            id: id.clone(),
            alias_target: target,
            path: self.path.to_owned(),
            line: self.line(call),
            range: call.range_trimmed_trivia(),
            id_range: php_argument_content_range(call, 0),
            ..Service::default()
        };
        self.services.insert(id.clone(), service);
        id
    }

    pub(crate) fn set_stack(&mut self, call: &SyntaxNode) -> String {
        let Some(id) = self.argument_value(call, 0).filter(|id| !id.is_empty()) else {
            return String::new();
        };
        let service = Service {
            id: id.clone(),
            path: self.path.to_owned(),
            line: self.line(call),
            range: call.range_trimmed_trivia(),
            id_range: php_argument_content_range(call, 0),
            ..Service::default()
        };
        self.services.insert(id.clone(), service);
        id
    }

    pub(crate) fn set_parameter(&mut self, call: &SyntaxNode) {
        let Some(name) = self.argument_value(call, 0).filter(|name| !name.is_empty()) else {
            return;
        };
        let mut value = String::new();
        let expression = phpquery::argument_value_text(call, 1);
        if !expression.is_empty() {
            if let Some(evaluated) = static_php_value(&expression, self.resolver, self.path) {
                value = evaluated;
            } else if is_static_parameter_expression(&expression) {
                value = expression.trim().to_owned();
            }
        }
        let parameter = Parameter {
            name: name.clone(),
            value,
            path: self.path.to_owned(),
            line: self.line(call),
        };
        self.parameters.insert(name, parameter);
    }

    pub(crate) fn argument_value(&self, call: &SyntaxNode, index: usize) -> Option<String> {
        let expression = phpquery::argument_value_text(call, index);
        if expression.is_empty() {
            return None;
        }
        static_php_value(&expression, self.resolver, self.path)
    }

    pub(crate) fn line(&self, node: &SyntaxNode) -> u32 {
        line_at(self.line_index, node.range_trimmed_trivia().start())
    }
}

pub(crate) fn php_service_argument_references_for_calls(
    base: ServiceArgumentReference,
    calls: &[SyntaxNode],
    resolver: &NameResolver,
    path: &str,
    accept: impl Fn(&SyntaxNode) -> bool,
) -> Vec<ServiceArgumentReference> {
    let mut result = Vec::new();
    for args_call in method_calls(calls, "args") {
        if !accept(&args_call) {
            continue;
        }
        let Some(expression) = phpquery::argument_expression(&args_call, 0)
            .filter(|expression| expression.kind() == phpsyntax::PHP_ARRAY)
        else {
            continue;
        };
        for (index, item) in phpquery::array_items(&expression).iter().enumerate() {
            let Some(mut reference) = php_service_reference_expression(
                phpquery::array_item_value(item).as_ref(),
                resolver,
                path,
                &base,
            ) else {
                continue;
            };
            reference.parameter_index = index as i32;
            if let Some(key) = phpquery::array_item_key(item)
                && let Some(value) = static_php_value(&key.text().to_string(), resolver, path)
            {
                if value.starts_with('$') {
                    reference.parameter_name = value;
                    reference.parameter_index = -1;
                } else if numeric_index(&value) >= 0 {
                    reference.parameter_index = numeric_index(&value);
                }
            }
            result.push(reference);
        }
    }
    for arg_call in method_calls(calls, "arg") {
        if !accept(&arg_call) {
            continue;
        }
        let Some(mut reference) = php_service_reference_expression(
            phpquery::argument_expression(&arg_call, 1).as_ref(),
            resolver,
            path,
            &base,
        ) else {
            continue;
        };
        if let Some(key) =
            static_php_value(&phpquery::argument_value_text(&arg_call, 0), resolver, path)
        {
            if key.starts_with('$') {
                reference.parameter_name = key;
            } else if numeric_index(&key) >= 0 {
                reference.parameter_index = numeric_index(&key);
            }
        }
        result.push(reference);
    }
    result
}

fn php_service_reference_expression(
    expression: Option<&SyntaxNode>,
    resolver: &NameResolver,
    path: &str,
    base: &ServiceArgumentReference,
) -> Option<ServiceArgumentReference> {
    let expression = expression?;
    let mut base = base.clone();
    for call in phpquery::calls(expression, &[]) {
        let name = phpquery::call_method_name(&call).to_lowercase();
        if name != "service" && name != "ref" {
            continue;
        }
        let mut service_id =
            static_php_value(&phpquery::argument_value_text(&call, 0), resolver, path)?;
        if let Some((normalized, _)) = parse_service_reference(&service_id) {
            service_id = normalized;
        }
        if service_id.is_empty() || service_id.contains(['%', '$', '{', '}']) {
            return None;
        }
        base.service_id = service_id;
        let argument = phpquery::argument_expression(&call, 0)?;
        base.range = argument.range_trimmed_trivia();
        base.replacement = "php_helper_expression".to_owned();
        if argument.kind() == phpsyntax::PHP_STRING {
            base.range = php_argument_content_range(&call, 0);
            base.replacement = "php_helper_string".to_owned();
        }
        return Some(base);
    }
    let value = static_php_value(&expression.text().to_string(), resolver, path)?;
    if let Some((service_id, _)) = parse_service_reference(&value) {
        base.service_id = service_id;
        base.range = php_expression_content_range(Some(expression));
        base.replacement = "php_raw_string".to_owned();
        return Some(base);
    }
    if phpquery::class_constant_name(expression).is_empty() {
        return None;
    }
    base.service_id = value;
    base.range = expression.range_trimmed_trivia();
    base.replacement = "php_raw_expression".to_owned();
    Some(base)
}

/// The trimmed expression range without one pair of matching quotes.
pub(crate) fn php_expression_content_range(expression: Option<&SyntaxNode>) -> TextRange {
    let Some(expression) = expression else {
        return TextRange::default();
    };
    unquoted_range(
        expression.range_trimmed_trivia(),
        &expression.text().to_string(),
    )
}

fn unquoted_range(range: TextRange, text: &str) -> TextRange {
    let text = text.trim().as_bytes();
    if text.len() >= 2
        && (text[0] == b'\'' || text[0] == b'"')
        && text[text.len() - 1] == text[0]
        && range.end() > range.start() + TextSize::from(1)
    {
        return TextRange::new(
            range.start() + TextSize::from(1),
            range.end() - TextSize::from(1),
        );
    }
    range
}

fn legacy_php_service_argument_references(
    path: &str,
    root: &SyntaxNode,
) -> Vec<ServiceArgumentReference> {
    let resolver = NameResolver::new(root);
    let mut result = Vec::new();
    for array in phpquery::arrays(root) {
        for definition in phpquery::array_items(&array) {
            let Some((owner_id, owner_class, config)) =
                legacy_php_service_definition(&definition, &resolver, path)
            else {
                continue;
            };
            let base = ServiceArgumentReference {
                owner_service_id: owner_id,
                owner_class,
                parameter_index: -1,
                format: "php".to_owned(),
                ..ServiceArgumentReference::default()
            };
            for arguments in legacy_php_service_argument_arrays(&config, &resolver, path) {
                result.extend(legacy_php_service_references(
                    &arguments, &resolver, path, &base,
                ));
            }
        }
    }
    result
}

fn legacy_php_service_definition(
    definition: &SyntaxNode,
    resolver: &NameResolver,
    path: &str,
) -> Option<(String, String, SyntaxNode)> {
    let key = phpquery::array_item_key(definition)?;
    let config = phpquery::array_item_value(definition)
        .filter(|config| config.kind() == phpsyntax::PHP_ARRAY)?;
    let owner_id =
        static_php_value(&key.text().to_string(), resolver, path).filter(|id| !id.is_empty())?;
    let mut owner_class = if owner_id.contains('\\') {
        owner_id.clone()
    } else {
        String::new()
    };
    for option in phpquery::array_items(&config) {
        if let Some(class_name) = legacy_php_service_class(&option, resolver, path) {
            owner_class = class_name;
        }
    }
    if owner_class.is_empty() {
        return None;
    }
    Some((owner_id, owner_class, config))
}

fn legacy_php_service_class(
    option: &SyntaxNode,
    resolver: &NameResolver,
    path: &str,
) -> Option<String> {
    let key = phpquery::array_item_key(option)?;
    let value = phpquery::array_item_value(option)?;
    let name = static_php_value(&key.text().to_string(), resolver, path)?;
    if name != "class" {
        return None;
    }
    static_php_value(&value.text().to_string(), resolver, path)
}

fn legacy_php_service_argument_arrays(
    config: &SyntaxNode,
    resolver: &NameResolver,
    path: &str,
) -> Vec<SyntaxNode> {
    let mut result = Vec::new();
    for option in phpquery::array_items(config) {
        let (Some(key), Some(arguments)) = (
            phpquery::array_item_key(&option),
            phpquery::array_item_value(&option),
        ) else {
            continue;
        };
        if arguments.kind() != phpsyntax::PHP_ARRAY {
            continue;
        }
        if static_php_value(&key.text().to_string(), resolver, path).as_deref() == Some("arguments")
        {
            result.push(arguments);
        }
    }
    result
}

fn legacy_php_service_references(
    arguments: &SyntaxNode,
    resolver: &NameResolver,
    path: &str,
    base: &ServiceArgumentReference,
) -> Vec<ServiceArgumentReference> {
    let mut result = Vec::new();
    for (index, item) in phpquery::array_items(arguments).iter().enumerate() {
        let Some(mut reference) = php_service_reference_expression(
            phpquery::array_item_value(item).as_ref(),
            resolver,
            path,
            base,
        ) else {
            continue;
        };
        reference.parameter_index = index as i32;
        if !set_legacy_php_reference_parameter(&mut reference, item, resolver, path) {
            continue;
        }
        result.push(reference);
    }
    result
}

fn set_legacy_php_reference_parameter(
    reference: &mut ServiceArgumentReference,
    item: &SyntaxNode,
    resolver: &NameResolver,
    path: &str,
) -> bool {
    let Some(key) = phpquery::array_item_key(item) else {
        return true;
    };
    let Some(value) = static_php_value(&key.text().to_string(), resolver, path) else {
        return false;
    };
    if value.starts_with('$') {
        reference.parameter_name = value;
        reference.parameter_index = -1;
        return true;
    }
    let index = numeric_index(&value);
    if index < 0 {
        return false;
    }
    reference.parameter_index = index;
    true
}

/// The `->services()->set(...)` call of a statement, if any.
pub(crate) fn direct_services_set_call(calls: &[SyntaxNode]) -> Option<SyntaxNode> {
    method_calls(calls, "set").into_iter().find(|candidate| {
        phpquery::call_name(candidate)
            .to_lowercase()
            .contains("->services()->set")
    })
}

fn direct_php_service_argument_references(
    path: &str,
    root: &SyntaxNode,
) -> Vec<ServiceArgumentReference> {
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
        if !value.is_empty()
            && let Some(class_name) = static_php_value(&value, &resolver, path)
        {
            owner_class = class_name;
        }
        if !owner_class.contains('\\') {
            continue;
        }
        let base = ServiceArgumentReference {
            owner_service_id: owner_id,
            owner_class,
            parameter_index: -1,
            format: "php".to_owned(),
            ..ServiceArgumentReference::default()
        };
        result.extend(php_service_argument_references_for_calls(
            base,
            &calls,
            &resolver,
            path,
            |_| true,
        ));
    }
    result
}

fn unique_service_argument_references(
    references: Vec<ServiceArgumentReference>,
) -> Vec<ServiceArgumentReference> {
    let mut seen = HashSet::new();
    references
        .into_iter()
        .filter(|reference| {
            seen.insert((
                reference.owner_service_id.clone(),
                reference.service_id.clone(),
                reference.method_name.clone(),
                reference.parameter_name.clone(),
                reference.parameter_index,
                reference.range,
            ))
        })
        .collect()
}

/// Resolves a configuration-relative path.
pub(crate) fn config_path(config_path: &str, value: &str) -> String {
    if value.starts_with('/') {
        return clean_path(value);
    }
    join_path(&[&dir_path(config_path), value])
}

/// The value range of argument `index` without one pair of quotes.
pub(crate) fn php_argument_content_range(call: &SyntaxNode, index: usize) -> TextRange {
    php_expression_content_range(phpquery::argument_expression(call, index).as_ref())
}

pub(crate) fn method_calls(calls: &[SyntaxNode], method: &str) -> Vec<SyntaxNode> {
    calls
        .iter()
        .filter(|call| phpquery::call_method_name(call) == method)
        .cloned()
        .collect()
}

/// Evaluates statically knowable PHP expressions: string literals,
/// concatenations, `Foo::class`, `__DIR__`, `__FILE__`, booleans, `null` and
/// numbers.
pub(crate) fn static_php_value(
    expression: &str,
    resolver: &NameResolver,
    file_path: &str,
) -> Option<String> {
    let mut expression = expression.trim();
    while wrapped_by_parentheses(expression) {
        expression = expression[1..expression.len() - 1].trim();
    }
    if expression.is_empty() {
        return None;
    }
    let parts = split_php_concatenation(expression);
    if parts.len() > 1 {
        let mut value = String::new();
        for part in parts {
            value.push_str(&static_php_value(part, resolver, file_path)?);
        }
        return Some(value);
    }
    if let Some(value) = php_string_literal(expression) {
        return Some(value);
    }
    let lower = swlsp_textutil::lowercase(expression);
    let suffix = expression.len().wrapping_sub("::class".len());
    if expression.len() >= "::class".len()
        && expression.is_char_boundary(suffix)
        && expression[suffix..].eq_ignore_ascii_case("::class")
    {
        let name = expression[..expression.len() - "::class".len()].trim();
        if !is_php_name(name)
            || name.eq_ignore_ascii_case("self")
            || name.eq_ignore_ascii_case("static")
            || name.eq_ignore_ascii_case("parent")
        {
            return None;
        }
        return Some(resolver.resolve(name));
    }
    match lower.as_str() {
        "__dir__" => {
            return (!file_path.is_empty()).then(|| dir_path(file_path));
        }
        "__file__" => {
            return (!file_path.is_empty()).then(|| file_path.to_owned());
        }
        "directory_separator" => return Some("/".to_owned()),
        "true" | "false" | "null" => return Some(lower),
        _ => {}
    }
    is_php_number(expression).then(|| expression.to_owned())
}

fn split_php_concatenation(expression: &str) -> Vec<&str> {
    let bytes = expression.as_bytes();
    let mut parts = Vec::new();
    let mut start = 0;
    let mut depth = 0usize;
    let mut quote = 0u8;
    let mut escaped = false;
    for (index, &character) in bytes.iter().enumerate() {
        if quote != 0 {
            if escaped {
                escaped = false;
            } else if character == b'\\' {
                escaped = true;
            } else if character == quote {
                quote = 0;
            }
            continue;
        }
        match character {
            b'\'' | b'"' => quote = character,
            b'(' | b'[' | b'{' => depth += 1,
            b')' | b']' | b'}' => depth = depth.saturating_sub(1),
            b'.' if depth == 0 => {
                parts.push(expression[start..index].trim());
                start = index + 1;
            }
            _ => {}
        }
    }
    if parts.is_empty() {
        return vec![expression];
    }
    parts.push(expression[start..].trim());
    parts
}

fn php_string_literal(expression: &str) -> Option<String> {
    let bytes = expression.as_bytes();
    if bytes.len() < 2 {
        return None;
    }
    let quote = bytes[0];
    if (quote != b'\'' && quote != b'"') || bytes[bytes.len() - 1] != quote {
        return None;
    }
    let body = &bytes[1..bytes.len() - 1];
    let mut value: Vec<u8> = Vec::with_capacity(body.len());
    let mut index = 0;
    while index < body.len() {
        let character = body[index];
        if quote == b'"' && character == b'$' {
            return None;
        }
        if character != b'\\' || index + 1 >= body.len() {
            value.push(character);
            index += 1;
            continue;
        }
        let next = body[index + 1];
        if quote == b'\'' {
            if next == b'\\' || next == b'\'' {
                value.push(next);
                index += 2;
                continue;
            }
            value.push(character);
            index += 1;
            continue;
        }
        match next {
            b'\\' | b'"' | b'$' => {
                value.push(next);
                index += 2;
            }
            b'n' => {
                value.push(b'\n');
                index += 2;
            }
            b'r' => {
                value.push(b'\r');
                index += 2;
            }
            b't' => {
                value.push(b'\t');
                index += 2;
            }
            _ => {
                // PHP keeps the slash for unknown double-quoted escapes.
                value.push(character);
                index += 1;
            }
        }
    }
    Some(
        String::from_utf8(value)
            .unwrap_or_else(|error| String::from_utf8_lossy(error.as_bytes()).into_owned()),
    )
}

fn wrapped_by_parentheses(expression: &str) -> bool {
    let bytes = expression.as_bytes();
    if bytes.len() < 2 || bytes[0] != b'(' || bytes[bytes.len() - 1] != b')' {
        return false;
    }
    let mut depth = 0i32;
    let mut quote = 0u8;
    let mut escaped = false;
    for (index, &character) in bytes.iter().enumerate() {
        if quote != 0 {
            if escaped {
                escaped = false;
            } else if character == b'\\' {
                escaped = true;
            } else if character == quote {
                quote = 0;
            }
            continue;
        }
        match character {
            b'\'' | b'"' => quote = character,
            b'(' => depth += 1,
            b')' => {
                depth -= 1;
                if depth == 0 && index != bytes.len() - 1 {
                    return false;
                }
            }
            _ => {}
        }
    }
    depth == 0
}

fn is_php_name(value: &str) -> bool {
    let value = value.strip_prefix('\\').unwrap_or(value);
    !value.is_empty() && value.split('\\').all(is_php_identifier)
}

fn is_php_identifier(value: &str) -> bool {
    !value.is_empty()
        && value.char_indices().all(|(index, character)| {
            character == '_'
                || u32::from(character) >= 0x80
                || character.is_ascii_alphabetic()
                || (index > 0 && character.is_ascii_digit())
        })
}

fn is_php_number(value: &str) -> bool {
    !value.is_empty()
        && value.char_indices().all(|(index, character)| {
            character.is_ascii_digit()
                || matches!(character, '_' | '.' | 'x' | 'X' | 'b' | 'B')
                || (index == 0 && (character == '+' || character == '-'))
        })
}

pub(crate) fn is_static_parameter_expression(expression: &str) -> bool {
    let expression = expression.trim();
    let lower = expression.to_lowercase();
    if expression.is_empty()
        || expression.contains('$')
        || expression.contains("->")
        || lower.contains("new ")
    {
        return false;
    }
    (expression.starts_with('[') && expression.ends_with(']'))
        || (lower.starts_with("array(") && expression.ends_with(')'))
}

#[cfg(test)]
#[path = "tests_phpsvc/php_services.rs"]
mod tests;
