//! Entity classes that cannot hold the fields of their definition.
//!
//! The DAL hydrates an entity by assigning each field of its definition to
//! the property of the same name, and reads it back through that property
//! or its accessors. A field without a writable property fails at runtime.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_php::PhpIndex;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Snapshot, Visibility};
use swlsp_php_types::Type;
use swlsp_shopware_dal as dal;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::eq_ignore_case;

use super::util::{trim_php_name, warning};
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
    definitions: Option<Arc<dal::Index>>,
}

impl DalDefinitionAnalyzer {
    pub fn new(php_index: Option<PhpIndex>, definitions: Option<Arc<dal::Index>>) -> Self {
        Self {
            php_index,
            definitions,
        }
    }
}

impl DeclaredAnalyzer for DalDefinitionAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.dal.definition",
        description: "Entity classes that cannot hold a field of their entity definition: no property of that name, a private or readonly property, or a protected property without a getter or setter.",
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
        Some(
            crate::diagnostics::inputs(&self.php_index)
                ^ crate::diagnostics::inputs(&self.definitions).rotate_left(32),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(php_index), Some(definitions)) = (&self.php_index, &self.definitions) else {
            return Ok(Vec::new());
        };
        // Cheap rejection before any index work: few files are entities.
        if document.language != Some(LanguageId::Php) || !document.source().contains("Entity") {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let namespace = phpquery::namespace(&root);
        let mut state = None;
        let mut catalog: Option<Vec<dal::Definition>> = None;
        let mut problems = Vec::new();
        for class in phpquery::classes(&root) {
            ctx.check()?;
            let name = phpquery::class_name(&class);
            if name.is_empty() || phpquery::class_extends(&class).is_empty() {
                continue;
            }
            let name = if namespace.is_empty() {
                name
            } else {
                format!("{namespace}\\{name}")
            };
            if state.is_none() {
                state = swlsp_lsp_phpanalysis::for_document(php_index, document)?;
            }
            let Some(state) = &state else {
                return Ok(Vec::new());
            };
            let snapshot = state.snapshot.as_ref();
            if eq_ignore_case(&name, ENTITY_CLASS)
                || snapshot.is_subtype_of(&name, ARRAY_ENTITY_CLASS)
                || !snapshot.is_subtype_of(&name, ENTITY_CLASS)
            {
                continue;
            }
            // Loaded for the first entity only: listing decodes every
            // indexed definition.
            if catalog.is_none() {
                catalog = Some(definitions.definitions()?);
            }
            let check = EntityCheck {
                snapshot,
                class: &class,
                entity: phpquery::class_name(&class),
                entity_type: Type::named(&name, Vec::new()),
            };
            for definition in catalog.iter().flatten() {
                // Without its own `getEntityClass()` a definition hydrates
                // the generic `ArrayEntity`.
                if !definition.entity_class_declared
                    || !eq_ignore_case(trim_php_name(&definition.entity_class), &name)
                {
                    continue;
                }
                for field in &definition.fields {
                    // The index reads the storage name of these as their
                    // name; a property name has no underscore.
                    if field.type_ == "ReferenceVersionField" || field.name.contains('_') {
                        continue;
                    }
                    problems.extend(check.field(field, &definition.class));
                }
            }
        }
        problems.sort_by_key(|problem| problem.range.start());
        Ok(problems)
    }
}

struct EntityCheck<'a> {
    snapshot: &'a Snapshot,
    /// The entity class declaration in the document.
    class: &'a SyntaxNode,
    entity: String,
    entity_type: Type,
}

impl EntityCheck<'_> {
    fn field(&self, field: &dal::Field, definition: &str) -> Vec<Problem> {
        let name = field.name.as_str();
        let entity = &self.entity;
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
                self.class_range(),
                format!("{entity} has no property ${name} for the field {definition} defines"),
            )];
        };
        // On the property when the class declares it, on the class when it
        // is inherited or comes from a trait.
        let range = self.property_range(name);
        let mut problems = Vec::new();
        if property.flags.has(Flags::READONLY) {
            problems.push(warning(
                DAL_PROPERTY_READONLY_CODE,
                range,
                format!("{entity}::${name} is readonly; the DAL cannot fill the field {definition} defines"),
            ));
        }
        match property.visibility {
            Visibility::Private => problems.push(warning(
                DAL_PROPERTY_PRIVATE_CODE,
                range,
                format!("{entity}::${name} is private; the DAL cannot fill private properties ({definition})"),
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
                        format!("{entity}::${name} is protected and has no getter ({definition})"),
                    ));
                }
                if !field.runtime && !field.computed && !self.has_method(&format!("set{accessor}"))
                {
                    problems.push(warning(
                        DAL_SETTER_MISSING_CODE,
                        range,
                        format!("{entity}::${name} is protected and has no setter ({definition})"),
                    ));
                }
            }
            Visibility::Public => {}
        }
        problems
    }

    fn class_range(&self) -> TextRange {
        phpquery::direct_child(self.class, swlsp_parser_php::syntax::PHP_NAME).map_or_else(
            || self.class.range_trimmed_trivia(),
            |name| name.range_trimmed_trivia(),
        )
    }

    fn property_range(&self, name: &str) -> TextRange {
        phpquery::properties(self.class)
            .iter()
            .flat_map(phpquery::property_variables)
            .find(|variable| phpquery::variable_name(variable).trim_start_matches('$') == name)
            .map_or_else(
                || self.class_range(),
                |variable| variable.range_trimmed_trivia(),
            )
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
