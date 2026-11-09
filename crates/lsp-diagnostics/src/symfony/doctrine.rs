//! Missing Doctrine tables, columns and type classes.

use std::sync::Arc;

use swlsp_doctrine::{DbalReference, DbalReferenceRole, Index, type_registrations_in_document};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_php::PhpIndex;
use swlsp_shopware_dal::Index as DalIndex;
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_textutil::eq_ignore_case;

use super::dbal_schema::{DbalSchemaCatalog, has_dbal_schema_name};
use super::{lower_ext, symfony_warning};
use crate::common::admin_nearby_suggestions;
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

pub const MISSING_DOCTRINE_TABLE_CODE: &str = "symfony.doctrine.table.missing";
pub const MISSING_DOCTRINE_COLUMN_CODE: &str = "symfony.doctrine.column.missing";
pub const MISSING_DOCTRINE_TYPE_CLASS_CODE: &str = "symfony.doctrine.type_class.missing";
pub const INVALID_DOCTRINE_TYPE_CLASS_CODE: &str = "symfony.doctrine.type_class.invalid";

const DBAL_TYPE: &str = "Doctrine\\DBAL\\Types\\Type";

pub struct DoctrineAnalyzer {
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
    dal_index: Option<Arc<DalIndex>>,
}

impl DoctrineAnalyzer {
    pub fn new(
        index: Option<Arc<Index>>,
        php_index: Option<PhpIndex>,
        dal_index: Option<Arc<DalIndex>>,
    ) -> Self {
        Self {
            index,
            php_index,
            dal_index,
        }
    }

    fn type_registration_diagnostics(
        php_index: &PhpIndex,
        path: &str,
        root: &SyntaxNode,
    ) -> Vec<Problem> {
        let registrations = type_registrations_in_document(path, root);
        if registrations.is_empty() {
            return Vec::new();
        }
        let snapshot = php_index.semantic_snapshot();
        let is_type = |name: &str| {
            !eq_ignore_case(name, DBAL_TYPE) && snapshot.is_subtype_of(name, DBAL_TYPE)
        };
        let type_classes: Vec<String> = php_index
            .class_symbols_view()
            .iter()
            .filter(|symbol| is_type(&symbol.fully_qualified))
            .map(|symbol| symbol.fully_qualified.to_string())
            .collect();
        let mut result = Vec::new();
        for registration in registrations {
            let Some(symbol) = php_index.find_class(&registration.class) else {
                result.push(symfony_warning(
                    MISSING_DOCTRINE_TYPE_CLASS_CODE,
                    registration.class_range,
                    format!(
                        "Doctrine DBAL type class '{}' not found",
                        registration.class
                    ),
                    swlsp_suggestion::similar(&registration.class, &type_classes),
                ));
                continue;
            };
            if !is_type(&symbol.fully_qualified) {
                result.push(symfony_warning(
                    INVALID_DOCTRINE_TYPE_CLASS_CODE,
                    registration.class_range,
                    format!("Class '{}' is not a Doctrine DBAL type", registration.class),
                    Vec::new(),
                ));
            }
        }
        result
    }
}

impl DeclaredAnalyzer for DoctrineAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.doctrine",
        description: "Doctrine DBAL queries naming unknown tables or columns, and invalid custom type classes.",
        domain: "symfony.doctrine",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "symfony",
        codes: &[
            MISSING_DOCTRINE_COLUMN_CODE,
            MISSING_DOCTRINE_TABLE_CODE,
            INVALID_DOCTRINE_TYPE_CLASS_CODE,
            MISSING_DOCTRINE_TYPE_CLASS_CODE,
        ],
    };
}

impl ProblemAnalyzer for DoctrineAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(
            crate::inputs(&self.index)
                + crate::inputs(&self.php_index)
                + crate::inputs(&self.dal_index),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(php_index), Some(root)) =
            (&self.index, &self.php_index, document.root())
        else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_default();
        let mut result = Self::type_registration_diagnostics(php_index, &path, &root);
        if lower_ext(&path) != ".php" {
            return Ok(result);
        }
        let context = swlsp_lsp_phpanalysis::context_for_document(php_index, document)?;
        let mut dbal = DbalSchemaCatalog::new(self.dal_index.clone());
        for literal in phpquery::nodes(&root, &[PHP_STRING]) {
            ctx.check()?;
            let Some(reference) = index.dbal_reference_at(context.as_ref(), &root, &literal) else {
                continue;
            };
            match reference.role {
                DbalReferenceRole::Table => {
                    validate_dbal_table(&mut dbal, &reference, &mut result)?;
                }
                DbalReferenceRole::Column => {
                    validate_dbal_column(&mut dbal, &reference, &mut result)?;
                }
                DbalReferenceRole::Alias => {}
            }
        }
        Ok(result)
    }
}

fn validate_dbal_table(
    dbal: &mut DbalSchemaCatalog,
    reference: &DbalReference,
    result: &mut Vec<Problem>,
) -> Result<(), BoxError> {
    if dbal.has_table(&reference.name)? {
        return Ok(());
    }
    let names = dbal.table_names()?;
    let suggestions = admin_nearby_suggestions(&reference.name, &names);
    if suggestions.is_empty() {
        return Ok(());
    }
    result.push(doctrine_problem(
        reference.range,
        MISSING_DOCTRINE_TABLE_CODE,
        format!("Doctrine DBAL table '{}' not found", reference.name),
        suggestions,
    ));
    Ok(())
}

fn validate_dbal_column(
    dbal: &mut DbalSchemaCatalog,
    reference: &DbalReference,
    result: &mut Vec<Problem>,
) -> Result<(), BoxError> {
    let (columns, table_exists) = dbal.columns(&reference.table)?;
    if !table_exists || has_dbal_schema_name(&columns, &reference.name) {
        return Ok(());
    }
    let suggestions = admin_nearby_suggestions(&reference.name, &columns);
    if suggestions.is_empty() {
        return Ok(());
    }
    result.push(doctrine_problem(
        reference.range,
        MISSING_DOCTRINE_COLUMN_CODE,
        format!(
            "Doctrine DBAL column '{}' not found on table '{}'",
            reference.name, reference.table
        ),
        suggestions,
    ));
    Ok(())
}

fn doctrine_problem(
    range: TextRange,
    code: &str,
    message: String,
    suggestions: Vec<String>,
) -> Problem {
    symfony_warning(code, range, message, suggestions)
}
