//! Uses of declarations Shopware announces to change.
//!
//! `bc_changes` covers the overrides a quick fix can adapt. This module
//! reports what has no mechanical answer: calls, property accesses and
//! subclasses that stop working once the change announced by a `BCChange`
//! attribute is made. They are the checks of phpstan-shopware's
//! future-compatibility rules, read from the same attributes.

use std::collections::HashSet;

use swlsp_lsp::{Context, Problem, TextDocument};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARGUMENT, PHP_ASSIGNMENT_EXPRESSION, PHP_EXTENDS_CLAUSE, PHP_FUNCTION_CALL,
    PHP_FUNCTION_DECLARATION, PHP_MEMBER_ACCESS, PHP_MEMBER_CALL, PHP_METHOD_DECLARATION, PHP_NAME,
    PHP_NAMED_ARGUMENT, PHP_OBJECT_CREATION, PHP_PARENTHESIZED, PHP_RETURN_STATEMENT,
    PHP_SCOPED_ACCESS, PHP_SCOPED_CALL, TK_ELLIPSIS, TK_OBJECT_OPERATOR,
};
use swlsp_php_semantic::{
    Attribute, Document, Flags, NameKind, Reference, Snapshot, Symbol, SymbolKind,
};
use swlsp_php_types::{Kind as TypeKind, Relations, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::bc_changes::{
    bc_changes, is_parameter_name, methods, parameter_named, string_argument, type_source,
    version_label,
};
use super::{
    ShopwareMigrationAnalyzer, ShopwareMigrationPayload, call_name_range, class_name_range,
    migration_problem, scan,
};
use crate::diagnostics::common::{call_target_name, local_argument_expression};
use crate::diagnostics::shopware::same_php_namespace_package;

pub(super) const INTERNAL_CODE: &str = "shopware.migration.announced.internal";
pub(super) const FINAL_CODE: &str = "shopware.migration.announced.final";
pub(super) const ABSTRACT_CODE: &str = "shopware.migration.announced.abstract";
pub(super) const VISIBILITY_CODE: &str = "shopware.migration.announced.visibility";
pub(super) const PARAMETER_REMOVED_CODE: &str = "shopware.migration.announced.parameter.removed";
pub(super) const PARAMETER_RENAMED_CODE: &str = "shopware.migration.announced.parameter.renamed";
pub(super) const PARAMETER_REQUIRED_CODE: &str = "shopware.migration.announced.parameter.required";
pub(super) const PARAMETER_TYPE_CODE: &str = "shopware.migration.announced.parameter.type";
pub(super) const PROPERTY_READONLY_CODE: &str = "shopware.migration.announced.property.readonly";
pub(super) const PROPERTY_TYPE_CODE: &str = "shopware.migration.announced.property.type";
pub(super) const PROPERTY_REDECLARED_CODE: &str =
    "shopware.migration.announced.property.redeclared";
pub(super) const TYPE_WIDENED_CODE: &str = "shopware.migration.announced.type.widened";

/// The property changes a redeclaration cannot follow.
const PROPERTY_CONTRACT_CHANGES: &[&str] = &[
    "becomesreadonly",
    "propertytypenarrowing",
    "propertytypewidening",
];

impl ShopwareMigrationAnalyzer {
    pub(super) fn announced_use_problems(
        &self,
        ctx: &Context,
        document: &TextDocument,
        root: &SyntaxNode,
        analysis: &swlsp_lsp_phpanalysis::State,
    ) -> Vec<Problem> {
        let semantic: &Document = &analysis.document;
        let snapshot: &Snapshot = &analysis.snapshot;
        let mut references: Vec<&Reference> = semantic
            .references
            .iter()
            .filter(|reference| matches!(reference.kind, NameKind::Member | NameKind::Function))
            .collect();
        references.sort_by_key(|reference| reference.range.start());
        let mut run = Run {
            root,
            semantic,
            snapshot,
            references,
            namespace: scan::namespace(root),
            problems: Vec::new(),
            widened: Vec::new(),
        };
        phpquery::visit(
            root,
            &[
                PHP_MEMBER_CALL,
                PHP_SCOPED_CALL,
                PHP_OBJECT_CREATION,
                PHP_MEMBER_ACCESS,
                PHP_SCOPED_ACCESS,
            ],
            |node| {
                if ctx.is_cancelled() {
                    return false;
                }
                match node.kind() {
                    PHP_MEMBER_CALL | PHP_SCOPED_CALL => run.check_call(node),
                    PHP_OBJECT_CREATION => run.check_creation(node),
                    _ => run.check_property(node),
                }
                true
            },
        );
        for class in scan::classes(root) {
            if ctx.is_cancelled() {
                break;
            }
            run.check_class(&class);
        }
        let widened = std::mem::take(&mut run.widened);
        let mut problems = run.problems;
        if !widened.is_empty() && !ctx.is_cancelled() {
            problems.extend(
                self.widened_analysis_problems(document, root, analysis, &widened, &problems),
            );
        }
        problems
    }

    /// What the PHP analysis reports once the declarations in `widened`
    /// have their announced types, and does not report today: a widened
    /// value reaching a parameter or a return type through a variable, for
    /// instance. Hover and completion keep the types of the installed
    /// version; only this second analysis sees the announced ones.
    fn widened_analysis_problems(
        &self,
        document: &TextDocument,
        root: &SyntaxNode,
        analysis: &swlsp_lsp_phpanalysis::State,
        widened: &[(Symbol, String)],
        reported: &[Problem],
    ) -> Vec<Problem> {
        let (Some(php_index), Ok(path)) = (&self.php_index, swlsp_uriutil::path(&document.uri))
        else {
            return Vec::new();
        };
        let mut announced = analysis.snapshot.clone();
        for (symbol, _) in widened {
            Snapshot::override_symbol(&mut announced, symbol);
        }
        let future = php_index.analyze_against(
            &announced,
            &path,
            i64::from(document.version),
            root,
            document.source(),
        );
        let subject = match widened {
            [(_, subject)] => subject.clone(),
            _ => format!(
                "Shopware announces wider types ({})",
                widened
                    .iter()
                    .map(|(symbol, _)| symbol.fully_qualified.trim_matches('\\'))
                    .collect::<Vec<_>>()
                    .join(", ")
            ),
        };
        let mut result: Vec<Problem> = future
            .issues
            .iter()
            .filter(|issue| !analysis.document.issues.contains(issue))
            // The direct uses are reported at the widened value itself.
            .filter(|issue| {
                !reported.iter().any(|problem| {
                    problem.id.as_str() == TYPE_WIDENED_CODE
                        && issue.range.contains_range(problem.range)
                })
            })
            .filter_map(|issue| {
                let element = root.covering_element(issue.range);
                let element = element
                    .as_node()
                    .cloned()
                    .or_else(|| element.as_token().and_then(|token| token.parent()))?;
                let message = &issue.message;
                let message = message
                    .chars()
                    .next()
                    .map(|first| {
                        first
                            .to_lowercase()
                            .chain(message.chars().skip(1))
                            .collect()
                    })
                    .unwrap_or_else(|| message.to_string());
                let message: String = message;
                Some(migration_problem(
                    TYPE_WIDENED_CODE,
                    issue.range,
                    &element,
                    format!("{subject}; then {message}"),
                    &ShopwareMigrationPayload {
                        rule: "announced-change".into(),
                        kind: "widened".into(),
                        ..ShopwareMigrationPayload::default()
                    },
                ))
            })
            .collect();
        // A value that is dereferenced and may be null only with the
        // announced types; the analysis itself reports no null receivers.
        let payload = ShopwareMigrationPayload {
            rule: "announced-change".into(),
            kind: "widened".into(),
            ..ShopwareMigrationPayload::default()
        };
        for access in phpquery::nodes(root, &[PHP_MEMBER_CALL, PHP_MEMBER_ACCESS]) {
            let Some(receiver) = access.children().next() else {
                continue;
            };
            if access.child_token_of_kind(TK_OBJECT_OPERATOR).is_none() {
                continue;
            }
            let today = analysis.document.type_of(&receiver).ty;
            if today.is_unknown()
                || is_nullable(&today)
                || !is_nullable(&future.type_of(&receiver).ty)
            {
                continue;
            }
            let range = receiver.range_trimmed_trivia();
            if reported.iter().any(|problem| {
                problem.id.as_str() == TYPE_WIDENED_CODE && range.contains_range(problem.range)
            }) {
                continue;
            }
            result.push(migration_problem(
                TYPE_WIDENED_CODE,
                range,
                &receiver,
                format!("{subject}; it may be null here"),
                &payload,
            ));
        }
        result
    }
}

struct Run<'a> {
    root: &'a SyntaxNode,
    semantic: &'a Document,
    snapshot: &'a Snapshot,
    /// The member references of the document, by start.
    references: Vec<&'a Reference>,
    namespace: String,
    problems: Vec<Problem>,
    /// The declarations used here whose type is announced to widen, as
    /// they will be, with what to say about each.
    widened: Vec<(Symbol, String)>,
}

