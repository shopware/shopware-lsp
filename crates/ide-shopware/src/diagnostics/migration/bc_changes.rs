//! Signature changes Shopware announces through its `BCChange` attributes.
//!
//! The attributes sit on the methods of the indexed Shopware source, so the
//! changes are read from the PHP index instead of a copied table: an
//! override must follow a narrowed return type, a widened parameter type or
//! a new optional parameter, and a constructor call that relies on a default
//! about to change should pass the current one.

use std::collections::HashSet;

use swlsp_lsp::{Context, Problem};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_ARGUMENT_LIST, PHP_NAMED_ARGUMENT, TK_ELLIPSIS};
use swlsp_php_semantic::{
    Attribute, AttributeValue, AttributeValueKind, Flags, Snapshot, Symbol, SymbolKind,
};
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::api::ClassMove;
use super::{
    ShopwareMigrationAnalyzer, ShopwareMigrationPayload, call_name_range, migration_problem, scan,
};

const ADD_METHOD_PARAMETER_CODE: &str = "shopware.migration.declaration.parameter.add";
const NATIVE_TYPE_MIGRATION_CODE: &str = "shopware.migration.declaration.type";
const ADD_DEFAULT_ARGUMENT_CODE: &str = "shopware.migration.arguments.add_default";

const BC_CHANGE_NAMESPACE: &str = "shopware\\core\\framework\\deprecation\\bcchange\\";

/// The announced changes of one method, by the short name of the attribute.
pub(super) fn bc_changes(method: &Symbol) -> impl Iterator<Item = (String, &Attribute)> {
    method.attributes().iter().filter_map(|attribute| {
        let name = lowercase(attribute.name.trim_matches('\\'));
        let kind = name.strip_prefix(BC_CHANGE_NAMESPACE)?.to_owned();
        Some((kind, attribute))
    })
}

impl ShopwareMigrationAnalyzer {
    pub(super) fn bc_change_problems(
        &self,
        ctx: &Context,
        root: &SyntaxNode,
        snapshot: Option<&Snapshot>,
    ) -> Vec<Problem> {
        let Some(snapshot) = snapshot else {
            return Vec::new();
        };
        let namespace = scan::namespace(root);
        let mut result = Vec::new();
        for class in scan::classes(root) {
            if ctx.is_cancelled() {
                return result;
            }
            let name = phpquery::class_name(&class);
            if name.is_empty() {
                continue;
            }
            let class_name = if namespace.is_empty() {
                name
            } else {
                format!("{namespace}\\{name}")
            };
            let ancestors = ancestors(snapshot, &class_name);
            if ancestors.is_empty() {
                continue;
            }
            for method in phpquery::methods(&class) {
                let method_name = phpquery::method_name(&method);
                // A constructor is not bound to the signature it overrides.
                if eq_ignore_case(&method_name, "__construct") {
                    continue;
                }
                let Some(own) = own_method(snapshot, &class_name, &method_name) else {
                    continue;
                };
                for ancestor in &ancestors {
                    for inherited in methods(snapshot, ancestor, &method_name) {
                        for (kind, attribute) in bc_changes(&inherited) {
                            let change = Override {
                                attribute,
                                inherited: &inherited,
                                inherited_class: ancestor,
                                own: &own,
                                own_class: &class_name,
                                node: &method,
                            };
                            result.extend(change.problem(&kind));
                        }
                    }
                }
            }
        }
        result.extend(changed_default_problems(ctx, root, snapshot));
        result
    }
}

/// An override (`own`, at `node`) of a method with an announced change.
struct Override<'a> {
    attribute: &'a Attribute,
    inherited: &'a Symbol,
    inherited_class: &'a str,
    own: &'a Symbol,
    own_class: &'a str,
    node: &'a SyntaxNode,
}

