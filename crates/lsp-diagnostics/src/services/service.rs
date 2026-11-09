//! Missing and deprecated service, parameter and class references.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_phpanalysis::State;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax::{XML_ATTRIBUTE, XML_TEXT};
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind};
use swlsp_php_types::Type;
use swlsp_symfony::{Parameter, PhpConfigReferenceKind, Service, ServiceIndex};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::lowercase;

use super::{deprecated_tags, lower_ext, problem, similar_value};
use crate::LookupError;
use crate::common::value_node_text_range;
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

const MISSING_SERVICE_CODE: &str = "symfony.service.missing";
const MISSING_PARAMETER_CODE: &str = "symfony.parameter.missing";
const MISSING_CLASS_CODE: &str = "symfony.class.missing";
const DEPRECATED_SERVICE_CODE: &str = "symfony.service.deprecated";
const DEPRECATED_CLASS_CODE: &str = "symfony.class.deprecated";

const CONTAINER_TYPES: &[&str] = &[
    "Psr\\Container\\ContainerInterface",
    "Symfony\\Component\\DependencyInjection\\ContainerInterface",
    "Symfony\\Contracts\\Service\\ServiceProviderInterface",
];

pub struct ServiceAnalyzer {
    service_index: Option<Arc<ServiceIndex>>,
    php_index: Option<PhpIndex>,
}

impl ServiceAnalyzer {
    pub fn new(service_index: Option<Arc<ServiceIndex>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            service_index,
            php_index,
        }
    }
}

impl DeclaredAnalyzer for ServiceAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.service",
        description: "References to services, parameters and classes that do not exist or are deprecated.",
        domain: "symfony.services",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php, LanguageId::Xml, LanguageId::Yaml],
        source: "symfony",
        codes: &[
            DEPRECATED_CLASS_CODE,
            MISSING_CLASS_CODE,
            MISSING_PARAMETER_CODE,
            DEPRECATED_SERVICE_CODE,
            MISSING_SERVICE_CODE,
        ],
    };
}

impl ProblemAnalyzer for ServiceAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.service_index) + crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(services), Some(php)) = (&self.service_index, &self.php_index) else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let mut collector = Collector {
            ctx,
            document,
            services,
            php,
            diagnostics: Vec::new(),
            seen: HashSet::new(),
            local_services: HashMap::new(),
            local_parameters: HashSet::new(),
            analysis: None,
            service_names: None,
            parameter_names: None,
            class_names: None,
        };
        match lower_ext(&document.uri).as_str() {
            ".php" => collector.collect_php(&root)?,
            ".xml" => collector.collect_xml(&root)?,
            ".yaml" | ".yml" => collector.collect_yaml(&root)?,
            _ => return Ok(Vec::new()),
        }
        Ok(collector.diagnostics)
    }
}

struct Collector<'a> {
    ctx: &'a Context,
    document: &'a TextDocument,
    services: &'a ServiceIndex,
    php: &'a PhpIndex,
    diagnostics: Vec<Problem>,
    seen: HashSet<(u32, u32, &'static str, String)>,
    local_services: HashMap<String, Service>,
    local_parameters: HashSet<String>,
    analysis: Option<Arc<State>>,
    service_names: Option<Vec<String>>,
    parameter_names: Option<Vec<String>>,
    class_names: Option<Arc<[String]>>,
}

