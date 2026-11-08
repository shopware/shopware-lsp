//! Runtime members PHP adds to every enum.

use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{
    Flags, Parameter, ScopeId, Str, Symbol, SymbolKind, Visibility, new_symbol_id,
};
use swlsp_php_types::{Type, go_to_lower};
use swlsp_syntax::SyntaxNode;
use swlsp_syntax::rowan::NodeOrToken;

use crate::builder::Builder;

impl Builder<'_> {
    pub(crate) fn enum_backing_type(&self, node: &SyntaxNode, context: &NameContext) -> Type {
        let mut after_colon = false;
        for child in node.children_with_tokens() {
            match child {
                NodeOrToken::Node(child) => {
                    if child.kind() == PHP_CLASS_BODY {
                        return Type::unknown();
                    }
                }
                NodeOrToken::Token(token) => {
                    if token.kind() == TK_COLON {
                        after_colon = true;
                        continue;
                    }
                    if !after_colon || token.kind().is_trivia() {
                        continue;
                    }
                    return match go_to_lower(token.text()).as_str() {
                        "int" | "string" => self.bind_native_type(token.text(), context),
                        _ => Type::unknown(),
                    };
                }
            }
        }
        Type::unknown()
    }

    pub(crate) fn bind_enum_runtime_members(
        &mut self,
        scope: ScopeId,
        enum_index: u32,
        backing: &Type,
    ) {
        let fully_qualified = self.doc.symbols[enum_index as usize]
            .fully_qualified
            .clone();
        let enum_type = Type::named(&fully_qualified, Vec::new());
        self.bind_enum_runtime_method(
            scope,
            enum_index,
            "cases",
            Type::list(enum_type.clone()),
            Vec::new(),
        );
        self.bind_enum_runtime_property(scope, enum_index, "name", Type::string());
        if !backing.is_unknown() {
            self.bind_enum_runtime_property(scope, enum_index, "value", backing.clone());
            let value_parameter = Parameter {
                name: Str::from("$value"),
                ty: backing.clone(),
                native_type: backing.clone(),
                ..Parameter::default()
            };
            self.bind_enum_runtime_method(
                scope,
                enum_index,
                "from",
                enum_type.clone(),
                vec![value_parameter.clone()],
            );
            self.bind_enum_runtime_method(
                scope,
                enum_index,
                "tryFrom",
                Type::nullable(enum_type),
                vec![value_parameter],
            );
        }
    }

    fn bind_enum_runtime_method(
        &mut self,
        scope: ScopeId,
        enum_index: u32,
        name: &str,
        result: Type,
        parameters: Vec<Parameter>,
    ) {
        let owner = &self.doc.symbols[enum_index as usize];
        let fully_qualified = format!("{}::{name}", owner.fully_qualified);
        let symbol = Symbol {
            id: new_symbol_id(SymbolKind::Method, &fully_qualified, &self.path, 0),
            kind: SymbolKind::Method,
            name: Str::from(name),
            fully_qualified: Str::from(fully_qualified),
            container: owner.id.clone(),
            path: self.path.clone(),
            visibility: Visibility::Public,
            flags: Flags::STATIC | Flags::INTERNAL | Flags::SYNTHETIC,
            return_type: result.clone(),
            native_type: result,
            parameters,
            ..Symbol::default()
        };
        self.add_symbol(scope, symbol);
    }

    fn bind_enum_runtime_property(
        &mut self,
        scope: ScopeId,
        enum_index: u32,
        name: &str,
        value: Type,
    ) {
        let owner = &self.doc.symbols[enum_index as usize];
        let fully_qualified = format!("{}::${name}", owner.fully_qualified);
        let symbol = Symbol {
            id: new_symbol_id(SymbolKind::Property, &fully_qualified, &self.path, 0),
            kind: SymbolKind::Property,
            name: Str::from(name),
            fully_qualified: Str::from(fully_qualified),
            container: owner.id.clone(),
            path: self.path.clone(),
            visibility: Visibility::Public,
            flags: Flags::READONLY | Flags::INTERNAL | Flags::SYNTHETIC,
            ty: value.clone(),
            native_type: value,
            ..Symbol::default()
        };
        self.add_symbol(scope, symbol);
    }
}
