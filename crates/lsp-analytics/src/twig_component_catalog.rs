//! Symfony UX Twig component catalog (port of
//! `twig_component_catalog.go`).

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{CommandFunc, CommandProvider, Context};
use swlsp_twigcomponent::{Component, Index, Prop, SourceKind};

use crate::request::{Field, decode, present};
use crate::{
    Result, SourceLineResolver, check, errorf, is_zero, lower, optional_file_uri, range_len,
    range_start, trim_space,
};

pub const LIST_TWIG_COMPONENTS_COMMAND: &str = "shopware/symfony/analytics/twig/components";

#[derive(Clone)]
pub struct TwigComponentCatalogProvider {
    index: Option<Arc<Index>>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigComponentCatalogRequest {
    pub search: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigComponentDeclarationEntry {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub template: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub template_from_method: String,
    pub source: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub source_line: i64,
    #[serde(skip_serializing_if = "is_zero")]
    pub live: bool,
    #[serde(skip_serializing_if = "is_zero")]
    pub expose_public_props: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigComponentTemplateEntry {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub template: String,
    pub file_uri: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigComponentPropEntry {
    pub name: String,
    #[serde(rename = "type", skip_serializing_if = "String::is_empty")]
    pub ty: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub default_value: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub description: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub class: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub member: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub source_line: i64,
    #[serde(skip_serializing_if = "is_zero")]
    pub live: bool,
    #[serde(skip_serializing_if = "is_zero")]
    pub writable: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigComponentBlockEntry {
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub line: i64,
    pub print: String,
    pub compose: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigComponentUsageEntry {
    pub syntax: String,
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub line: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct TwigComponentSyntaxEntry {
    #[serde(rename = "htmlTag")]
    pub html_tag: String,
    pub function: String,
    pub composition: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct TwigComponentCatalogEntry {
    pub name: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub declarations: Vec<TwigComponentDeclarationEntry>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub templates: Vec<TwigComponentTemplateEntry>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub props: Vec<TwigComponentPropEntry>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub computed: Vec<TwigComponentPropEntry>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub blocks: Vec<TwigComponentBlockEntry>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub usages: Vec<TwigComponentUsageEntry>,
    pub syntax: TwigComponentSyntaxEntry,
}

impl TwigComponentCatalogProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }

    fn list(&self, ctx: &Context, args: Option<&Value>) -> Result<Value> {
        let request = match present(args) {
            Some(value) => {
                let members = decode(
                    value,
                    "TwigComponentCatalogRequest",
                    &[("search", Field::String)],
                )
                .map_err(|error| errorf!("invalid twig component catalog request: {error}"))?;
                TwigComponentCatalogRequest {
                    search: members.string("search"),
                }
            }
            None => TwigComponentCatalogRequest::default(),
        };
        Ok(serde_json::to_value(self.catalog(ctx, &request)?)?)
    }

    pub fn catalog(
        &self,
        ctx: &Context,
        request: &TwigComponentCatalogRequest,
    ) -> Result<Vec<TwigComponentCatalogEntry>> {
        let Some(index) = &self.index else {
            return Err(errorf!("twig component catalog is unavailable"));
        };
        check(ctx)?;
        let search = lower(trim_space(&request.search));
        let mut grouped: HashMap<String, Vec<Component>> = HashMap::new();
        let mut names: HashMap<String, String> = HashMap::new();
        for component in index.components()? {
            if component.name.is_empty()
                || (!search.is_empty() && !lower(&component.name).contains(&search))
            {
                continue;
            }
            let key = lower(&component.name);
            names
                .entry(key.clone())
                .or_insert_with(|| component.name.clone());
            grouped.entry(key).or_default().push(component);
        }
        let mut lines = SourceLineResolver::default();
        let mut result = Vec::with_capacity(grouped.len());
        for (key, declarations) in grouped {
            check(ctx)?;
            result.push(catalog_entry(
                index,
                &names[&key],
                &declarations,
                &mut lines,
            )?);
        }
        result.sort_by_cached_key(|entry| lower(&entry.name));
        Ok(result)
    }
}

impl CommandProvider for TwigComponentCatalogProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let command: CommandFunc = Arc::new(move |ctx, args| provider.list(ctx, args));
        BTreeMap::from([(LIST_TWIG_COMPONENTS_COMMAND.to_owned(), command)])
    }
}

fn catalog_entry(
    index: &Index,
    name: &str,
    components: &[Component],
    lines: &mut SourceLineResolver,
) -> Result<TwigComponentCatalogEntry> {
    let literal = name.replace('\'', "\\'");
    let mut entry = TwigComponentCatalogEntry {
        name: name.to_owned(),
        syntax: TwigComponentSyntaxEntry {
            html_tag: format!("<twig:{name}></twig:{name}>"),
            function: format!("{{{{ component('{literal}') }}}}"),
            composition: String::new(),
        },
        ..TwigComponentCatalogEntry::default()
    };
    for component in components {
        let mut declaration = TwigComponentDeclarationEntry {
            class: component.class.clone(),
            template: component.template.clone(),
            template_from_method: component.template_from_method.clone(),
            source: component_source(component.source).to_owned(),
            live: component.live,
            expose_public_props: component.expose_public_props,
            ..TwigComponentDeclarationEntry::default()
        };
        if !component.file.is_empty() {
            declaration.file_uri = swlsp_uriutil::file_uri(&component.file);
            let offset = if range_len(component.name_range) == 0 {
                range_start(component.class_range)
            } else {
                range_start(component.name_range)
            };
            declaration.source_line = lines.line(&component.file, offset);
        }
        entry.declarations.push(declaration);
        for file in index.template_files(component)? {
            entry.templates.push(TwigComponentTemplateEntry {
                template: component.template.clone(),
                file_uri: swlsp_uriutil::file_uri(&file),
            });
        }
    }
    entry.props = props(&index.props(name)?, lines);
    entry.computed = props(&index.computed(name)?, lines);
    let mut composition = format!("{{% component '{literal}' %}}");
    for block in index.blocks(name)? {
        let block_name = block.name.replace('\'', "");
        composition.push_str(&format!("{{% block {block_name} %}}{{% endblock %}}"));
        entry.blocks.push(TwigComponentBlockEntry {
            name: block.name.clone(),
            file_uri: optional_file_uri(&block.file),
            line: block.line,
            print: format!("{{{{ block('{block_name}') }}}}"),
            compose: format!("{{% block {block_name} %}}{{% endblock %}}"),
        });
    }
    composition.push_str("{% endcomponent %}");
    entry.syntax.composition = composition;
    for usage in index.usages(name)? {
        entry.usages.push(TwigComponentUsageEntry {
            syntax: usage.kind.to_string(),
            file_uri: optional_file_uri(&usage.file),
            line: lines.line(&usage.file, range_start(usage.range)),
        });
    }

    let mut seen = HashSet::new();
    entry.declarations.retain(|value| {
        seen.insert(format!(
            "{}\x00{}\x00{}\x00{}",
            value.class, value.template, value.file_uri, value.source_line
        ))
    });
    let mut seen = HashSet::new();
    entry
        .templates
        .retain(|value| seen.insert(format!("{}\x00{}", value.template, value.file_uri)));
    entry
        .templates
        .sort_by(|left, right| left.file_uri.cmp(&right.file_uri));
    let mut seen = HashSet::new();
    entry.usages.retain(|value| {
        seen.insert(format!(
            "{}\x00{}\x00{}",
            value.syntax, value.file_uri, value.line
        ))
    });
    entry.usages.sort_by(|left, right| {
        left.file_uri
            .cmp(&right.file_uri)
            .then(left.line.cmp(&right.line))
    });
    Ok(entry)
}

fn props(props: &[Prop], lines: &mut SourceLineResolver) -> Vec<TwigComponentPropEntry> {
    props
        .iter()
        .map(|prop| {
            let mut entry = TwigComponentPropEntry {
                name: prop.name.clone(),
                ty: prop.ty.clone(),
                default_value: prop.default_value.clone(),
                description: prop.description.clone(),
                class: prop.class.clone(),
                member: prop.member.clone(),
                live: prop.live,
                writable: prop.writable,
                ..TwigComponentPropEntry::default()
            };
            if !prop.file.is_empty() {
                entry.file_uri = swlsp_uriutil::file_uri(&prop.file);
                entry.source_line = lines.line(&prop.file, range_start(prop.range));
            }
            entry
        })
        .collect()
}

fn component_source(source: SourceKind) -> &'static str {
    match source {
        SourceKind::Service => "service",
        SourceKind::CompiledContainer => "compiledContainer",
        SourceKind::AnonymousTemplate => "anonymousTemplate",
        SourceKind::Attribute => "attribute",
    }
}
