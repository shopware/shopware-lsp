//! Persisted component contracts (port of Go `component.go`).

use crate::*;

/// A Shopware Administration Vue component (Go `VueComponent`).
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueComponent {
    /// Component name, e.g. `sw-base-filter`.
    pub name: String,
    /// Normalized `@deprecated` documentation of this component's own
    /// registration or definition; never inherited from a parent.
    pub deprecated: String,
    /// Registry participation; `Unset` is treated as register.
    pub kind: ComponentRegistrationKind,
    /// Parent for extend, overridden component for override.
    pub target_component: String,
    /// Parent component name for extend registrations.
    pub extends_component: String,
    /// Path of the import statement.
    pub import_path: String,
    /// Absolute registration file path.
    pub file_path: String,
    /// Resolved absolute component definition path.
    pub definition_path: String,
    /// 1-based registration line.
    pub line: u32,
    pub props: Vec<VueComponentProp>,
    /// Explicit legacy Options API `model` contract.
    pub model_prop: String,
    pub model_event: String,
    pub emits: Vec<String>,
    /// Source-aware emitted event declarations.
    pub events: Vec<VueComponentEvent>,
    pub methods: Vec<String>,
    pub computed: Vec<String>,
    pub data: Vec<String>,
    pub injected: Vec<String>,
    pub mixins: Vec<String>,
    pub local_components: Vec<VueLocalComponent>,
    pub local_directives: Vec<VueLocalDirective>,
    pub members: Vec<VueComponentMember>,
    /// Spreads or unresolved mixins can add further members at runtime.
    pub open_runtime_members: bool,
    pub assignments: Vec<VueComponentAssignment>,
    pub slots: Vec<VueComponentSlot>,
    pub blocks: Vec<TwigBlock>,
    pub template_path: String,
    /// Inline registration definition; only populated while indexing.
    #[serde(skip)]
    pub inline_definition: Option<Box<ComponentDefinition>>,
    /// Compact TypeScript contexts parsed from an open Vue document; never
    /// persisted.
    #[serde(skip)]
    pub(crate) live_type_files: Vec<AdminTypeFile>,
}

pub(crate) fn component_live_type_files(component: Option<&VueComponent>) -> &[AdminTypeFile] {
    component.map_or(&[], |component| component.live_type_files.as_slice())
}

/// One statically declared Options API `components` entry.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueLocalComponent {
    pub name: String,
    pub symbol: String,
    pub import_path: String,
    pub file_path: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
    pub shorthand: bool,
    pub quoted: bool,
}

/// One statically declared Options API `directives` entry (name without
/// `v-`).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueLocalDirective {
    pub name: String,
    pub file_path: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
    pub shorthand: bool,
    pub quoted: bool,
}

impl VueComponent {
    pub fn local_directive(&self, name: &str) -> Option<&VueLocalDirective> {
        self.local_directives
            .iter()
            .find(|local| go_equal_fold(&local.name, name))
    }

    pub fn local_component(&self, name: &str) -> Option<&VueLocalComponent> {
        self.local_components
            .iter()
            .find(|local| go_equal_fold(&local.name, name))
    }
}

/// Go `strings.EqualFold` (simple Unicode case folding).
pub(crate) fn go_equal_fold(left: &str, right: &str) -> bool {
    let mut left = left.chars();
    let mut right = right.chars();
    loop {
        match (left.next(), right.next()) {
            (None, None) => return true,
            (Some(a), Some(b)) => {
                if a == b {
                    continue;
                }
                let la: Vec<char> = a.to_lowercase().collect();
                let lb: Vec<char> = b.to_lowercase().collect();
                if la != lb {
                    return false;
                }
            }
            _ => return false,
        }
    }
}

string_enum! {
    pub enum ComponentRegistrationKind {
        Register => "register",
        Extend => "extend",
        Override => "override",
    }
}

/// A registered Administration mixin.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminMixin {
    pub name: String,
    pub file_path: String,
    pub line: u32,
    pub definition: ComponentDefinition,
}

