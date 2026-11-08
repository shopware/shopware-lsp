//! Batch-scoped canonicalization of retained graph values.
//!
//! Identifier-like strings (class, member, parameter, and template names)
//! are shared through the current atom generation. Free text (summaries,
//! attribute expressions, literal values) is deduplicated only within the
//! batch so that discarded documents release it. Types are deduplicated
//! structurally and their names interned, so equal types in different
//! documents share one immutable node.

use std::sync::Arc;

use rustc_hash::FxHashSet;
use swlsp_php_types::{Detacher, Type};

use crate::str::Str;

use crate::atom::intern_str;
use crate::call_contract::{CallContract, CallValue, ExpectedArgumentContract};
use crate::packed::SymbolDetail;
use crate::symbol::{
    Attribute, AttributeValue, Shared, SignatureExtras, SymbolHierarchy, SymbolMetadata,
};

pub(crate) struct Canon {
    types: FxHashSet<Type>,
    text: FxHashSet<Str>,
    detacher: Detacher<'static>,
    /// Re-homing into a new interner generation: types (whose names are
    /// `Arc`s, not atoms) and free text are kept as they are, so values
    /// shared across documents stay shared.
    identifiers_only: bool,
}

impl Default for Canon {
    fn default() -> Self {
        Canon {
            types: FxHashSet::default(),
            text: FxHashSet::default(),
            detacher: Detacher::new(),
            identifiers_only: false,
        }
    }
}

impl Canon {
    /// A canonicalizer that only re-interns identifiers.
    pub fn identifiers_only() -> Self {
        Canon {
            identifiers_only: true,
            ..Canon::default()
        }
    }

    pub fn with_capacity(types: usize) -> Self {
        let mut canon = Canon::default();
        canon.types.reserve(types);
        canon
    }

    pub fn identifier(&mut self, value: &Str) -> Str {
        if value.is_empty() {
            Str::EMPTY
        } else {
            intern_str(value)
        }
    }

    pub fn text(&mut self, value: &Str) -> Str {
        if self.identifiers_only || value.is_empty() || value.is_static() || value.is_interned() {
            return value.clone();
        }
        if let Some(existing) = self.text.get(value) {
            return existing.clone();
        }
        self.text.insert(value.clone());
        value.clone()
    }

    pub fn ty(&mut self, value: &Type) -> Type {
        if self.identifiers_only {
            return value.clone();
        }
        if let Some(existing) = self.types.get(value) {
            return existing.clone();
        }
        let detached = self.detacher.detach(value);
        self.types.insert(detached.clone());
        detached
    }

    fn types(&mut self, values: &mut [Type]) {
        for value in values {
            *value = self.ty(value);
        }
    }

    fn identifiers(&mut self, values: &mut [Str]) {
        for value in values {
            *value = self.identifier(value);
        }
    }

    pub fn hierarchy(&mut self, hierarchy: &mut Arc<SymbolHierarchy>) {
        let mut value = SymbolHierarchy::clone(hierarchy);
        self.identifiers(&mut value.extends);
        self.identifiers(&mut value.implements);
        self.identifiers(&mut value.traits);
        self.types(&mut value.extends_types);
        self.types(&mut value.implements_types);
        self.types(&mut value.trait_types);
        for alias in &mut value.aliases {
            alias.trait_name = self.identifier(&alias.trait_name);
            alias.method = self.identifier(&alias.method);
            alias.alias = self.identifier(&alias.alias);
        }
        *hierarchy = Arc::new(value);
    }

    pub fn detail(&mut self, detail: &mut Arc<SymbolDetail>) {
        let source: &SymbolDetail = detail;
        let mut parameters = Vec::with_capacity(source.parameters.len());
        for parameter in source.parameters.iter() {
            let extras = parameter
                .extras
                .as_ref()
                .map(|extras| crate::packed::ParameterExtras {
                    id: extras.id.clone(),
                    ranges: None,
                    ty: extras.ty.clone(),
                    assistant_tags: extras.assistant_tags.clone(),
                    attributes: extras.attributes.clone(),
                    default_value: extras.default_value.clone(),
                });
            let mut parameter = crate::packed::PackedParameter::new(
                parameter.name.in_target(),
                self.ty(&parameter.native_type),
                self.ty(&parameter.doc_type),
                parameter.ranges(),
                extras.unwrap_or_default(),
                parameter.flags,
                parameter.source,
                parameter.optional,
            );
            if let Some(extras) = &mut parameter.extras {
                extras.ty = self.ty(&extras.ty);
                self.identifiers(&mut extras.assistant_tags);
                self.attributes(&mut extras.attributes);
                if let Some(value) = &mut extras.default_value {
                    self.attribute_value(value);
                }
            }
            parameters.push(parameter);
        }
        let signature = source.signature.get().map(|signature| {
            let mut value = SignatureExtras::clone(signature);
            for template in &mut value.templates {
                template.name = self.identifier(&template.name);
                template.bound = self.ty(&template.bound);
                template.default = self.ty(&template.default);
            }
            self.types(&mut value.throws);
            for assertion in &mut value.assertions {
                assertion.target = self.identifier(&assertion.target);
                assertion.ty = self.ty(&assertion.ty);
            }
            for literal in &mut value.literal_returns {
                literal.value = self.text(&literal.value);
                literal.ty = self.ty(&literal.ty);
            }
            for constant in &mut value.constant_returns {
                constant.receiver = self.identifier(&constant.receiver);
                constant.name = self.identifier(&constant.name);
            }
            Arc::new(value)
        });
        let metadata = source.metadata.get().map(|metadata| {
            let mut value = SymbolMetadata::clone(metadata);
            self.attributes(&mut value.attributes);
            for item in &mut value.constant_array {
                item.key = self.text(&item.key);
                item.value = self.text(&item.value);
                item.ty = self.ty(&item.ty);
            }
            Arc::new(value)
        });
        *detail = Arc::new(SymbolDetail {
            parameters: parameters.into_boxed_slice(),
            signature: Shared::from_arc(signature),
            metadata: Shared::from_arc(metadata),
        });
    }

    fn attributes(&mut self, attributes: &mut [Attribute]) {
        for attribute in attributes {
            attribute.name = self.identifier(&attribute.name);
            for argument in &mut attribute.arguments {
                argument.name = self.identifier(&argument.name);
                self.attribute_value(&mut argument.value);
            }
        }
    }

    fn attribute_value(&mut self, value: &mut AttributeValue) {
        value.value = self.text(&value.value);
        value.expression = self.text(&value.expression);
        for item in &mut value.items {
            self.attribute_value(&mut item.key);
            self.attribute_value(&mut item.value);
        }
    }

    pub fn call_contract(&mut self, contract: &mut CallContract) {
        contract.target.name = self.identifier(&contract.target.name);
        contract.target.class = self.identifier(&contract.target.class);
        for entry in &mut contract.return_contract.map {
            self.call_value(&mut entry.key);
            self.call_value(&mut entry.result);
        }
        self.expected(&mut contract.expected_arguments);
        for value in &mut contract.expected_return_values {
            self.call_value(value);
        }
        self.expected(&mut contract.exit_arguments);
    }

    fn expected(&mut self, contracts: &mut [ExpectedArgumentContract]) {
        for contract in contracts {
            for value in &mut contract.values {
                self.call_value(value);
            }
        }
    }

    fn call_value(&mut self, value: &mut CallValue) {
        value.value = self.text(&value.value);
        value.expression = self.text(&value.expression);
    }
}
