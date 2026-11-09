//! Constructor arguments, configured method calls, argument types and named
//! arguments of configured services.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use serde_json::json;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_php::PhpIndex;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Parameter, Symbol, SymbolKind, Visibility};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_symfony::{Service, ServiceArgumentReference, ServiceIndex, ServiceMethodReference};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::{lower_ext, problem, similar_value};
use crate::LookupError;
use crate::ProblemAnalyzer;
use crate::common::value_node_text_range;

const MISSING_SERVICE_ARGUMENTS_CODE: &str = "symfony.service.arguments.missing";
const UNKNOWN_SERVICE_NAMED_ARGUMENT_CODE: &str = "symfony.service.named_argument.unknown";
const MISSING_CONFIGURED_SERVICE_METHOD_CODE: &str = "symfony.service.method.missing";
const INVALID_SERVICE_ARGUMENT_TYPE_CODE: &str = "symfony.service.argument.type";

const UNSUPPORTED_SERVICE_CONFIGURATION_KEYS: &[&str] = &[
    "parent",
    "factory_class",
    "factory_service",
    "factory_method",
    "abstract",
    "factory",
    "resource",
    "exclude",
    "alias",
];

pub struct ServiceArgumentAnalyzer {
    service_index: Option<Arc<ServiceIndex>>,
    php_index: Option<PhpIndex>,
}

struct Definition {
    node: SyntaxNode,
    service_id: String,
    class_name: String,
    positional_count: usize,
    named_arguments: HashSet<String>,
    format: &'static str,
}

impl ServiceArgumentAnalyzer {
    pub fn new(service_index: Option<Arc<ServiceIndex>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            service_index,
            php_index,
        }
    }

    fn services(&self) -> Option<&ServiceIndex> {
        self.service_index.as_deref()
    }
}

impl ProblemAnalyzer for ServiceArgumentAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.service_index) + crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php) = &self.php_index else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let definitions = match lower_ext(&document.uri).as_str() {
            ".xml" => xml_service_argument_definitions(&root),
            ".yaml" | ".yml" => yaml_service_argument_definitions(&root),
            ".php" => Vec::new(),
            _ => return Ok(Vec::new()),
        };

        let mut service_suggestions: HashMap<String, Vec<String>> = HashMap::new();
        let mut result = self.configured_service_method_diagnostics(ctx, php, document, &root)?;
        result.extend(self.service_argument_type_diagnostics(ctx, php, document, &root)?);
        result.extend(self.yaml_named_argument_diagnostics(ctx, php, document, &root)?);
        for definition in definitions {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let constructors = php.find_methods(&definition.class_name, "__construct");
            let Some(constructor) = constructors.first() else {
                continue;
            };
            let missing = missing_constructor_parameters(constructor, &definition);
            if missing.is_empty() {
                continue;
            }
            let mut arguments = Vec::with_capacity(missing.len());
            let mut names = Vec::with_capacity(missing.len());
            for parameter in missing {
                let type_name = injectable_object_type(&parameter.ty);
                if !service_suggestions.contains_key(&type_name)
                    && !type_name.is_empty()
                    && let Some(services) = self.services()
                {
                    let found = services.get_services_by_type(&type_name).map_err(|error| {
                        LookupError::new(
                            format!("find services for constructor type {type_name:?}"),
                            error,
                        )
                    })?;
                    service_suggestions
                        .insert(type_name.clone(), found.into_iter().map(|s| s.id).collect());
                }
                let candidates = service_suggestions
                    .get(&type_name)
                    .cloned()
                    .unwrap_or_default();
                let suggested_service = if candidates.len() == 1 {
                    candidates[0].clone()
                } else {
                    "?".to_owned()
                };
                arguments.push(json!({
                    "name": parameter.name.as_str(),
                    "type": parameter.ty.to_string(),
                    "suggestedService": suggested_service,
                    "candidates": candidates,
                }));
                names.push(parameter.name.as_str().to_owned());
            }
            let label = if definition.service_id.is_empty() {
                &definition.class_name
            } else {
                &definition.service_id
            };
            let mut problem = problem(
                MISSING_SERVICE_ARGUMENTS_CODE,
                value_node_text_range(Some(&definition.node), &definition.class_name),
                format!(
                    "Service '{label}' is missing required constructor arguments: {}",
                    names.join(", ")
                ),
                DiagnosticSeverity::WARNING,
            );
            problem.payload = Some(json!({
                "serviceID": definition.service_id,
                "className": definition.class_name,
                "format": definition.format,
                "missingArguments": arguments,
            }));
            result.push(problem);
        }
        Ok(result)
    }
}

