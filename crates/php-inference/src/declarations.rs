//! Declaration validation: final parents, overrides, abstract methods (port
//! of `declarations.go`).

use std::collections::HashSet;

use rustc_hash::FxHashSet;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Parameter, Snapshot, Symbol, SymbolKind, SymbolView, Visibility};
use swlsp_php_types::{Kind, Relations, Type, equal_fold, go_to_lower};

use crate::analyzer::AnalyzerState;
use crate::syntax::trim_backslash;

impl AnalyzerState<'_> {
    pub(crate) fn validate_declarations(&mut self, snapshot: &Snapshot) {
        let classes: Vec<Symbol> = self
            .document
            .symbols
            .iter()
            .filter(|symbol| symbol.is_class_like())
            .cloned()
            .collect();
        let validator = Validator {
            snapshot,
            relations: snapshot.relations(),
        };
        for class in &classes {
            validator.validate_final_parents(self, class);
            validator.validate_overrides(self, class);
            validator.validate_required_methods(self, class);
        }
    }
}

struct Validator<'s> {
    snapshot: &'s Snapshot,
    relations: Relations<'s>,
}

impl<'s> Validator<'s> {
    fn validate_final_parents(&self, state: &mut AnalyzerState<'_>, class: &Symbol) {
        if class.kind != SymbolKind::Class {
            return;
        }
        for parent_name in class.extends() {
            self.snapshot.visit_class_views(parent_name, |parent_view| {
                let parent = parent_view.materialize();
                if parent.flags.has(Flags::FINAL) {
                    state.report_range(
                        class.selection_range,
                        "php.inheritance",
                        format!(
                            "Class {} cannot extend final class {}",
                            class.fully_qualified, parent.fully_qualified
                        ),
                    );
                }
                true
            });
        }
    }

    fn validate_overrides(&self, state: &mut AnalyzerState<'_>, class: &Symbol) {
        if class.kind == SymbolKind::Interface {
            return;
        }
        let parents = parent_receiver_types(class);
        let resolver = MemberResolver::new(self.snapshot);
        for member_view in self.snapshot.member_views_of(&class.id) {
            let member = member_view.materialize();
            if member.kind != SymbolKind::Method && member.kind != SymbolKind::Property {
                continue;
            }
            if member.flags.has(Flags::SYNTHETIC) {
                continue;
            }
            let mut inherited: Vec<Symbol> = Vec::new();
            let mut seen: FxHashSet<String> = FxHashSet::default();
            let mut visit_candidate = |candidate: Symbol| {
                if candidate.visibility == Visibility::Private
                    || candidate.flags.has(Flags::SYNTHETIC)
                    || !seen.insert(candidate.id.to_string())
                {
                    return;
                }
                inherited.push(candidate);
            };
            for parent in &parents {
                if member.kind == SymbolKind::Method {
                    resolver.visit_methods(parent, &member.name, |candidate| {
                        visit_candidate(candidate.symbol);
                        true
                    });
                } else {
                    for candidate in resolver.properties(parent, &member.name) {
                        visit_candidate(candidate.symbol);
                    }
                }
            }
            if member.kind == SymbolKind::Method
                && has_attribute_named(&member, "Override")
                && inherited.is_empty()
            {
                state.report_range(
                    member.selection_range,
                    "php.override",
                    format!(
                        "Method {}::{} is marked #[Override] but does not override a parent method",
                        class.fully_qualified, member.name
                    ),
                );
            }
            for parent in &inherited {
                self.validate_override(state, class, &member, parent);
            }
        }
    }

    fn validate_override(
        &self,
        state: &mut AnalyzerState<'_>,
        class: &Symbol,
        member: &Symbol,
        parent: &Symbol,
    ) {
        let name = format!("{}::{}", class.fully_qualified, member.name);
        if parent.flags.has(Flags::FINAL) {
            state.report_range(
                member.selection_range,
                "php.override",
                format!(
                    "{name} cannot override final member {}",
                    parent.fully_qualified
                ),
            );
            return;
        }
        if member.visibility > parent.visibility {
            state.report_range(
                member.selection_range,
                "php.override",
                format!(
                    "{name} cannot reduce the visibility of {}",
                    parent.fully_qualified
                ),
            );
        }
        if member.flags.has(Flags::STATIC) != parent.flags.has(Flags::STATIC) {
            state.report_range(
                member.selection_range,
                "php.override",
                format!(
                    "{name} must preserve static declaration of {}",
                    parent.fully_qualified
                ),
            );
        }
        if member.kind == SymbolKind::Property {
            self.validate_property_override(state, class, member, parent);
            return;
        }
        if equal_fold(&member.name, "__construct") {
            return;
        }
        if !self.compatible_method(class, member, parent) {
            state.report_range(
                member.selection_range,
                "php.override",
                format!(
                    "{name} has an incompatible signature with {}",
                    parent.fully_qualified
                ),
            );
        }
    }