/// A custom directive registered through Shopware's directive registry. The
/// name excludes the `v-` prefix.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminDirective {
    pub name: String,
    pub file_path: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
    pub local: bool,
}

/// A formatter registered through `Shopware.Filter.register`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminFilter {
    pub name: String,
    pub file_path: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
    pub signature: String,
}

/// A `Module.register` declaration and its generated route names.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminModule {
    pub name: String,
    pub display_name: String,
    #[serde(rename = "type")]
    pub type_: String,
    pub title: String,
    pub description: String,
    pub file_path: String,
    pub line: u32,
    pub routes: Vec<AdminModuleRoute>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AdminModuleRoute {
    pub name: String,
    pub local_name: String,
    pub path: String,
    pub component: String,
    pub line: u32,
}

/// A prop definition of a Vue component.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueComponentProp {
    pub name: String,
    /// Public description without typed metadata tags.
    pub documentation: String,
    pub name_range: AdminSourceRange,
    pub deprecated: String,
    /// Prop type, e.g. `String`, `Boolean`, `Object`.
    #[serde(rename = "type")]
    pub type_: String,
    pub required: bool,
    pub default: String,
    /// Statically declared literal values (e.g. `validValues`).
    pub allowed_values: Vec<String>,
    /// Whether `allowed_values` is the complete accepted domain.
    pub allowed_values_complete: bool,
    /// 1-based declaration line.
    pub line: u32,
    /// Definition which owns the prop.
    pub file_path: String,
}

/// An event exposed by a Vue component.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueComponentEvent {
    pub name: String,
    #[serde(rename = "type")]
    pub type_: String,
    pub documentation: String,
    pub file_path: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
}

/// The two-sided contract behind one `v-model` directive.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct VueComponentModelBinding {
    pub attribute_name: String,
    pub prop_name: String,
    pub event_name: String,
    pub prop: VueComponentProp,
    pub event: VueComponentEvent,
}

impl VueComponent {
    /// Resolves a concrete `v-model` or `v-model:argument` attribute. Only
    /// returned when both sides of the public contract are indexed.
    pub fn component_model(&self, attribute_name: &str) -> Option<VueComponentModelBinding> {
        let argument = normalize_model_argument(attribute_name)?;
        let (prop_name, event_name) = if argument.is_empty() {
            if !self.model_prop.is_empty() || !self.model_event.is_empty() {
                let prop = if self.model_prop.is_empty() {
                    "value"
                } else {
                    &self.model_prop
                };
                let event = if self.model_event.is_empty() {
                    "input"
                } else {
                    &self.model_event
                };
                (prop.to_owned(), event.to_owned())
            } else if component_has_model_pair(self, "modelValue", "update:modelValue") {
                ("modelValue".to_owned(), "update:modelValue".to_owned())
            } else if component_has_model_pair(self, "value", "input") {
                ("value".to_owned(), "input".to_owned())
            } else {
                return None;
            }
        } else {
            let event = format!("update:{}", camel_to_kebab(&argument));
            (argument, event)
        };
        let prop = self.component_prop(&prop_name)?.clone();
        let event = self.component_event(&event_name)?;
        Some(VueComponentModelBinding {
            attribute_name: attribute_name.to_owned(),
            prop_name: prop.name.clone(),
            event_name: canonical_event_name(&event.name),
            prop,
            event,
        })
    }

    /// Every statically complete model contract: the default model followed
    /// by named `update:*` pairs.
    pub fn component_models(&self) -> Vec<VueComponentModelBinding> {
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        let mut add = |attribute_name: String, result: &mut Vec<VueComponentModelBinding>| {
            let Some(binding) = self.component_model(&attribute_name) else {
                return;
            };
            if !seen.insert(attribute_name) {
                return;
            }
            result.push(binding);
        };
        add("v-model".to_owned(), &mut result);
        let default_model = self.component_model("v-model");
        for event in self.component_events() {
            let name = canonical_event_name(&event.name);
            let Some(argument) = name.strip_prefix("update:") else {
                continue;
            };
            if argument.is_empty() {
                continue;
            }
            if default_model
                .as_ref()
                .is_some_and(|model| model.event_name == name)
            {
                continue;
            }
            add(format!("v-model:{argument}"), &mut result);
        }
        result
    }