/// A call and the method declaration it reaches.
struct Call<'a> {
    node: &'a SyntaxNode,
    method: &'a Symbol,
    /// The fully qualified class declaring `method`.
    class: String,
}

impl Call<'_> {
    fn label(&self) -> String {
        format!("{}::{}()", self.class, self.method.name)
    }

    fn has_spread(&self) -> bool {
        phpquery::arguments(self.node)
            .any(|argument| argument.child_token_of_kind(TK_ELLIPSIS).is_some())
    }

    /// The argument passed for `parameter`, by name or at the position the
    /// method declares it. `None` behind a spread, where positions are
    /// unknown.
    fn argument(&self, parameter: &str) -> Option<SyntaxNode> {
        let position = parameter_named(self.method, parameter).map(|(position, _)| position);
        for (index, argument) in phpquery::arguments(self.node).enumerate() {
            if argument.child_token_of_kind(TK_ELLIPSIS).is_some() {
                return None;
            }
            if argument.kind() == PHP_NAMED_ARGUMENT {
                if eq_ignore_case(&phpquery::argument_name(&argument), parameter) {
                    return Some(argument);
                }
            } else if Some(index) == position {
                return Some(argument);
            }
        }
        None
    }

    fn named_argument(&self, parameter: &str) -> Option<SyntaxNode> {
        phpquery::arguments(self.node).find(|argument| {
            argument.kind() == PHP_NAMED_ARGUMENT
                && eq_ignore_case(&phpquery::argument_name(argument), parameter)
        })
    }
}

