//! By-reference argument outputs and variable reference relinking.

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::{
    Flags, NameKind, Str, Symbol, SymbolId, SymbolKind, TypeSource, new_symbol_id,
};
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use crate::analyzer::{AnalyzerState, Fs};
use crate::env::Env;
use crate::extensions::CallArgument;
use crate::syntax::{last_node, start};

impl Fs<'_, '_, '_> {
    pub(crate) fn apply_by_reference_arguments(
        &mut self,
        call: &SyntaxNode,
        symbol: &Symbol,
        arguments: &[CallArgument],
        env: &Env,
    ) {
        let parameters = &symbol.parameters;
        if !parameters
            .iter()
            .any(|parameter| parameter.flags.has(Flags::BY_REFERENCE))
        {
            return;
        }
        let nodes: Vec<SyntaxNode> = query::arguments(call).collect();
        if nodes.is_empty() {
            return;
        }
        let last_variadic = parameters
            .last()
            .is_some_and(|parameter| parameter.flags.has(Flags::VARIADIC));
        let mut provided = vec![false; parameters.len()];
        let mut next_positional = 0;
        for (argument_index, argument) in arguments.iter().enumerate() {
            let mut parameter_index = None;
            if !argument.name.is_empty() {
                let name = argument.name.strip_prefix('$').unwrap_or(&argument.name);
                parameter_index = parameters.iter().position(|parameter| {
                    parameter.name.strip_prefix('$').unwrap_or(&parameter.name) == name
                });
                if parameter_index.is_none() && last_variadic {
                    parameter_index = Some(parameters.len() - 1);
                }
            } else {
                while next_positional < parameters.len()
                    && provided[next_positional]
                    && !parameters[next_positional].flags.has(Flags::VARIADIC)
                {
                    next_positional += 1;
                }
                if next_positional < parameters.len() {
                    parameter_index = Some(next_positional);
                } else if last_variadic {
                    parameter_index = Some(parameters.len() - 1);
                }
            }
            let Some(parameter_index) = parameter_index else {
                continue;
            };
            let parameter = &parameters[parameter_index];
            provided[parameter_index] = true;
            if !parameter.flags.has(Flags::VARIADIC) {
                next_positional = parameter_index + 1;
            }
            if !parameter.flags.has(Flags::BY_REFERENCE) || argument_index >= nodes.len() {
                continue;
            }
            let Some(value) = last_node(&nodes[argument_index]) else {
                continue;
            };
            if value.kind() != PHP_VARIABLE {
                continue;
            }
            self.declare_by_reference_output(&value, &parameter.ty, env);
        }
    }

    fn declare_by_reference_output(&mut self, node: &SyntaxNode, value: &Type, env: &Env) {
        let name = query::variable_key(node);
        if name.is_empty() {
            return;
        }
        let mut value = if value.is_unknown() {
            Type::MIXED
        } else {
            value.clone()
        };
        if let Some(previous) = env.get(&name) {
            value = self.relations.join(&previous, &value);
        }
        env.set(&name, value.clone());
        self.record(node, &value, TypeSource::Signature, "by-reference argument");

        let offset = start(node);
        let document = &self.st.document;
        let Some(scope_index) = document.scope_at(offset).map(|scope| scope.id as usize) else {
            return;
        };
        let mut current = scope_index;
        loop {
            let scope = &document.scopes[current];
            if scope.has_symbol(&document.symbols, &name) {
                self.update_local_type(&name, offset, &value);
                return;
            }
            let parent = scope.parent as usize;
            if scope.id as usize == parent || parent >= document.scopes.len() {
                break;
            }
            current = parent;
        }

        let document = &mut self.st.document;
        let owner = document.scopes[scope_index].owner.clone();
        let fully_qualified = format!("{owner}:{name}");
        let range = node.range_trimmed_trivia();
        let symbol = Symbol {
            id: new_symbol_id(SymbolKind::Local, &fully_qualified, &document.path, offset),
            kind: SymbolKind::Local,
            name: Str::from(name),
            fully_qualified: Str::from(fully_qualified),
            container: owner,
            path: document.path.clone(),
            range,
            selection_range: range,
            ty: value,
            ..Symbol::default()
        };
        let symbol_index = document.symbols.len() as u32;
        document.symbols.push(symbol);
        document.scopes[scope_index].add_symbol(symbol_index);
    }
}

impl AnalyzerState<'_> {
    /// Resolves unresolved variable references to locals declared during
    /// analysis (for example by-reference outputs).
    pub(crate) fn relink_variable_references(&mut self) {
        let document = &mut self.document;
        for index in 0..document.references.len() {
            let reference = &document.references[index];
            if reference.kind != NameKind::Variable
                || !reference.resolved.is_empty()
                || !reference.candidate_ids().is_empty()
                || reference.scope as usize >= document.scopes.len()
            {
                continue;
            }
            let name = reference.name.clone();
            let mut scope = reference.scope as usize;
            let mut resolved: Option<SymbolId> = None;
            loop {
                let current = &document.scopes[scope];
                if let Some(id) = current.symbol_ids(&document.symbols, &name).next() {
                    resolved = Some(id.clone());
                    break;
                }
                let parent = current.parent as usize;
                if current.id as usize == parent || parent >= document.scopes.len() {
                    break;
                }
                scope = parent;
            }
            if let Some(id) = resolved {
                document.references[index].resolved = id;
            }
        }
    }
}
