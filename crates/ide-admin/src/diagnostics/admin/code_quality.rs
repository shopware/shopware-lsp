//! Administration code rules of Shopware's `admin-eslint-rules`: imports of
//! core sources, snippets passed to a module, overrides of the Extension
//! Manager, the deprecated Vuex `State`, and events missing from `emits`.

use std::collections::BTreeSet;
use std::sync::Arc;

use swlsp_admin::{AdminComponentIndexer, canonical_event_name};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{
    JS_ARGUMENT, JS_ARRAY, JS_CALL_EXPRESSION, JS_EXPORT_DEFAULT, JS_IDENTIFIER,
    JS_IMPORT_STATEMENT, JS_MEMBER_EXPRESSION, JS_OBJECT, JS_PROPERTY, JS_STRING,
    JS_VARIABLE_DECLARATION, TK_OPERATOR,
};
use swlsp_parser_twig::syntax as twigsyntax;
use swlsp_shopware::SupportedVersions;
use swlsp_syntax::{SyntaxElement, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

use super::{payload, text};
use crate::diagnostics::ProblemAnalyzer;

pub const ADMIN_CORE_IMPORT_CODE: &str = "admin.import.core-source";
pub const ADMIN_MODULE_SNIPPETS_CODE: &str = "admin.module.snippets-import";
pub const ADMIN_EXTENSION_MANAGER_OVERRIDE_CODE: &str =
    "admin.component.extension-manager-override";
pub const ADMIN_STATE_DEPRECATED_CODE: &str = "admin.state.deprecated";
pub const ADMIN_UNDECLARED_EMIT_CODE: &str = "admin.component.undeclared-emit";

/// Every code of [`AdminCodeQualityAnalyzer`].
pub const ADMIN_CODE_QUALITY_CODES: &[&str] = &[
    ADMIN_CORE_IMPORT_CODE,
    ADMIN_MODULE_SNIPPETS_CODE,
    ADMIN_EXTENSION_MANAGER_OVERRIDE_CODE,
    ADMIN_STATE_DEPRECATED_CODE,
    ADMIN_UNDECLARED_EMIT_CODE,
];

/// The calls a component or mixin configuration object is passed to.
const COMPONENT_REGISTRATIONS: &[&str] = &[
    "Component.register",
    "Component.extend",
    "Component.override",
    "Component.wrapComponentConfig",
    "Shopware.Component.register",
    "Shopware.Component.extend",
    "Shopware.Component.override",
    "Shopware.Component.wrapComponentConfig",
    "Mixin.register",
    "Shopware.Mixin.register",
];

pub struct AdminCodeQualityAnalyzer {
    index: Option<Arc<AdminComponentIndexer>>,
    /// Vuex is deprecated and `emits` is required from Shopware 6.7 on.
    versions: SupportedVersions,
}

impl AdminCodeQualityAnalyzer {
    pub fn new(
        index: Option<Arc<AdminComponentIndexer>>,
        versions: impl Into<SupportedVersions>,
    ) -> Self {
        Self {
            index,
            versions: versions.into(),
        }
    }
}

impl ProblemAnalyzer for AdminCodeQualityAnalyzer {
    fn input_revision(&self, document: &TextDocument) -> Option<u64> {
        if !document.uri.contains("Resources/app/administration") {
            Some(0)
        } else {
            Some(crate::diagnostics::inputs(&self.index))
        }
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if !document.uri.contains("Resources/app/administration") {
            return Ok(Vec::new());
        }
        // The Administration itself imports its own sources and maintains
        // the Extension Manager; those rules are about extensions.
        let core =
            lowercase(&document.uri).contains("/administration/resources/app/administration/");
        let vue3_only = self.versions.installed().at_least(6, 7, 0);
        let mut result = Vec::new();
        match lowercase(filepath::ext(&document.uri)).as_str() {
            ".js" | ".ts" => {
                if !core {
                    core_imports(&root, &mut result);
                    for call in jsquery::iter_calls(&root, &[]) {
                        ctx.check()?;
                        module_snippets(&call, &mut result);
                        extension_manager_override(&call, &mut result);
                    }
                }
                // The Pinia stores replace Vuex from 6.7 on: an extension
                // that still supports 6.6 keeps State. Declaring emits is
                // valid on every Vue 3 Administration.
                if self
                    .versions
                    .minimum_for_uri(&document.uri)
                    .at_least(6, 7, 0)
                {
                    deprecated_state(&root, &mut result);
                }
                if vue3_only {
                    self.script_emits(document, &root, &mut result);
                }
            }
            ".twig" if vue3_only => self.template_emits(document, &root, &mut result),
            _ => {}
        }
        Ok(result)
    }
}

fn problem(code: &str, node: &SyntaxNode, message: String) -> Problem {
    Problem {
        id: code.into(),
        range: node.range_trimmed_trivia(),
        element: Some(SyntaxElement::Node(node.clone())),
        message,
        severity: Some(DiagnosticSeverity::WARNING),
        source: "shopware-lsp".into(),
        ..Problem::default()
    }
}

/// Argument `index` when it is a string literal and nothing else.
fn literal_argument(call: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    jsquery::argument_expression(call, index).filter(|argument| {
        // A template string with an interpolation is no fixed name.
        argument.kind() == JS_STRING && !trimmed(argument).contains("${")
    })
}

fn trimmed(node: &SyntaxNode) -> String {
    node.text().to_string().trim().to_owned()
}

/// `import … from '@administration/…'`: the core sources are not part of an
/// extension's bundle.
fn core_imports(root: &SyntaxNode, result: &mut Vec<Problem>) {
    for import in jsquery::nodes(root, &[JS_IMPORT_STATEMENT]) {
        let Some(source) = import.children().find(|child| child.kind() == JS_STRING) else {
            continue;
        };
        let path = jsquery::string_value(&source);
        if path.starts_with("@administration/") {
            result.push(problem(
                ADMIN_CORE_IMPORT_CODE,
                &source,
                format!(
                    "\"{path}\" imports a file of the Shopware core, which an extension's bundle does not contain; use the global Shopware object instead"
                ),
            ));
        }
    }
}

/// `Module.register(name, { snippets: … })`: snippets below a `snippet`
/// directory are loaded on their own.
fn module_snippets(call: &SyntaxNode, result: &mut Vec<Problem>) {
    if !matches!(
        jsquery::call_name(call).as_deref(),
        Some("Shopware.Module.register" | "Module.register")
    ) {
        return;
    }
    let Some(options) = jsquery::object_argument(call, 1) else {
        return;
    };
    let Some(snippets) = jsquery::property(&options, "snippets") else {
        return;
    };
    let name = jsquery::property_name_node(&snippets).unwrap_or(snippets);
    result.push(problem(
        ADMIN_MODULE_SNIPPETS_CODE,
        &name,
        "Snippets passed to Module.register() are bundled with the module; files in a directory named snippet are loaded automatically".to_owned(),
    ));
}

/// `Component.override('sw-extension-…')`: the Extension Manager must not be
/// changed.
fn extension_manager_override(call: &SyntaxNode, result: &mut Vec<Problem>) {
    if !matches!(
        jsquery::call_name(call).as_deref(),
        Some("Shopware.Component.override" | "Component.override")
    ) {
        return;
    }
    let Some(name) = literal_argument(call, 0) else {
        return;
    };
    if jsquery::string_value(&name).starts_with("sw-extension-") {
        result.push(problem(
            ADMIN_EXTENSION_MANAGER_OVERRIDE_CODE,
            &name,
            "Changing the Shopware Extension Manager is not allowed".to_owned(),
        ));
    }
}

// -- Shopware.State -------------------------------------------------------

/// Whether the file binds `State` with `const { State } = Shopware`.
fn destructured_state(root: &SyntaxNode) -> Vec<SyntaxNode> {
    let mut names = Vec::new();
    for declaration in jsquery::nodes(root, &[JS_VARIABLE_DECLARATION]) {
        let mut children = declaration.children();
        let (Some(pattern), Some(value)) = (children.next(), children.next()) else {
            continue;
        };
        if pattern.kind() != JS_OBJECT
            || value.kind() != JS_IDENTIFIER
            || trimmed(&value) != "Shopware"
        {
            continue;
        }
        names.extend(pattern.children().filter_map(|property| {
            let name = property.children().next()?;
            (property.kind() == JS_PROPERTY
                && name.kind() == JS_IDENTIFIER
                && trimmed(&name) == "State"
                && property.children().count() == 1)
                .then_some(name)
        }));
    }
    names
}

fn deprecated_state(root: &SyntaxNode, result: &mut Vec<Problem>) {
    let destructured = destructured_state(root);
    for name in &destructured {
        result.push(problem(
            ADMIN_STATE_DEPRECATED_CODE,
            name,
            "Shopware.State (Vuex) is deprecated and removed in Shopware 6.8; use Shopware.Store"
                .to_owned(),
        ));
    }
    let mut reported: Vec<TextRange> = Vec::new();
    for call in jsquery::iter_calls(root, &[]) {
        let name = jsquery::call_name(&call).unwrap_or_default();
        let on_state = match name.as_str() {
            "Shopware.State.commit" => true,
            "State.commit" => !destructured.is_empty(),
            _ => false,
        };
        if !on_state {
            continue;
        }
        let Some(callee) = jsquery::call_callee(&call) else {
            continue;
        };
        reported.push(callee.text_range());
        let mut found = problem(
            ADMIN_STATE_DEPRECATED_CODE,
            &call,
            "State.commit() is deprecated and removed in Shopware 6.8; call the action on Shopware.Store.get()"
                .to_owned(),
        );
        if let Some(replacement) = commit_replacement(&call) {
            found.payload = Some(rewrite_payload(
                "Call the store action on Shopware.Store.get()",
                &call,
                &replacement,
            ));
        }
        result.push(found);
    }
    for member in jsquery::nodes(root, &[JS_MEMBER_EXPRESSION]) {
        if trimmed(&member) != "Shopware.State"
            || reported
                .iter()
                .any(|range| range.contains_range(member.text_range()))
        {
            continue;
        }
        let name = jsquery::member_name_node(&member).unwrap_or(member);
        result.push(problem(
            ADMIN_STATE_DEPRECATED_CODE,
            &name,
            "Shopware.State (Vuex) is deprecated and removed in Shopware 6.8; use Shopware.Store"
                .to_owned(),
        ));
    }
    for property in jsquery::nodes(root, &[JS_PROPERTY]) {
        let Some(call) = spread_call(&property) else {
            continue;
        };
        let helper = jsquery::call_name(&call).unwrap_or_default();
        // Pinia has helpers of the same names; only Vuex takes the store
        // as a namespace string.
        if (helper != "mapState" && helper != "mapGetters") || literal_argument(&call, 0).is_none()
        {
            continue;
        }
        let mut found = problem(
            ADMIN_STATE_DEPRECATED_CODE,
            &property,
            format!(
                "Vuex {helper}() is deprecated and removed in Shopware 6.8; define computed properties that read Shopware.Store.get()"
            ),
        );
        if let Some(replacement) = map_helper_replacement(&property, &call) {
            found.payload = Some(rewrite_payload(
                "Replace with computed properties reading Shopware.Store.get()",
                &property,
                &replacement,
            ));
        }
        result.push(found);
    }
}

/// The call a `...call()` object entry spreads.
fn spread_call(property: &SyntaxNode) -> Option<SyntaxNode> {
    let spread = property
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| !token.kind().is_trivia())?;
    if spread.kind() != TK_OPERATOR || spread.text() != "..." {
        return None;
    }
    property
        .children()
        .next()
        .filter(|child| child.kind() == JS_CALL_EXPRESSION)
}