impl Run<'_> {
    fn add(&mut self, code: &str, range: TextRange, element: &SyntaxNode, message: String) {
        self.problems.push(migration_problem(
            code,
            range,
            element,
            message,
            &ShopwareMigrationPayload {
                rule: "announced-change".into(),
                kind: code.rsplit('.').next().unwrap_or_default().into(),
                ..ShopwareMigrationPayload::default()
            },
        ));
    }

    fn check_call(&mut self, call: &SyntaxNode) {
        let Some(target) = call_target_name(call) else {
            return;
        };
        let range = target.range_trimmed_trivia();
        for method in self.referenced(range, SymbolKind::Method) {
            let Some(class) = self.container(&method) else {
                continue;
            };
            if self.check_method(call, range, &method, &class) {
                return;
            }
        }
    }

    fn check_creation(&mut self, creation: &SyntaxNode) {
        let name = phpquery::object_class_name(creation);
        if name.is_empty() {
            return;
        }
        let resolved = scan::resolver(self.root).resolve(&name);
        let Some(class) = self.class(resolved.trim_matches('\\')) else {
            return;
        };
        let range = call_name_range(creation);
        let constructor = self.constructor(&class);
        if let Some((method, declaring)) = constructor
            && self.check_method(creation, range, &method, &declaring)
        {
            return;
        }
        self.check_internal_class(creation, range, &class);
    }

    fn widen(&mut self, future: Symbol, subject: String) {
        if !self.widened.iter().any(|(known, _)| known.id == future.id) {
            self.widened.push((future, subject));
        }
    }

    /// The constructor `class` declares or inherits, with its class.
    fn constructor(&self, class: &Symbol) -> Option<(Symbol, Symbol)> {
        std::iter::once(class.clone())
            .chain(self.parents(class))
            .find_map(|class| {
                let name = class.fully_qualified.trim_matches('\\');
                methods(self.snapshot, name, "__construct")
                    .into_iter()
                    .next()
                    .map(|method| (method, class))
            })
    }

    /// Reports a use of `value` that accepts its type of today (`current`)
    /// but not the `announced`, wider one: as the receiver of a member
    /// access, as an argument, or as a returned value. What is stored in a
    /// variable first is not followed.
    fn check_widened_use(
        &mut self,
        value: &SyntaxNode,
        range: TextRange,
        current: &Type,
        announced: &Type,
        subject: &str,
    ) {
        if current.is_unknown() || !self.known(current) {
            return;
        }
        let mut value = value.clone();
        while let Some(parent) = value
            .parent()
            .filter(|parent| parent.kind() == PHP_PARENTHESIZED)
        {
            value = parent;
        }
        let Some(parent) = value.parent() else {
            return;
        };
        let relations = Relations::new(self.snapshot);
        let expected = match parent.kind() {
            PHP_MEMBER_CALL | PHP_MEMBER_ACCESS
                if parent.children().next().as_ref() == Some(&value) =>
            {
                let dereferenced = parent.child_token_of_kind(TK_OBJECT_OPERATOR).is_some();
                if dereferenced && is_nullable(announced) && !is_nullable(current) {
                    self.add(
                        TYPE_WIDENED_CODE,
                        range,
                        &value,
                        format!("{subject}; it may be null here"),
                    );
                }
                return;
            }
            PHP_ARGUMENT | PHP_NAMED_ARGUMENT => self.parameter_type(&parent),
            PHP_RETURN_STATEMENT => self.declared_return_type(&parent),
            _ => None,
        };
        let Some(expected) = expected else {
            return;
        };
        if matches!(
            expected.kind(),
            TypeKind::Mixed | TypeKind::SelfType | TypeKind::Static | TypeKind::Parent
        ) || !self.known(&expected)
            || !relations.is_assignable_to(current, &expected)
            || relations.is_assignable_to(announced, &expected)
        {
            return;
        }
        self.add(
            TYPE_WIDENED_CODE,
            range,
            &value,
            format!("{subject}; {expected} is expected here"),
        );
    }

    /// The type of the parameter `argument` is passed for.
    fn parameter_type(&self, argument: &SyntaxNode) -> Option<Type> {
        let call = argument.parent()?.parent()?;
        let callee = match call.kind() {
            PHP_MEMBER_CALL | PHP_SCOPED_CALL => {
                let target = call_target_name(&call)?;
                self.referenced(target.range_trimmed_trivia(), SymbolKind::Method)
                    .into_iter()
                    .next()?
            }
            PHP_FUNCTION_CALL => {
                let target = call_target_name(&call)?;
                self.referenced(target.range_trimmed_trivia(), SymbolKind::Function)
                    .into_iter()
                    .next()?
            }
            PHP_OBJECT_CREATION => {
                let name = phpquery::object_class_name(&call);
                let resolved = scan::resolver(self.root).resolve(&name);
                let class = self.class(resolved.trim_matches('\\'))?;
                self.constructor(&class)?.0
            }
            _ => return None,
        };
        let parameter = if argument.kind() == PHP_NAMED_ARGUMENT {
            parameter_named(&callee, &phpquery::argument_name(argument))?.1
        } else {
            let position = phpquery::arguments(&call).position(|other| other == *argument)?;
            callee
                .parameters
                .get(position)
                .filter(|parameter| !parameter.flags.has(Flags::VARIADIC))?
        };
        Some(parameter.ty.clone())
    }

    /// The return type of the function or method `statement` returns from.
    fn declared_return_type(&self, statement: &SyntaxNode) -> Option<Type> {
        let function = phpquery::function_like_at(statement)?;
        if !matches!(
            function.kind(),
            PHP_METHOD_DECLARATION | PHP_FUNCTION_DECLARATION
        ) {
            return None;
        }
        let name = phpquery::direct_child(&function, PHP_NAME)?.range_trimmed_trivia();
        self.semantic
            .symbols
            .iter()
            .find(|symbol| symbol.is_function_like() && symbol.selection_range == name)
            .map(|symbol| symbol.native_type.clone())
            .filter(|ty| !ty.is_unknown())
    }

    /// Reports what `call` breaks about `method`; whether anything was
    /// reported.
    fn check_method(
        &mut self,
        node: &SyntaxNode,
        range: TextRange,
        method: &Symbol,
        class: &Symbol,
    ) -> bool {
        let before = self.problems.len();
        let call = Call {
            node,
            method,
            class: class.fully_qualified.trim_matches('\\').to_owned(),
        };
        let mut internal = false;
        for (kind, attribute) in bc_changes(method) {
            let Some(label) = version_label(attribute) else {
                continue;
            };
            if self.staged(node, attribute) {
                continue;
            }
            let symbol = call.label();
            match kind.as_str() {
                "becomesinternal" => {
                    internal = true;
                    if !self.same_package(&call.class) {
                        self.add(
                            INTERNAL_CODE,
                            range,
                            node,
                            format!("Shopware {label}: {symbol} becomes internal; stop calling it"),
                        );
                    }
                }
                "visibilitychange" => {
                    let Some(visibility) = string_argument(attribute, "newVisibility", 1) else {
                        continue;
                    };
                    if !self.can_access(&visibility, &call.class, node) {
                        self.add(
                            VISIBILITY_CODE,
                            range,
                            node,
                            format!(
                                "Shopware {label}: {symbol} becomes {visibility}; this call will \
                                 no longer be allowed"
                            ),
                        );
                    }
                }
                "parameterremoval" => {
                    let Some(parameter) = string_argument(attribute, "parameterName", 1) else {
                        continue;
                    };
                    if let Some(argument) = call.argument(&parameter) {
                        self.add(
                            PARAMETER_REMOVED_CODE,
                            argument.range_trimmed_trivia(),
                            node,
                            format!(
                                "Shopware {label}: ${parameter} of {symbol} is removed; stop \
                                 passing it"
                            ),
                        );
                    }
                }
                "parameternamechange" => {
                    let (Some(parameter), Some(new_name)) = (
                        string_argument(attribute, "parameterName", 1),
                        string_argument(attribute, "newName", 2),
                    ) else {
                        continue;
                    };
                    if let Some(argument) = call.named_argument(&parameter) {
                        let range = phpquery::direct_child(&argument, PHP_NAME).map_or_else(
                            || argument.range_trimmed_trivia(),
                            |name| name.range_trimmed_trivia(),
                        );
                        self.add(
                            PARAMETER_RENAMED_CODE,
                            range,
                            node,
                            format!(
                                "Shopware {label}: ${parameter} of {symbol} is renamed to \
                                 ${new_name}; pass it by position"
                            ),
                        );
                    }
                }
                "newrequiredparameter" | "newoptionalparameter" => {
                    let Some(parameter) = string_argument(attribute, "parameterName", 1) else {
                        continue;
                    };
                    if call.has_spread() {
                        continue;
                    }
                    let Some(argument) = call.argument(&parameter) else {
                        if kind == "newrequiredparameter" {
                            self.add(
                                PARAMETER_REQUIRED_CODE,
                                range,
                                node,
                                format!(
                                    "Shopware {label}: {symbol} requires ${parameter}; pass it by \
                                     position now"
                                ),
                            );
                        }
                        continue;
                    };
                    let announced = attribute
                        .argument("parameterType", 2)
                        .and_then(|value| self.announced_type(value, &call.class));
                    self.check_argument_type(
                        &call,
                        &argument,
                        announced,
                        &format!("Shopware {label}: ${parameter} of {symbol} requires"),
                    );
                }
                "returntypewidening" => {
                    let current = if method.return_type.is_unknown() {
                        &method.native_type
                    } else {
                        &method.return_type
                    };
                    if let Some((announced, text)) = attribute
                        .argument("newType", 1)
                        .and_then(|value| self.announced_type(value, &call.class))
                    {
                        let subject = format!("Shopware {label}: {symbol} returns {text}");
                        self.check_widened_use(node, range, current, &announced, &subject);
                        let mut future = method.clone();
                        future.return_type = announced.clone();
                        future.native_type = announced;
                        future.doc_type = Type::unknown();
                        self.widen(future, subject);
                    }
                }
                "parametertypenarrowing" => {
                    let Some(parameter) = string_argument(attribute, "parameterName", 1) else {
                        continue;
                    };
                    let Some(argument) = call.argument(&parameter) else {
                        continue;
                    };
                    let announced = attribute
                        .argument("newType", 2)
                        .and_then(|value| self.announced_type(value, &call.class));
                    self.check_argument_type(
                        &call,
                        &argument,
                        announced,
                        &format!("Shopware {label}: ${parameter} of {symbol} is narrowed to"),
                    );
                }
                _ => {}
            }
        }
        if !internal {
            self.check_internal_class(node, range, class);
        }
        self.problems.len() > before
    }

    fn check_argument_type(
        &mut self,
        call: &Call<'_>,
        argument: &SyntaxNode,
        announced: Option<(Type, String)>,
        subject: &str,
    ) {
        let (Some((announced, text)), Some(expression)) =
            (announced, local_argument_expression(argument))
        else {
            return;
        };
        let actual = self.semantic.type_of(&expression).ty;
        if self.incompatible(&actual, &announced) {
            self.add(
                PARAMETER_TYPE_CODE,
                expression.range_trimmed_trivia(),
                call.node,
                format!("{subject} {text}, but {actual} is passed"),
            );
        }
    }

    /// A call on, or a construction of, a class that becomes internal.
    fn check_internal_class(&mut self, node: &SyntaxNode, range: TextRange, class: &Symbol) {
        let name = class.fully_qualified.trim_matches('\\').to_owned();
        if self.same_package(&name) {
            return;
        }
        for (kind, attribute) in bc_changes(class) {
            if kind != "becomesinternal" || self.staged(node, attribute) {
                continue;
            }
            if let Some(label) = version_label(attribute) {
                self.add(
                    INTERNAL_CODE,
                    range,
                    node,
                    format!("Shopware {label}: {name} becomes internal; stop using it"),
                );
            }
            return;
        }
    }

    fn check_property(&mut self, access: &SyntaxNode) {
        let Some(name) = access.children().last() else {
            return;
        };
        let range = name.range_trimmed_trivia();
        for property in self.referenced(range, SymbolKind::Property) {
            let Some(class) = self.container(&property) else {
                continue;
            };
            let class_name = class.fully_qualified.trim_matches('\\').to_owned();
            let symbol = format!("{class_name}::${}", property.name.trim_start_matches('$'));
            let before = self.problems.len();
            let assigned = assigned_value(access);
            let foreign = self
                .caller_class(access)
                .is_none_or(|caller| !eq_ignore_case(&caller, &class_name));
            for (kind, attribute) in bc_changes(&property) {
                let Some(label) = version_label(attribute) else {
                    continue;
                };
                if self.staged(access, attribute) {
                    continue;
                }
                match kind.as_str() {
                    "visibilitychange" => {
                        let Some(visibility) = string_argument(attribute, "newVisibility", 1)
                        else {
                            continue;
                        };
                        if !self.can_access(&visibility, &class_name, access) {
                            self.add(
                                VISIBILITY_CODE,
                                range,
                                access,
                                format!(
                                    "Shopware {label}: {symbol} becomes {visibility}; this \
                                     access will no longer be allowed"
                                ),
                            );
                        }
                    }
                    "propertytypewidening" if assigned.is_none() => {
                        if let Some((announced, text)) = attribute
                            .argument("newType", 1)
                            .and_then(|value| self.announced_type(value, &class_name))
                        {
                            let subject = format!("Shopware {label}: {symbol} becomes {text}");
                            self.check_widened_use(
                                access,
                                range,
                                &property.ty,
                                &announced,
                                &subject,
                            );
                            let mut future = property.clone();
                            future.ty = announced.clone();
                            future.native_type = announced;
                            future.doc_type = Type::unknown();
                            self.widen(future, subject);
                        }
                    }
                    "becomesreadonly" if foreign && assigned.is_some() => {
                        self.add(
                            PROPERTY_READONLY_CODE,
                            range,
                            access,
                            format!(
                                "Shopware {label}: {symbol} becomes readonly; stop assigning to \
                                 it outside its class"
                            ),
                        );
                    }
                    "propertytypenarrowing" if foreign => {
                        let (Some(value), Some((announced, text))) = (
                            assigned.as_ref(),
                            attribute
                                .argument("newType", 1)
                                .and_then(|value| self.announced_type(value, &class_name)),
                        ) else {
                            continue;
                        };
                        let actual = self.semantic.type_of(value).ty;
                        if self.incompatible(&actual, &announced) {
                            self.add(
                                PROPERTY_TYPE_CODE,
                                value.range_trimmed_trivia(),
                                access,
                                format!(
                                    "Shopware {label}: {symbol} is narrowed to {text}, but \
                                     {actual} is assigned"
                                ),
                            );
                        }
                    }
                    _ => {}
                }
            }
            if self.problems.len() > before {
                return;
            }
        }
    }

    fn check_class(&mut self, node: &SyntaxNode) {
        let name = phpquery::class_name(node);
        if name.is_empty() {
            return;
        }
        let name = if self.namespace.is_empty() {
            name
        } else {
            format!("{}\\{name}", self.namespace)
        };
        let Some(own) = self.class(&name) else {
            return;
        };
        if own.kind != SymbolKind::Class {
            return;
        }
        let range = phpquery::direct_child(node, PHP_EXTENDS_CLAUSE).map_or_else(
            || class_name_range(node),
            |clause| {
                phpquery::direct_child(&clause, PHP_NAME).map_or_else(
                    || clause.range_trimmed_trivia(),
                    |name| name.range_trimmed_trivia(),
                )
            },
        );
        let parents = self.parents(&own);
        for (depth, parent) in parents.iter().enumerate() {
            let parent_name = parent.fully_qualified.trim_matches('\\').to_owned();
            for (kind, attribute) in bc_changes(parent) {
                let Some(label) = version_label(attribute) else {
                    continue;
                };
                if self.staged(node, attribute) {
                    continue;
                }
                match kind.as_str() {
                    "becomesfinal" => self.add(
                        FINAL_CODE,
                        range,
                        node,
                        format!(
                            "Shopware {label}: {parent_name} becomes final; {} can no longer \
                             extend it",
                            own.name
                        ),
                    ),
                    "becomesinternal" if !self.same_package(&parent_name) => self.add(
                        INTERNAL_CODE,
                        range,
                        node,
                        format!(
                            "Shopware {label}: {parent_name} becomes internal; stop extending it"
                        ),
                    ),
                    _ => {}
                }
            }
            // The classes between the subclass and this parent, nearest
            // first: one of them may already implement or redeclare.
            let nearer: Vec<&Symbol> = std::iter::once(&own).chain(&parents[..depth]).collect();
            for member in self.snapshot.members_of(parent.id.as_str()) {
                // The older way to announce an abstract method: `@abstract`
                // in its PHPDoc.
                if member.kind == SymbolKind::Method
                    && member.flags.has(Flags::SOFT_ABSTRACT)
                    && !self.implements(&nearer, &member.name)
                    && !bc_changes(&member).any(|(kind, _)| kind == "becomesabstract")
                {
                    self.add(
                        ABSTRACT_CODE,
                        class_name_range(node),
                        node,
                        format!(
                            "Shopware: {parent_name}::{}() is marked @abstract and becomes \
                             abstract; implement it in {}",
                            member.name, own.name
                        ),
                    );
                }
                for (kind, attribute) in bc_changes(&member) {
                    let Some(label) = version_label(attribute) else {
                        continue;
                    };
                    if self.staged(node, attribute) {
                        continue;
                    }
                    if member.kind == SymbolKind::Method
                        && kind == "becomesabstract"
                        && !own.flags.has(Flags::ABSTRACT)
                        && !self.implements(&nearer, &member.name)
                    {
                        self.add(
                            ABSTRACT_CODE,
                            class_name_range(node),
                            node,
                            format!(
                                "Shopware {label}: {parent_name}::{}() becomes abstract; \
                                 implement it in {}",
                                member.name, own.name
                            ),
                        );
                    }
                    let contract = PROPERTY_CONTRACT_CHANGES.contains(&kind.as_str())
                        || kind == "visibilitychange"
                            && string_argument(attribute, "newVisibility", 1)
                                .is_some_and(|visibility| visibility == "private");
                    if member.kind == SymbolKind::Property
                        && contract
                        && let Some(redeclared) = own_property(node, &member.name)
                    {
                        self.add(
                            PROPERTY_REDECLARED_CODE,
                            redeclared.range_trimmed_trivia(),
                            node,
                            format!(
                                "Shopware {label}: {parent_name}::${} changes; stop redeclaring \
                                 it in {}",
                                member.name.trim_start_matches('$'),
                                own.name
                            ),
                        );
                        // One report per property, whatever changes about it.
                        break;
                    }
                }
            }
        }
    }

    /// Whether one of `classes`, or a trait it uses, has a concrete method
    /// `name`.
    fn implements(&self, classes: &[&Symbol], name: &str) -> bool {
        classes.iter().any(|class| {
            let traits = class
                .traits()
                .iter()
                .filter_map(|name| self.class(name.trim_matches('\\')));
            std::iter::once((*class).clone())
                .chain(traits)
                .any(|holder| {
                    self.snapshot
                        .members(holder.id.as_str(), name)
                        .iter()
                        .any(|member| {
                            member.kind == SymbolKind::Method && !member.flags.has(Flags::ABSTRACT)
                        })
                })
        })
    }

    /// The symbols of `kind` the member reference at `range` resolves to.
    fn referenced(&self, range: TextRange, kind: SymbolKind) -> Vec<Symbol> {
        let first = self
            .references
            .partition_point(|reference| reference.range.start() < range.start());
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for reference in &self.references[first..] {
            if reference.range.start() >= range.end() {
                break;
            }
            let matches = if kind == SymbolKind::Function {
                reference.kind == NameKind::Function
            } else {
                reference.kind == NameKind::Member && reference.target_kind == kind
            };
            if !matches {
                continue;
            }
            for id in std::iter::once(&reference.resolved).chain(reference.candidate_ids()) {
                if id.is_empty() || !seen.insert(id.clone()) {
                    continue;
                }
                if let Some(symbol) = self.snapshot.symbol(id.as_str())
                    && symbol.kind == kind
                {
                    result.push(symbol);
                }
            }
        }
        result
    }

    fn container(&self, member: &Symbol) -> Option<Symbol> {
        if member.container.is_empty() {
            return None;
        }
        self.snapshot
            .symbol(member.container.as_str())
            .filter(Symbol::is_class_like)
    }

    /// The declaration of the class `name`, preferring source over stubs.
    fn class(&self, name: &str) -> Option<Symbol> {
        let classes = self.snapshot.classes(name);
        classes
            .iter()
            .find(|class| !class.flags.has(Flags::GENERATED_STUB | Flags::CLASS_ALIAS))
            .or(classes.first())
            .cloned()
    }

    /// The parent classes of `class`, nearest first.
    fn parents(&self, class: &Symbol) -> Vec<Symbol> {
        let mut seen = HashSet::from([lowercase(class.fully_qualified.trim_matches('\\'))]);
        let mut result = Vec::new();
        let mut current = class.clone();
        while let Some(parent) = current
            .extends()
            .first()
            .and_then(|name| self.class(name.trim_matches('\\')))
        {
            if !seen.insert(lowercase(parent.fully_qualified.trim_matches('\\'))) {
                break;
            }
            result.push(parent.clone());
            current = parent;
        }
        result
    }

    /// The fully qualified name of the class around `node`.
    fn caller_class(&self, node: &SyntaxNode) -> Option<String> {
        let name = phpquery::class_name(&phpquery::class_at(node)?);
        if name.is_empty() {
            return None;
        }
        Some(if self.namespace.is_empty() {
            name
        } else {
            format!("{}\\{name}", self.namespace)
        })
    }

    /// Whether code at `node` may still use a member of `class` once it is
    /// `visibility`.
    fn can_access(&self, visibility: &str, class: &str, node: &SyntaxNode) -> bool {
        let caller = self.caller_class(node);
        match lowercase(visibility).as_str() {
            "private" => caller.is_some_and(|caller| eq_ignore_case(&caller, class)),
            "protected" => caller.is_some_and(|caller| {
                eq_ignore_case(&caller, class) || self.snapshot.is_subtype_of(&caller, class)
            }),
            _ => true,
        }
    }

    /// Whether the document belongs to the package of `class`, which may
    /// keep using what becomes internal.
    fn same_package(&self, class: &str) -> bool {
        let namespace = class
            .rsplit_once('\\')
            .map_or("", |(namespace, _)| namespace);
        same_php_namespace_package(&self.namespace, namespace)
    }

    /// Whether the code around `node` goes away with the change: Shopware
    /// marks such code `@deprecated tag:<version>`.
    fn staged(&self, node: &SyntaxNode, attribute: &Attribute) -> bool {
        let Some(version) = attribute
            .argument("version", 0)
            .map(|value| value.value.to_string())
            .filter(|version| !version.is_empty())
        else {
            return false;
        };
        let tag = format!("tag:{version}");
        [phpquery::class_at(node), phpquery::function_like_at(node)]
            .into_iter()
            .flatten()
            .any(|declaration| {
                leading_comment(&declaration)
                    .split_whitespace()
                    .any(|word| word == tag)
            })
    }

    /// The announced type and its source. `None` when it names a class the
    /// project does not know, so nothing can be said about it.
    fn announced_type(
        &self,
        value: &swlsp_php_semantic::AttributeValue,
        class: &str,
    ) -> Option<(Type, String)> {
        let text = type_source(value)?;
        let ty = if ["self", "static", "$this"].contains(&lowercase(&text).as_str()) {
            Type::named(class, Vec::new())
        } else {
            swlsp_php_types::parse_native(&text).ok()?
        };
        self.known(&ty).then_some((ty, text))
    }

    /// Whether every class `ty` names is indexed.
    fn known(&self, ty: &Type) -> bool {
        match ty.kind() {
            TypeKind::Unknown | TypeKind::Error | TypeKind::Template | TypeKind::Conditional => {
                false
            }
            TypeKind::Object if !ty.name().is_empty() => !self
                .snapshot
                .classes(ty.name().trim_matches('\\'))
                .is_empty(),
            _ => ty.arguments().iter().all(|argument| self.known(argument)),
        }
    }

    /// Whether no value of `actual` fits `announced`. A value that may fit
    /// (a nullable one for a non-nullable type) is left alone, as is a
    /// type the analysis is not sure about.
    fn incompatible(&self, actual: &Type, announced: &Type) -> bool {
        if actual.kind() == TypeKind::Mixed
            || matches!(
                actual.kind(),
                TypeKind::SelfType | TypeKind::Static | TypeKind::Parent
            )
            || !self.known(actual)
        {
            return false;
        }
        let relations = Relations::new(self.snapshot);
        let alternatives: &[Type] = if actual.kind() == TypeKind::Union {
            actual.arguments()
        } else {
            std::slice::from_ref(actual)
        };
        !alternatives.is_empty()
            && alternatives.iter().all(|alternative| {
                alternative.kind() != TypeKind::Mixed
                    && !relations.is_assignable_to(alternative, announced)
            })
    }
}

