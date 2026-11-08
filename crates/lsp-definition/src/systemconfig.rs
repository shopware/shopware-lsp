use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_php::PhpIndex;
use swlsp_systemconfig::{SystemConfigEntry, SystemConfigIndexer};

use crate::common::request_uri;
use crate::php_common::{php_context, unclamped_line_location};
use crate::snippet::twig_string_value;

const SYSTEM_CONFIG_SERVICE: &str = "Shopware\\Core\\System\\SystemConfig\\SystemConfigService";

pub struct SystemConfigDefinitionProvider {
    index: Option<Arc<SystemConfigIndexer>>,
    php: Option<PhpIndex>,
}

impl SystemConfigDefinitionProvider {
    pub fn new(index: Option<Arc<SystemConfigIndexer>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }

    fn php_definition(&self, request: &DefinitionRequest) -> Vec<protocol::Location> {
        let (Some(index), Some(php), Some(node)) = (&self.index, &self.php, request.node.as_ref())
        else {
            return Vec::new();
        };
        let context = php_context(Some(php), request);
        if !php.is_method_called_on_class(context.as_ref(), node, SYSTEM_CONFIG_SERVICE) {
            return Vec::new();
        }
        if php.is_method_called_name(
            node,
            &["get", "getInt", "getString", "getFloat", "getBool", "set"],
        ) {
            let Ok(entries) = index.system_config_entry(&phpquery::string_value(node)) else {
                return Vec::new();
            };
            return entry_locations(entries.iter());
        }
        if php.is_method_called_name(node, &["getDomain"]) {
            let value = phpquery::string_value(node);
            let Ok(entries) = index.all_system_config_entries() else {
                return Vec::new();
            };
            return entry_locations(
                entries
                    .iter()
                    .filter(|entry| entry.namespace == value)
                    .take(1),
            );
        }
        Vec::new()
    }
}

fn entry_locations<'a>(
    entries: impl Iterator<Item = &'a SystemConfigEntry>,
) -> Vec<protocol::Location> {
    entries
        .map(|entry| unclamped_line_location(&entry.file_path, entry.line))
        .collect()
}

/// Go `filepath.Ext` (case-sensitive).
fn ext(uri: &str) -> &str {
    let base = uri.rsplit('/').next().unwrap_or(uri);
    base.rfind('.').map_or("", |index| &base[index..])
}

impl GotoDefinitionProvider for SystemConfigDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        match ext(request_uri(request)) {
            ".twig" => {
                let Some(node) = request.node.as_ref() else {
                    return Vec::new();
                };
                if !twigquery::string_in_function(node, &["config"]) {
                    return Vec::new();
                }
                let Some(index) = &self.index else {
                    return Vec::new();
                };
                let Ok(entries) = index.system_config_entry(&twig_string_value(node)) else {
                    return Vec::new();
                };
                entry_locations(entries.iter())
            }
            ".php" => self.php_definition(request),
            _ => Vec::new(),
        }
    }
}