impl ServiceArgumentAnalyzer {
    fn configured_service_method_diagnostics(
        &self,
        ctx: &Context,
        php: &PhpIndex,
        document: &TextDocument,
        root: &SyntaxNode,
    ) -> Result<Vec<Problem>, BoxError> {
        let references: Vec<ServiceMethodReference> = match lower_ext(&document.uri).as_str() {
            ".yaml" | ".yml" => swlsp_symfony::yaml_service_method_references(root),
            ".xml" => swlsp_symfony::xml_service_method_references(root),
            ".php" => swlsp_symfony::php_service_method_references(
                &document.uri,
                root,
                document.line_index(),
            ),
            _ => return Ok(Vec::new()),
        };
        if references.is_empty() {
            return Ok(Vec::new());
        }
        let local_services = configured_services(document, root);
        let mut result = Vec::new();
        for reference in references {
            if ctx.is_cancelled() {
                return Ok(result);
            }
            let (receiver_service, receiver_class) = reference.receiver();
            let Some(class_name) = swlsp_symfony::resolve_configured_service_class(
                receiver_service,
                receiver_class,
                &local_services,
                self.services(),
            )?
            else {
                continue;
            };
            if !php
                .find_methods(&class_name, &reference.method_name)
                .is_empty()
            {
                continue;
            }
            if php.find_class(&class_name).is_none() {
                continue;
            }
            let names = public_php_method_names(php, &class_name);
            let mut problem = problem(
                MISSING_CONFIGURED_SERVICE_METHOD_CODE,
                reference.range,
                "Missing Method".to_owned(),
                DiagnosticSeverity::WARNING,
            );
            problem.payload = Some(json!({
                "className": class_name,
                "methodName": reference.method_name,
                "suggestions": similar_value(&reference.method_name, &names),
            }));
            result.push(problem);
        }
        Ok(result)
    }

    fn service_argument_type_diagnostics(
        &self,
        ctx: &Context,
        php: &PhpIndex,
        document: &TextDocument,
        root: &SyntaxNode,
    ) -> Result<Vec<Problem>, BoxError> {
        let references = configured_service_argument_references(document, root);
        if references.is_empty() {
            return Ok(Vec::new());
        }
        let local_services = configured_services(document, root);
        let snapshot = php.semantic_snapshot();
        let relations = snapshot.relations();
        let mut result = Vec::new();
        for reference in &references {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(owner_class) = swlsp_symfony::resolve_configured_service_class(
                &reference.owner_service_id,
                &reference.owner_class,
                &local_services,
                self.services(),
            )?
            else {
                continue;
            };
            let Some(parameter) = configured_target_parameter(php, &owner_class, reference) else {
                continue;
            };
            if !has_named_object_type(&parameter.ty) {
                continue;
            }
            let Some(actual_class) = swlsp_symfony::resolve_configured_service_class(
                &reference.service_id,
                "",
                &local_services,
                self.services(),
            )?
            else {
                continue;
            };
            if relations.is_assignable_to(&Type::named(&actual_class, Vec::new()), &parameter.ty) {
                continue;
            }
            let suggestions = self.compatible_service_suggestions(
                php,
                &parameter.ty,
                &local_services,
                reference,
            )?;
            let expected = parameter.ty.to_string();
            let mut problem = problem(
                INVALID_SERVICE_ARGUMENT_TYPE_CODE,
                reference.range,
                format!("Expect instance of: {expected}"),
                DiagnosticSeverity::WARNING,
            );
            problem.payload = Some(json!({
                "expectedType": expected,
                "actualClass": actual_class,
                "suggestions": suggestions,
            }));
            result.push(problem);
        }
        Ok(result)
    }

    fn compatible_service_suggestions(
        &self,
        php: &PhpIndex,
        expected: &Type,
        local: &HashMap<String, Service>,
        reference: &ServiceArgumentReference,
    ) -> Result<Vec<String>, BoxError> {
        let snapshot = php.semantic_snapshot();
        let relations = snapshot.relations();
        let mut candidates: HashMap<String, String> = HashMap::new();
        if let Some(services) = self.services() {
            for type_name in named_object_types(expected) {
                for service in services.get_services_by_type(&type_name)? {
                    let class_name = static_configured_class(&service.class);
                    if !class_name.is_empty()
                        && relations
                            .is_assignable_to(&Type::named(&class_name, Vec::new()), expected)
                    {
                        candidates.insert(service.id.clone(), class_name);
                    }
                }
            }
        }
        for (service_id, service) in local {
            let class_name = static_configured_class(&service.class);
            if !class_name.is_empty()
                && relations.is_assignable_to(&Type::named(&class_name, Vec::new()), expected)
            {
                candidates.insert(service_id.clone(), class_name);
            }
        }
        let mut result: Vec<String> = candidates
            .iter()
            .map(|(service_id, class_name)| {
                format_compatible_service_suggestion(service_id, class_name, reference)
            })
            .collect();
        result.sort();
        Ok(result)
    }

