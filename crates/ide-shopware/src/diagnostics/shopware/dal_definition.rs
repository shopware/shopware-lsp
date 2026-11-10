//! Entity definition fields the entity class cannot hold.
//!
//! The DAL hydrates an entity by assigning each field of its definition to
//! the property of the same name, and reads it back through that property
//! or its accessors. A field without a writable property fails at runtime.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARGUMENT, PHP_MEMBER_CALL, PHP_OBJECT_CREATION, PHP_PARENTHESIZED,
};
use swlsp_php::PhpIndex;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Snapshot, Symbol, Visibility};
use swlsp_php_types::Type;
use swlsp_shopware_dal as dal;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::eq_ignore_case;

use super::util::{trim_php_name, warning};
use crate::diagnostics::common::short_php_name;
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

pub const DAL_PROPERTY_MISSING_CODE: &str = "shopware.dal.entity_property.missing";
pub const DAL_PROPERTY_READONLY_CODE: &str = "shopware.dal.entity_property.readonly";
pub const DAL_PROPERTY_PRIVATE_CODE: &str = "shopware.dal.entity_property.private";
pub const DAL_GETTER_MISSING_CODE: &str = "shopware.dal.entity_property.getter_missing";
pub const DAL_SETTER_MISSING_CODE: &str = "shopware.dal.entity_property.setter_missing";

const ENTITY_CLASS: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Entity";
/// Keeps its values in an array, so it needs no properties.
const ARRAY_ENTITY_CLASS: &str = "Shopware\\Core\\Framework\\Struct\\ArrayEntity";

#[derive(Clone)]
pub struct DalDefinitionAnalyzer {
    php_index: Option<PhpIndex>,
}

impl DalDefinitionAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl DeclaredAnalyzer for DalDefinitionAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.dal.definition",
        description: "Entity definition fields the entity class cannot hold: no property of that name, a private or readonly property, or a protected property without a getter or setter.",
        domain: "shopware",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "shopware-lsp",
        codes: &[
            DAL_PROPERTY_MISSING_CODE,
            DAL_PROPERTY_READONLY_CODE,
            DAL_PROPERTY_PRIVATE_CODE,
            DAL_GETTER_MISSING_CODE,
            DAL_SETTER_MISSING_CODE,
        ],
    };
}

impl ProblemAnalyzer for DalDefinitionAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        // Cheap rejection before any semantic work: few files are
        // definitions.
        if document.language != Some(LanguageId::Php)
            || !document.source().contains("defineFields")
            || !document.source().contains("getEntityClass")
        {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let definitions = dal::document_definitions(&root, &document.uri, document.line_index());
        if definitions.is_empty() {
            return Ok(Vec::new());
        }
        let Some(state) = swlsp_lsp_phpanalysis::for_document(php_index, document)? else {
            return Ok(Vec::new());
        };
        let snapshot = state.snapshot.as_ref();
        let mut problems = Vec::new();
        for definition in &definitions {
            ctx.check()?;
            let Some(class) = phpquery::classes(&root)
                .into_iter()
                .find(|class| class.range_trimmed_trivia() == definition.class_range)
            else {
                continue;
            };
            // Without its own `getEntityClass()` a definition hydrates the
            // generic `ArrayEntity`.
            if !phpquery::methods(&class)
                .iter()
                .any(|method| eq_ignore_case(&phpquery::method_name(method), "getEntityClass"))
            {
                continue;
            }
            let Some(entity) = entity_class(snapshot, &definition.entity_class) else {
                continue;
            };
            let check = EntityCheck {
                snapshot,
                entity: &entity,
                entity_type: Type::named(trim_php_name(&entity.fully_qualified), Vec::new()),
            };
            for field in &definition.fields {
                // The index reads the storage name of these as their name;
                // a property name has no underscore.
                if field.type_ == "ReferenceVersionField" || field.name.contains('_') {
                    continue;
                }
                let flags = field_flags(&root, field.range);
                problems.extend(check.field(&field.name, field.range, &flags));
            }
        }
        problems.sort_by_key(|problem| problem.range.start());
        Ok(problems)
    }
}

/// The class the definition hydrates, when it holds values in properties.
fn entity_class(snapshot: &Snapshot, name: &str) -> Option<Symbol> {
    let name = trim_php_name(name);
    if name.is_empty()
        || eq_ignore_case(name, ENTITY_CLASS)
        || snapshot.is_subtype_of(name, ARRAY_ENTITY_CLASS)
        || !snapshot.is_subtype_of(name, ENTITY_CLASS)
    {
        return None;
    }
    let classes = snapshot.classes(name);
    classes
        .iter()
        .find(|class| !class.flags.has(Flags::GENERATED_STUB))
        .or(classes.first())
        .cloned()
}

