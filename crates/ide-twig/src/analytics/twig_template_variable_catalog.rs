//! Twig template variable catalog.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{CommandFunc, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Visibility};
use swlsp_php_types::{Kind, Type};
use swlsp_twig::{Global, TwigFile, TwigIndexer};
use swlsp_twigcomponent::Index as ComponentIndex;
use swlsp_uriutil::filepath;

use crate::analytics::request::{Field, decode, present};
use crate::analytics::twig_template_usage_catalog::{
    TwigTemplateSourceLocation, template_files_match_glob, template_names_for_path_input,
    unique_twig_files,
};
use crate::analytics::{
    Result, SourceLineResolver, check, errorf, is_zero, optional_file_uri, range_start,
};
use swlsp_ide_symfony::analytics::profiler::utf8_lossy_per_byte;
use swlsp_textutil::lowercase;

pub const LIST_TWIG_TEMPLATE_VARIABLES_COMMAND: &str =
    "shopware/symfony/analytics/twig/templateVariables";

const TEMPLATE_REQUIRED: &str = "at least one of template or fileGlob is required";

#[derive(Clone)]
pub struct TwigTemplateVariableCatalogProvider {
    root: String,
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
    components: Option<Arc<ComponentIndex>>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigTemplateVariableCatalogRequest {
    pub template: String,
    pub file_glob: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigTemplateVariableSource {
    pub kind: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub line: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigTemplateVariableProperty {
    pub name: String,
    pub kind: String,
    #[serde(rename = "type", skip_serializing_if = "String::is_empty")]
    pub ty: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub source_line: i64,
    #[serde(skip_serializing_if = "is_zero")]
    pub deprecated: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct TwigTemplateVariableEntry {
    pub name: String,
    #[serde(rename = "type")]
    pub ty: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub types: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub properties: Vec<TwigTemplateVariableProperty>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub sources: Vec<TwigTemplateVariableSource>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct TwigTemplateVariableCatalogEntry {
    pub template: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub files: Vec<TwigTemplateSourceLocation>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub variables: Vec<TwigTemplateVariableEntry>,
}

struct Accumulator {
    name: String,
    types: HashMap<String, String>,
    sources: Vec<TwigTemplateVariableSource>,
}

#[derive(Default)]
struct Variables {
    values: HashMap<String, Accumulator>,
}

impl Variables {
    fn add(&mut self, name: &str, type_name: &str, source: TwigTemplateVariableSource) {
        let name = name.trim();
        let name = name.strip_prefix('$').unwrap_or(name);
        if name.is_empty() {
            return;
        }
        let current = self
            .values
            .entry(lowercase(name))
            .or_insert_with(|| Accumulator {
                name: name.to_owned(),
                types: HashMap::new(),
                sources: Vec::new(),
            });
        add_variable_types(&mut current.types, type_name);
        current.sources.push(source);
    }
}

impl TwigTemplateVariableCatalogProvider {
    pub fn new(
        root: &str,
        twig: Option<Arc<TwigIndexer>>,
        php: Option<PhpIndex>,
        components: Option<Arc<ComponentIndex>>,
    ) -> Self {
        Self {
            root: filepath::clean(root),
            twig,
            php,
            components,
        }
    }

    fn list(&self, ctx: &Context, args: Option<&Value>) -> Result<Value> {
        let Some(value) = present(args) else {
            return Err(errorf!("{TEMPLATE_REQUIRED}"));
        };
        let members = decode(
            value,
            &[("template", Field::String), ("fileGlob", Field::String)],
        )
        .map_err(|error| errorf!("invalid twig template variable request: {error}"))?;
        let request = TwigTemplateVariableCatalogRequest {
            template: members.string("template"),
            file_glob: members.string("fileGlob"),
        };
        Ok(serde_json::to_value(self.catalog(ctx, &request)?)?)
    }

    pub fn catalog(
        &self,
        ctx: &Context,
        request: &TwigTemplateVariableCatalogRequest,
    ) -> Result<Vec<TwigTemplateVariableCatalogEntry>> {
        let (Some(twig), Some(php)) = (&self.twig, &self.php) else {
            return Err(errorf!("twig template variable catalog is unavailable"));
        };
        check(ctx)?;
        if request.template.trim().is_empty() && request.file_glob.trim().is_empty() {
            return Err(errorf!("{TEMPLATE_REQUIRED}"));
        }
        let names = self.resolve_template_variable_names(twig, request)?;
        let globals = twig.get_all_globals()?;
        let mut lines = SourceLineResolver::default();
        let mut result = Vec::with_capacity(names.len());
        for name in names {
            check(ctx)?;
            let files = unique_twig_files(twig.get_twig_files_by_rel_path(&name)?);
            result.push(self.variable_entry(twig, php, &name, &files, &globals, &mut lines)?);
        }
        Ok(result)
    }

    fn resolve_template_variable_names(
        &self,
        twig: &TwigIndexer,
        request: &TwigTemplateVariableCatalogRequest,
    ) -> Result<Vec<String>> {
        let mut selected: HashSet<String> = HashSet::new();
        for input in request.template.split(',') {
            let input = input.trim();
            if input.is_empty() {
                continue;
            }
            if !twig.get_twig_files_by_rel_path(input)?.is_empty() {
                selected.insert(input.to_owned());
            }
            selected.extend(template_names_for_path_input(&self.root, twig, input));
        }
        let file_glob = filepath::to_slash(request.file_glob.trim());
        if !file_glob.is_empty() {
            for name in twig.get_all_template_files()? {
                let files = unique_twig_files(twig.get_twig_files_by_rel_path(&name)?);
                if template_files_match_glob(&self.root, &files, &file_glob) {
                    selected.insert(name);
                }
            }
        }
        let mut result: Vec<String> = selected.into_iter().collect();
        result.sort_by_cached_key(|name| lowercase(name));
        Ok(result)
    }

    fn variable_entry(
        &self,
        twig: &TwigIndexer,
        php: &PhpIndex,
        template: &str,
        files: &[TwigFile],
        globals: &[Global],
        lines: &mut SourceLineResolver,
    ) -> Result<TwigTemplateVariableCatalogEntry> {
        let mut entry = TwigTemplateVariableCatalogEntry {
            template: template.to_owned(),
            ..TwigTemplateVariableCatalogEntry::default()
        };
        let mut values = Variables::default();
        for file in files {
            entry.files.push(TwigTemplateSourceLocation {
                file_uri: swlsp_uriutil::file_uri(&file.path),
                line: 1,
            });
        }
        for variable in twig.get_template_variables(&[template])? {
            let line = lines.line(&variable.file_path, range_start(variable.range));
            values.add(
                &variable.name,
                "",
                source("templateInput", &variable.file_path, line),
            );
        }
        for variable in php.twig_template_variables(&[template])? {
            let line = lines.line(&variable.file, variable.range.start);
            values.add(
                &variable.name,
                &variable.ty,
                source("controller", &variable.file, line),
            );
        }
        for global in globals {
            let line = lines.line(&global.file, range_start(global.range));
            values.add(
                &global.name,
                &global.ty,
                source("global", &global.file, line),
            );
        }
        for file in files {
            let mut annotations: Vec<(String, String)> =
                twig_template_annotations(&file.path)?.into_iter().collect();
            annotations.sort();
            for (name, type_name) in annotations {
                values.add(
                    &name,
                    &type_name,
                    TwigTemplateVariableSource {
                        kind: "annotation".to_owned(),
                        file_uri: swlsp_uriutil::file_uri(&file.path),
                        line: 1,
                    },
                );
            }
            let Some(components) = &self.components else {
                continue;
            };
            let (_, props) = components.context_for_template(&file.path, None)?;
            for prop in props {
                let line = lines.line(&prop.file, range_start(prop.range));
                values.add(
                    &prop.name,
                    &prop.ty,
                    source("componentProp", &prop.file, line),
                );
            }
        }

        for value in values.values.into_values() {
            let type_names = sorted_variable_types(&value.types);
            let mut type_name = type_names.join("|");
            if type_name.is_empty() {
                type_name = "unknown".to_owned();
            }
            entry.variables.push(TwigTemplateVariableEntry {
                name: value.name,
                ty: type_name,
                properties: variable_properties(php, &type_names, lines),
                types: type_names,
                sources: unique_variable_sources(value.sources),
            });
        }
        entry
            .variables
            .sort_by_cached_key(|variable| lowercase(&variable.name));
        Ok(entry)
    }
}

impl CommandProvider for TwigTemplateVariableCatalogProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let command: CommandFunc = Arc::new(move |ctx, args| provider.list(ctx, args));
        BTreeMap::from([(LIST_TWIG_TEMPLATE_VARIABLES_COMMAND.to_owned(), command)])
    }
}

fn source(kind: &str, path: &str, line: i64) -> TwigTemplateVariableSource {
    TwigTemplateVariableSource {
        kind: kind.to_owned(),
        file_uri: optional_file_uri(path),
        line,
    }
}

fn twig_template_annotations(path: &str) -> Result<HashMap<String, String>> {
    let source = std::fs::read(path).map_err(|error| errorf!("open {path}: {error}"))?;
    let parsed = swlsp_parser_twig::parse(&utf8_lossy_per_byte(&source));
    Ok(swlsp_twig::twig_type_annotations(&parsed.tree.root())
        .into_iter()
        .map(|(name, value)| (name, value.to_string()))
        .collect())
}

fn add_variable_types(target: &mut HashMap<String, String>, value: &str) {
    if let Ok(parsed) = swlsp_php_types::parse(value.trim())
        && !parsed.is_unknown()
    {
        let mut members = Vec::new();
        flatten_variable_types(&parsed, &mut members);
        for member in members {
            let rendered = member.to_string();
            if !rendered.is_empty() && rendered != "unknown" {
                target.insert(lowercase(&rendered), rendered);
            }
        }
        return;
    }
    for member in value.split('|') {
        let member = member.trim();
        if !member.is_empty() && !swlsp_textutil::eq_ignore_case(member, "unknown") {
            target.insert(lowercase(member), member.to_owned());
        }
    }
}

fn flatten_variable_types(value: &Type, out: &mut Vec<Type>) {
    if value.kind() == Kind::Union {
        for argument in value.arguments() {
            flatten_variable_types(argument, out);
        }
        return;
    }
    out.push(value.clone());
}

fn sorted_variable_types(values: &HashMap<String, String>) -> Vec<String> {
    let mut result: Vec<String> = values.values().cloned().collect();
    result.sort_by_cached_key(|value| lowercase(value));
    result
}

fn variable_properties(
    php: &PhpIndex,
    type_names: &[String],
    lines: &mut SourceLineResolver,
) -> Vec<TwigTemplateVariableProperty> {
    // Classes sharing a member name resolve in the order of their first
    // appearance in the type, keeping the result deterministic.
    let mut classes: Vec<(String, String)> = Vec::new();
    let mut shapes = Vec::new();
    for type_name in type_names {
        if let Ok(value) = swlsp_php_types::parse(type_name) {
            collect_variable_classes(&value, &mut classes, &mut shapes);
        }
    }
    let mut entries: HashMap<String, TwigTemplateVariableProperty> = HashMap::new();
    for shape in &shapes {
        for field in shape.fields() {
            entries.insert(
                lowercase(&field.name),
                TwigTemplateVariableProperty {
                    name: field.name.to_string(),
                    kind: "shapeField".to_owned(),
                    ty: field.ty.to_string(),
                    ..TwigTemplateVariableProperty::default()
                },
            );
        }
    }
    for (_, class_name) in &classes {
        for property in php.properties(class_name) {
            if property.visibility != Visibility::Public || property.flags.has(Flags::STATIC) {
                continue;
            }
            let name = property.name.strip_prefix('$').unwrap_or(&property.name);
            entries.insert(
                lowercase(name),
                TwigTemplateVariableProperty {
                    name: name.to_owned(),
                    kind: "property".to_owned(),
                    ty: property.ty.to_string(),
                    class: class_name.clone(),
                    file_uri: optional_file_uri(&property.path),
                    source_line: lines.line(&property.path, range_start(property.selection_range)),
                    deprecated: property.flags.has(Flags::DEPRECATED),
                },
            );
        }
        for method in php.methods(class_name) {
            if method.visibility != Visibility::Public || method.flags.has(Flags::STATIC) {
                continue;
            }
            let lowered = lowercase(&method.name);
            if lowered.starts_with("set") || lowered.starts_with("__") {
                continue;
            }
            let mut name = swlsp_twig::twig_attribute_name(&method.name);
            let mut kind = "getter";
            if name.is_empty() {
                name = method.name.to_string();
                kind = "method";
            }
            let key = lowercase(&name);
            if entries.contains_key(&key) {
                continue;
            }
            entries.insert(
                key,
                TwigTemplateVariableProperty {
                    name,
                    kind: kind.to_owned(),
                    ty: method.return_type.to_string(),
                    class: class_name.clone(),
                    file_uri: optional_file_uri(&method.path),
                    source_line: lines.line(&method.path, range_start(method.selection_range)),
                    deprecated: method.flags.has(Flags::DEPRECATED),
                },
            );
        }
    }
    let mut result: Vec<TwigTemplateVariableProperty> = entries.into_values().collect();
    result.sort_by_cached_key(|entry| lowercase(&entry.name));
    result
}

fn collect_variable_classes(
    value: &Type,
    classes: &mut Vec<(String, String)>,
    shapes: &mut Vec<Type>,
) {
    match value.kind() {
        Kind::Object => {
            if !value.name().is_empty() {
                let key = lowercase(value.name());
                match classes.iter_mut().find(|(existing, _)| *existing == key) {
                    Some((_, name)) => *name = value.name().to_owned(),
                    None => classes.push((key, value.name().to_owned())),
                }
            }
        }
        Kind::ObjectShape | Kind::ArrayShape => shapes.push(value.clone()),
        _ => {}
    }
    for argument in value.arguments() {
        collect_variable_classes(argument, classes, shapes);
    }
}

fn unique_variable_sources(
    values: Vec<TwigTemplateVariableSource>,
) -> Vec<TwigTemplateVariableSource> {
    let mut seen = HashSet::with_capacity(values.len());
    let mut result: Vec<_> = values
        .into_iter()
        .filter(|value| {
            seen.insert(format!(
                "{}\x00{}\x00{}",
                value.kind, value.file_uri, value.line
            ))
        })
        .collect();
    result.sort_by(|left, right| {
        left.kind
            .cmp(&right.kind)
            .then_with(|| left.file_uri.cmp(&right.file_uri))
            .then(left.line.cmp(&right.line))
    });
    result
}