    /// The source-aware event API, synthesizing entries for `emits` names.
    pub fn component_events(&self) -> Vec<VueComponentEvent> {
        let mut result = self.events.clone();
        let mut seen: HashSet<String> = result
            .iter()
            .map(|event| canonical_event_name(&event.name))
            .collect();
        for name in &self.emits {
            let canonical = canonical_event_name(name);
            if canonical.is_empty() || !seen.insert(canonical) {
                continue;
            }
            result.push(VueComponentEvent {
                name: name.clone(),
                file_path: self.definition_path.clone(),
                ..Default::default()
            });
        }
        result
    }

    pub fn component_event(&self, name: &str) -> Option<VueComponentEvent> {
        let canonical = canonical_event_name(name);
        self.component_events()
            .into_iter()
            .find(|event| canonical_event_name(&event.name) == canonical)
    }

    pub fn component_prop(&self, name: &str) -> Option<&VueComponentProp> {
        self.props.iter().find(|prop| prop.name == name)
    }

    /// Resolves an exact slot first, then the most specific dynamic family.
    pub fn component_slot(&self, name: &str) -> Option<&VueComponentSlot> {
        let mut best: Option<(usize, &VueComponentSlot)> = None;
        for slot in &self.slots {
            if slot.name == name && !name.is_empty() {
                return Some(slot);
            }
            if !slot.matches_name(name) {
                continue;
            }
            let specificity = slot.name_prefix.len() + slot.name_suffix.len();
            if best.is_none_or(|(current, _)| specificity > current) {
                best = Some((specificity, slot));
            }
        }
        best.map(|(_, slot)| slot)
    }

    /// The declaration source owning an effective prop, event or slot.
    pub fn symbol_source(&self, kind: AdminSymbolKind, name: &str) -> Option<String> {
        let owner = match kind {
            AdminSymbolKind::ComponentProp => {
                let prop = self.component_prop(name)?;
                first_non_empty(&[&prop.file_path, &self.definition_path, &self.file_path])
            }
            AdminSymbolKind::ComponentEvent => {
                let event = self.component_event(name)?;
                first_non_empty(&[&event.file_path, &self.definition_path, &self.file_path])
            }
            AdminSymbolKind::ComponentSlot => {
                let slot = self.component_slot(name)?;
                first_non_empty(&[&slot.file_path, &self.template_path])
            }
            _ => return None,
        };
        (!owner.is_empty()).then_some(owner)
    }
}

fn first_non_empty(values: &[&str]) -> String {
    values
        .iter()
        .find(|value| !value.is_empty())
        .map_or_else(String::new, |value| (*value).to_owned())
}

fn component_has_model_pair(component: &VueComponent, prop_name: &str, event_name: &str) -> bool {
    component.component_prop(prop_name).is_some() && component.component_event(event_name).is_some()
}

string_enum! {
    pub enum VueComponentMemberKind {
        Prop => "prop",
        Data => "data",
        Computed => "computed",
        Method => "method",
        Inject => "inject",
    }
}

/// A source-aware value available to an Administration component template.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueComponentMember {
    pub name: String,
    pub kind: VueComponentMemberKind,
    #[serde(rename = "type")]
    pub type_: String,
    pub file_path: String,
    pub line: u32,
    /// Normalized `@deprecated` documentation of this declaration.
    pub deprecated: String,
    /// Exact public-name declaration range.
    pub name_range: AdminSourceRange,
    pub binding_name: String,
    pub shorthand: bool,
    /// A safely reusable initial-value or return expression.
    pub source_expression: String,
    /// Every return value of a computed value or method.
    pub return_expressions: Vec<String>,
    /// Whether control flow proves the function always returns.
    pub returns_complete: bool,
    /// The structural type was inferred from a JavaScript object literal.
    pub open_runtime_shape: bool,
    /// The member is derived from one of Shopware's CMS registries.
    pub cms_registry_kind: AdminCMSRegistrationKind,
    /// Properties assigned to elements while iterating this member.
    pub element_members: Vec<VueComponentElementMember>,
    /// Declaration context in which `type_` must be resolved.
    pub type_context_path: String,
}

