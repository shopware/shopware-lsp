//! Variables a template reads that nothing defines, and the types
//! `{% dump_type %}` asks for.

use std::sync::Arc;

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_textutil::lowercase;
use swlsp_twig::{PhpAccessResolver, TwigIndexer};

use crate::diagnostics::ProblemAnalyzer;

pub const TWIG_VARIABLE_UNDEFINED_CODE: &str = "twig.variable.undefined";

pub struct TwigUndefinedVariableAnalyzer {
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

impl TwigUndefinedVariableAnalyzer {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            twig_index,
            php_index,
        }
    }
}

impl ProblemAnalyzer for TwigUndefinedVariableAnalyzer {
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        // The templates around the document are read from disk after the
        // index names them, and every change to them advances the index.
        Some(
            crate::diagnostics::inputs(&self.twig_index)
                + crate::diagnostics::inputs(&self.php_index),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        if !lowercase(&document.uri).ends_with(".twig")
            || document.uri.contains("/Resources/app/administration/")
        {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let template_path = swlsp_uriutil::path(&document.uri)?;
        let resolver =
            PhpAccessResolver::new(Some(php_index), self.twig_index.as_deref()).for_document(&root);
        let undefined = resolver.undefined_variables(&template_path, &root);
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        Ok(undefined
            .into_iter()
            .map(|variable| Problem {
                id: TWIG_VARIABLE_UNDEFINED_CODE.into(),
                range: variable.range,
                message: if variable.in_macro {
                    format!(
                        "Variable '{}' is not defined: a macro sees only its parameters and the globals",
                        variable.name
                    )
                } else {
                    format!(
                        "Variable '{}' is not defined: no render call, template or assignment provides it",
                        variable.name
                    )
                },
                source: "twig".into(),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                ..Problem::default()
            })
            .collect())
    }
}

pub const TWIG_TYPE_DUMPED_CODE: &str = "twig.type.dumped";

/// Shows the type of the variable a `{% dump_type %}` tag names.
pub struct TwigDumpTypeAnalyzer {
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

impl TwigDumpTypeAnalyzer {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            twig_index,
            php_index,
        }
    }
}

impl ProblemAnalyzer for TwigDumpTypeAnalyzer {
    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        if !document.source().contains(swlsp_twig::DUMP_TYPE_TAG) {
            return Some(0);
        }
        Some(
            crate::diagnostics::inputs(&self.twig_index)
                + crate::diagnostics::inputs(&self.php_index),
        )
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if !lowercase(&document.uri).ends_with(".twig")
            || !document.source().contains(swlsp_twig::DUMP_TYPE_TAG)
        {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let template_path = swlsp_uriutil::path(&document.uri)?;
        let resolver = PhpAccessResolver::new(self.php_index.as_ref(), self.twig_index.as_deref())
            .for_document(&root);
        Ok(resolver
            .dumped_types(&template_path, &root)
            .into_iter()
            .map(|dumped| Problem {
                id: TWIG_TYPE_DUMPED_CODE.into(),
                range: dumped.range,
                message: dumped.message,
                source: "twig".into(),
                severity: Some(protocol::DiagnosticSeverity::INFORMATION),
                ..Problem::default()
            })
            .collect())
    }
}