    fn validate_property_override(
        &self,
        state: &mut AnalyzerState<'_>,
        class: &Symbol,
        property: &Symbol,
        parent: &Symbol,
    ) {
        let mut child_type = declaration_type(&property.native_type, class);
        let Some(parent_class) = self.snapshot.symbol(&parent.container) else {
            return;
        };
        let mut parent_type = declaration_type(&parent.native_type, &parent_class);
        if child_type.kind() == Kind::Error || parent_type.kind() == Kind::Error {
            return;
        }
        if child_type.is_unknown() {
            child_type = Type::MIXED;
        }
        if parent_type.is_unknown() {
            parent_type = Type::MIXED;
        }
        if child_type != parent_type {
            state.report_range(
                property.selection_range,
                "php.override",
                format!(
                    "{}::${} must keep the invariant type {parent_type}",
                    class.fully_qualified, property.name
                ),
            );
        }
        if property.flags.has(Flags::READONLY) != parent.flags.has(Flags::READONLY) {
            state.report_range(
                property.selection_range,
                "php.override",
                format!(
                    "{}::${} must preserve readonly declaration of {}",
                    class.fully_qualified, property.name, parent.fully_qualified
                ),
            );
        }
    }

    fn compatible_method(&self, class: &Symbol, method: &Symbol, parent: &Symbol) -> bool {
        let Some(parent_class) = self.snapshot.symbol(&parent.container) else {
            return true;
        };
        let child_parameters = declared_parameters(&method.parameters);
        let parent_parameters = declared_parameters(&parent.parameters);
        if child_parameters.len() < parent_parameters.len() {
            return false;
        }
        let parent_type_context = if parent_class.kind == SymbolKind::Trait {
            class
        } else {
            &parent_class
        };
        for (parent_parameter, child_parameter) in parent_parameters.iter().zip(&child_parameters) {
            if parent_parameter.flags.has(Flags::BY_REFERENCE)
                != child_parameter.flags.has(Flags::BY_REFERENCE)
            {
                return false;
            }
            if parent_parameter.flags.has(Flags::VARIADIC)
                && !child_parameter.flags.has(Flags::VARIADIC)
            {
                return false;
            }
            if parent_parameter.optional
                && !child_parameter.optional
                && !child_parameter.flags.has(Flags::VARIADIC)
            {
                return false;
            }
            let mut parent_type =
                declaration_type(&parent_parameter.native_type, parent_type_context);
            let mut child_type = declaration_type(&child_parameter.native_type, class);
            if parent_type.is_unknown() {
                parent_type = Type::MIXED;
            }
            if child_type.is_unknown() {
                child_type = Type::MIXED;
            }
            if parent_type.kind() != Kind::Error
                && child_type.kind() != Kind::Error
                && !self.relations.is_subtype(&parent_type, &child_type)
            {
                return false;
            }
        }
        for added in &child_parameters[parent_parameters.len()..] {
            if !added.optional && !added.flags.has(Flags::VARIADIC) {
                return false;
            }
        }
        if has_attribute_named(method, "ReturnTypeWillChange") {
            return true;
        }
        let parent_return = declaration_type(&parent.native_type, parent_type_context);
        if parent_return.is_unknown() {
            return true;
        }
        let child_return = declaration_type(&method.native_type, class);
        if child_return.is_unknown() {
            return false;
        }
        if parent_return.kind() == Kind::Error || child_return.kind() == Kind::Error {
            return true;
        }
        self.relations.is_subtype(&child_return, &parent_return)
    }

    fn validate_required_methods(&self, state: &mut AnalyzerState<'_>, class: &Symbol) {
        if class.kind != SymbolKind::Class && class.kind != SymbolKind::Enum {
            return;
        }
        if class.flags.has(Flags::ABSTRACT) {
            return;
        }
        let receiver = class_receiver_type(class);
        let mut missing: Vec<String> = Vec::new();
        MemberResolver::new(self.snapshot).visit_all_method_ids(&receiver, |method| {
            if !self.method_is_abstract(method) || self.has_concrete_method(&receiver, method) {
                return true;
            }
            missing.push(method.fully_qualified().to_owned());
            true
        });
        for method in missing {
            state.report_range(
                class.selection_range,
                "php.abstract",
                format!(
                    "{} must implement abstract method {method}",
                    class.fully_qualified
                ),
            );
        }
    }

