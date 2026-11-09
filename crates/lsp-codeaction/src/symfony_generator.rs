//! The reference plugin's interactive
//! service and compiler-pass generators. Code actions hand off editor input
//! to a client command; the server commands keep inference and source
//! rewriting editor-independent.

use crate::shared::document_snapshot;
use std::collections::{BTreeMap, HashSet};
use std::sync::{Arc, LazyLock};

use regex::Regex;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    ActionProvider, BoxError, CodeActionRequest, CommandFunc, CommandProvider, Context,
    SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_BLOCK, PHP_CLASS_DECLARATION};
use swlsp_php::PhpIndex;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Snapshot, SymbolKind, Visibility};
use swlsp_php_types::{Kind, Type};
use swlsp_symfony::{Service, ServiceIndex, suggested_service_tags};
use swlsp_textutil::{eq_ignore_case, lowercase};
use swlsp_uriutil::filepath;

use crate::service_tag::{SnapshotHierarchy, html_escape};
use crate::shared::{
    PhpSourceReplacement, apply_php_source_replacements, decode_generator_request,
    escape_php_single_quoted, php_block_delimiters, php_class_fully_qualified_name,
    php_class_qualifier, php_import_block, php_import_insertion_offset, php_line_indentation,
    php_member_indent_unit, php_methods_by_name, token_start, trimmed_start,
};

pub const GENERATE_SYMFONY_SERVICE_COMMAND: &str = "shopware/symfony/service/generate";
pub const COLLECT_SYMFONY_SERVICES_COMMAND: &str =
    "shopware/symfony/analytics/services/generateDefinitions";
pub const CREATE_COMPILER_PASS_COMMAND: &str = "shopware/symfony/compilerPass/create";
pub const GENERATE_SYMFONY_SERVICE_ACTION: &str = "shopware.symfony.generateService";
pub const CREATE_COMPILER_PASS_ACTION: &str = "shopware.symfony.createCompilerPass";

const BUNDLE_INTERFACE_CLASS: &str = "Symfony\\Component\\HttpKernel\\Bundle\\BundleInterface";
const CONTAINER_BUILDER_CLASS: &str = "Symfony\\Component\\DependencyInjection\\ContainerBuilder";

/// A PHP class identifier. PHP allows the bytes 0x80..0xFF in names; this
/// accepts the code points U+0080..U+00FF, since the input is a `str`.
static PHP_CLASS_IDENTIFIER_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"^[A-Za-z_\x{80}-\x{ff}][A-Za-z0-9_\x{80}-\x{ff}]*$").expect("valid regex")
});

#[derive(Clone)]
pub struct SymfonyGeneratorProvider {
    php_index: Option<PhpIndex>,
    service_index: Option<Arc<ServiceIndex>>,
}

impl SymfonyGeneratorProvider {
    pub fn new(php_index: Option<PhpIndex>, service_index: Option<Arc<ServiceIndex>>) -> Self {
        Self {
            php_index,
            service_index,
        }
    }
}

impl ActionProvider for SymfonyGeneratorProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(php_index) = self.php_index.as_ref() else {
            return Vec::new();
        };
        if ctx.is_cancelled() {
            return Vec::new();
        }
        let (Some(document), Some(root), Some(node)) = (
            request.document.as_ref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        let Some(class) = phpquery::class_at(node) else {
            return Vec::new();
        };
        if class.kind() != PHP_CLASS_DECLARATION {
            return Vec::new();
        }
        let class_name = php_class_fully_qualified_name(root, &class);
        if class_name.is_empty() {
            return Vec::new();
        }
        let uri = request.params.text_document.uri.clone();
        let command_action = |title: &str, command: &str| protocol::CodeAction {
            title: title.to_owned(),
            kind: Some(CodeActionKind::REFACTOR_REWRITE),
            command: Some(protocol::CommandAction {
                title: title.to_owned(),
                command: command.to_owned(),
                arguments: vec![Value::from(uri.clone()), Value::from(class_name.clone())],
            }),
            ..protocol::CodeAction::default()
        };

        let mut actions = Vec::new();
        if phpquery::method_at(node).is_some() {
            actions.push(command_action(
                "Generate Symfony service",
                GENERATE_SYMFONY_SERVICE_ACTION,
            ));
        }
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_else(|_| document.uri.clone());
        let snapshot = document_snapshot(php_index, document, &path, root);
        if snapshot.is_subtype_of(&class_name, BUNDLE_INTERFACE_CLASS) {
            actions.push(command_action(
                "Symfony: Create CompilerPass",
                CREATE_COMPILER_PASS_ACTION,
            ));
        }
        actions
    }
}