    fn yaml_named_argument_diagnostics(
        &self,
        ctx: &Context,
        php: &PhpIndex,
        document: &TextDocument,
        root: &SyntaxNode,
    ) -> Result<Vec<Problem>, BoxError> {
        if !matches!(lower_ext(&document.uri).as_str(), ".yaml" | ".yml") {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for argument in swlsp_symfony::yaml_service_named_arguments(root) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if argument.has_factory {
                continue;
            }
            let class_name = swlsp_symfony::resolve_yaml_service_named_argument_class(
                self.services(),
                &argument,
            )
            .map_err(|error| {
                LookupError::new(
                    format!(
                        "resolve service class for named argument {:?}",
                        argument.name
                    ),
                    error,
                )
            })?;
            let Some(class_name) = class_name else {
                continue;
            };
            if php.find_class(&class_name).is_none() {
                continue;
            }
            let name = normalize_argument_name(&argument.name);
            let mut candidates = Vec::new();
            let mut valid = false;
            for constructor in php.find_methods(&class_name, "__construct") {
                for parameter in &constructor.parameters {
                    let parameter_name = normalize_argument_name(&parameter.name);
                    candidates.push(format!("${parameter_name}"));
                    if parameter_name == name {
                        valid = true;
                    }
                }
            }
            if valid {
                continue;
            }
            let mut problem = problem(
                UNKNOWN_SERVICE_NAMED_ARGUMENT_CODE,
                argument.range,
                "Symfony: named argument does not exists".to_owned(),
                DiagnosticSeverity::WARNING,
            );
            problem.payload = Some(json!({
                "suggestions": similar_value(&argument.name, &candidates),
            }));
            result.push(problem);
        }
        Ok(result)
    }
}

fn public_php_method_names(index: &PhpIndex, class_name: &str) -> Vec<String> {
    if class_name.is_empty() {
        return Vec::new();
    }
    let snapshot = index.semantic_snapshot();
    let members = MemberResolver {
        snapshot: Some(&snapshot),
    }
    .all(&Type::named(class_name, Vec::new()));
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for member in members {
        let symbol = &member.symbol;
        if symbol.kind != SymbolKind::Method || symbol.visibility != Visibility::Public {
            continue;
        }
        if !seen.insert(lowercase(&symbol.name)) {
            continue;
        }
        result.push(symbol.name.as_str().to_owned());
    }
    result.sort();
    result
}

fn configured_service_argument_references(
    document: &TextDocument,
    root: &SyntaxNode,
) -> Vec<ServiceArgumentReference> {
    match lower_ext(&document.uri).as_str() {
        ".yaml" | ".yml" => swlsp_symfony::yaml_service_argument_references(root),
        ".xml" => swlsp_symfony::xml_service_argument_references(root),
        ".php" => swlsp_symfony::php_service_argument_references(
            &document.uri,
            root,
            document.line_index(),
        ),
        _ => Vec::new(),
    }
}

fn configured_services(document: &TextDocument, root: &SyntaxNode) -> HashMap<String, Service> {
    let services = match lower_ext(&document.uri).as_str() {
        ".yaml" | ".yml" => {
            swlsp_symfony::parse_yaml_services_tree(
                &document.uri,
                document.tree(),
                document.line_index(),
            )
            .0
        }
        ".xml" => {
            swlsp_symfony::parse_xml_services_tree(
                &document.uri,
                document.tree(),
                document.line_index(),
            )
            .0
        }
        ".php" => {
            swlsp_symfony::parse_php_services_tree(&document.uri, root, document.line_index()).0
        }
        _ => Vec::new(),
    };
    services
        .into_iter()
        .filter(|service| !service.id.is_empty())
        .map(|service| (service.id.clone(), service))
        .collect()
}

fn static_configured_class(value: &str) -> String {
    let value = value.strip_prefix('\\').unwrap_or(value).trim();
    if value.is_empty() || value.contains(['%', '$', '{', '}', '@', ' ', '\t']) {
        return String::new();
    }
    value.to_owned()
}

