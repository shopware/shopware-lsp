//! Service definition lenses on PHP classes and autowired constructors.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind, Visibility};
use swlsp_symfony::{Location, ServiceIndex};

use swlsp_ide::codelens::{
    by_position_title, cancelled, related_lens, related_protocol_range, related_target,
    unique_related_targets,
};

pub struct PhpServiceCodeLensProvider {
    php: Option<PhpIndex>,
    services: Option<Arc<ServiceIndex>>,
}

impl PhpServiceCodeLensProvider {
    pub fn new(php: Option<PhpIndex>, services: Option<Arc<ServiceIndex>>) -> Self {
        Self { php, services }
    }
}

impl CodeLensProvider for PhpServiceCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let (Some(php), Some(services)) = (&self.php, &self.services) else {
            return Ok(Vec::new());
        };
        let uri = &request.params.text_document.uri;
        if !uri.to_ascii_lowercase().ends_with(".php") {
            return Ok(Vec::new());
        }
        let document = &request.document;
        let path = swlsp_uriutil::path(uri)?;
        let semantic = match swlsp_lsp_phpanalysis::for_document(php, document)? {
            Some(state) => Some(state.document.clone()),
            None => php.semantic_document(&path).map(Arc::new),
        };
        let symbols: &[Symbol] = semantic
            .as_deref()
            .map_or(&[], |document| document.symbols.as_slice());
        if symbols.is_empty() {
            return Ok(Vec::new());
        }
        let lines = document.line_index();
        let mut lenses = Vec::new();
        let mut classes: HashMap<&str, &Symbol> = HashMap::new();
        for class in symbols {
            cancelled(ctx)?;
            if class.kind != SymbolKind::Class || class.flags.has(Flags::ABSTRACT) {
                continue;
            }
            classes.insert(class.id.as_str(), class);
            let locations = services.get_services_usage_by_class_name(&class.fully_qualified)?;
            if locations.is_empty() {
                continue;
            }
            let title = if locations.len() > 1 {
                format!("Open {} Service Definitions", locations.len())
            } else {
                "Open Service Definition".to_owned()
            };
            lenses.push(related_lens(
                related_protocol_range(class.selection_range, lines),
                title,
                service_location_targets(&locations),
            ));
        }
        for method in symbols {
            cancelled(ctx)?;
            let Some(class) = classes.get(method.container.as_str()) else {
                continue;
            };
            if method.kind != SymbolKind::Method
                || !method.name.eq_ignore_ascii_case("__construct")
                || method.visibility != Visibility::Public
            {
                continue;
            }
            let locations =
                services.get_autowired_services_usage_by_class_name(&class.fully_qualified)?;
            if locations.is_empty() {
                continue;
            }
            let title = if locations.len() > 1 {
                format!("Open {} autowired service definitions", locations.len())
            } else {
                "Open autowired service definition".to_owned()
            };
            lenses.push(related_lens(
                related_protocol_range(method.selection_range, lines),
                title,
                service_location_targets(&locations),
            ));
        }
        lenses.sort_by(by_position_title);
        Ok(lenses)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

pub(crate) fn service_location_targets(locations: &[Location]) -> Vec<String> {
    unique_related_targets(
        locations
            .iter()
            .map(|location| related_target(&location.path, i64::from(location.line)))
            .collect(),
    )
}