/// `State.commit('store/action', a, b)` as `Shopware.Store.get('store').action(a, b)`.
fn commit_replacement(call: &SyntaxNode) -> Option<String> {
    let target = jsquery::string_value(&literal_argument(call, 0)?);
    let (store, action) = target.split_once('/')?;
    if store.is_empty() || !is_identifier(action) || store.contains('\'') {
        return None;
    }
    let arguments: Vec<String> = jsquery::iter_arguments(call)
        .skip(1)
        .map(|argument| trimmed(&argument))
        .collect();
    Some(format!(
        "Shopware.Store.get('{store}').{action}({})",
        arguments.join(", ")
    ))
}

/// `...mapState('store', ['a', 'b'])` (or `{ local: 'a' }`) as computed
/// properties. A mapping through a function is left to the developer.
fn map_helper_replacement(property: &SyntaxNode, call: &SyntaxNode) -> Option<String> {
    let store = jsquery::string_value(&literal_argument(call, 0)?);
    if store.is_empty() || store.contains('\'') || jsquery::iter_arguments(call).count() != 2 {
        return None;
    }
    let mapping = jsquery::argument_expression(call, 1)?;
    let mut entries: Vec<(String, String)> = Vec::new();
    match mapping.kind() {
        JS_ARRAY => {
            for item in mapping.children() {
                if item.kind() != JS_STRING {
                    return None;
                }
                let name = jsquery::string_value(&item);
                entries.push((name.clone(), name));
            }
        }
        JS_OBJECT => {
            for entry in mapping.children() {
                let value =
                    jsquery::property_value(&entry).filter(|value| value.kind() == JS_STRING)?;
                entries.push((
                    jsquery::property_name(&entry)?,
                    jsquery::string_value(&value),
                ));
            }
        }
        _ => return None,
    }
    if entries.is_empty()
        || entries
            .iter()
            .any(|(local, mapped)| !is_identifier(local) || !is_identifier(mapped))
    {
        return None;
    }
    let indent = line_indent(property);
    let methods: Vec<String> = entries
        .iter()
        .map(|(local, mapped)| {
            format!(
                "{local}() {{\n{indent}    return Shopware.Store.get('{store}').{mapped};\n{indent}}}"
            )
        })
        .collect();
    Some(methods.join(&format!(",\n{indent}")))
}

