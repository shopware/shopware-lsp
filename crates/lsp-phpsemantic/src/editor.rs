//! Document outline and highlights (port of `document_symbols.go` and
//! `document_highlights.go`).

use std::collections::HashMap;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Cancelled, Context, DocumentHighlightRequest, TextDocument};
use swlsp_lsp_protocol::{DocumentHighlight, DocumentHighlightKind, DocumentSymbol, SymbolKind};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_ANONYMOUS_CLASS, PHP_ATTRIBUTE_GROUP, PHP_BLOCK, PHP_CLASS_CONST_DECLARATION,
    PHP_CLASS_DECLARATION, PHP_CONST_DECLARATION, PHP_ENUM_CASE_DECLARATION, PHP_ENUM_DECLARATION,
    PHP_FUNCTION_DECLARATION, PHP_INTERFACE_DECLARATION, PHP_METHOD_DECLARATION, PHP_NAME,
    PHP_NAMESPACE, PHP_PARAMETER_LIST, PHP_PROPERTY_DECLARATION, PHP_TRAIT_DECLARATION,
    PHP_TRAIT_USE_DECLARATION, PHP_USE_DECLARATION, PHP_VARIABLE, TK_KEYWORD,
};
use swlsp_php_semantic::SymbolKind as PhpSymbolKind;
use swlsp_syntax::{Kind, LineIndex, SyntaxElement, SyntaxNode, SyntaxNodeExt, TextRange};

use crate::Provider;
use crate::helpers::range_from_text;

pub(crate) fn php_editor_document(document: &TextDocument) -> bool {
    document.language == Some(LanguageId::Php) && document.tree().is_some()
}

impl Provider {
    pub(crate) fn document_symbols(
        &self,
        ctx: &Context,
        document: &TextDocument,
    ) -> Result<Vec<DocumentSymbol>, BoxError> {
        ctx.check()?;
        if !php_editor_document(document) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let builder = Outline {
            ctx,
            lines: document.line_index(),
        };
        let result = builder.children(&root);
        ctx.check()?;
        Ok(result)
    }

    pub(crate) fn document_highlights(
        &self,
        ctx: &Context,
        request: &DocumentHighlightRequest,
    ) -> Result<Vec<DocumentHighlight>, BoxError> {
        ctx.check()?;
        let Some(document) = request.document.as_deref() else {
            return Ok(Vec::new());
        };
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        if !php_editor_document(document) {
            return Ok(Vec::new());
        }
        let Some(state) = swlsp_lsp_phpanalysis::for_document(index, document)? else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let position = request.params.position;
        let offset = document
            .line_index()
            .offset_utf16(position.line, position.character);
        let Some(target) = swlsp_php::symbol_at(&state.document, &state.snapshot, offset) else {
            return Ok(Vec::new());
        };
        // Text < Read < Write, like the LSP numeric values.
        let mut occurrences: HashMap<TextRange, u8> = HashMap::new();
        let mut add = |range: TextRange, kind: u8| {
            if range.is_empty() {
                return;
            }
            let current = occurrences.entry(range).or_insert(0);
            if kind > *current {
                *current = kind;
            }
        };
        for symbol in &state.document.symbols {
            if ctx.is_cancelled() {
                return Err(Box::new(Cancelled));
            }
            if symbol.id == target.id {
                let kind = match symbol.kind {
                    PhpSymbolKind::Local | PhpSymbolKind::Parameter | PhpSymbolKind::Property => 3,
                    _ => 1,
                };
                add(symbol.selection_range, kind);
            }
        }
        for reference in &state.document.references {
            if ctx.is_cancelled() {
                return Err(Box::new(Cancelled));
            }
            if reference.resolved != target.id {
                continue;
            }
            let write = reference.write
                || root
                    .node_at(reference.range.start())
                    .is_some_and(|node| query::variable_is_write(&node));
            add(reference.range, if write { 3 } else { 2 });
        }
        let mut ranges: Vec<(TextRange, u8)> = occurrences.into_iter().collect();
        ranges.sort_by_key(|(range, _)| (range.start(), range.end()));
        Ok(ranges
            .into_iter()
            .map(|(range, kind)| DocumentHighlight {
                range: range_from_text(document.line_index(), range),
                kind: Some(match kind {
                    3 => DocumentHighlightKind::WRITE,
                    2 => DocumentHighlightKind::READ,
                    _ => DocumentHighlightKind::TEXT,
                }),
            })
            .collect())
    }
}

struct Outline<'a> {
    ctx: &'a Context,
    lines: &'a LineIndex,
}