/// The flags of a field that decide who writes its property.
#[derive(Default)]
struct FieldFlags {
    /// Filled by PHP code, not by the hydrator's setter-free assignment.
    runtime: bool,
    /// Computed by the database or an indexer.
    computed: bool,
}

/// The `Runtime` and `Computed` flags of the field declared at `range`.
fn field_flags(root: &SyntaxNode, range: TextRange) -> FieldFlags {
    let mut flags = FieldFlags::default();
    let covering = root.covering_element(range);
    let start = covering
        .as_node()
        .cloned()
        .or_else(|| covering.as_token().and_then(|token| token.parent()));
    let Some(mut field) = start.and_then(|node| {
        node.ancestors().find(|ancestor| {
            ancestor.kind() == PHP_OBJECT_CREATION
                && short_php_name(&phpquery::object_class_name(ancestor)).ends_with("Field")
        })
    }) else {
        return flags;
    };
    // `(new Field(...))->addFlags(...)`: the expression the field starts.
    while let Some(parent) = field.parent().filter(|parent| {
        parent.kind() == PHP_PARENTHESIZED
            || parent.kind() == PHP_MEMBER_CALL
                && phpquery::call_receiver(parent).as_ref() == Some(&field)
    }) {
        field = parent;
    }
    for flag in phpquery::nodes(&field, &[PHP_OBJECT_CREATION]) {
        // Not the flags of a field nested in an argument of another field.
        if flag
            .ancestors()
            .take_while(|ancestor| *ancestor != field)
            .filter(|ancestor| ancestor.kind() == PHP_ARGUMENT)
            .count()
            > 1
        {
            continue;
        }
        match short_php_name(&phpquery::object_class_name(&flag)) {
            "Runtime" => flags.runtime = true,
            "Computed" => flags.computed = true,
            _ => {}
        }
    }
    flags
}

struct EntityCheck<'a> {
    snapshot: &'a Snapshot,
    entity: &'a Symbol,
    entity_type: Type,
}

impl EntityCheck<'_> {
    fn field(&self, name: &str, range: TextRange, flags: &FieldFlags) -> Vec<Problem> {
        let entity = &self.entity.name;
        let resolver = MemberResolver::new(self.snapshot);
        let Some(property) = [name.to_owned(), format!("${name}")]
            .iter()
            .find_map(|name| {
                resolver
                    .properties(&self.entity_type, name)
                    .into_iter()
                    .next()
            })
            .map(|member| member.symbol)
        else {
            return vec![warning(
                DAL_PROPERTY_MISSING_CODE,
                range,
                format!("Entity {entity} has no property ${name} for this field"),
            )];
        };
        let mut problems = Vec::new();
        if property.flags.has(Flags::READONLY) {
            problems.push(warning(
                DAL_PROPERTY_READONLY_CODE,
                range,
                format!("{entity}::${name} is readonly; the DAL cannot fill it"),
            ));
        }
        match property.visibility {
            Visibility::Private => problems.push(warning(
                DAL_PROPERTY_PRIVATE_CODE,
                range,
                format!("{entity}::${name} is private; the DAL cannot fill private properties"),
            )),
            Visibility::Protected => {
                let accessor = upper_first(name);
                let getters = [
                    format!("get{accessor}"),
                    format!("is{accessor}"),
                    format!("has{accessor}"),
                    format!("has{}", accessor.strip_prefix("Has").unwrap_or(&accessor)),
                ];
                if !getters.iter().any(|getter| self.has_method(getter)) {
                    problems.push(warning(
                        DAL_GETTER_MISSING_CODE,
                        range,
                        format!("{entity}::${name} is protected and has no getter"),
                    ));
                }
                if !flags.runtime && !flags.computed && !self.has_method(&format!("set{accessor}"))
                {
                    problems.push(warning(
                        DAL_SETTER_MISSING_CODE,
                        range,
                        format!("{entity}::${name} is protected and has no setter"),
                    ));
                }
            }
            Visibility::Public => {}
        }
        problems
    }

    fn has_method(&self, name: &str) -> bool {
        !MemberResolver::new(self.snapshot)
            .methods(&self.entity_type, name)
            .is_empty()
    }
}

fn upper_first(name: &str) -> String {
    let mut characters = name.chars();
    characters.next().map_or_else(String::new, |first| {
        first.to_uppercase().chain(characters).collect()
    })
}