fn configured_target_parameter(
    php: &PhpIndex,
    class_name: &str,
    reference: &ServiceArgumentReference,
) -> Option<Parameter> {
    let method_name = if reference.method_name.is_empty() {
        "__construct"
    } else {
        &reference.method_name
    };
    for method in php.find_methods(class_name, method_name) {
        if !reference.parameter_name.is_empty() {
            let name = normalize_argument_name(&reference.parameter_name);
            if let Some(parameter) = method
                .parameters
                .iter()
                .find(|parameter| normalize_argument_name(&parameter.name) == name)
            {
                return Some(parameter.clone());
            }
            continue;
        }
        if reference.parameter_index >= 0
            && (reference.parameter_index as usize) < method.parameters.len()
        {
            return Some(method.parameters[reference.parameter_index as usize].clone());
        }
    }
    None
}

fn has_named_object_type(value: &Type) -> bool {
    match value.kind() {
        TypeKind::Object => !value.name().is_empty(),
        TypeKind::Union | TypeKind::Intersection => {
            value.arguments().iter().any(has_named_object_type)
        }
        _ => false,
    }
}

fn named_object_types(value: &Type) -> Vec<String> {
    match value.kind() {
        TypeKind::Object if !value.name().is_empty() => vec![value.name().to_owned()],
        TypeKind::Union | TypeKind::Intersection => value
            .arguments()
            .iter()
            .flat_map(named_object_types)
            .collect(),
        _ => Vec::new(),
    }
}

fn format_compatible_service_suggestion(
    service_id: &str,
    class_name: &str,
    reference: &ServiceArgumentReference,
) -> String {
    match reference.replacement.as_str() {
        "php_helper_string" => return service_id.to_owned(),
        "php_helper_expression" => {
            if service_id == class_name && !class_name.is_empty() {
                return format!("\\{class_name}::class");
            }
            return php_single_quoted_string(service_id);
        }
        "php_raw_string" => return format!("@{service_id}"),
        "php_raw_expression" => {
            if service_id == class_name && !class_name.is_empty() {
                return format!("service(\\{class_name}::class)");
            }
            return format!("service({})", php_single_quoted_string(service_id));
        }
        _ => {}
    }
    if reference.format == "yaml" {
        return format!("@{service_id}");
    }
    service_id.to_owned()
}

fn php_single_quoted_string(value: &str) -> String {
    format!("'{}'", value.replace('\\', "\\\\").replace('\'', "\\'"))
}

fn xml_service_argument_definitions(root: &SyntaxNode) -> Vec<Definition> {
    let mut result = Vec::new();
    for services in xmlquery::elements(root, &["services"]) {
        let default_autowire = xmlquery::child_element(&services, &["defaults"])
            .and_then(|defaults| xmlquery::attribute(&defaults, "autowire"))
            .and_then(|attribute| xmlquery::attribute_value(&attribute))
            .is_some_and(|value| eq_ignore_case(&value, "true"));
        for service in xmlquery::child_elements(&services, &["service"]) {
            let attributes = xmlquery::attribute_values(&service);
            if unsupported_xml_service_configuration(&attributes)
                || xmlquery::child_element(&service, &["factory"]).is_some()
            {
                continue;
            }
            let attribute = |name: &str| attributes.get(name).cloned().unwrap_or_default();
            let autowire = match lowercase(&attribute("autowire")).as_str() {
                "true" => true,
                "false" => false,
                _ => default_autowire,
            };
            if autowire {
                continue;
            }
            let mut class_name = attribute("class");
            if class_name.is_empty() && attribute("id").contains('\\') {
                class_name = attribute("id");
            }
            let class_name = class_name
                .strip_prefix('\\')
                .unwrap_or(&class_name)
                .to_owned();
            if class_name.is_empty() || class_name.contains(['%', '$', '{', '}']) {
                continue;
            }
            let mut named = HashSet::new();
            let mut positional_count = 0;
            for argument in xmlquery::child_elements(&service, &["argument"]) {
                let key = xmlquery::attribute(&argument, "key")
                    .and_then(|attribute| xmlquery::attribute_value(&attribute))
                    .unwrap_or_default();
                if key.is_empty() {
                    positional_count += 1;
                } else {
                    named.insert(normalize_argument_name(&key).to_owned());
                }
            }
            result.push(Definition {
                node: service.clone(),
                service_id: attribute("id"),
                class_name,
                positional_count,
                named_arguments: named,
                format: "xml",
            });
        }
    }
    result
}

fn unsupported_xml_service_configuration(attributes: &BTreeMap<String, String>) -> bool {
    UNSUPPORTED_SERVICE_CONFIGURATION_KEYS
        .iter()
        .any(|key| attributes.contains_key(&key.replace('_', "-")))
}

