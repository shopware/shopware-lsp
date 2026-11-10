//! PHPDoc-derived declarations and type selection.

use std::collections::HashMap;

use swlsp_textutil::eq_ignore_case;

use crate::fast::Fast;
use rustc_hash::FxHashSet;
use swlsp_php_phpdoc::{Assertion, Document as PhpDoc, Template};
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{
    Flags, Parameter, ScopeId, Str, Symbol, SymbolId, SymbolKind, TemplateParameter, TypeAssertion,
    Visibility, new_symbol_id,
};
use swlsp_php_types::{Kind as TypeKind, Relations, Type, contains_template, phpdoc_alias};
use swlsp_syntax::SyntaxNode;

use crate::builder::Builder;
use crate::modifiers::text;

pub(crate) fn leading_doc_comment<'s>(node: &SyntaxNode, source: &'s str) -> &'s str {
    let range = node.text_range();
    let trimmed = node.trimmed();
    if trimmed.start() <= range.start() {
        return "";
    }
    let full = text(source, node);
    let prefix_length = (u32::from(trimmed.start() - range.start()) as usize).min(full.len());
    let prefix = &full[..prefix_length];
    let Some(start) = prefix.rfind("/**") else {
        return "";
    };
    let Some(end) = prefix[start..].find("*/") else {
        return "";
    };
    let end = end + start + 2;
    if !prefix[end..].trim().is_empty() {
        return "";
    }
    &prefix[start..end]
}

/// Chooses between a native declaration type and its PHPDoc refinement.
pub fn effective_type(native: &Type, documented: &Type) -> Type {
    if documented.is_unknown() || documented.kind() == TypeKind::Error {
        return native.clone();
    }
    if native.is_unknown() || native.kind() == TypeKind::Mixed {
        return documented.clone();
    }
    if documented.kind() == TypeKind::SelfType && native.kind() == TypeKind::SelfType {
        return documented.clone();
    }
    if documented.kind() == TypeKind::Parent && native.kind() == TypeKind::Parent {
        return documented.clone();
    }
    if documented.kind() == TypeKind::Static {
        if matches!(native.kind(), TypeKind::SelfType | TypeKind::Object) {
            return documented.clone();
        }
        if let Some(base) = nullable_native_base(native)
            && base.kind() == TypeKind::Object
        {
            return Type::nullable(documented.clone());
        }
    }
    if documented.kind() == TypeKind::Callable && native.kind() == TypeKind::Callable {
        return documented.clone();
    }
    if documented.kind() == TypeKind::Conditional {
        return documented.clone();
    }
    if contains_template(documented) {
        return documented.clone();
    }
    // PHPDoc commonly narrows an interface or abstract native declaration to
    // the concrete object accepted by a particular implementation. Binding
    // happens before a workspace hierarchy is available, so two differently
    // named object types cannot be proven related here. Preserve the
    // documented contract and retain the native type separately.
    if documented.kind() == TypeKind::Object && native.kind() == TypeKind::Object {
        return documented.clone();
    }
    let relations = Relations::default();
    if let Some(base) = nullable_native_base(native)
        && relations.is_subtype(documented, base)
    {
        // Native nullability remains part of the runtime contract when
        // PHPDoc only refines the non-null payload (`?array` plus
        // `@return array<K,V>`).
        return Type::nullable(documented.clone());
    }
    if relations.is_subtype(documented, native) {
        return documented.clone();
    }
    native.clone()
}

fn nullable_native_base(value: &Type) -> Option<&Type> {
    if value.kind() != TypeKind::Union || value.argument_count() != 2 {
        return None;
    }
    let arguments = value.arguments();
    if arguments[0].kind() == TypeKind::Null {
        Some(&arguments[1])
    } else if arguments[1].kind() == TypeKind::Null {
        Some(&arguments[0])
    } else {
        None
    }
}

pub(crate) fn bind_templates(
    values: &[Template],
    context: &NameContext,
    inherited: &[String],
) -> Vec<TemplateParameter> {
    let mut names: Vec<&str> = inherited.iter().map(String::as_str).collect();
    names.extend(values.iter().map(|value| value.name.as_str()));
    values
        .iter()
        .map(|value| TemplateParameter {
            name: Str::from(value.name.as_str()),
            bound: context.resolve_phpdoc_type(&value.bound, &names),
            default: context.resolve_phpdoc_type(&value.default, &names),
            covariant: value.covariant,
            contravariant: value.contravariant,
        })
        .collect()
}