impl VueComponentMember {
    pub fn renameable(&self) -> bool {
        match self.kind {
            VueComponentMemberKind::Data
            | VueComponentMemberKind::Computed
            | VueComponentMemberKind::Method => {
                !self.name.is_empty()
                    && !self.file_path.is_empty()
                    && (self.name_range.end_line != 0
                        || self.name_range.end_character != 0
                        || self.name_range.start_line != 0
                        || self.name_range.start_character != 0)
            }
            _ => false,
        }
    }

    /// Distinguishes same-named members declared in one file; stable across
    /// inheritance, overrides and cache restoration.
    pub fn source_identity(&self) -> String {
        if self.file_path.is_empty() || self.name.is_empty() {
            return String::new();
        }
        format!(
            "{}\x00{}\x00{}:{}\x00{}",
            gopath::clean(&self.file_path),
            self.kind.as_str(),
            self.name_range.start_line,
            self.name_range.start_character,
            self.name
        )
    }
}

/// One component-local extension of a collection element.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueComponentElementMember {
    pub name: String,
    #[serde(rename = "type")]
    pub type_: String,
    pub file_path: String,
    pub line: u32,
}

/// One statically direct `this.member = value` write.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueComponentAssignment {
    pub target: String,
    pub expression: String,
    pub file_path: String,
    pub line: u32,
}

impl VueComponent {
    /// The effective Vue instance scope in a stable order, synthesizing
    /// source-aware entries from the legacy name lists.
    pub fn template_members(&self) -> Vec<VueComponentMember> {
        let mut result = self.members.clone();
        let mut positions: HashMap<String, usize> = HashMap::with_capacity(result.len());
        for (index, member) in result.iter().enumerate() {
            positions.insert(member.name.clone(), index);
        }
        fn add(
            result: &mut Vec<VueComponentMember>,
            positions: &mut HashMap<String, usize>,
            member: VueComponentMember,
        ) {
            if member.name.is_empty() {
                return;
            }
            if let Some(&index) = positions.get(&member.name) {
                result[index] = member;
                return;
            }
            positions.insert(member.name.clone(), result.len());
            result.push(member);
        }
        for prop in &self.props {
            let file_path = if prop.file_path.is_empty() {
                self.definition_path.clone()
            } else {
                prop.file_path.clone()
            };
            if let Some(&index) = positions.get(&prop.name) {
                let member = &mut result[index];
                member.kind = VueComponentMemberKind::Prop;
                if member.type_.is_empty() {
                    member.type_ = prop.type_.clone();
                }
                if member.type_context_path.is_empty() {
                    member.type_context_path = file_path;
                }
                if member.deprecated.is_empty() {
                    member.deprecated = prop.deprecated.clone();
                }
                continue;
            }
            add(
                &mut result,
                &mut positions,
                VueComponentMember {
                    name: prop.name.clone(),
                    kind: VueComponentMemberKind::Prop,
                    type_: prop.type_.clone(),
                    file_path: file_path.clone(),
                    line: prop.line,
                    deprecated: prop.deprecated.clone(),
                    name_range: prop.name_range.clone(),
                    type_context_path: file_path,
                    ..Default::default()
                },
            );
        }
        for (kind, names) in [
            (VueComponentMemberKind::Data, &self.data),
            (VueComponentMemberKind::Computed, &self.computed),
            (VueComponentMemberKind::Method, &self.methods),
            (VueComponentMemberKind::Inject, &self.injected),
        ] {
            for name in names {
                if positions.contains_key(name) {
                    continue;
                }
                add(
                    &mut result,
                    &mut positions,
                    VueComponentMember {
                        name: name.clone(),
                        kind,
                        file_path: self.definition_path.clone(),
                        ..Default::default()
                    },
                );
            }
        }
        result
    }

