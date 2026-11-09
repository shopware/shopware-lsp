//! Public asset, Encore/Vite/importmap entry, Assetic and package names.

use std::sync::Arc;

use swlsp_asset::{HtmlAssetType, Index, Reference, ReferenceKind};
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;

use super::{item, lower_ext, strings};
use crate::common::{php_context, protocol_range, text_edit};

pub struct AssetCompletionProvider {
    index: Arc<Index>,
    php: Option<PhpIndex>,
}

impl AssetCompletionProvider {
    pub fn new(index: Arc<Index>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl CompletionProvider for AssetCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(node) = request.node.as_ref() else {
            return Vec::new();
        };
        let uri = &request.params.text_document.uri;
        let extension = lower_ext(uri);
        if extension != ".php" && extension != ".twig" {
            return Vec::new();
        }
        let Some(reference) = swlsp_asset::reference_at(uri, node) else {
            return Vec::new();
        };
        if extension == ".php" {
            let Some(php) = self.php.as_ref() else {
                return Vec::new();
            };
            let context = php_context(php, request);
            if !swlsp_asset::validate_php_reference(context.as_ref(), &reference, php) {
                return Vec::new();
            }
        }
        let mut detail = "Symfony public asset";
        let names = match reference.kind {
            ReferenceKind::EncoreEntry => {
                detail = "Webpack Encore entry";
                self.index.entry_names()
            }
            ReferenceKind::Importmap => {
                detail = "Symfony AssetMapper entrypoint";
                self.index.importmap_entry_names()
            }
            ReferenceKind::ViteEntry => {
                detail = "Symfony Vite entry";
                self.index.vite_entry_names()
            }
            ReferenceKind::AsseticNamed => {
                detail = "Assetic named asset";
                Ok(self.index.assetic_named_names())
            }
            ReferenceKind::AssetPackage => {
                detail = "Symfony asset package";
                self.index.package_names()
            }
            ReferenceKind::Asset => {
                if reference.assetic {
                    detail = "Assetic file";
                    self.index.assetic_names(&reference.package)
                } else {
                    self.index.names_for_package(&reference.package)
                }
            }
        };
        let Ok(mut names) = names else {
            return Vec::new();
        };
        if reference.html_type != HtmlAssetType::None
            && reference.kind != ReferenceKind::AsseticNamed
        {
            names.retain(|name| swlsp_asset::matches_html_asset_type(name, reference.html_type));
            detail = "Symfony asset for Twig HTML";
        }
        let line_index = request.line_index();
        names
            .into_iter()
            .map(|name| {
                let label = if reference.kind == ReferenceKind::AsseticNamed {
                    format!("@{name}")
                } else if reference.assetic && !reference.package.is_empty() {
                    format!("{}/Resources/public/{name}", reference.package)
                } else {
                    name.clone()
                };
                let kind = if matches!(
                    reference.kind,
                    ReferenceKind::AssetPackage
                        | ReferenceKind::Importmap
                        | ReferenceKind::ViteEntry
                ) {
                    CompletionItemKind::MODULE
                } else {
                    CompletionItemKind::FILE
                };
                let mut completion = item(label.clone(), kind, detail);
                if reference.html_type != HtmlAssetType::None
                    && !reference.assetic
                    && let Some(line_index) = line_index
                {
                    completion.filter_text = name;
                    completion.text_edit = Some(text_edit(
                        protocol_range(swlsp_asset::reference_range(&reference), line_index),
                        twig_asset_expression(&reference, &label),
                    ));
                }
                completion
            })
            .collect()
    }

    fn trigger_characters(&self) -> Vec<String> {
        strings(&["/", "'", "\""])
    }
}

fn twig_asset_expression(reference: &Reference, name: &str) -> String {
    let text = reference.container.text().to_string();
    let quote = if text.trim().starts_with('\'') {
        '"'
    } else {
        '\''
    };
    let escaped = name.replace('\\', "\\\\");
    let escaped = if quote == '\'' {
        escaped.replace('\'', "\\'")
    } else {
        escaped.replace('"', "\\\"")
    };
    format!("{{{{ asset({quote}{escaped}{quote}) }}}}")
}