/// Whether `ty` admits `null`.
fn is_nullable(ty: &Type) -> bool {
    ty.kind() == TypeKind::Null
        || ty.kind() == TypeKind::Union
            && ty
                .arguments()
                .iter()
                .any(|alternative| alternative.kind() == TypeKind::Null)
}

/// The value assigned to `access`, when it is the target of an assignment.
fn assigned_value(access: &SyntaxNode) -> Option<SyntaxNode> {
    let assignment = access.parent()?;
    if assignment.kind() != PHP_ASSIGNMENT_EXPRESSION {
        return None;
    }
    let mut children = assignment.children();
    (children.next().as_ref() == Some(access))
        .then(|| children.last())
        .flatten()
}

/// The declaration of the property `name` in the class `node`.
fn own_property(node: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    phpquery::properties(node).into_iter().find(|property| {
        phpquery::property_variables(property)
            .iter()
            .any(|variable| is_parameter_name(&phpquery::variable_name(variable), name))
    })
}

/// The comments in front of a declaration.
fn leading_comment(declaration: &SyntaxNode) -> String {
    let start = declaration.text_range().start();
    let length = declaration.range_trimmed_trivia().start() - start;
    let text = declaration.text().to_string();
    let mut comment = text
        .get(..usize::from(length))
        .unwrap_or_default()
        .to_owned();
    // A comment may also be trivia of what precedes the declaration.
    let mut previous = declaration.prev_sibling_or_token();
    while let Some(element) = previous {
        let Some(token) = element.as_token() else {
            break;
        };
        if !token.kind().is_trivia() {
            break;
        }
        comment.insert_str(0, token.text());
        previous = token.prev_sibling_or_token();
    }
    comment
}