    pub fn template_member(&self, name: &str) -> Option<VueComponentMember> {
        self.template_members()
            .into_iter()
            .find(|member| member.name == name)
    }
}

fn builtin(name: &str, kind: VueComponentMemberKind, type_: &str) -> VueComponentMember {
    VueComponentMember {
        name: name.to_owned(),
        kind,
        type_: type_.to_owned(),
        ..Default::default()
    }
}

pub fn vue_builtin_members() -> Vec<VueComponentMember> {
    use VueComponentMemberKind::{Data as D, Method as M};
    vec![
        builtin("$emit", M, "(event: string, ...args: unknown[]) => void"),
        builtin("$forceUpdate", M, "() => void"),
        builtin("$nextTick", M, "(callback?: Function) => Promise<void>"),
        builtin(
            "$sanitize",
            M,
            "(html: string, config?: Record<string, unknown>) => string",
        ),
        builtin("$super", M, ""),
        builtin(
            "$t",
            M,
            "(key: string, values?: Record<string, unknown>, plural?: number) => string",
        ),
        builtin(
            "$tc",
            M,
            "(key: string, plural?: number, values?: Record<string, unknown>) => string",
        ),
        builtin("$te", M, "(key: string, locale?: string) => boolean"),
        builtin(
            "$watch",
            M,
            "(source: string | Function, callback: Function) => Function",
        ),
        builtin("$attrs", D, "Record<string, unknown>"),
        builtin("$createTitle", M, ""),
        builtin("$device", D, ""),
        builtin("$el", D, ""),
        builtin("$i18n", D, ""),
        builtin("$listeners", D, "Record<string, Function>"),
        builtin("$options", D, ""),
        builtin("$parent", D, ""),
        builtin("$props", D, "Record<string, unknown>"),
        builtin("$refs", D, "Record<string, unknown>"),
        builtin("$root", D, ""),
        builtin("$route", D, ""),
        builtin("$router", D, ""),
        builtin("$scopedSlots", D, "Record<string, Function | undefined>"),
        builtin("$slots", D, "Record<string, Function | undefined>"),
        builtin("$store", D, ""),
        builtin("$swLegacyBlockElse", D, ""),
        builtin("$swLegacyBlockElseIf", D, ""),
        builtin("$swLegacyBlockIf", D, ""),
    ]
}

pub fn is_vue_builtin_member(name: &str) -> bool {
    vue_builtin_member(name).is_some()
}

pub fn vue_builtin_member(name: &str) -> Option<VueComponentMember> {
    vue_builtin_members()
        .into_iter()
        .find(|member| member.name == name)
}

/// JavaScript values Vue exposes to template expressions without resolving
/// them through the component instance.
pub fn vue_template_globals() -> Vec<VueComponentMember> {
    use VueComponentMemberKind::{Data as D, Method as M};
    vec![
        builtin("Infinity", D, "number"),
        builtin("undefined", D, "undefined"),
        builtin("NaN", D, "number"),
        builtin("isFinite", M, "(value: unknown) => boolean"),
        builtin("isNaN", M, "(value: unknown) => boolean"),
        builtin("parseFloat", M, "(value: string) => number"),
        builtin("parseInt", M, "(value: string, radix?: number) => number"),
        builtin("decodeURI", M, "(value: string) => string"),
        builtin("decodeURIComponent", M, "(value: string) => string"),
        builtin("encodeURI", M, "(value: string) => string"),
        builtin("encodeURIComponent", M, "(value: string) => string"),
        builtin("Array", D, "ArrayConstructor"),
        builtin("BigInt", D, "BigIntConstructor"),
        builtin("Boolean", D, "BooleanConstructor"),
        builtin("Date", D, "DateConstructor"),
        builtin("Error", D, "ErrorConstructor"),
        builtin("Intl", D, "typeof Intl"),
        builtin("JSON", D, "JSON"),
        builtin("Map", D, "MapConstructor"),
        builtin("Math", D, "Math"),
        builtin("Number", D, "NumberConstructor"),
        builtin("Object", D, "ObjectConstructor"),
        builtin("RegExp", D, "RegExpConstructor"),
        builtin("Set", D, "SetConstructor"),
        builtin("String", D, "StringConstructor"),
        builtin("Symbol", D, "SymbolConstructor"),
        builtin("console", D, "Console"),
        builtin("Shopware", D, "ShopwareGlobal"),
        builtin("document", D, "Document"),
        builtin("window", D, "Window"),
    ]
}