impl Collector<'_> {
    fn collect_php(&mut self, root: &SyntaxNode) -> Result<(), BoxError> {
        let config_file = self.document.source().contains("ContainerConfigurator");
        let assistant_context =
            swlsp_lsp_phpanalysis::context_for_document(self.php, self.document)?;
        for literal in phpquery::nodes(root, &[PHP_STRING]) {
            if self.ctx.is_cancelled() {
                return Ok(());
            }
            let value = phpquery::string_value(&literal);
            if value.is_empty() {
                continue;
            }
            if let Some((_, tags)) = assistant_context.as_ref().and_then(|context| {
                swlsp_php::assistant_argument_tags(
                    context,
                    &literal,
                    &[
                        "Service",
                        "Parameter",
                        "Class",
                        "Interface",
                        "ClassInterface",
                    ],
                )
            }) && !tags.is_empty()
            {
                for tag in &tags {
                    match tag.as_str() {
                        "Service" => self.check_service(&literal, &value)?,
                        "Parameter" => self.check_parameter(&literal, &value)?,
                        "Class" | "Interface" | "ClassInterface" => {
                            self.check_assistant_class(&literal, &value, tag);
                        }
                        _ => {}
                    }
                }
                continue;
            }

            let mut kind = swlsp_symfony::php_array_service_reference_at(&literal);
            if kind == PhpConfigReferenceKind::None {
                kind = swlsp_symfony::php_attribute_reference_at(&literal);
            }
            if kind == PhpConfigReferenceKind::None
                && swlsp_symfony::php_array_service_context_at(&literal)
            {
                kind = swlsp_symfony::php_config_reference_at(&literal);
            }
            if kind != PhpConfigReferenceKind::None {
                self.check_php_value(&literal, &value, kind)?;
                continue;
            }
            if phpquery::string_argument_index(&literal) == Some(0)
                && self.is_typed_parameter_read_call(&literal)
            {
                self.check_parameter(&literal, &value)?;
                continue;
            }
            if config_file {
                let kind = swlsp_symfony::php_config_reference_at(&literal);
                if kind != PhpConfigReferenceKind::None {
                    self.check_php_value(&literal, &value, kind)?;
                    continue;
                }
            }
            if phpquery::string_in_call(&literal, 0, &["get", "has"])
                && self.is_typed_container_call(&literal)
            {
                self.check_service(&literal, &value)?;
            }
        }
        Ok(())
    }

    fn check_php_value(
        &mut self,
        node: &SyntaxNode,
        value: &str,
        kind: PhpConfigReferenceKind,
    ) -> Result<(), BoxError> {
        match kind {
            PhpConfigReferenceKind::Service => self.check_service(node, value)?,
            PhpConfigReferenceKind::Parameter => {
                let mut references = swlsp_symfony::parameter_references(value);
                if references.is_empty() {
                    references = vec![value.trim().to_owned()];
                }
                for name in references {
                    self.check_parameter(node, &name)?;
                }
            }
            PhpConfigReferenceKind::Class => {
                self.check_class(node, &value.replace("\\\\", "\\"));
            }
            _ => {}
        }
        Ok(())
    }

    fn is_typed_container_call(&mut self, node: &SyntaxNode) -> bool {
        if self.is_typed_parameter_read_call(node) {
            return false;
        }
        self.is_typed_receiver_subtype(node, CONTAINER_TYPES)
    }

    fn is_typed_parameter_read_call(&mut self, node: &SyntaxNode) -> bool {
        let method = phpquery::call_method_name(node);
        let targets = swlsp_symfony::php_parameter_read_access_types(&method);
        self.is_typed_receiver_subtype(node, &targets)
    }

    fn is_typed_receiver_subtype(&mut self, node: &SyntaxNode, targets: &[&str]) -> bool {
        let Some(receiver) = phpquery::call_receiver(node) else {
            return false;
        };
        if self.analysis.is_none() {
            match swlsp_lsp_phpanalysis::for_document(self.php, self.document) {
                Ok(Some(state)) => self.analysis = Some(state),
                _ => return false,
            }
        }
        let Some(state) = &self.analysis else {
            return false;
        };
        let receiver_type = state.document.type_of(&receiver).ty;
        if receiver_type.is_unknown() {
            return false;
        }
        let relations = state.snapshot.relations();
        targets
            .iter()
            .any(|target| relations.is_subtype(&receiver_type, &Type::named(target, Vec::new())))
    }

    fn collect_xml(&mut self, root: &SyntaxNode) -> Result<(), BoxError> {
        let (services, parameters) = swlsp_symfony::parse_xml_services_tree(
            "",
            self.document.tree(),
            self.document.line_index(),
        );
        self.add_local_references(services, &parameters);
        for attribute in xmlquery::nodes(root, &[XML_ATTRIBUTE]) {
            if self.ctx.is_cancelled() {
                return Ok(());
            }
            if let Some(name) = swlsp_symfony::xml_service_reference_name(&attribute) {
                let value = if swlsp_symfony::xml_service_reference_optional(&attribute) {
                    format!("@?{name}")
                } else {
                    name
                };
                self.check_service(&attribute, &value)?;
            }
            if let Some(name) = swlsp_symfony::xml_class_reference_name(&attribute) {
                self.check_class(&attribute, &name);
            }
            self.check_parameters(&attribute, &xmlquery::node_value(&attribute))?;
        }
        for text in xmlquery::nodes(root, &[XML_TEXT]) {
            self.check_parameters(&text, &xmlquery::node_value(&text))?;
        }
        Ok(())
    }

    fn collect_yaml(&mut self, root: &SyntaxNode) -> Result<(), BoxError> {
        let (services, parameters) = swlsp_symfony::parse_yaml_services_tree(
            "",
            self.document.tree(),
            self.document.line_index(),
        );
        self.add_local_references(services, &parameters);
        for scalar in yamlquery::nodes(root, &[YAML_SCALAR]) {
            if self.ctx.is_cancelled() {
                return Ok(());
            }
            let mut value = yamlquery::scalar_value(&scalar);
            if let Some(name) = swlsp_symfony::yaml_service_reference_name(&scalar) {
                if swlsp_symfony::parse_service_reference(&value).is_none() {
                    value = name;
                }
                self.check_service(&scalar, &value)?;
            }
            if let Some(name) = swlsp_symfony::yaml_class_reference_name(&scalar) {
                self.check_class(&scalar, &name);
            }
            self.check_parameters(&scalar, &value)?;
        }
        Ok(())
    }

    fn check_service(&mut self, node: &SyntaxNode, value: &str) -> Result<(), BoxError> {
        let (name, optional) = match swlsp_symfony::parse_service_reference(value) {
            Some(parsed) => parsed,
            None => (
                value.strip_prefix('@').unwrap_or(value).trim().to_owned(),
                false,
            ),
        };
        if name.is_empty() || name.contains(['%', '$', '{', '}']) {
            return Ok(());
        }
        if let Some(service) = self.local_services.get(&name).cloned() {
            self.check_service_deprecations(node, &service);
            return Ok(());
        }
        let service = self
            .services
            .get_service_by_id(&name)
            .map_err(|error| LookupError::new(format!("query Symfony service {name:?}"), error))?;
        if let Some(service) = service {
            self.check_service_deprecations(node, &service);
            return Ok(());
        }
        if optional || name == ".inner" || name.ends_with(".inner") {
            return Ok(());
        }
        let candidates = self.all_service_names()?;
        let suggestions = similar_value(&name, candidates);
        self.add(
            node,
            MISSING_SERVICE_CODE,
            &name,
            format!("Service '{name}' not found"),
            suggestions,
        );
        Ok(())
    }

    fn check_service_deprecations(&mut self, node: &SyntaxNode, service: &Service) {
        self.check_deprecated_service(node, service);
        let class_name = self.service_class(service);
        if class_name.is_empty() {
            return;
        }
        let Some(symbol) = self.php.find_class(&class_name) else {
            return;
        };
        if !symbol.flags.has(Flags::DEPRECATED) {
            return;
        }
        self.add_deprecated(
            node,
            DEPRECATED_CLASS_CODE,
            &service.id,
            format!("Class '{class_name}' is deprecated"),
        );
    }

    fn service_class(&self, service: &Service) -> String {
        let mut service = service.clone();
        let mut seen = HashSet::new();
        for _ in 0..16 {
            let class = service.class.trim();
            let class = class.strip_prefix('\\').unwrap_or(class);
            if !class.is_empty() {
                return class.to_owned();
            }
            let target = service.alias_target.trim().to_owned();
            if target.is_empty() || !seen.insert(target.clone()) {
                return String::new();
            }
            if let Some(local) = self.local_services.get(&target) {
                service = local.clone();
                continue;
            }
            match self.services.get_service_by_id(&target) {
                Ok(Some(indexed)) => service = indexed,
                _ => return String::new(),
            }
        }
        String::new()
    }

    fn check_deprecated_service(&mut self, node: &SyntaxNode, service: &Service) {
        if !service.deprecated {
            return;
        }
        let mut message = format!("Service '{}' is deprecated", service.id);
        if !service.deprecation.is_empty() {
            let detail = service
                .deprecation
                .replace("%service_id%", &service.id)
                .replace("%alias_id%", &service.id);
            message.push_str(": ");
            message.push_str(&detail);
        }
        self.add_deprecated(node, DEPRECATED_SERVICE_CODE, &service.id, message);
    }

    fn check_parameters(&mut self, node: &SyntaxNode, value: &str) -> Result<(), BoxError> {
        for name in swlsp_symfony::parameter_references(value) {
            self.check_parameter(node, &name)?;
        }
        Ok(())
    }

    fn check_parameter(&mut self, node: &SyntaxNode, name: &str) -> Result<(), BoxError> {
        let name = name.trim_matches('%').trim();
        if name.is_empty() || name.contains(['$', '{', '}']) {
            return Ok(());
        }
        if self.local_parameters.contains(&lowercase(name)) {
            return Ok(());
        }
        let found = self.services.get_parameter_by_name(name).map_err(|error| {
            LookupError::new(format!("query Symfony parameter {name:?}"), error)
        })?;
        if found.is_none() {
            let candidates = self.all_parameter_names()?;
            let suggestions = similar_value(name, candidates);
            self.add(
                node,
                MISSING_PARAMETER_CODE,
                name,
                format!("Parameter '{name}' not found"),
                suggestions,
            );
        }
        Ok(())
    }

    fn check_class(&mut self, node: &SyntaxNode, name: &str) {
        self.check_class_kinds(node, name, "Class", &[]);
    }

    fn check_assistant_class(&mut self, node: &SyntaxNode, name: &str, tag: &str) {
        let (label, kinds): (&str, &[SymbolKind]) = match tag {
            "Class" => ("Class", &[SymbolKind::Class]),
            "Interface" => ("Interface", &[SymbolKind::Interface]),
            "ClassInterface" => (
                "Class or interface",
                &[SymbolKind::Class, SymbolKind::Interface],
            ),
            _ => ("Class", &[]),
        };
        self.check_class_kinds(node, name, label, kinds);
    }

    fn check_class_kinds(
        &mut self,
        node: &SyntaxNode,
        name: &str,
        label: &str,
        allowed: &[SymbolKind],
    ) {
        let name = name.trim();
        let name = name.strip_prefix('\\').unwrap_or(name);
        if name.is_empty() {
            return;
        }
        let mut symbol: Option<Symbol> = None;
        for candidate in self.php.semantic_snapshot().classes(name) {
            if !allowed.is_empty() && !allowed.contains(&candidate.kind) {
                continue;
            }
            let replace = match &symbol {
                None => true,
                Some(current) => {
                    current.flags.has(Flags::INTERNAL) && !candidate.flags.has(Flags::INTERNAL)
                }
            };
            if replace {
                symbol = Some(candidate);
            }
        }
        let Some(symbol) = symbol else {
            let suggestions = if allowed.is_empty() {
                let php = self.php;
                let names = self
                    .class_names
                    .get_or_insert_with(|| php.class_names_view())
                    .clone();
                similar_value(name, &names)
            } else {
                let mut seen = HashSet::new();
                let mut candidates = Vec::new();
                for candidate in self.php.class_symbols_view().iter() {
                    if !allowed.contains(&candidate.kind) {
                        continue;
                    }
                    let fqn = candidate.fully_qualified.as_str();
                    let fqn = fqn.strip_prefix('\\').unwrap_or(fqn);
                    if fqn.is_empty() || !seen.insert(lowercase(fqn)) {
                        continue;
                    }
                    candidates.push(fqn.to_owned());
                }
                similar_value(name, &candidates)
            };
            self.add(
                node,
                MISSING_CLASS_CODE,
                name,
                format!("{label} '{name}' not found"),
                suggestions,
            );
            return;
        };
        if symbol.flags.has(Flags::DEPRECATED) {
            self.add_deprecated(
                node,
                DEPRECATED_CLASS_CODE,
                name,
                format!("Class '{name}' is deprecated"),
            );
        }
    }

    fn all_service_names(&mut self) -> Result<&[String], BoxError> {
        if self.service_names.is_none() {
            let names = self
                .services
                .get_all_services()
                .map_err(|error| LookupError::new("query Symfony services", error))?;
            self.service_names = Some(names);
        }
        Ok(self.service_names.as_deref().unwrap_or_default())
    }

    fn all_parameter_names(&mut self) -> Result<&[String], BoxError> {
        if self.parameter_names.is_none() {
            let parameters = self
                .services
                .get_all_parameters()
                .map_err(|error| LookupError::new("query Symfony parameters", error))?;
            self.parameter_names = Some(parameters.into_iter().map(|p| p.name).collect());
        }
        Ok(self.parameter_names.as_deref().unwrap_or_default())
    }

    fn add_local_references(&mut self, services: Vec<Service>, parameters: &[Parameter]) {
        for service in services {
            self.local_services.insert(service.id.clone(), service);
        }
        for parameter in parameters {
            self.local_parameters.insert(lowercase(&parameter.name));
        }
    }

    fn mark_seen(&mut self, node: &SyntaxNode, code: &'static str, name: &str) -> bool {
        let range = node.range_trimmed_trivia();
        self.seen.insert((
            u32::from(range.start()),
            u32::from(range.end()),
            code,
            lowercase(name),
        ))
    }

    fn add_deprecated(
        &mut self,
        node: &SyntaxNode,
        code: &'static str,
        name: &str,
        message: String,
    ) {
        if !self.mark_seen(node, code, name) {
            return;
        }
        let mut problem = problem(
            code,
            value_node_text_range(Some(node), name),
            message,
            protocol::DiagnosticSeverity::HINT,
        );
        problem.tags = deprecated_tags();
        self.diagnostics.push(problem);
    }

    fn add(
        &mut self,
        node: &SyntaxNode,
        code: &'static str,
        name: &str,
        message: String,
        suggestions: serde_json::Value,
    ) {
        if !self.mark_seen(node, code, name) {
            return;
        }
        let mut problem = problem(
            code,
            value_node_text_range(Some(node), name),
            message,
            protocol::DiagnosticSeverity::ERROR,
        );
        problem.payload = Some(json!({
            "referenceName": name,
            "suggestions": suggestions,
        }));
        self.diagnostics.push(problem);
    }
}