impl CommandProvider for SymfonyGeneratorProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let mut result: BTreeMap<String, CommandFunc> = BTreeMap::new();
        let provider = self.clone();
        result.insert(
            GENERATE_SYMFONY_SERVICE_COMMAND.to_owned(),
            Arc::new(move |ctx, args| provider.generate_service_command(ctx, args)),
        );
        let provider = self.clone();
        result.insert(
            COLLECT_SYMFONY_SERVICES_COMMAND.to_owned(),
            Arc::new(move |ctx, args| {
                let request: SymfonyServiceDefinitionCollectionRequest =
                    decode_generator_request(args)?;
                Ok(serde_json::to_value(
                    provider.collect_service_definitions(ctx, &request)?,
                )?)
            }),
        );
        let provider = self.clone();
        result.insert(
            CREATE_COMPILER_PASS_COMMAND.to_owned(),
            Arc::new(move |ctx, args| provider.create_compiler_pass_command(ctx, args)),
        );
        result
    }
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SymfonyServiceGenerationRequest {
    #[serde(rename = "className")]
    pub class_name: String,
    pub output: String,
    #[serde(rename = "classAsId")]
    pub class_as_id: bool,
    #[serde(rename = "serviceId")]
    pub service_id: String,
    #[serde(rename = "fileUri")]
    pub file_uri: String,
    pub source: String,
    pub version: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct SymfonyServiceGenerationResponse {
    pub content: String,
    pub language: String,
}

#[derive(Debug, Clone)]
struct GeneratedServiceMethod {
    name: String,
    arguments: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SymfonyServiceDefinitionCollectionRequest {
    #[serde(rename = "classNames")]
    pub class_names: String,
    pub output: String,
    #[serde(rename = "classAsId")]
    pub class_as_id: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SymfonyServiceParameterSuggestion {
    pub method: String,
    pub parameter: String,
    #[serde(rename = "type", skip_serializing_if = "String::is_empty")]
    pub ty: String,
    pub services: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SymfonyServiceDefinitionCollectionEntry {
    #[serde(rename = "className")]
    pub class_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub content: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub language: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub suggestions: Vec<SymfonyServiceParameterSuggestion>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SymfonyServiceDefinitionCollectionResponse {
    pub definitions: Vec<SymfonyServiceDefinitionCollectionEntry>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CompilerPassCreationRequest {
    #[serde(rename = "bundleUri")]
    pub bundle_uri: String,
    #[serde(rename = "bundleClass")]
    pub bundle_class: String,
    #[serde(rename = "className")]
    pub class_name: String,
    pub source: String,
    pub version: i64,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct CompilerPassCreationResponse {
    #[serde(rename = "fileUri")]
    pub file_uri: String,
    #[serde(rename = "fileContent")]
    pub file_content: String,
    #[serde(rename = "bundleContent")]
    pub bundle_content: String,
}

fn cancelled(ctx: &Context) -> Result<(), BoxError> {
    ctx.check().map_err(|error| Box::new(error) as BoxError)
}

fn trim_backslash_space(value: &str) -> &str {
    value.trim_matches(|character| character == '\\' || character == ' ')
}

fn supported_output(output: &str) -> bool {
    matches!(output, "yaml" | "xml" | "fluent" | "php-array")
}

impl SymfonyGeneratorProvider {
    fn generate_service_command(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<Value, BoxError> {
        let (Some(_), Some(_)) = (self.php_index.as_ref(), self.service_index.as_ref()) else {
            return Err("symfony service generator is unavailable".into());
        };
        let params: SymfonyServiceGenerationRequest = decode_generator_request(args)?;
        Ok(serde_json::to_value(self.generate_service(ctx, &params)?)?)
    }

    pub fn generate_service(
        &self,
        ctx: &Context,
        params: &SymfonyServiceGenerationRequest,
    ) -> Result<SymfonyServiceGenerationResponse, BoxError> {
        let (Some(php_index), Some(service_index)) =
            (self.php_index.as_ref(), self.service_index.as_ref())
        else {
            return Err("symfony service generator is unavailable".into());
        };
        cancelled(ctx)?;
        let class_name = trim_backslash_space(&params.class_name);
        let mut snapshot = php_index.semantic_snapshot();
        if !params.source.is_empty() {
            let parsed = swlsp_parser_php::parse(&params.source);
            let path =
                swlsp_uriutil::path(&params.file_uri).unwrap_or_else(|_| params.file_uri.clone());
            let document =
                php_index.analyze_document(&path, params.version, &parsed.root(), &params.source);
            snapshot = snapshot.with_document(&document);
        }
        let classes = snapshot.classes(class_name);
        if classes
            .first()
            .is_none_or(|class| class.kind != SymbolKind::Class)
        {
            return Err(format!("PHP class {:?} was not found", class_name).into());
        }
        let output = lowercase(params.output.trim());
        if !supported_output(&output) {
            return Err(format!("unsupported service output format {:?}", params.output).into());
        }
        let mut service_id = params.service_id.trim().to_owned();
        if params.class_as_id {
            service_id = class_name.to_owned();
        } else if service_id.is_empty() {
            service_id = default_symfony_service_id(class_name);
        }

        let methods = generated_service_methods(ctx, service_index, &snapshot, class_name)?;
        let tags = suggested_service_tags(class_name, Some(&SnapshotHierarchy(&snapshot)));
        let content = render_symfony_service_definition(
            &output,
            class_name,
            &service_id,
            params.class_as_id,
            &methods,
            &tags,
            Some(php_index),
        );
        Ok(SymfonyServiceGenerationResponse {
            content,
            language: generated_service_language(&output).to_owned(),
        })
    }

    pub fn collect_service_definitions(
        &self,
        ctx: &Context,
        request: &SymfonyServiceDefinitionCollectionRequest,
    ) -> Result<SymfonyServiceDefinitionCollectionResponse, BoxError> {
        let (Some(php_index), Some(service_index)) =
            (self.php_index.as_ref(), self.service_index.as_ref())
        else {
            return Err("symfony service generator is unavailable".into());
        };
        cancelled(ctx)?;
        let output = lowercase(request.output.trim());
        if !supported_output(&output) {
            return Err(format!("unsupported service output format {:?}", request.output).into());
        }
        let class_names = unique_symfony_generator_class_names(&request.class_names);
        if class_names.is_empty() {
            return Err("classNames is required".into());
        }
        let snapshot = php_index.semantic_snapshot();
        let mut response = SymfonyServiceDefinitionCollectionResponse {
            definitions: Vec::with_capacity(class_names.len()),
        };
        for class_name in class_names {
            cancelled(ctx)?;
            let mut entry = SymfonyServiceDefinitionCollectionEntry {
                class_name: class_name.clone(),
                language: generated_service_language(&output).to_owned(),
                ..SymfonyServiceDefinitionCollectionEntry::default()
            };
            let classes = snapshot.classes(&class_name);
            if classes
                .first()
                .is_none_or(|class| class.kind != SymbolKind::Class)
            {
                entry.error = format!("PHP class {:?} was not found", class_name);
                response.definitions.push(entry);
                continue;
            }
            let methods = generated_service_methods(ctx, service_index, &snapshot, &class_name)?;
            entry.suggestions =
                generated_service_suggestions(ctx, service_index, &snapshot, &class_name)?;
            let service_id = if request.class_as_id {
                class_name.clone()
            } else {
                default_symfony_service_id(&class_name)
            };
            let content = render_symfony_service_definition(
                &output,
                &class_name,
                &service_id,
                request.class_as_id,
                &methods,
                &suggested_service_tags(&class_name, Some(&SnapshotHierarchy(&snapshot))),
                Some(php_index),
            );
            entry.content =
                append_service_suggestion_comments(content, &output, &entry.suggestions);
            response.definitions.push(entry);
        }
        Ok(response)
    }

    fn create_compiler_pass_command(
        &self,
        ctx: &Context,
        args: Option<&Value>,
    ) -> Result<Value, BoxError> {
        if self.php_index.is_none() {
            return Err("compiler-pass generator is unavailable".into());
        }
        let params: CompilerPassCreationRequest = decode_generator_request(args)?;
        Ok(serde_json::to_value(
            self.create_compiler_pass(ctx, params)?,
        )?)
    }

    pub fn create_compiler_pass(
        &self,
        ctx: &Context,
        mut params: CompilerPassCreationRequest,
    ) -> Result<CompilerPassCreationResponse, BoxError> {
        let Some(php_index) = self.php_index.as_ref() else {
            return Err("compiler-pass generator is unavailable".into());
        };
        cancelled(ctx)?;
        params.bundle_class = trim_backslash_space(&params.bundle_class).to_owned();
        params.class_name = params.class_name.trim().to_owned();
        if !PHP_CLASS_IDENTIFIER_PATTERN.is_match(&params.class_name) {
            return Err(format!("invalid compiler-pass class name {:?}", params.class_name).into());
        }
        let parsed_bundle = swlsp_parser_php::parse(&params.source);
        let bundle_path = swlsp_uriutil::path(&params.bundle_uri)
            .map_err(|error| format!("resolve bundle URI: {error}"))?;
        let bundle_document = php_index.analyze_document(
            &bundle_path,
            params.version,
            &parsed_bundle.root(),
            &params.source,
        );
        let snapshot = php_index
            .semantic_snapshot()
            .with_document(&bundle_document);
        if !snapshot.is_subtype_of(&params.bundle_class, BUNDLE_INTERFACE_CLASS) {
            return Err(format!("{} is not a Symfony bundle", params.bundle_class).into());
        }
        let bundle_dir = filepath::dir(&bundle_path);
        let mut relative_directory = filepath::join(&["DependencyInjection", "Compiler"]);
        let compiler_directory =
            filepath::join(&[bundle_dir.as_str(), relative_directory.as_str()]);
        let legacy_directory =
            filepath::join(&[bundle_dir.as_str(), "DependencyInjection", "CompilerPass"]);
        let compiler_info = std::fs::metadata(&compiler_directory);
        let legacy_info = std::fs::metadata(&legacy_directory);
        if let Err(error) = &compiler_info
            && error.kind() != std::io::ErrorKind::NotFound
        {
            return Err(format!("inspect compiler directory: {error}").into());
        }
        if let Err(error) = &legacy_info
            && error.kind() != std::io::ErrorKind::NotFound
        {
            return Err(format!("inspect legacy compiler directory: {error}").into());
        }
        let compiler_is_dir = compiler_info.as_ref().is_ok_and(std::fs::Metadata::is_dir);
        let legacy_is_dir = legacy_info.as_ref().is_ok_and(std::fs::Metadata::is_dir);
        if !compiler_is_dir && legacy_is_dir {
            relative_directory = filepath::join(&["DependencyInjection", "CompilerPass"]);
        }
        let file_name = format!("{}.php", params.class_name);
        let target_path = filepath::join(&[
            bundle_dir.as_str(),
            relative_directory.as_str(),
            file_name.as_str(),
        ]);
        for candidate in [
            target_path.clone(),
            filepath::join(&[
                bundle_dir.as_str(),
                "DependencyInjection",
                "Compiler",
                file_name.as_str(),
            ]),
            filepath::join(&[
                bundle_dir.as_str(),
                "DependencyInjection",
                "CompilerPass",
                file_name.as_str(),
            ]),
        ] {
            match std::fs::metadata(&candidate) {
                Ok(_) => {
                    return Err(format!("compiler-pass file already exists: {candidate}").into());
                }
                Err(error) if error.kind() != std::io::ErrorKind::NotFound => {
                    return Err(format!("inspect compiler-pass path: {error}").into());
                }
                Err(_) => {}
            }
        }
        let bundle_namespace = namespace_of_php_class(&params.bundle_class);
        let compiler_namespace = format!(
            "{bundle_namespace}\\{}",
            relative_directory.replace(filepath::SEPARATOR, "\\")
        );
        let compiler_class = format!("{compiler_namespace}\\{}", params.class_name)
            .trim_matches('\\')
            .to_owned();
        let updated = add_compiler_pass_to_bundle(
            &params.bundle_uri,
            &params.source,
            params.version,
            &params.bundle_class,
            &compiler_class,
            &params.class_name,
        )?;
        Ok(CompilerPassCreationResponse {
            file_uri: swlsp_uriutil::file_uri(&target_path),
            file_content: compiler_pass_template(&compiler_namespace, &params.class_name),
            bundle_content: updated,
        })
    }
}

fn generated_service_methods(
    ctx: &Context,
    service_index: &ServiceIndex,
    snapshot: &Snapshot,
    class_name: &str,
) -> Result<Vec<GeneratedServiceMethod>, BoxError> {
    let mut methods = Vec::new();
    let members =
        MemberResolver::new(snapshot).all(&Type::named(class_name.trim_matches('\\'), Vec::new()));
    for member in members {
        let method = member.symbol;
        cancelled(ctx)?;
        if method.kind != SymbolKind::Method
            || method.visibility != Visibility::Public
            || method.flags.has(Flags::STATIC)
            || method.parameters.is_empty()
        {
            continue;
        }
        let mut generated = GeneratedServiceMethod {
            name: method.name.to_string(),
            arguments: Vec::with_capacity(method.parameters.len()),
        };
        let mut has_service = false;
        for parameter in &method.parameters {
            let service_id = services_for_type(service_index, snapshot, &parameter.ty)?
                .into_iter()
                .next()
                .unwrap_or_default();
            if service_id.is_empty() {
                generated.arguments.push("?".to_owned());
            } else {
                has_service = true;
                generated.arguments.push(service_id);
            }
        }
        if has_service {
            methods.push(generated);
        }
    }
    methods.sort_by(|left, right| {
        let left_constructor = eq_ignore_case(&left.name, "__construct");
        let right_constructor = eq_ignore_case(&right.name, "__construct");
        right_constructor
            .cmp(&left_constructor)
            .then_with(|| lowercase(&left.name).cmp(&lowercase(&right.name)))
    });
    Ok(methods)
}

fn services_for_type(
    service_index: &ServiceIndex,
    snapshot: &Snapshot,
    value: &Type,
) -> Result<Vec<String>, BoxError> {
    let mut type_names = Vec::new();
    collect_service_type_names(value, &mut type_names);
    let mut services: Vec<Service> = Vec::new();
    let mut seen = HashSet::new();
    let relations = snapshot.relations();
    for type_name in type_names {
        for service in service_index.get_services_by_type(&type_name)? {
            let key = lowercase(&service.id);
            if seen.contains(&key) {
                continue;
            }
            let mut class_name = trim_backslash_space(&service.class);
            if class_name.is_empty() && service.id.contains('\\') {
                class_name = trim_backslash_space(&service.id);
            }
            if class_name.is_empty()
                || !relations.is_assignable_to(&Type::named(class_name, Vec::new()), value)
            {
                continue;
            }
            seen.insert(key);
            services.push(service);
        }
    }
    services.sort_by(|left, right| {
        let left_class_id = left.id.contains('\\');
        let right_class_id = right.id.contains('\\');
        right_class_id
            .cmp(&left_class_id)
            .then_with(|| lowercase(&left.id).cmp(&lowercase(&right.id)))
    });
    Ok(services.into_iter().map(|service| service.id).collect())
}

fn generated_service_suggestions(
    ctx: &Context,
    service_index: &ServiceIndex,
    snapshot: &Snapshot,
    class_name: &str,
) -> Result<Vec<SymfonyServiceParameterSuggestion>, BoxError> {
    let mut result = Vec::new();
    let members =
        MemberResolver::new(snapshot).all(&Type::named(class_name.trim_matches('\\'), Vec::new()));
    for member in members {
        let method = member.symbol;
        cancelled(ctx)?;
        if method.kind != SymbolKind::Method
            || method.visibility != Visibility::Public
            || method.flags.has(Flags::STATIC)
        {
            continue;
        }
        for parameter in &method.parameters {
            let mut services = services_for_type(service_index, snapshot, &parameter.ty)?;
            if services.len() <= 1 {
                continue;
            }
            services.truncate(15);
            let name: &str = &parameter.name;
            result.push(SymfonyServiceParameterSuggestion {
                method: method.name.to_string(),
                parameter: name.strip_prefix('$').unwrap_or(name).to_owned(),
                ty: parameter.ty.to_string(),
                services,
            });
        }
    }
    result.sort_by(|left, right| {
        let left_constructor = eq_ignore_case(&left.method, "__construct");
        let right_constructor = eq_ignore_case(&right.method, "__construct");
        right_constructor.cmp(&left_constructor).then_with(|| {
            if !eq_ignore_case(&left.method, &right.method) {
                lowercase(&left.method).cmp(&lowercase(&right.method))
            } else {
                lowercase(&left.parameter).cmp(&lowercase(&right.parameter))
            }
        })
    });
    Ok(result)
}

fn unique_symfony_generator_class_names(value: &str) -> Vec<String> {
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for candidate in value.split(',') {
        let class_name = candidate.trim_matches(['\\', ' ', '\t', '\r', '\n']);
        if class_name.is_empty() {
            continue;
        }
        if !seen.insert(lowercase(class_name)) {
            continue;
        }
        result.push(class_name.to_owned());
    }
    result
}

fn generated_service_language(output: &str) -> &str {
    if output == "yaml" || output == "xml" {
        output
    } else {
        "php"
    }
}

fn append_service_suggestion_comments(
    content: String,
    output: &str,
    suggestions: &[SymfonyServiceParameterSuggestion],
) -> String {
    if suggestions.is_empty() {
        return content;
    }
    let (prefix, suffix) = match output {
        "xml" => ("<!-- ", " -->"),
        "fluent" | "php-array" => ("// ", ""),
        _ => ("# ", ""),
    };
    let mut lines = vec![format!("{prefix}Possible services per parameter:{suffix}")];
    for suggestion in suggestions {
        let services: Vec<String> = suggestion
            .services
            .iter()
            .map(|service| {
                if service.contains(',') {
                    format!("\"{service}\"")
                } else {
                    service.clone()
                }
            })
            .collect();
        let mut label = format!("${}", suggestion.parameter);
        if !eq_ignore_case(&suggestion.method, "__construct") {
            label.push_str(&format!(" ({})", suggestion.method));
        }
        let line = format!(
            "{label} [{}] => {}",
            first_non_empty_generator_type(&suggestion.ty),
            services.join(", ")
        );
        lines.push(format!("{prefix}{line}{suffix}"));
    }
    format!("{content}\n\n{}", lines.join("\n"))
}

fn first_non_empty_generator_type(value: &str) -> &str {
    let value = value.trim();
    if value.is_empty() { "mixed" } else { value }
}

fn collect_service_type_names(value: &Type, result: &mut Vec<String>) {
    match value.kind() {
        Kind::Object => {
            let name = trim_backslash_space(value.name());
            if !name.is_empty() {
                result.push(name.to_owned());
            }
        }
        Kind::Union | Kind::Intersection => {
            for member in value.arguments() {
                collect_service_type_names(member, result);
            }
        }
        _ => {}
    }
}

fn default_symfony_service_id(class_name: &str) -> String {
    lowercase(&trim_backslash_space(class_name).replace('\\', "_"))
}

fn render_symfony_service_definition(
    output: &str,
    class_name: &str,
    service_id: &str,
    class_as_id: bool,
    methods: &[GeneratedServiceMethod],
    tags: &[String],
    php_index: Option<&PhpIndex>,
) -> String {
    match output {
        "xml" => render_xml_service_definition(class_name, service_id, class_as_id, methods, tags),
        "fluent" => render_fluent_service_definition(
            class_name,
            service_id,
            class_as_id,
            methods,
            tags,
            php_index,
        ),
        "php-array" => render_php_array_service_definition(
            class_name,
            service_id,
            class_as_id,
            methods,
            tags,
            php_index,
        ),
        _ => render_yaml_service_definition(class_name, service_id, class_as_id, methods, tags),
    }
}

fn render_yaml_service_definition(
    class_name: &str,
    service_id: &str,
    class_as_id: bool,
    methods: &[GeneratedServiceMethod],
    tags: &[String],
) -> String {
    let mut lines = vec![format!("{}:", yaml_service_key(service_id))];
    if !class_as_id {
        lines.push(format!("    class: {class_name}"));
    }
    for method in methods {
        let arguments: Vec<String> = method
            .arguments
            .iter()
            .map(|argument| format!("'@{}'", argument.replace('\'', "''")))
            .collect();
        if eq_ignore_case(&method.name, "__construct") {
            lines.push(format!("    arguments: [{}]", arguments.join(", ")));
        } else {
            if !lines.iter().any(|line| line == "    calls:") {
                lines.push("    calls:".to_owned());
            }
            lines.push(format!(
                "        - [{}, [{}]]",
                method.name,
                arguments.join(", ")
            ));
        }
    }
    if !tags.is_empty() {
        lines.push("    tags:".to_owned());
        for tag in tags {
            lines.push(format!("        - {{ name: {tag} }}"));
        }
    }
    if lines.len() == 1 {
        lines[0].push_str(" ~");
    }
    lines.join("\n")
}

fn yaml_service_key(value: &str) -> String {
    if !value.is_empty()
        && value
            .chars()
            .all(|character| character.is_ascii_alphanumeric() || "_.\\-".contains(character))
    {
        return value.to_owned();
    }
    format!("'{}'", value.replace('\'', "''"))
}

fn render_xml_service_definition(
    class_name: &str,
    service_id: &str,
    class_as_id: bool,
    methods: &[GeneratedServiceMethod],
    tags: &[String],
) -> String {
    let mut builder = String::new();
    builder.push_str("<service id=\"");
    builder.push_str(&html_escape(service_id));
    builder.push('"');
    if !class_as_id {
        builder.push_str(" class=\"");
        builder.push_str(&html_escape(class_name));
        builder.push('"');
    }
    if methods.is_empty() && tags.is_empty() {
        builder.push_str("/>");
        return builder;
    }
    builder.push_str(">\n");
    for method in methods {
        if eq_ignore_case(&method.name, "__construct") {
            for argument in &method.arguments {
                builder.push_str("  <argument type=\"service\" id=\"");
                builder.push_str(&html_escape(argument));
                builder.push_str("\"/>\n");
            }
            continue;
        }
        builder.push_str("  <call method=\"");
        builder.push_str(&html_escape(&method.name));
        builder.push_str("\">\n");
        for argument in &method.arguments {
            builder.push_str("    <argument type=\"service\" id=\"");
            builder.push_str(&html_escape(argument));
            builder.push_str("\"/>\n");
        }
        builder.push_str("  </call>\n");
    }
    for tag in tags {
        builder.push_str("  <tag name=\"");
        builder.push_str(&html_escape(tag));
        builder.push_str("\"/>\n");
    }
    builder.push_str("</service>");
    builder
}

fn render_fluent_service_definition(
    class_name: &str,
    service_id: &str,
    class_as_id: bool,
    methods: &[GeneratedServiceMethod],
    tags: &[String],
    php_index: Option<&PhpIndex>,
) -> String {
    let id = if class_as_id {
        format!("\\{class_name}::class")
    } else {
        format!("'{}'", escape_php_single_quoted(service_id))
    };
    let mut first = format!("$services->set({id}");
    if !class_as_id {
        first.push_str(&format!(", \\{class_name}::class"));
    }
    first.push(')');
    let mut lines = vec![first];
    for method in methods {
        if eq_ignore_case(&method.name, "__construct") {
            lines.push("    ->args([".to_owned());
        } else {
            lines.push(format!(
                "    ->call('{}', [",
                escape_php_single_quoted(&method.name)
            ));
        }
        for argument in &method.arguments {
            lines.push(format!(
                "        {},",
                php_service_argument(argument, php_index)
            ));
        }
        lines.push("    ])".to_owned());
    }
    for tag in tags {
        lines.push(format!("    ->tag('{}')", escape_php_single_quoted(tag)));
    }
    if let Some(last) = lines.last_mut() {
        last.push(';');
    }
    lines.join("\n")
}

fn render_php_array_service_definition(
    class_name: &str,
    service_id: &str,
    class_as_id: bool,
    methods: &[GeneratedServiceMethod],
    tags: &[String],
    php_index: Option<&PhpIndex>,
) -> String {
    const CALLS: &str = "    'calls' => [";
    let id = if class_as_id {
        format!("\\{class_name}::class")
    } else {
        format!("'{}'", escape_php_single_quoted(service_id))
    };
    let mut lines = vec![format!("{id} => [")];
    if !class_as_id {
        lines.push(format!("    'class' => \\{class_name}::class,"));
    }
    for method in methods {
        if eq_ignore_case(&method.name, "__construct") {
            lines.push("    'arguments' => [".to_owned());
            for argument in &method.arguments {
                lines.push(format!(
                    "        {},",
                    php_service_argument(argument, php_index)
                ));
            }
            lines.push("    ],".to_owned());
            continue;
        }
        if !lines.iter().any(|line| line == CALLS) {
            lines.push(CALLS.to_owned());
        }
        lines.push(format!(
            "        ['{}', [",
            escape_php_single_quoted(&method.name)
        ));
        for argument in &method.arguments {
            lines.push(format!(
                "            {},",
                php_service_argument(argument, php_index)
            ));
        }
        lines.push("        ]],".to_owned());
    }
    if lines.iter().any(|line| line == CALLS) {
        lines.push("    ],".to_owned());
    }
    if !tags.is_empty() {
        lines.push("    'tags' => [".to_owned());
        for tag in tags {
            lines.push(format!("        '{}',", escape_php_single_quoted(tag)));
        }
        lines.push("    ],".to_owned());
    }
    lines.push("],".to_owned());
    lines.join("\n")
}

fn php_service_argument(value: &str, php_index: Option<&PhpIndex>) -> String {
    if let Some(php_index) = php_index
        && value.contains('\\')
        && php_index.find_class(value).is_some()
    {
        return format!("\\{}::class", value.trim_matches('\\'));
    }
    format!("service('{}')", escape_php_single_quoted(value))
}

pub(crate) fn add_compiler_pass_to_bundle(
    uri: &str,
    source: &str,
    version: i64,
    bundle_class: &str,
    compiler_class: &str,
    compiler_short_name: &str,
) -> Result<String, BoxError> {
    let parsed = swlsp_parser_php::parse(source);
    let root = parsed.root();
    let class = phpquery::classes(&root)
        .into_iter()
        .find(|candidate| {
            candidate.kind() == PHP_CLASS_DECLARATION
                && eq_ignore_case(
                    &php_class_fully_qualified_name(&root, candidate),
                    bundle_class,
                )
        })
        .ok_or_else(|| {
            format!(
                "bundle class {:?} was not found in the document",
                bundle_class
            )
        })?;
    let document = Arc::new(TextDocument::new(uri, source, version as i32));
    let request = CodeActionRequest {
        params: protocol::CodeActionParams::default(),
        syntax: SyntaxContext {
            language: Some(LanguageId::Php),
            document: Some(document),
            root: Some(root.clone()),
            token: None,
            node: None,
        },
    };
    let methods = php_methods_by_name(&class);
    let build = methods.get("build");

    let (mut compiler_qualifier, mut compiler_import) =
        php_class_qualifier(&request, compiler_class);
    if compiler_qualifier.is_empty() {
        return Err("resolve compiler-pass class import".into());
    }
    let mut imports: Vec<String> = Vec::new();
    if compiler_import.is_some() {
        imports.push(compiler_class.to_owned());
    }
    // The generated `build()` method always spells `ContainerBuilder`; the
    // resolved qualifier only decides whether an import is added.
    let container_qualifier = "ContainerBuilder";
    if build.is_none() {
        imports.clear();
        let (inner_container_qualifier, container_import) =
            php_class_qualifier(&request, CONTAINER_BUILDER_CLASS);
        if inner_container_qualifier.is_empty() {
            return Err("resolve ContainerBuilder import".into());
        }
        if eq_ignore_case(&inner_container_qualifier, &compiler_qualifier)
            && !eq_ignore_case(CONTAINER_BUILDER_CLASS, compiler_class)
        {
            compiler_qualifier = format!("\\{compiler_class}");
            compiler_import = None;
            imports.clear();
        }
        if container_import.is_some() {
            imports.push(CONTAINER_BUILDER_CLASS.to_owned());
        }
        if compiler_import.is_some() {
            imports.push(compiler_class.to_owned());
        }
    }

    let mut replacements = Vec::new();
    if !imports.is_empty() {
        imports.sort();
        let offset = php_import_insertion_offset(&root);
        replacements.push(PhpSourceReplacement {
            start: offset,
            end: offset,
            text: php_import_block(&root, &imports),
        });
    }
    match build {
        None => {
            let body = phpquery::class_body(&class).ok_or("bundle class has no body")?;
            let (_, close) = php_block_delimiters(&body);
            let close = close.ok_or("bundle class has no closing brace")?;
            let class_indent = php_line_indentation(source, trimmed_start(&class));
            let member_indent = phpquery::methods(&class).first().map_or_else(
                || format!("{class_indent}    "),
                |method| php_line_indentation(source, trimmed_start(method)).to_owned(),
            );
            let statement_indent = format!(
                "{member_indent}{}",
                php_member_indent_unit(source, Some(&class), &member_indent)
            );
            let method_text = format!(
                "{member_indent}public function build({container_qualifier} $container): void\n\
                 {member_indent}{{\n\
                 {statement_indent}parent::build($container);\n\
                 {statement_indent}$container->addCompilerPass(new {compiler_qualifier}());\n\
                 {member_indent}}}"
            );
            replacements.push(replacement_before_php_close_brace(
                source,
                token_start(&close),
                &method_text,
                class_indent,
                true,
            ));
        }
        Some(build) => {
            let block = phpquery::direct_child(build, PHP_BLOCK);
            let close = block
                .as_ref()
                .and_then(|block| php_block_delimiters(block).1);
            let parameters: Vec<_> = phpquery::parameters(build).collect();
            let (Some(close), Some(first_parameter)) = (close, parameters.first()) else {
                return Err("bundle build method has no writable body".into());
            };
            let container_variable = phpquery::parameter_name(first_parameter);
            if container_variable.is_empty() {
                return Err("bundle build method has no container parameter".into());
            }
            let build_text = lowercase(&build.text().to_string());
            if build_text.contains(&lowercase(&format!("new {compiler_short_name}")))
                || build_text.contains(&lowercase(&format!("new \\{compiler_class}")))
            {
                return Err(
                    format!("compiler pass {compiler_short_name} is already registered").into(),
                );
            }
            let method_indent = php_line_indentation(source, trimmed_start(build));
            let statement_indent = format!(
                "{method_indent}{}",
                php_member_indent_unit(source, Some(&class), method_indent)
            );
            let statement = format!(
                "{statement_indent}{container_variable}->addCompilerPass(new {compiler_qualifier}());"
            );
            replacements.push(replacement_before_php_close_brace(
                source,
                token_start(&close),
                &statement,
                method_indent,
                false,
            ));
        }
    }
    let updated = apply_php_source_replacements(source, &mut replacements)
        .ok_or("apply compiler-pass bundle edits")?;
    let reparsed = swlsp_parser_php::parse(&updated);
    if parsed.errors.is_empty() && !reparsed.errors.is_empty() {
        return Err("generated bundle source is not valid PHP".into());
    }
    Ok(updated)
}

fn replacement_before_php_close_brace(
    source: &str,
    close_offset: u32,
    content: &str,
    close_indent: &str,
    blank_line: bool,
) -> PhpSourceReplacement {
    let bytes = source.as_bytes();
    let mut start = close_offset as usize;
    while start > 0 && bytes[start - 1] != b'\n' && bytes[start - 1] != b'\r' {
        start -= 1;
    }
    let whitespace_only = source[start..close_offset as usize].trim().is_empty();
    let prefix = if blank_line { "\n" } else { "" };
    if whitespace_only {
        return PhpSourceReplacement {
            start: start as u32,
            end: close_offset,
            text: format!("{prefix}{content}\n{close_indent}"),
        };
    }
    PhpSourceReplacement {
        start: close_offset,
        end: close_offset,
        text: format!("\n{content}\n{close_indent}"),
    }
}

fn namespace_of_php_class(class_name: &str) -> &str {
    let class_name = trim_backslash_space(class_name);
    match class_name.rfind('\\') {
        Some(index) => &class_name[..index],
        None => "",
    }
}

fn compiler_pass_template(namespace: &str, class_name: &str) -> String {
    format!(
        "<?php\n\ndeclare(strict_types=1);\n\nnamespace {};\n\nuse Symfony\\Component\\DependencyInjection\\Compiler\\CompilerPassInterface;\nuse Symfony\\Component\\DependencyInjection\\ContainerBuilder;\n\nclass {class_name} implements CompilerPassInterface\n{{\n    public function process(ContainerBuilder $container): void\n    {{\n    }}\n}}\n",
        namespace.trim_matches('\\')
    )
}

#[cfg(test)]
#[path = "tests/symfony_generator.rs"]
mod tests;
