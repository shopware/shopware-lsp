//! Declaration migration fix (Go `shopware_migration_declarations.go`).

use swlsp_lsp::{BoxError, Context, FixContext, FixId, FixPresentation, QuickFix, RewriteQuickFix};
use swlsp_lsp_diagnostics::ShopwareMigrationPayload;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_CLASS_DECLARATION, PHP_EXTENDS_CLAUSE, PHP_IMPLEMENTS_CLAUSE, PHP_METHOD_DECLARATION,
    PHP_NAME, PHP_PARAMETER, PHP_PROPERTY_DECLARATION,
};
use swlsp_php_rewrite::Editor;
use swlsp_rewrite::WorkspacePlan;
use swlsp_syntax::SyntaxNodeExt;

use super::{ancestor_node, document_root, finish_php_rewrite, invalid, present_migration};
use crate::resolve_anchor;

pub(crate) const DECLARATION_MIGRATION_FIX_ID: &str = "shopware-rector-declaration-migration";

pub(crate) struct DeclarationMigrationFix;

impl QuickFix for DeclarationMigrationFix {
    fn id(&self) -> FixId {
        FixId::from(DECLARATION_MIGRATION_FIX_ID)
    }

    fn present(&self, _: &Context, fix: &FixContext) -> Result<Option<FixPresentation>, BoxError> {
        present_migration(fix, "declaration-migration", |payload| {
            match payload.kind.as_str() {
                "interface-class" | "interface-parameter" | "interface-property" => {
                    "Shopware: Replace interface with abstract class"
                }
                "parameter-add" => "Shopware: Add required method parameter",
                "parameter-type" | "return-type" => "Shopware: Update native type",
                _ => "Shopware: Update declaration",
            }
        })
    }

    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        Some(self)
    }
}

impl RewriteQuickFix for DeclarationMigrationFix {
    fn build(&self, _: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        let payload: ShopwareMigrationPayload = fix.fix_payload()?;
        if !payload.safe
            || payload.rule != "declaration-migration"
            || payload.replacement.is_empty()
        {
            return Err(invalid("declaration migration is no longer safe"));
        }
        let element = resolve_anchor(fix)?;
        let root = document_root(&fix.document)?;
        let mut editor = Editor::new(fix.document.source(), Some(&root));
        match payload.kind.as_str() {
            "interface-class" => {
                let class = ancestor_node(&element, PHP_CLASS_DECLARATION);
                let Some(class) = class.filter(|_| !payload.original.is_empty()) else {
                    return Err(invalid("interface migration class changed"));
                };
                let parent = editor.class_reference(payload.replacement.trim_matches('\\'))?;
                let extends = phpquery::direct_child(&class, PHP_EXTENDS_CLAUSE);
                let implements = phpquery::direct_child(&class, PHP_IMPLEMENTS_CLAUSE);
                let implemented_names = implements.as_ref().map_or(0, |implements| {
                    implements
                        .children()
                        .filter(|child| child.kind() == PHP_NAME)
                        .count()
                });
                if extends.is_some() {
                    if !editor.remove_implements(&class, &payload.original)? {
                        return Err(invalid("implemented interface changed"));
                    }
                } else if let Some(implements) = implements.filter(|_| implemented_names == 1) {
                    editor.replace_range(
                        implements.range_trimmed_trivia(),
                        format!("extends {parent}"),
                    )?;
                } else {
                    editor.set_extends(&class, &parent)?;
                    if !editor.remove_implements(&class, &payload.original)? {
                        return Err(invalid("implemented interface changed"));
                    }
                }
            }
            "interface-parameter" | "parameter-type" => {
                let Some(parameter) = ancestor_node(&element, PHP_PARAMETER) else {
                    return Err(invalid("parameter type target changed"));
                };
                let type_text = migration_type_reference(&mut editor, &payload.replacement)?;
                editor.set_parameter_type(&parameter, &type_text)?;
            }
            "interface-property" => {
                let Some(property) = ancestor_node(&element, PHP_PROPERTY_DECLARATION) else {
                    return Err(invalid("property type target changed"));
                };
                let type_text = migration_type_reference(&mut editor, &payload.replacement)?;
                editor.set_property_type(&property, &type_text)?;
            }
            "parameter-add" => {
                let method = ancestor_node(&element, PHP_METHOD_DECLARATION);
                let Some(method) = method.filter(|_| payload.argument_index >= 0) else {
                    return Err(invalid("method parameter target changed"));
                };
                editor.insert_parameter(
                    &method,
                    payload.argument_index as usize,
                    &payload.replacement,
                )?;
            }
            "return-type" => {
                let Some(method) = ancestor_node(&element, PHP_METHOD_DECLARATION) else {
                    return Err(invalid("return type target changed"));
                };
                let type_text = migration_type_reference(&mut editor, &payload.replacement)?;
                editor.set_return_type(&method, &type_text)?;
            }
            other => {
                return Err(invalid(format!("unknown declaration migration {other:?}")));
            }
        }
        finish_php_rewrite(fix, &editor)
    }
}

/// Go `migrationTypeReference`: imports a class type (keeping `?`) and
/// returns native types unchanged.
fn migration_type_reference(editor: &mut Editor, type_text: &str) -> Result<String, BoxError> {
    let type_text = type_text.trim();
    if type_text.is_empty() {
        return Err(invalid("migration type is empty"));
    }
    let unprefixed = type_text.strip_prefix('?').unwrap_or(type_text);
    if !unprefixed.contains('\\') {
        return Ok(type_text.to_owned());
    }
    let nullable = type_text.starts_with('?');
    let reference = editor.class_reference(unprefixed.trim_matches('\\'))?;
    Ok(if nullable {
        format!("?{reference}")
    } else {
        reference
    })
}