pub(crate) fn bind_assertions(
    values: &[Assertion],
    context: &NameContext,
    templates: &[String],
) -> Vec<TypeAssertion> {
    values
        .iter()
        .map(|value| TypeAssertion {
            target: Str::from(value.target.as_str()),
            ty: context.resolve_phpdoc_type(&value.ty, templates),
            when_true: value.when_true,
            conditional: value.conditional,
            negated: value.negated,
        })
        .collect()
}

pub(crate) fn resolve_doc_types(
    values: &[Type],
    context: &NameContext,
    templates: &[String],
) -> Vec<Type> {
    values
        .iter()
        .map(|value| context.resolve_phpdoc_type(value, templates))
        .collect()
}

pub(crate) fn template_names(values: &[Template]) -> Vec<String> {
    values.iter().map(|value| value.name.clone()).collect()
}

pub(crate) fn apply_document_flags(symbol: &mut Symbol, documentation: &PhpDoc) {
    if documentation.deprecated {
        symbol.flags |= Flags::DEPRECATED;
    }
    if documentation.internal {
        symbol.flags |= Flags::INTERNAL;
    }
    if documentation.is_final {
        symbol.flags |= Flags::SOFT_FINAL;
    }
    if documentation.is_abstract && !symbol.flags.has(Flags::ABSTRACT) {
        symbol.flags |= Flags::SOFT_ABSTRACT;
    }
}

pub(crate) fn apply_attribute_flags(symbol: &mut Symbol) {
    let deprecated = symbol.attributes().iter().any(|attribute| {
        let name = attribute.name.strip_prefix('\\').unwrap_or(&attribute.name);
        eq_ignore_case(name, "Deprecated")
            || swlsp_textutil::lowercase(name).ends_with("\\deprecated")
    });
    if deprecated {
        symbol.flags |= Flags::DEPRECATED;
    }
}

pub(crate) fn append_unique(values: &mut Vec<Str>, value: &str) {
    if value.is_empty()
        || values
            .iter()
            .any(|existing| eq_ignore_case(existing, value))
    {
        return;
    }
    values.push(Str::from(value));
}