fn is_identifier(value: &str) -> bool {
    let mut characters = value.chars();
    characters
        .next()
        .is_some_and(|first| first.is_alphabetic() || first == '_' || first == '$')
        && characters
            .all(|character| character.is_alphanumeric() || character == '_' || character == '$')
}

/// The whitespace `node` is indented with on its line.
pub(crate) fn line_indent(node: &SyntaxNode) -> String {
    let start = usize::from(node.range_trimmed_trivia().start());
    let source = node
        .ancestors()
        .last()
        .map(|root| root.text().to_string())
        .unwrap_or_default();
    let line_start = source[..start.min(source.len())]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    source[line_start..start.min(source.len())]
        .chars()
        .take_while(|character| *character == ' ' || *character == '\t')
        .collect()
}

/// The payload of a fix that replaces the reported node with `replacement`.
fn rewrite_payload(title: &str, node: &SyntaxNode, replacement: &str) -> serde_json::Value {
    payload(vec![
        ("title", text(title)),
        ("expected", text(&trimmed(node))),
        ("replacement", text(replacement)),
    ])
}

// -- emits ----------------------------------------------------------------

/// The component (or mixin) configuration object `node` belongs to, and the
/// registration call it is passed to (none for `export default`).
pub(crate) fn component_object(node: &SyntaxNode) -> Option<(SyntaxNode, Option<SyntaxNode>)> {
    node.ancestors().find_map(|object| {
        if object.kind() != JS_OBJECT {
            return None;
        }
        let parent = object.parent()?;
        if parent.kind() == JS_EXPORT_DEFAULT {
            return Some((object, None));
        }
        let call = (parent.kind() == JS_ARGUMENT)
            .then(|| jsquery::call_at(&parent))
            .flatten()?;
        COMPONENT_REGISTRATIONS
            .contains(&jsquery::call_name(&call).unwrap_or_default().as_str())
            .then_some((object, Some(call)))
    })
}

