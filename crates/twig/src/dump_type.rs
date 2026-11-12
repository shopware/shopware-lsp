//! `{% dump_type %}`: the type the analysis has for a variable, shown where
//! the tag stands. With no argument, the types of the whole known context.

use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::context::node_at_offset;
use crate::inference::set_index;
use crate::path::template_names;
use crate::php_access::{PhpAccessResolver, twig_type_declarations};
use crate::token_parser::twig_tag_usages;

pub const DUMP_TYPE_TAG: &str = "dump_type";

/// One `{% dump_type %}` tag with the text to show for it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DumpedType {
    /// The tag name.
    pub range: TextRange,
    pub message: String,
}

impl PhpAccessResolver<'_> {
    pub fn dumped_types(&self, template_path: &str, root: &SyntaxNode) -> Vec<DumpedType> {
        let source = root.text().to_string();
        let mut result = Vec::new();
        for usage in twig_tag_usages(source.as_bytes()) {
            if usage.name != DUMP_TYPE_TAG {
                continue;
            }
            let end = usize::from(usage.range.end());
            let Some(close) = source[end..].find("%}") else {
                continue;
            };
            let argument = source[end..end + close].trim().trim_end_matches('-').trim();
            let Some(anchor) = node_at_offset(root, usage.range.start()) else {
                continue;
            };
            let message = if argument.is_empty() {
                self.dumped_context(template_path, root, &anchor)
            } else {
                match self.dumped_path(template_path, root, &anchor, argument) {
                    Some(value) => format!("Dumped type: {}", shown(&value)),
                    None => "dump_type takes a variable or an attribute path such as product.name"
                        .to_owned(),
                }
            };
            result.push(DumpedType {
                range: usage.range,
                message,
            });
        }
        result
    }

    /// The type of `name.member.member` where `anchor` stands.
    fn dumped_path(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        anchor: &SyntaxNode,
        path: &str,
    ) -> Option<Type> {
        let mut segments = path.split('.').map(str::trim);
        let name = segments.next().filter(|name| is_identifier(name))?;
        let mut value = self.name_type_at(template_path, root, anchor, name);
        for member in segments {
            if !is_identifier(member) {
                return None;
            }
            let declared = self.attribute_type(&value, member);
            value = self.framework_member_type(template_path, root, &value, member, declared);
        }
        Some(value)
    }

    /// Every variable known where `anchor` stands, with its type.
    fn dumped_context(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        anchor: &SyntaxNode,
    ) -> String {
        let mut names: Vec<String> = twig_type_declarations(root).into_keys().collect();
        names.extend(set_index(root).into_keys());
        if let Some(php) = self.php {
            names.extend(
                php.twig_template_variables(&template_names(template_path))
                    .unwrap_or_default()
                    .into_iter()
                    .map(|variable| variable.name),
            );
        }
        names.sort();
        names.dedup();
        let entries: Vec<String> = names
            .iter()
            .map(|name| {
                let value = self.name_type_at(template_path, root, anchor, name);
                format!("{name}: {}", shown(&value))
            })
            .collect();
        if entries.is_empty() {
            "Dumped context: no variables are known here".to_owned()
        } else {
            format!("Dumped context: {}", entries.join(", "))
        }
    }
}

fn shown(value: &Type) -> String {
    if value.is_unknown() {
        "mixed (nothing is known about it)".to_owned()
    } else {
        value.to_string()
    }
}

fn is_identifier(text: &str) -> bool {
    !text.is_empty()
        && text
            .chars()
            .all(|character| character.is_alphanumeric() || character == '_')
        && !text.starts_with(|character: char| character.is_ascii_digit())
}