    fn has_concrete_method(&self, receiver: &Type, required: SymbolView<'s>) -> bool {
        if receiver.kind() != Kind::Object || receiver.name().is_empty() {
            return false;
        }
        let required_method = required.materialize();
        let mut visited: HashSet<String> = HashSet::new();
        let mut found = false;
        self.snapshot.visit_class_views(receiver.name(), |class| {
            if self.visit_concrete_class(class, required, &required_method, &mut visited) {
                found = true;
                return false;
            }
            true
        });
        found
    }

    fn visit_concrete_class(
        &self,
        class: SymbolView<'s>,
        required: SymbolView<'s>,
        required_method: &Symbol,
        visited: &mut HashSet<String>,
    ) -> bool {
        if !visited.insert(class.id().to_owned()) {
            return false;
        }
        let mut found = false;
        self.snapshot
            .visit_member_views(class.id(), required.name(), |candidate_view| {
                let candidate = candidate_view.materialize();
                if candidate.kind != SymbolKind::Method
                    || self.method_is_abstract(candidate_view)
                    || candidate.visibility > required_method.visibility
                    || candidate.flags.has(Flags::STATIC)
                        != required_method.flags.has(Flags::STATIC)
                {
                    return true;
                }
                found = true;
                false
            });
        if found {
            return true;
        }
        let (traits, extends, _) = class.hierarchy_names();
        for related_names in [traits, extends] {
            for related_name in related_names {
                let complete = self.snapshot.visit_class_views(related_name, |related| {
                    if self.visit_concrete_class(related, required, required_method, visited) {
                        found = true;
                        return false;
                    }
                    true
                });
                if !complete {
                    return true;
                }
            }
        }
        found
    }

    fn method_is_abstract(&self, method: SymbolView<'_>) -> bool {
        if method.flags().has(Flags::ABSTRACT) {
            return true;
        }
        self.snapshot
            .symbol_view(method.container())
            .is_some_and(|container| container.kind() == SymbolKind::Interface)
    }
}

fn declared_parameters(parameters: &[Parameter]) -> Vec<&Parameter> {
    parameters
        .iter()
        .filter(|parameter| !parameter.flags.has(Flags::SYNTHETIC))
        .collect()
}

fn parent_receiver_types(class: &Symbol) -> Vec<Type> {
    let declared: Vec<&Type> = class
        .extends_types()
        .iter()
        .chain(class.implements_types())
        .collect();
    let mut result = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();
    for name in class.extends().iter().chain(class.implements()) {
        let value = declared
            .iter()
            .find(|candidate| equal_fold(candidate.name(), name))
            .map_or_else(|| Type::named(name, Vec::new()), |found| (*found).clone());
        if !seen.insert(value.key().into_owned()) {
            continue;
        }
        result.push(value);
    }
    result
}

fn class_receiver_type(class: &Symbol) -> Type {
    Type::named(
        &class.fully_qualified,
        class
            .templates()
            .iter()
            .map(|template| Type::template(&template.name))
            .collect(),
    )
}

fn declaration_type(value: &Type, class: &Symbol) -> Type {
    match value.kind() {
        Kind::SelfType | Kind::Static => {
            Type::named(&class.fully_qualified, value.arguments().to_vec())
        }
        Kind::Parent => match class.extends().first() {
            Some(parent) => Type::named(parent, value.arguments().to_vec()),
            None => Type::UNKNOWN,
        },
        Kind::Union => Type::union(
            value
                .arguments()
                .iter()
                .map(|member| declaration_type(member, class)),
        ),
        Kind::Intersection => Type::intersection(
            value
                .arguments()
                .iter()
                .map(|member| declaration_type(member, class)),
        ),
        _ => value.clone(),
    }
}

fn has_attribute_named(symbol: &Symbol, expected: &str) -> bool {
    let expected = go_to_lower(trim_backslash(expected));
    symbol.attributes().iter().any(|attribute| {
        let name = go_to_lower(trim_backslash(&attribute.name));
        name == expected || name.ends_with(&format!("\\{expected}"))
    })
}