/// The events an `emits` option declares; `None` when it cannot be read
/// (a variable, a spread).
fn own_declared_emits(object: &SyntaxNode) -> Option<BTreeSet<String>> {
    let Some(property) = jsquery::property(object, "emits") else {
        return Some(BTreeSet::new());
    };
    let value = jsquery::property_value(&property)?;
    match value.kind() {
        JS_ARRAY => value
            .children()
            .map(|item| (item.kind() == JS_STRING).then(|| jsquery::string_value(&item)))
            .collect(),
        JS_OBJECT => value
            .children()
            .map(|entry| jsquery::property_name(&entry))
            .collect(),
        _ => None,
    }
}

fn declares(declared: &BTreeSet<String>, event: &str) -> bool {
    let canonical = canonical_event_name(event);
    declared
        .iter()
        .any(|name| name == event || canonical_event_name(name) == canonical)
}

impl AdminCodeQualityAnalyzer {
    /// What the component declares beyond its own object: its parents, its
    /// mixins and its other registrations. `None` when that cannot be told.
    fn inherited_emits(
        &self,
        document: &TextDocument,
        object: &SyntaxNode,
        registration: Option<&SyntaxNode>,
    ) -> Option<BTreeSet<String>> {
        let mut declared = BTreeSet::new();
        let mut components = Vec::new();
        if let Some(index) = &self.index {
            let name = registration.and_then(|call| literal_argument(call, 0));
            let mixin = registration.is_some_and(|call| {
                jsquery::call_name(call).is_some_and(|name| name.contains("Mixin"))
            });
            match name {
                _ if mixin => {}
                Some(name) => {
                    let name = jsquery::string_value(&name);
                    components.extend(index.get_effective_component(&name).ok().flatten());
                }
                // `export default {…}` and `wrapComponentConfig({…})`: the
                // components registered with this file as their definition.
                None => {
                    let path = swlsp_uriutil::path(&document.uri).ok()?;
                    components = index.get_components_by_definition_path(&path).ok()?;
                }
            }
        }
        for component in &components {
            declared.extend(component.declared_emits.iter().cloned());
        }
        // Without the index entry, an object that takes members from
        // elsewhere says nothing about what it may emit.
        let inherits = registration.is_some_and(|call| {
            jsquery::call_name(call)
                .is_some_and(|name| name.ends_with(".extend") || name.ends_with(".override"))
        }) || jsquery::property(object, "mixins").is_some()
            || jsquery::property(object, "extends").is_some();
        if inherits && components.is_empty() {
            return None;
        }
        Some(declared)
    }

