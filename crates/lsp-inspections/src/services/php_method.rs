//! Creates a missing PHP method in an indexed class.

use std::sync::LazyLock;

use regex::Regex;
use serde::{Deserialize, Serialize};
use swlsp_lsp::{
    BoxError, Context, FixContext, FixId, FixPresentation, FixResolution, QuickFix,
    RewriteQuickFix, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_php::PhpIndex;
use swlsp_rewrite::{Builder, DocumentPlan, WorkspacePlan};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::eq_ignore_case;

use crate::inspection::resolve_anchor;

static PHP_IDENTIFIER: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"^[A-Za-z_\x{80}-\x{ff}][A-Za-z0-9_\x{80}-\x{ff}]*$").expect("identifier regex")
});

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PhpMethodPayload {
    #[serde(default)]
    pub class_name: String,
    #[serde(default)]
    pub method_name: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub parameters: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub typed_parameters: Vec<PhpMethodParameter>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct PhpMethodParameter {
    #[serde(default)]
    pub name: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub types: Vec<String>,
}

pub struct PhpMethodFix {
    pub id: &'static str,
    pub title_prefix: &'static str,
    pub php_index: Option<PhpIndex>,
}

fn is_identifier(value: &str) -> bool {
    PHP_IDENTIFIER.is_match(value)
}

impl QuickFix for PhpMethodFix {
    fn id(&self) -> FixId {
        FixId::from(self.id)
    }

    fn present(
        &self,
        _ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<FixPresentation>, BoxError> {
        let payload: PhpMethodPayload = fix.fix_payload()?;
        if self.php_index.is_none()
            || payload.class_name.is_empty()
            || !is_identifier(&payload.method_name)
        {
            return Ok(None);
        }
        for parameter in &payload.parameters {
            if !is_identifier(parameter.strip_prefix('$').unwrap_or(parameter)) {
                return Ok(None);
            }
        }
        for parameter in &payload.typed_parameters {
            if !is_identifier(parameter.name.strip_prefix('$').unwrap_or(&parameter.name)) {
                return Ok(None);
            }
            if !parameter
                .types
                .iter()
                .all(|name| valid_php_class_name(name))
            {
                return Ok(None);
            }
        }
        Ok(Some(FixPresentation {
            title: format!(
                "Symfony: Create {} method '{}'",
                self.title_prefix, payload.method_name
            ),
            kind: Some(protocol::CodeActionKind::QUICKFIX),
            preferred: true,
            resolution: FixResolution::Lazy,
        }))
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for PhpMethodFix {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: PhpMethodPayload = fix.fix_payload()?;
        resolve_anchor(fix)?;
        let symbol = self
            .php_index
            .as_ref()
            .and_then(|php| php.find_class(&payload.class_name));
        let Some(symbol) = symbol.filter(|symbol| {
            !symbol.path.is_empty() && !symbol.path.as_str().starts_with("phpstub://")
        }) else {
            return Err(format!("class {:?} is not editable", payload.class_name).into());
        };
        let target_uri = swlsp_uriutil::file_uri(symbol.path.as_str());
        let target = fix.documents.resolve_document(ctx, &target_uri)?;
        if target.document.root().is_none() {
            return Err("target class has no PHP syntax tree".into());
        }
        let class = php_class_for_symbol(
            &target.document,
            &payload.class_name,
            u32::from(symbol.range.start()),
        );
        let body = class.as_ref().and_then(phpquery::class_body);
        let (Some(class), Some(body)) = (class, body) else {
            return Err(format!("class {:?} has no body", payload.class_name).into());
        };
        for method in phpquery::methods(&class) {
            if eq_ignore_case(&phpquery::method_name(&method), &payload.method_name) {
                return Err(format!(
                    "method {}::{} already exists",
                    payload.class_name, payload.method_name
                )
                .into());
            }
        }
        let method_text = php_method_fragment(
            &payload.method_name,
            &payload.parameters,
            &payload.typed_parameters,
        );
        validate_php_method_fragment(&method_text, &payload.method_name)?;
        let source = target.document.source();
        let Some((offset, insertion)) = php_method_insertion(source, &class, &body, &method_text)
        else {
            return Err("class closing brace was not found".into());
        };
        let mut builder = Builder::new(source);
        builder.insert(offset, insertion)?;
        let edits = builder.finish()?;
        Ok(WorkspacePlan {
            documents: vec![DocumentPlan::new(
                target_uri,
                target.version,
                target.document.source_arc().clone(),
                edits,
            )],
            ..WorkspacePlan::default()
        })
    }
}

fn php_class_for_symbol(
    document: &TextDocument,
    class_name: &str,
    old_offset: u32,
) -> Option<SyntaxNode> {
    let short_name = class_name.rsplit('\\').next().unwrap_or(class_name);
    let root = document.root()?;
    let mut best: Option<(SyntaxNode, u32)> = None;
    for class in phpquery::classes(&root) {
        if !eq_ignore_case(&phpquery::class_name(&class), short_name) {
            continue;
        }
        let distance = u32::from(class.text_range().start()).abs_diff(old_offset);
        if best.as_ref().is_none_or(|(_, best)| distance < *best) {
            best = Some((class, distance));
        }
    }
    best.map(|(class, _)| class)
}

fn php_method_fragment(
    method_name: &str,
    parameters: &[String],
    typed: &[PhpMethodParameter],
) -> String {
    let mut normalized = Vec::with_capacity(parameters.len() + typed.len());
    for parameter in parameters {
        let parameter = parameter.trim();
        if !parameter.is_empty() && !parameter.starts_with('$') {
            normalized.push(format!("${parameter}"));
        } else {
            normalized.push(parameter.to_owned());
        }
    }
    for parameter in typed {
        let mut name = parameter.name.trim().to_owned();
        if !name.is_empty() && !name.starts_with('$') {
            name = format!("${name}");
        }
        let types: Vec<String> = parameter
            .types
            .iter()
            .map(|name| name.trim().trim_matches('\\'))
            .filter(|name| !name.is_empty())
            .map(|name| format!("\\{name}"))
            .collect();
        if !types.is_empty() {
            name = format!("{} {name}", types.join("|"));
        }
        normalized.push(name);
    }
    format!(
        "public function {method_name}({})\n{{\n}}",
        normalized.join(", ")
    )
}

fn valid_php_class_name(value: &str) -> bool {
    let value = value.trim().trim_matches('\\');
    !value.is_empty() && value.split('\\').all(is_identifier)
}

fn validate_php_method_fragment(fragment: &str, method_name: &str) -> Result<(), BoxError> {
    let parsed = swlsp_parser_php::parse(&format!("<?php\nclass Fragment {{\n{fragment}\n}}\n"));
    if !parsed.errors.is_empty() {
        return Err("generated PHP method is not syntactically valid".into());
    }
    let classes = phpquery::classes(&parsed.tree.root());
    if classes.len() != 1 {
        return Err("generated PHP method has no class context".into());
    }
    let methods = phpquery::methods(&classes[0]);
    if methods.len() != 1 || phpquery::method_name(&methods[0]) != method_name {
        return Err(format!("generated PHP fragment is not method {method_name:?}").into());
    }
    Ok(())
}

fn php_method_insertion(
    source: &str,
    class: &SyntaxNode,
    body: &SyntaxNode,
    fragment: &str,
) -> Option<(u32, String)> {
    let body_text = body.text().to_string();
    let closing_index = body_text.rfind('}')?;
    let closing_offset = u32::from(body.text_range().start()) + closing_index as u32;
    let class_indent = source_indentation_at(source, u32::from(class.text_range().start()));
    let mut member_indent = format!("{class_indent}    ");
    if let Some(first) = phpquery::methods(class).first() {
        let inferred = source_indentation_at(source, u32::from(first.text_range().start()));
        if !inferred.is_empty() {
            member_indent = inferred.to_owned();
        }
    }
    let block = fragment
        .split('\n')
        .map(|line| format!("{member_indent}{line}"))
        .collect::<Vec<_>>()
        .join("\n");
    let body_content = if closing_index > 0 {
        body_text[1..closing_index].trim()
    } else {
        ""
    };
    let line_start = source_line_start(source, closing_offset);
    if source[line_start as usize..closing_offset as usize]
        .trim()
        .is_empty()
    {
        let prefix = if body_content.is_empty() { "" } else { "\n" };
        return Some((line_start, format!("{prefix}{block}\n")));
    }
    Some((closing_offset, format!("\n{block}\n{class_indent}")))
}

fn source_indentation_at(source: &str, offset: u32) -> &str {
    let start = source_line_start(source, offset) as usize;
    let bytes = source.as_bytes();
    let mut end = start;
    while end < bytes.len() && matches!(bytes[end], b' ' | b'\t') {
        end += 1;
    }
    &source[start..end]
}

fn source_line_start(source: &str, offset: u32) -> u32 {
    let bytes = source.as_bytes();
    let mut offset = (offset as usize).min(bytes.len());
    while offset > 0 && bytes[offset - 1] != b'\n' {
        offset -= 1;
    }
    offset as u32
}