impl Override<'_> {
    fn problem(&self, kind: &str) -> Option<Problem> {
        // An override that announces the change itself is staging it.
        if bc_changes(self.own).any(|(own, _)| own == kind) {
            return None;
        }
        let label = version_label(self.attribute)?;
        let method = &self.inherited.name;
        match kind {
            "returntypenarrowing" => {
                let ty = type_source(self.attribute.argument("newType", 1)?)?;
                if !self.follows(&self.own.native_type, &self.inherited.native_type, &ty) {
                    return None;
                }
                Some(declaration_problem(
                    NATIVE_TYPE_MIGRATION_CODE,
                    self.node,
                    format!("Shopware {label}: use {ty} as {method}() return type"),
                    "return-type",
                    phpquery::method_return_type(self.node),
                    ty,
                    -1,
                ))
            }
            "parametertypewidening" => {
                let parameter = string_argument(self.attribute, "parameterName", 1)?;
                let ty = type_source(self.attribute.argument("newType", 2)?)?;
                let inherited = parameter_named(self.inherited, &parameter)?;
                let own = parameter_named(self.own, &parameter)?;
                if !self.follows(&own.1.native_type, &inherited.1.native_type, &ty) {
                    return None;
                }
                let node = phpquery::parameters(self.node).nth(own.0)?;
                Some(declaration_problem(
                    NATIVE_TYPE_MIGRATION_CODE,
                    &node,
                    format!("Shopware {label}: use {ty} for parameter ${parameter}"),
                    "parameter-type",
                    phpquery::parameter_type(&node),
                    ty,
                    own.0 as i64,
                ))
            }
            "newoptionalparameter" => {
                let parameter = string_argument(self.attribute, "parameterName", 1)?;
                let ty = type_source(self.attribute.argument("parameterType", 2)?)?;
                let default = self
                    .attribute
                    .argument("defaultValue", 3)
                    .map_or_else(|| "null".to_owned(), value_source);
                let declared = phpquery::parameters(self.node).count();
                if self.own.parameters.iter().any(|own| {
                    own.flags.has(Flags::VARIADIC) || is_parameter_name(&own.name, &parameter)
                }) {
                    return None;
                }
                // The parameter follows the ones the method declares today.
                let position = self
                    .inherited
                    .parameters
                    .iter()
                    .filter(|inherited| !inherited.flags.has(Flags::SYNTHETIC))
                    .count()
                    .min(declared);
                Some(declaration_problem(
                    ADD_METHOD_PARAMETER_CODE,
                    self.node,
                    format!("Shopware {label}: add ${parameter} to {method}()"),
                    "parameter-add",
                    String::new(),
                    format!("{ty} ${parameter} = {default}"),
                    position as i64,
                ))
            }
            _ => None,
        }
    }

    /// Whether the override still declares the type the inherited method
    /// declares today, and not yet the announced one.
    fn follows(&self, own: &Type, inherited: &Type, announced: &str) -> bool {
        let own = alternatives(&own.text(), self.own_class);
        own == alternatives(&inherited.text(), self.inherited_class)
            && own != alternatives(announced, self.own_class)
    }
}

/// `new Foo()` calls that omit a parameter whose default is about to change.
fn changed_default_problems(ctx: &Context, root: &SyntaxNode, snapshot: &Snapshot) -> Vec<Problem> {
    let resolver = scan::resolver(root);
    let mut result = Vec::new();
    for creation in phpquery::object_creations(root, &[]) {
        if ctx.is_cancelled() {
            return result;
        }
        let class_name = phpquery::object_class_name(&creation);
        if class_name.is_empty() {
            continue;
        }
        let resolved = resolver.resolve(&class_name);
        // Only the class's own constructor: a subclass may declare another.
        for constructor in methods(snapshot, resolved.trim_matches('\\'), "__construct") {
            for (kind, attribute) in bc_changes(&constructor) {
                if kind != "parameterdefaultvaluechange" {
                    continue;
                }
                let (Some(label), Some(parameter)) = (
                    version_label(attribute),
                    string_argument(attribute, "parameterName", 1),
                ) else {
                    continue;
                };
                let Some((position, declared)) = parameter_named(&constructor, &parameter) else {
                    continue;
                };
                let Some(default) = declared.default_value.as_deref().map(value_source) else {
                    continue;
                };
                if passes_argument(&creation, position, &parameter) {
                    continue;
                }
                result.push(migration_problem(
                    ADD_DEFAULT_ARGUMENT_CODE,
                    call_name_range(&creation),
                    &creation,
                    format!(
                        "Shopware {label}: the default of ${parameter} changes; pass {default} \
                         to keep the current behavior"
                    ),
                    &ShopwareMigrationPayload {
                        rule: "argument-migration".into(),
                        kind: "named-argument".into(),
                        safe: phpquery::direct_child(&creation, PHP_ARGUMENT_LIST).is_some(),
                        value: format!("{parameter}: {default}"),
                        ..ShopwareMigrationPayload::default()
                    },
                ));
            }
        }
    }
    result
}

