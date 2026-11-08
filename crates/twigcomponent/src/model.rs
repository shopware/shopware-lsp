//! Component, prop and Live Component records (port of `model.go`).

use serde::{Deserialize, Serialize};
use swlsp_php_types::go_to_lower;
use swlsp_syntax::TextRange;

macro_rules! u8_enum_default {
    ($(#[$meta:meta])* $name:ident { $first:ident = $first_value:literal => $first_label:literal $(, $variant:ident = $value:literal => $label:literal)* $(,)? }) => {
        $(#[$meta])*
        #[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
        #[serde(into = "u8", from = "u8")]
        pub enum $name {
            #[default]
            $first = $first_value,
            $($variant = $value),*
        }

        impl From<$name> for u8 {
            fn from(value: $name) -> u8 {
                value as u8
            }
        }

        impl From<u8> for $name {
            fn from(value: u8) -> Self {
                match value {
                    $($value => $name::$variant,)*
                    _ => $name::$first,
                }
            }
        }

        impl std::fmt::Display for $name {
            fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str(match self {
                    $name::$first => $first_label,
                    $($name::$variant => $label),*
                })
            }
        }
    };
}

u8_enum_default!(
    /// Where a component declaration comes from.
    SourceKind {
        Attribute = 0 => "PHP attribute",
        Service = 1 => "service tag",
        CompiledContainer = 2 => "compiled container",
        AnonymousTemplate = 3 => "anonymous template",
    }
);

u8_enum_default!(
    /// How a template uses a component.
    UsageKind {
        Function = 0 => "component()",
        Block = 1 => "component tag",
        Html = 2 => "HTML component",
    }
);

u8_enum_default!(
    LiveActionReferenceKind {
        Helper = 0 => "live_action()",
        Attribute = 1 => "data-live-action-param",
    }
);

u8_enum_default!(
    LiveEventReferenceKind {
        Emit = 0 => "emit",
        EmitUp = 1 => "emitUp",
        EmitSelf = 2 => "emitSelf",
        Attribute = 3 => "live#emit",
    }
);

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Declaration {
    pub name: String,
    pub class: String,
    pub template: String,
    pub template_from_method: String,
    pub file: String,
    pub name_range: TextRange,
    pub class_range: TextRange,
    pub template_range: TextRange,
    pub source: SourceKind,
    pub expose_public_props: bool,
    pub live: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Namespace {
    pub class_prefix: String,
    pub template_directory: String,
    pub name_prefix: String,
    pub file: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Prop {
    pub name: String,
    #[serde(rename = "type")]
    pub ty: String,
    pub default_value: String,
    pub description: String,
    pub file: String,
    pub class: String,
    pub member: String,
    pub range: TextRange,
    pub live: bool,
    pub writable: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Usage {
    pub name: String,
    pub file: String,
    pub range: TextRange,
    pub kind: UsageKind,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PropUsage {
    pub component: String,
    pub name: String,
    pub range: TextRange,
    pub dynamic: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Block {
    pub name: String,
    pub file: String,
    pub line: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ComponentBlockUsage {
    pub component: String,
    pub name: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct LiveActionParameter {
    pub name: String,
    pub php_name: String,
    #[serde(rename = "type")]
    pub ty: String,
    pub optional: bool,
    pub live_arg: bool,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct LiveAction {
    pub name: String,
    pub class: String,
    pub method: String,
    pub file: String,
    pub range: TextRange,
    pub parameters: Vec<LiveActionParameter>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct LiveActionReference {
    pub name: String,
    pub file: String,
    pub range: TextRange,
    pub context_range: TextRange,
    pub kind: LiveActionReferenceKind,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct LiveActionArgumentReference {
    pub action: String,
    pub name: String,
    pub file: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct LiveListener {
    pub name: String,
    pub class: String,
    pub method: String,
    pub file: String,
    pub range: TextRange,
    pub method_range: TextRange,
    pub parameters: Vec<LiveActionParameter>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct LiveEventReference {
    pub name: String,
    pub file: String,
    pub class: String,
    pub range: TextRange,
    pub context_range: TextRange,
    pub kind: LiveEventReferenceKind,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct LiveEventArgumentReference {
    pub event: String,
    pub name: String,
    pub file: String,
    pub range: TextRange,
}

/// Everything one file contributes, persisted under the file's path.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Record {
    pub file: String,
    pub declarations: Vec<Declaration>,
    pub namespaces: Vec<Namespace>,
    pub anonymous_directories: Vec<String>,
    pub props: Vec<Prop>,
    pub usages: Vec<Usage>,
    pub live_actions: Vec<LiveAction>,
    pub live_action_references: Vec<LiveActionReference>,
    pub live_action_arguments: Vec<LiveActionArgumentReference>,
    pub live_listeners: Vec<LiveListener>,
    pub live_event_references: Vec<LiveEventReference>,
    pub live_event_arguments: Vec<LiveEventArgumentReference>,
}

impl Record {
    pub(crate) fn is_empty(&self) -> bool {
        self.declarations.is_empty()
            && self.namespaces.is_empty()
            && self.anonymous_directories.is_empty()
            && self.props.is_empty()
            && self.usages.is_empty()
            && self.live_actions.is_empty()
            && self.live_action_references.is_empty()
            && self.live_action_arguments.is_empty()
            && self.live_listeners.is_empty()
            && self.live_event_references.is_empty()
            && self.live_event_arguments.is_empty()
    }
}

/// A resolved component.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Component {
    pub name: String,
    pub class: String,
    pub template: String,
    pub template_from_method: String,
    pub file: String,
    pub name_range: TextRange,
    pub class_range: TextRange,
    pub template_range: TextRange,
    pub source: SourceKind,
    pub expose_public_props: bool,
    pub live: bool,
}

/// Go `strings.Trim(strings.TrimSpace(name), "\\")`.
pub(crate) fn normalize_class(name: &str) -> String {
    name.trim().trim_matches('\\').to_owned()
}

pub(crate) fn normalize_directory(directory: &str) -> String {
    let replaced = directory.replace('\\', "/");
    let directory = replaced.trim().trim_matches('/');
    if directory.is_empty() {
        return String::new();
    }
    format!("{directory}/")
}

pub(crate) fn normalize_template(template: &str) -> String {
    let replaced = template.replace('\\', "/");
    let mut template = replaced.trim().to_owned();
    if template.is_empty() {
        return String::new();
    }
    if template.contains(':') && !template.starts_with('@') && !template.contains('/') {
        template = template.replace(':', "/");
        if !template.ends_with(".twig") {
            template.push_str(".html.twig");
        }
    }
    template
        .strip_prefix('/')
        .map(str::to_owned)
        .unwrap_or(template)
}

pub(crate) fn normalize_component_name(name: &str) -> String {
    name.trim().trim_matches(':').to_owned()
}

/// Go `cst.TextRange.String()`.
pub(crate) fn range_key(range: TextRange) -> String {
    format!("{}..{}", u32::from(range.start()), u32::from(range.end()))
}

pub(crate) fn component_key(component: &Component) -> String {
    format!(
        "{}\0{}\0{}\0{}\0{}\0{}",
        go_to_lower(&component.name),
        go_to_lower(&component.class),
        component.template,
        component.file,
        component.template_from_method,
        range_key(component.name_range),
    )
}

pub(crate) fn sort_components(components: &mut [Component]) {
    components.sort_by(|left, right| {
        go_to_lower(&left.name)
            .cmp(&go_to_lower(&right.name))
            .then(left.source.cmp(&right.source))
            .then_with(|| left.file.cmp(&right.file))
            .then(left.name_range.start().cmp(&right.name_range.start()))
    });
}

pub(crate) fn sort_usages(usages: &mut [Usage]) {
    usages.sort_by(|left, right| {
        left.file
            .cmp(&right.file)
            .then(left.range.start().cmp(&right.range.start()))
    });
}