fn yaml_service_argument_definitions(root: &SyntaxNode) -> Vec<Definition> {
    let Some(services) = yamlquery::root_value(root)
        .filter(yamlquery::is_mapping)
        .and_then(|mapping| yamlquery::property(&mapping, "services"))
    else {
        return Vec::new();
    };
    if !yamlquery::is_mapping(&services) {
        return Vec::new();
    }
    let default_autowire = yaml_boolean_property(
        yamlquery::property_pair(&services, "_defaults")
            .and_then(|pair| yamlquery::pair_value(&pair))
            .as_ref(),
        "autowire",
    );

    let mut result = Vec::new();
    for pair in yamlquery::pairs(&services) {
        let Some(key) = yamlquery::pair_key(&pair) else {
            continue;
        };
        let service_id = yamlquery::scalar_value(&key);
        if service_id.is_empty() || service_id.starts_with('_') {
            continue;
        }
        let Some(config) = yamlquery::pair_value(&pair) else {
            continue;
        };
        if !yamlquery::is_mapping(&config) || unsupported_yaml_service_configuration(&config) {
            continue;
        }
        let autowire = match yamlquery::property(&config, "autowire") {
            Some(property) => eq_ignore_case(&yamlquery::scalar_value(&property), "true"),
            None => default_autowire,
        };
        if autowire {
            continue;
        }
        let mut class_name = yamlquery::property(&config, "class")
            .map(|class| yamlquery::scalar_value(&class))
            .unwrap_or_default();
        if class_name.is_empty() && service_id.contains('\\') {
            class_name = service_id.clone();
        }
        let class_name = class_name
            .strip_prefix('\\')
            .unwrap_or(&class_name)
            .to_owned();
        if class_name.is_empty() || class_name.contains(['%', '$', '{', '}']) {
            continue;
        }

        let mut positional_count = 0;
        let mut named = HashSet::new();
        if let Some(arguments) = yamlquery::property(&config, "arguments") {
            if yamlquery::is_sequence(&arguments) {
                positional_count = yamlquery::items(&arguments).count();
            } else if yamlquery::is_mapping(&arguments) {
                for argument in yamlquery::pairs(&arguments) {
                    let name = yamlquery::pair_key(&argument)
                        .map(|key| yamlquery::scalar_value(&key))
                        .unwrap_or_default();
                    named.insert(normalize_argument_name(&name).to_owned());
                }
            } else {
                continue;
            }
        }
        result.push(Definition {
            node: key,
            service_id,
            class_name,
            positional_count,
            named_arguments: named,
            format: "yaml",
        });
    }
    result
}

fn yaml_boolean_property(mapping: Option<&SyntaxNode>, name: &str) -> bool {
    let Some(mapping) = mapping.filter(|mapping| yamlquery::is_mapping(mapping)) else {
        return false;
    };
    let value = yamlquery::property(mapping, name)
        .map(|property| yamlquery::scalar_value(&property))
        .unwrap_or_default();
    eq_ignore_case(&value, "true")
}

fn unsupported_yaml_service_configuration(mapping: &SyntaxNode) -> bool {
    UNSUPPORTED_SERVICE_CONFIGURATION_KEYS
        .iter()
        .any(|key| yamlquery::property(mapping, key).is_some())
}

fn missing_constructor_parameters<'a>(
    constructor: &'a Symbol,
    definition: &Definition,
) -> Vec<&'a Parameter> {
    constructor
        .parameters
        .iter()
        .filter(|parameter| !parameter.optional && !parameter.flags.has(Flags::VARIADIC))
        .enumerate()
        .filter(|(index, parameter)| {
            *index >= definition.positional_count
                && !definition
                    .named_arguments
                    .contains(normalize_argument_name(&parameter.name))
        })
        .map(|(_, parameter)| parameter)
        .collect()
}

pub(crate) fn normalize_argument_name(value: &str) -> &str {
    let value = value.trim();
    value.strip_prefix('$').unwrap_or(value)
}

fn injectable_object_type(value: &Type) -> String {
    if value.kind() == TypeKind::Object {
        return value.name().to_owned();
    }
    if value.kind() != TypeKind::Union {
        return String::new();
    }
    let mut result = "";
    for member in value.arguments() {
        if member.kind() != TypeKind::Object || member.name().is_empty() {
            continue;
        }
        if !result.is_empty() {
            return String::new();
        }
        result = member.name();
    }
    result.to_owned()
}