impl Builder<'_> {
    pub(crate) fn with_phpdoc_aliases(
        &mut self,
        mut context: NameContext,
        class_name: &str,
        documentation: &PhpDoc,
        templates: &[String],
    ) -> NameContext {
        if context.phpdoc_aliases.is_empty()
            && documentation.imports.is_empty()
            && documentation.aliases.is_empty()
        {
            return context;
        }
        let mut aliases: HashMap<Str, Type> = HashMap::with_capacity(
            context.phpdoc_aliases.len()
                + documentation.imports.len()
                + documentation.aliases.len(),
        );
        for (name, value) in context.phpdoc_aliases.iter() {
            aliases.insert(name.clone(), value.clone());
        }
        for (local_name, imported) in &documentation.imports {
            aliases.insert(
                Str::from(local_name.as_str()),
                phpdoc_alias(&context.resolve_class(&imported.from), &imported.name),
            );
        }
        for name in documentation.aliases.keys() {
            aliases.insert(Str::from(name.as_str()), phpdoc_alias(class_name, name));
        }
        context.set_phpdoc_aliases(aliases);
        for local_name in documentation.imports.keys() {
            let value = context.phpdoc_aliases[local_name.as_str()].clone();
            self.doc
                .type_aliases
                .insert(Str::from(local_name.as_str()), value);
        }
        for (name, value) in &documentation.aliases {
            let resolved = context.resolve_phpdoc_type(value, templates);
            self.doc
                .type_aliases
                .insert(Str::from(name.as_str()), resolved);
        }
        context
    }

    pub(crate) fn bind_type_aliases(
        &mut self,
        documentation: &PhpDoc,
        scope: ScopeId,
        context: &NameContext,
        container: &SymbolId,
        templates: &[String],
    ) {
        for (name, value) in &documentation.aliases {
            let resolved = context.resolve_phpdoc_type(value, templates);
            let fully_qualified = format!("{container}::{name}");
            let symbol = Symbol {
                id: new_symbol_id(SymbolKind::TypeAlias, &fully_qualified, &self.path, 0),
                kind: SymbolKind::TypeAlias,
                name: Str::from(name.as_str()),
                fully_qualified: Str::from(fully_qualified),
                container: container.clone(),
                path: self.path.clone(),
                flags: Flags::SYNTHETIC,
                ty: resolved.clone(),
                doc_type: resolved,
                ..Symbol::default()
            };
            self.add_symbol(scope, symbol);
        }
    }

    pub(crate) fn bind_synthetic_members(
        &mut self,
        documentation: &PhpDoc,
        scope: ScopeId,
        context: &NameContext,
        container: &SymbolId,
    ) {
        let templates = self.container_template_names(container);
        for property in &documentation.properties {
            if self.member_exists(container, SymbolKind::Property, &property.name) {
                continue;
            }
            let value = context.resolve_phpdoc_type(&property.ty, &templates);
            let mut flags = Flags::SYNTHETIC;
            if property.read_only {
                flags |= Flags::READONLY;
            }
            let name = format!("{container}::${}", property.name);
            let symbol = Symbol {
                id: new_symbol_id(SymbolKind::Property, &name, &self.path, 0),
                kind: SymbolKind::Property,
                name: Str::from(property.name.as_str()),
                fully_qualified: Str::from(name),
                container: container.clone(),
                path: self.path.clone(),
                visibility: Visibility::Public,
                flags,
                ty: value.clone(),
                doc_type: value,
                ..Symbol::default()
            };
            self.add_symbol(scope, symbol);
        }
        for method in &documentation.methods {
            if self.member_exists(container, SymbolKind::Method, &method.name) {
                continue;
            }
            let name = format!("{container}::{}", method.name);
            let return_type = context.resolve_phpdoc_type(&method.return_type, &templates);
            let mut symbol = Symbol {
                id: new_symbol_id(SymbolKind::Method, &name, &self.path, 0),
                kind: SymbolKind::Method,
                name: Str::from(method.name.as_str()),
                fully_qualified: Str::from(name.as_str()),
                container: container.clone(),
                path: self.path.clone(),
                visibility: Visibility::Public,
                flags: Flags::SYNTHETIC,
                return_type: return_type.clone(),
                doc_type: return_type,
                ..Symbol::default()
            };
            if method.is_static {
                symbol.flags |= Flags::STATIC;
            }
            symbol.parameters = method
                .parameters
                .iter()
                .enumerate()
                .map(|(index, parameter)| {
                    let value = context.resolve_phpdoc_type(&parameter.ty, &templates);
                    Parameter {
                        id: new_symbol_id(
                            SymbolKind::Parameter,
                            &format!("{name}:{}", parameter.name),
                            &self.path,
                            index as u32,
                        ),
                        name: Str::from(parameter.name.as_str()),
                        ty: value.clone(),
                        doc_type: value,
                        assistant_tags: parameter
                            .tags
                            .iter()
                            .map(|tag| Str::from(tag.as_str()))
                            .collect(),
                        optional: parameter.optional,
                        flags: if parameter.variadic {
                            Flags::VARIADIC
                        } else {
                            Flags::empty()
                        },
                        ..Parameter::default()
                    }
                })
                .collect();
            self.add_symbol(scope, symbol);
        }
    }

    /// Template names declared by `container` and its enclosing
    /// declarations, innermost first and deduplicated.
    pub(crate) fn container_template_names(&self, container: &SymbolId) -> Vec<String> {
        let mut result: Vec<String> = Vec::new();
        let mut seen: FxHashSet<Str> = FxHashSet::default();
        let mut current = container.clone();
        while !current.is_empty() {
            let Some(symbol) = self.symbol(&current) else {
                break;
            };
            for template in symbol.templates() {
                if seen.insert(template.name.clone()) {
                    result.push(template.name.to_string());
                }
            }
            current = symbol.container.clone();
        }
        result
    }

    pub(crate) fn member_exists(&self, container: &SymbolId, kind: SymbolKind, name: &str) -> bool {
        // Members are always added after their container.
        let start = self.symbol_index(container).unwrap_or(0) as usize;
        self.doc.symbols[start..].iter().any(|symbol| {
            symbol.container == *container
                && symbol.kind == kind
                && eq_ignore_case(&symbol.name, name)
        })
    }
}