pub fn vue_template_global(name: &str) -> Option<VueComponentMember> {
    vue_template_globals()
        .into_iter()
        .find(|global| global.name == name)
}

/// A slot definition in a component template.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueComponentSlot {
    /// Slot name, e.g. `default`; empty for dynamic families.
    pub name: String,
    /// Dynamic slot family prefix/suffix (e.g. `column-${...}`).
    pub name_prefix: String,
    pub name_suffix: String,
    /// 1-based declaration line.
    pub line: u32,
    /// Exact UTF-16 declaration range in `file_path`.
    pub name_range: AdminSourceRange,
    pub file_path: String,
    /// Complete scoped-slot payload type when declared.
    pub payload_type: String,
    pub members: Vec<VueComponentSlotMember>,
    /// Whether the declaration proves the entire payload shape.
    pub members_complete: bool,
}

impl VueComponentSlot {
    pub fn is_dynamic_name(&self) -> bool {
        self.name.is_empty() && (!self.name_prefix.is_empty() || !self.name_suffix.is_empty())
    }

    pub fn matches_name(&self, name: &str) -> bool {
        if !self.name.is_empty() {
            return self.name == name;
        }
        if !self.is_dynamic_name() || name.len() < self.name_prefix.len() + self.name_suffix.len() {
            return false;
        }
        name.starts_with(&self.name_prefix) && name.ends_with(&self.name_suffix)
    }

    pub fn display_name(&self) -> String {
        if !self.name.is_empty() {
            return self.name.clone();
        }
        if self.is_dynamic_name() {
            return format!("{}*{}", self.name_prefix, self.name_suffix);
        }
        String::new()
    }

    pub(crate) fn identity_key(&self) -> String {
        if !self.name.is_empty() {
            return format!("exact\x00{}", self.name);
        }
        format!("dynamic\x00{}\x00{}", self.name_prefix, self.name_suffix)
    }

    pub fn member(&self, name: &str) -> Option<&VueComponentSlotMember> {
        self.members.iter().find(|member| member.name == name)
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct VueComponentSlotMember {
    pub name: String,
    #[serde(rename = "type")]
    pub type_: String,
    pub file_path: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
}

/// A Twig block definition in a component template.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct TwigBlock {
    pub name: String,
    pub deprecated: String,
    /// 1-based declaration line.
    pub line: u32,
    pub name_range: AdminSourceRange,
    pub file_path: String,
    /// Lexical values supplied by the parent block context.
    pub scope_members: Vec<TwigBlockScopeMember>,
}

/// One lexical input available while rendering a Twig extensibility block.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct TwigBlockScopeMember {
    pub name: String,
    #[serde(rename = "type")]
    pub type_: String,
    pub file_path: String,
    pub line: u32,
    pub name_range: AdminSourceRange,
}

impl TwigBlock {
    pub fn scope_member(&self, name: &str) -> Option<&TwigBlockScopeMember> {
        self.scope_members.iter().find(|member| member.name == name)
    }
}

impl VueComponent {
    pub fn component_block(&self, name: &str) -> Option<&TwigBlock> {
        self.blocks.iter().find(|block| block.name == name)
    }
}