/// The move of `name`, an alias of a class that announces it was `name`.
pub(super) fn announced_class_move(snapshot: &Snapshot, name: &str) -> Option<ClassMove> {
    let alias = snapshot
        .class_views(name)
        .into_iter()
        .find(|view| view.flags().has(Flags::CLASS_ALIAS))?
        .materialize();
    let [target] = alias.extends() else {
        return None;
    };
    let target = target.trim_matches('\\');
    for class in snapshot.classes(target) {
        for (kind, attribute) in bc_changes(&class) {
            let previous = string_argument(attribute, "previousClassName", 1);
            if kind == "classmoved"
                && previous
                    .is_some_and(|previous| eq_ignore_case(previous.trim_matches('\\'), name))
            {
                return Some(ClassMove {
                    label: version_label(attribute)?,
                    from: name.to_owned(),
                    to: target.to_owned(),
                });
            }
        }
    }
    None
}

/// The supertypes of `class`, nearest first.
fn ancestors(snapshot: &Snapshot, class: &str) -> Vec<String> {
    let supertypes = |name: &str| -> Vec<String> {
        snapshot
            .classes(name)
            .iter()
            .flat_map(|symbol| symbol.extends().iter().chain(symbol.implements()))
            .map(|name| name.trim_matches('\\').to_owned())
            .collect()
    };
    let mut seen = HashSet::from([lowercase(class)]);
    let mut result = Vec::new();
    let mut queue = supertypes(class);
    queue.reverse();
    while let Some(name) = queue.pop() {
        if !seen.insert(lowercase(&name)) {
            continue;
        }
        let mut next = supertypes(&name);
        next.reverse();
        queue.splice(0..0, next);
        result.push(name);
    }
    result
}

/// The methods named `method` that `class` itself declares.
pub(super) fn methods(snapshot: &Snapshot, class: &str, method: &str) -> Vec<Symbol> {
    snapshot
        .classes(class)
        .iter()
        .flat_map(|class| snapshot.members(class.id.as_str(), method))
        .filter(|member| member.kind == SymbolKind::Method)
        .collect()
}

fn own_method(snapshot: &Snapshot, class: &str, method: &str) -> Option<Symbol> {
    methods(snapshot, class, method).into_iter().next()
}

pub(super) fn is_parameter_name(declared: &str, name: &str) -> bool {
    eq_ignore_case(declared.trim_start_matches('$'), name)
}

pub(super) fn parameter_named<'a>(
    method: &'a Symbol,
    name: &str,
) -> Option<(usize, &'a swlsp_php_semantic::Parameter)> {
    method
        .parameters
        .iter()
        .enumerate()
        .find(|(_, parameter)| is_parameter_name(&parameter.name, name))
}

pub(super) fn string_argument(attribute: &Attribute, name: &str, index: usize) -> Option<String> {
    let value = attribute.argument(name, index)?;
    (value.kind == AttributeValueKind::String && !value.value.is_empty())
        .then(|| value.value.trim_start_matches('$').to_owned())
}

/// `6.8` for `version: 'v6.8.0'`.
pub(super) fn version_label(attribute: &Attribute) -> Option<String> {
    let version = string_argument(attribute, "version", 0)?;
    let mut parts = version.trim_start_matches(['v', 'V']).split('.');
    let (major, minor) = (parts.next()?, parts.next()?);
    Some(match parts.next() {
        Some(patch) if patch != "0" => format!("{major}.{minor}.{patch}"),
        _ => format!("{major}.{minor}"),
    })
}

