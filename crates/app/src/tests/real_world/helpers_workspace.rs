//! Port of Go `real_world_helpers_workspace_test.go`.

#[allow(unused_imports)]
use super::indexing_main::record_failure as soft;

/// Records a soft failure (Go `t.Errorf` in the soft oracle run) and
/// continues with the zero value.
#[allow(dead_code)]
fn soft_default<T: Default>(message: String) -> T {
    soft(message);
    T::default()
}

macro_rules! soft_assert {
    ($cond:expr, $($arg:tt)+) => {
        if !$cond {
            soft(format!($($arg)+));
        }
    };
}

use std::sync::Arc;

use serde::de::DeserializeOwned;
use serde_json::Value;
use swlsp_admin::{
    AdminComponentIndexer, AdminUsageSet, ComponentDefinition, TwigVueMember, VueComponentEvent,
    VueComponentMember, VueComponentProp, VueComponentSlot, VueComponentSlotMember,
    canonical_event_name,
};
use swlsp_lsp::{SemanticToken, TextDocument};
use swlsp_lsp_protocol as protocol;

use crate::Workspace;

pub fn require_code_lens_command(
    lenses: &[protocol::CodeLens],
    title: &str,
    command: &str,
    arguments: &[Value],
) {
    for lens in lenses {
        let Some(current) = &lens.command else {
            continue;
        };
        if current.title != title {
            continue;
        }
        soft_assert!(
            command == current.command,
            "code lens {title:?}: {:?}",
            current.command
        );
        soft_assert!(
            arguments == current.arguments.as_slice(),
            "code lens {title:?}: {:?}",
            current.arguments
        );
        return;
    }
    soft(format!(
        "code lens command {title:?} not found in {lenses:#?}"
    ));
}

pub fn workspace_admin_index(workspace: &Workspace) -> Arc<AdminComponentIndexer> {
    workspace
        .services()
        .admin
        .clone()
        .expect("Administration index is not registered")
}

pub fn admin_prop_names(props: &[VueComponentProp]) -> Vec<String> {
    props.iter().map(|prop| prop.name.clone()).collect()
}

pub fn component_definition_member_named(
    definition: Option<&ComponentDefinition>,
    name: &str,
) -> Option<VueComponentMember> {
    definition?
        .members
        .iter()
        .find(|member| member.name == name)
        .cloned()
}

pub fn require_admin_prop(props: &[VueComponentProp], name: &str) -> VueComponentProp {
    props
        .iter()
        .find(|prop| prop.name == name)
        .cloned()
        .unwrap_or_else(|| {
            soft_default(format!(
                "Administration prop {name:?} not found in {props:#?}"
            ))
        })
}

pub fn admin_slot_names(slots: &[VueComponentSlot]) -> Vec<String> {
    slots.iter().map(|slot| slot.name.clone()).collect()
}

pub fn require_admin_slot(slots: &[VueComponentSlot], name: &str) -> VueComponentSlot {
    slots
        .iter()
        .find(|slot| slot.name == name)
        .cloned()
        .unwrap_or_else(|| {
            soft_default(format!(
                "Administration slot {name:?} not found in {slots:#?}"
            ))
        })
}

pub fn require_admin_slot_member(
    members: &[VueComponentSlotMember],
    name: &str,
) -> VueComponentSlotMember {
    members
        .iter()
        .find(|member| member.name == name)
        .cloned()
        .unwrap_or_else(|| {
            soft_default(format!(
                "Administration slot member {name:?} not found in {members:#?}"
            ))
        })
}

pub fn require_admin_event(events: &[VueComponentEvent], name: &str) -> VueComponentEvent {
    let canonical = canonical_event_name(name);
    events
        .iter()
        .find(|event| canonical_event_name(&event.name) == canonical)
        .cloned()
        .unwrap_or_else(|| {
            soft_default(format!(
                "Administration event {name:?} not found in {events:#?}"
            ))
        })
}

pub fn admin_usage_occurrence_count(sets: &[AdminUsageSet]) -> usize {
    sets.iter().map(|set| set.occurrences.len()).sum()
}

pub fn real_world_admin_member_names(members: &[TwigVueMember]) -> Vec<String> {
    members.iter().map(|member| member.name.clone()).collect()
}

pub fn require_workspace_symbol(
    symbols: &[protocol::SymbolInformation],
    name: &str,
    container: &str,
) {
    for current in symbols {
        if current.name == name && current.container_name.contains(container) {
            assert!(!current.location.uri.is_empty());
            return;
        }
    }
    soft(format!(
        "workspace symbol {name:?} in {container:?} not found in {symbols:#?}"
    ));
}

pub fn require_semantic_token_text(
    document: &TextDocument,
    tokens: &[SemanticToken],
    text: &str,
    token_type: u32,
) {
    let source = document.source();
    for token in tokens {
        let start: usize = token.range.start().into();
        let end: usize = token.range.end().into();
        if token.token_type != token_type || end > source.len() {
            continue;
        }
        if &source[start..end] == text {
            return;
        }
    }
    soft(format!(
        "semantic token {text:?} with type {token_type} not found"
    ));
}

/// Go `realWorldDecodeCommandResponse`: JSON round trip into `T`.
pub fn real_world_decode_command_response<T: DeserializeOwned>(value: &Value) -> T {
    serde_json::from_value(value.clone()).expect("decode command response")
}

/// Go `workspaceAssetSnapshot` with readable fields (the portable test's
/// copy keeps them private).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AssetSnapshot {
    pub names: Vec<String>,
    pub packages: Vec<String>,
    pub encore: Vec<String>,
    pub importmap: Vec<String>,
    pub vite: Vec<String>,
    pub administration: Vec<swlsp_asset::Resource>,
    pub theme: Vec<swlsp_asset::Resource>,
    pub administration_usages: Vec<swlsp_asset::Usage>,
    pub html_usages: Vec<swlsp_asset::Usage>,
    pub vite_usages: Vec<swlsp_asset::Usage>,
    pub vite_administration_usages: Vec<swlsp_asset::Usage>,
}

/// Go `captureWorkspaceAssets`.
pub fn capture_workspace_assets(workspace: &Workspace) -> AssetSnapshot {
    use swlsp_asset::ReferenceKind;
    let index = &workspace.services().assets;
    AssetSnapshot {
        names: index.names().expect("names"),
        packages: index.package_names().expect("packages"),
        encore: index.entry_names().expect("entries"),
        importmap: index.importmap_entry_names().expect("importmap"),
        vite: index.vite_entry_names().expect("vite"),
        administration: index
            .find_assets_for_package(
                "administration/static/img/favicon/favicon-16x16.png",
                "@Administration",
            )
            .expect("assets"),
        theme: index
            .find_assets_for_package("assets/illustration/404_error.svg", "theme")
            .expect("assets"),
        administration_usages: index
            .usages("@Administration", ReferenceKind::AssetPackage)
            .expect("usages"),
        html_usages: index
            .usages(
                "_webpack_hot_proxy_/storefront/hot-reloading.js",
                ReferenceKind::Asset,
            )
            .expect("usages"),
        vite_usages: index
            .usages("app", ReferenceKind::ViteEntry)
            .expect("usages"),
        vite_administration_usages: index
            .usages("administration", ReferenceKind::ViteEntry)
            .expect("usages"),
    }
}