    /// `this.$emit('event')` in a component whose `emits` does not list it.
    fn script_emits(&self, document: &TextDocument, root: &SyntaxNode, result: &mut Vec<Problem>) {
        for call in jsquery::iter_calls(root, &[]) {
            if jsquery::call_name(&call).as_deref() != Some("this.$emit") {
                continue;
            }
            let Some(event) = literal_argument(&call, 0) else {
                continue;
            };
            let name = jsquery::string_value(&event);
            let Some((object, registration)) = component_object(&call) else {
                continue;
            };
            let Some(mut declared) = own_declared_emits(&object) else {
                continue;
            };
            let Some(inherited) = self.inherited_emits(document, &object, registration.as_ref())
            else {
                continue;
            };
            declared.extend(inherited);
            if name.is_empty() || declares(&declared, &name) {
                continue;
            }
            let mut found = problem(
                ADMIN_UNDECLARED_EMIT_CODE,
                &event,
                format!("Event '{name}' is emitted but not declared in the emits option"),
            );
            found.payload = Some(payload(vec![("event", text(&name))]));
            result.push(found);
        }
    }

    /// `$emit('event')` in the template of a component whose `emits` does
    /// not list it.
    fn template_emits(
        &self,
        document: &TextDocument,
        root: &SyntaxNode,
        result: &mut Vec<Problem>,
    ) {
        let Some(index) = &self.index else {
            return;
        };
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return;
        };
        let Ok(Some(component)) = index.get_component_by_template_path(&path) else {
            return;
        };
        let Ok(Some(effective)) = index.get_effective_component(&component.name) else {
            return;
        };
        let declared: BTreeSet<String> = effective.declared_emits.iter().cloned().collect();
        for value in root
            .descendants()
            .filter(|node| node.kind() == twigsyntax::HTML_STRING_INNER)
        {
            let expression = value.text().to_string();
            // Only a directive or binding holds code; any other attribute
            // value is text.
            let bound = value
                .ancestors()
                .find(|node| node.kind() == twigsyntax::HTML_ATTRIBUTE)
                .is_some_and(|attribute| {
                    let name = attribute.text().to_string();
                    let name = name.trim_start();
                    name.starts_with(['@', ':', '#']) || name.starts_with("v-")
                });
            if !bound || !expression.contains("$emit") {
                continue;
            }
            let base = value.text_range().start();
            let parsed = swlsp_parser_js::parse(&expression);
            for call in jsquery::iter_calls(&parsed.root(), &[]) {
                if jsquery::call_name(&call).as_deref() != Some("$emit") {
                    continue;
                }
                let Some(event) = literal_argument(&call, 0) else {
                    continue;
                };
                let name = jsquery::string_value(&event);
                if name.is_empty() || declares(&declared, &name) {
                    continue;
                }
                let range = event.range_trimmed_trivia();
                result.push(Problem {
                    id: ADMIN_UNDECLARED_EMIT_CODE.into(),
                    range: TextRange::new(
                        base + TextSize::from(u32::from(range.start())),
                        base + TextSize::from(u32::from(range.end())),
                    ),
                    element: Some(SyntaxElement::Node(value.clone())),
                    message: format!(
                        "Event '{name}' is emitted but not declared in the emits option of {}",
                        component.name
                    ),
                    severity: Some(DiagnosticSeverity::WARNING),
                    source: "shopware-lsp".into(),
                    ..Problem::default()
                });
            }
        }
    }
}
