//! The reference plugin's UX component gutter navigation as portable LSP
//! code lenses.

use std::collections::BTreeSet;
use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, Range};
use swlsp_php::PhpIndex;
use swlsp_twigcomponent::Index;

use swlsp_ide::codelens::{
    cancelled, related_lens, related_protocol_range, related_source_line, related_target,
    same_path, sort_related_code_lenses, unique_related_targets,
};

pub struct TwigComponentRelatedCodeLensProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl TwigComponentRelatedCodeLensProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl CodeLensProvider for TwigComponentRelatedCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let uri = &request.params.text_document.uri;
        if !uri.to_ascii_lowercase().ends_with(".twig") {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(uri)?;
        let lines = document.line_index();
        let mut result = Vec::new();
        if let Some(lens) = self.template_code_lens(index, ctx, &path)? {
            result.push(lens);
        }
        for usage in swlsp_twigcomponent::block_usages_in_twig(&root) {
            cancelled(ctx)?;
            let targets = unique_related_targets(
                index
                    .blocks(&usage.component)?
                    .iter()
                    .filter(|block| block.name == usage.name)
                    .map(|block| related_target(&block.file, block.line))
                    .collect(),
            );
            if targets.is_empty() {
                continue;
            }
            let title = if targets.len() > 1 {
                format!("Open {} component blocks", targets.len())
            } else {
                "Open component block".to_owned()
            };
            result.push(related_lens(
                related_protocol_range(usage.range, lines),
                title,
                targets,
            ));
        }
        sort_related_code_lenses(&mut result);
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

impl TwigComponentRelatedCodeLensProvider {
    fn template_code_lens(
        &self,
        index: &Index,
        ctx: &Context,
        path: &str,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        let components = index.components_for_template(path)?;
        let mut targets = Vec::new();
        let mut names = BTreeSet::new();
        for component in &components {
            cancelled(ctx)?;
            if !component.name.is_empty() {
                names.insert(component.name.as_str());
            }
            let Some(php) = &self.php else {
                continue;
            };
            if component.class.is_empty() {
                continue;
            }
            let Some(class) = php.find_class(&component.class) else {
                continue;
            };
            targets.push(related_target(
                &class.path,
                related_source_line(&class.path, class.selection_range.start().into()),
            ));
        }
        for name in names {
            for usage in index.usages(name)? {
                if same_path(&usage.file, path) {
                    continue;
                }
                targets.push(related_target(
                    &usage.file,
                    related_source_line(&usage.file, usage.range.start().into()),
                ));
            }
        }
        let targets = unique_related_targets(targets);
        if targets.is_empty() {
            return Ok(None);
        }
        Ok(Some(related_lens(
            Range::default(),
            "Open UX component",
            targets,
        )))
    }
}
