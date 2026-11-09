//! Closures and arrow functions.

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::Symbol;
use swlsp_php_types::{CallableParameter, Type};
use swlsp_syntax::SyntaxNode;

use crate::analyzer::Fs;
use crate::env::Env;
use crate::syntax::{
    contains_direct_yield, has_direct_token, has_direct_token_text, last_node, start,
};
use crate::types_util::join_types;

impl Fs<'_, '_, '_> {
    pub(crate) fn infer_arrow_function(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
        parameter_types: &[Type],
    ) -> Type {
        let closure_env = env.fork();
        let parameters = self.callable_parameters(node, &closure_env, parameter_types);
        let mut result = self.infer_opt(last_node(node).as_ref(), &closure_env);
        let declared = query::method_return_type(node);
        if !declared.is_empty() {
            result = self.parse_native_type(&declared, start(node));
        }
        Type::callable(parameters, result)
    }

    pub(crate) fn infer_closure(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
        parameter_types: &[Type],
    ) -> Type {
        let closure_env = Env::new(0);
        if !has_direct_token_text(node, "static")
            && let Some(value) = env.get("$this")
        {
            closure_env.set("$this", value);
        }
        for captured in node.children() {
            if captured.kind() != PHP_VARIABLE {
                continue;
            }
            let name = query::variable_key(&captured);
            if let Some(value) = env.get(&name) {
                closure_env.set(&name, value);
            }
        }
        let parameters = self.callable_parameters(node, &closure_env, parameter_types);
        let mut declared = Type::UNKNOWN;
        let source = query::method_return_type(node);
        if !source.is_empty() {
            declared = self.parse_native_type(&source, start(node));
        }
        let symbol = Symbol {
            return_type: declared.clone(),
            ..Symbol::default()
        };
        let relations = self.relations;
        let returns = {
            let mut nested = self.nested(symbol);
            if let Some(body) = query::direct_child(node, PHP_BLOCK) {
                nested.generator = contains_direct_yield(&body);
                nested.analyze_block(&body, &closure_env);
            }
            std::mem::take(&mut nested.returns)
        };
        let mut result = join_types(relations, &returns);
        if !declared.is_unknown() {
            result = declared;
        }
        Type::callable(parameters, result)
    }

    fn callable_parameters(
        &mut self,
        node: &SyntaxNode,
        env: &Env,
        parameter_types: &[Type],
    ) -> Vec<CallableParameter> {
        let mut result = Vec::new();
        for parameter in query::parameters(node) {
            let name = query::parameter_name(&parameter);
            let mut value =
                self.parse_native_type(&query::parameter_type(&parameter), start(&parameter));
            if let Some(contextual) = parameter_types.get(result.len())
                && !contextual.is_unknown()
            {
                value = contextual.clone();
            }
            env.set(&name, value.clone());
            result.push(CallableParameter {
                name: name.as_str().into(),
                ty: value,
                optional: query::parameter_optional(&parameter),
                variadic: has_direct_token(&parameter, TK_ELLIPSIS),
                by_reference: false,
            });
        }
        result
    }
}