const TYPE_KEYWORDS: &[&str] = &[
    "array", "bool", "callable", "false", "float", "int", "iterable", "mixed", "never", "null",
    "object", "parent", "self", "static", "string", "true", "void",
];

/// The PHP source of an announced type, with fully qualified class names.
/// `None` for what is no plain or union type.
pub(super) fn type_source(value: &AttributeValue) -> Option<String> {
    let text = match value.kind {
        AttributeValueKind::String => value.value.to_string(),
        AttributeValueKind::ClassConstant => {
            let (class, member) = value.value.rsplit_once("::")?;
            eq_ignore_case(member, "class").then(|| class.to_owned())?
        }
        _ => return None,
    };
    let text = text.trim();
    if text.is_empty() || text.contains(['&', '(', ' ']) {
        return None;
    }
    let (text, nullable) = match text.strip_prefix('?') {
        Some(rest) => (rest, "?"),
        None => (text, ""),
    };
    let parts: Vec<String> = text
        .split('|')
        .map(|part| {
            if TYPE_KEYWORDS.contains(&lowercase(part).as_str()) {
                lowercase(part)
            } else {
                format!("\\{}", part.trim_matches('\\'))
            }
        })
        .collect();
    Some(format!("{nullable}{}", parts.join("|")))
}

/// The alternatives of a native type, comparable whatever their order and
/// the spelling of `null`; `class` itself reads as `self`.
fn alternatives(type_text: &str, class: &str) -> Vec<String> {
    let type_text = type_text.trim();
    let (type_text, nullable) = match type_text.strip_prefix('?') {
        Some(rest) => (rest, true),
        None => (type_text, false),
    };
    let mut result: Vec<String> = type_text
        .split('|')
        .map(|part| lowercase(part.trim().trim_matches('\\')))
        .filter(|part| !part.is_empty() && part != "unknown")
        .map(|part| {
            if eq_ignore_case(&part, class.trim_matches('\\')) {
                "self".to_owned()
            } else {
                part
            }
        })
        .collect();
    if nullable {
        result.push("null".to_owned());
    }
    result.sort();
    result.dedup();
    result
}

/// The PHP source of a constant value, with fully qualified class names.
fn value_source(value: &AttributeValue) -> String {
    match value.kind {
        AttributeValueKind::String => {
            format!(
                "'{}'",
                value.value.replace('\\', "\\\\").replace('\'', "\\'")
            )
        }
        AttributeValueKind::ClassConstant => {
            let receiver = value.value.split("::").next().unwrap_or_default();
            if ["self", "static", "parent"].contains(&lowercase(receiver).as_str()) {
                value.expression.to_string()
            } else {
                format!("\\{}", value.value.trim_matches('\\'))
            }
        }
        AttributeValueKind::Array => {
            let items: Vec<String> = value
                .items
                .iter()
                .map(|item| {
                    if item.has_key {
                        format!(
                            "{} => {}",
                            value_source(&item.key),
                            value_source(&item.value)
                        )
                    } else {
                        value_source(&item.value)
                    }
                })
                .collect();
            format!("[{}]", items.join(", "))
        }
        _ => value.expression.to_string(),
    }
}

fn declaration_problem(
    code: &str,
    element: &SyntaxNode,
    message: String,
    kind: &str,
    original: String,
    replacement: String,
    position: i64,
) -> Problem {
    migration_problem(
        code,
        element.range_trimmed_trivia(),
        element,
        message,
        &ShopwareMigrationPayload {
            rule: "declaration-migration".into(),
            kind: kind.into(),
            safe: true,
            original,
            replacement,
            argument_index: position,
            ..ShopwareMigrationPayload::default()
        },
    )
}

/// Whether the call passes `parameter`, by position, by name, or possibly
/// through a spread.
fn passes_argument(call: &SyntaxNode, position: usize, parameter: &str) -> bool {
    phpquery::arguments(call)
        .enumerate()
        .any(|(index, argument)| {
            if argument.child_token_of_kind(TK_ELLIPSIS).is_some() {
                return true;
            }
            if argument.kind() == PHP_NAMED_ARGUMENT {
                return eq_ignore_case(&phpquery::argument_name(&argument), parameter);
            }
            index == position
        })
}