impl Outline<'_> {
    fn children(&self, parent: &SyntaxNode) -> Vec<DocumentSymbol> {
        let mut result: Vec<DocumentSymbol> = Vec::new();
        let mut namespace: Option<usize> = None;
        for node in parent.children() {
            if self.ctx.is_cancelled() {
                break;
            }
            if node.kind() == PHP_NAMESPACE {
                if let Some(open) = namespace {
                    result[open].range.end =
                        range_from_text(self.lines, node.range_trimmed_trivia()).start;
                }
                namespace = None;
                let symbols = self.node(&node);
                let added = !symbols.is_empty();
                result.extend(symbols);
                if added && query::direct_child(&node, PHP_BLOCK).is_none() {
                    let open = result.len() - 1;
                    result[open].range.end = range_from_text(self.lines, parent.text_range()).end;
                    namespace = Some(open);
                }
                continue;
            }
            let symbols = self.node(&node);
            match namespace {
                Some(open) => result[open].children.extend(symbols),
                None => result.extend(symbols),
            }
        }
        result
    }

    fn node(&self, node: &SyntaxNode) -> Vec<DocumentSymbol> {
        if let Some(kind) = outline_kind(node.kind()) {
            let Some(name) = query::direct_child(node, PHP_NAME) else {
                if node.kind() == PHP_NAMESPACE
                    && let Some(keyword) = node.child_token_of_kind(TK_KEYWORD)
                {
                    let mut symbol = self.symbol(
                        node,
                        &SyntaxElement::Token(keyword),
                        "(global)",
                        SymbolKind::NAMESPACE,
                    );
                    symbol.children = self.children(node);
                    return vec![symbol];
                }
                return self.children(node);
            };
            let mut symbol = self.symbol(
                node,
                &SyntaxElement::Node(name.clone()),
                &query::name_value(&name),
                kind,
            );
            symbol.children = self.children(node);
            if node.kind() == PHP_METHOD_DECLARATION
                && swlsp_php_types::equal_fold(&symbol.name, "__construct")
            {
                symbol.kind = SymbolKind::CONSTRUCTOR;
                // Promoted parameters are properties of the class, not of the
                // constructor.
                let mut result = vec![symbol];
                for parameter in query::parameters(node) {
                    if query::declaration_visibility(&parameter).is_empty() {
                        continue;
                    }
                    if let Some(variable) = query::direct_child(&parameter, PHP_VARIABLE) {
                        result.push(self.symbol(
                            &parameter,
                            &SyntaxElement::Node(variable.clone()),
                            &query::variable_name(&variable),
                            SymbolKind::PROPERTY,
                        ));
                    }
                }
                return result;
            }
            return vec![symbol];
        }
        match node.kind() {
            PHP_ANONYMOUS_CLASS => {
                let Some(keyword) = node.child_token_of_kind(TK_KEYWORD) else {
                    return Vec::new();
                };
                let mut symbol = self.symbol(
                    node,
                    &SyntaxElement::Token(keyword),
                    "(anonymous class)",
                    SymbolKind::CLASS,
                );
                symbol.children = self.children(node);
                vec![symbol]
            }
            PHP_PROPERTY_DECLARATION => query::property_variables(node)
                .into_iter()
                .filter_map(|variable| {
                    let name = query::variable_name(&variable);
                    if name.is_empty() {
                        return None;
                    }
                    let mut symbol = self.symbol(
                        node,
                        &SyntaxElement::Node(variable),
                        &name,
                        SymbolKind::PROPERTY,
                    );
                    symbol.detail = query::property_type(node);
                    Some(symbol)
                })
                .collect(),
            PHP_CONST_DECLARATION | PHP_CLASS_CONST_DECLARATION => query::constant_names(node)
                .into_iter()
                .map(|name| {
                    let label = query::name_value(&name);
                    self.symbol(
                        node,
                        &SyntaxElement::Node(name),
                        &label,
                        SymbolKind::CONSTANT,
                    )
                })
                .collect(),
            PHP_PARAMETER_LIST
            | PHP_ATTRIBUTE_GROUP
            | PHP_USE_DECLARATION
            | PHP_TRAIT_USE_DECLARATION => Vec::new(),
            _ => self.children(node),
        }
    }

    fn symbol(
        &self,
        node: &SyntaxNode,
        name: &SyntaxElement,
        label: &str,
        kind: SymbolKind,
    ) -> DocumentSymbol {
        let selection = match name {
            SyntaxElement::Node(node) => node.range_trimmed_trivia(),
            SyntaxElement::Token(token) => token.text_range(),
        };
        DocumentSymbol {
            name: label.to_owned(),
            detail: String::new(),
            kind,
            deprecated: false,
            range: range_from_text(self.lines, node.range_trimmed_trivia()),
            selection_range: range_from_text(self.lines, selection),
            children: Vec::new(),
        }
    }
}

fn outline_kind(kind: Kind) -> Option<SymbolKind> {
    Some(match kind {
        PHP_NAMESPACE => SymbolKind::NAMESPACE,
        PHP_CLASS_DECLARATION => SymbolKind::CLASS,
        PHP_INTERFACE_DECLARATION => SymbolKind::INTERFACE,
        PHP_TRAIT_DECLARATION => SymbolKind::CLASS,
        PHP_ENUM_DECLARATION => SymbolKind::ENUM,
        PHP_ENUM_CASE_DECLARATION => SymbolKind::ENUM_MEMBER,
        PHP_METHOD_DECLARATION => SymbolKind::METHOD,
        PHP_FUNCTION_DECLARATION => SymbolKind::FUNCTION,
        _ => return None,
    })
}
