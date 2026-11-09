//! Structural queries over JavaScript trees.
//!
//! Every function accepting a "cursor" node also accepts any descendant of the
//! construct it looks for (it walks up to the nearest matching ancestor), so
//! callers can pass the node under the editor cursor directly.

use std::collections::HashMap;

use swlsp_syntax::{Kind, SyntaxNode, rowan::NodeOrToken};

use crate::syntax::*;

// -- whole-tree queries ---------------------------------------------------

/// All nodes of the given kinds in preorder, `root` included.
pub fn nodes(root: &SyntaxNode, kinds: &[Kind]) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| kinds.contains(&node.kind()))
        .collect()
}

/// Call expressions in preorder whose [`call_name`] is one of `names`; every
/// call when `names` is empty.
pub fn calls(root: &SyntaxNode, names: &[&str]) -> Vec<SyntaxNode> {
    iter_calls(root, names).collect()
}

/// Lazy variant of [`calls`].
pub fn iter_calls<'n>(
    root: &SyntaxNode,
    names: &'n [&'n str],
) -> impl Iterator<Item = SyntaxNode> + 'n {
    root.descendants()
        .filter(move |node| node.kind() == JS_CALL_EXPRESSION && call_matches(node, names))
}

fn call_matches(call: &SyntaxNode, names: &[&str]) -> bool {
    names.is_empty() || call_name(call).is_some_and(|name| names.contains(&name.as_str()))
}

/// Records a tree's nodes by kind once, for analyses that issue several
/// whole-document queries.
#[derive(Clone, Debug)]
pub struct NodeIndex {
    root: SyntaxNode,
    by_kind: HashMap<Kind, Vec<SyntaxNode>>,
}

impl NodeIndex {
    pub fn new(root: &SyntaxNode) -> Self {
        let mut by_kind: HashMap<Kind, Vec<SyntaxNode>> = HashMap::new();
        for node in root.descendants() {
            by_kind.entry(node.kind()).or_default().push(node);
        }
        Self {
            root: root.clone(),
            by_kind,
        }
    }

    /// Nodes of one kind in preorder.
    pub fn nodes_of_kind(&self, kind: Kind) -> &[SyntaxNode] {
        self.by_kind.get(&kind).map_or(&[], Vec::as_slice)
    }

    /// Nodes of any of the given kinds in preorder.
    pub fn nodes(&self, kinds: &[Kind]) -> Vec<SyntaxNode> {
        match kinds {
            [] => Vec::new(),
            [kind] => self.nodes_of_kind(*kind).to_vec(),
            _ => nodes(&self.root, kinds),
        }
    }

    pub fn calls(&self, names: &[&str]) -> Vec<SyntaxNode> {
        self.iter_calls(names).cloned().collect()
    }

    pub fn iter_calls<'s>(
        &'s self,
        names: &'s [&'s str],
    ) -> impl Iterator<Item = &'s SyntaxNode> + 's {
        self.nodes_of_kind(JS_CALL_EXPRESSION)
            .iter()
            .filter(move |call| call_matches(call, names))
    }
}

// -- calls ----------------------------------------------------------------

pub fn call_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, JS_CALL_EXPRESSION)
}

/// The expression invoked by the call at `node`.
pub fn call_callee(node: &SyntaxNode) -> Option<SyntaxNode> {
    call_at(node)?.first_child()
}

/// The compact callee text of the call at `node` (for example
/// `Shopware.Component.register`), trivia removed. For a callee that is itself
/// a call, the inner call's name.
pub fn call_name(node: &SyntaxNode) -> Option<String> {
    let call = call_at(node)?;
    for child in call.children() {
        match child.kind() {
            JS_MEMBER_EXPRESSION | JS_IDENTIFIER => return Some(compact_node_expression(&child)),
            JS_CALL_EXPRESSION => return call_name(&child),
            _ => {}
        }
    }
    None
}

/// The terminal identifier of a call's callee; stable for fluent chains such
/// as `a.add(...).add(...)`.
pub fn call_method_name(node: &SyntaxNode) -> Option<String> {
    let callee = call_callee(node)?;
    match callee.kind() {
        JS_IDENTIFIER => Some(trimmed_text(&callee)),
        JS_MEMBER_EXPRESSION => last_identifier_child(&callee).map(|child| trimmed_text(&child)),
        _ => None,
    }
}

/// The terminal identifier of a member expression (or the identifier itself).
pub fn member_name_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    match node.kind() {
        JS_IDENTIFIER => Some(node.clone()),
        JS_MEMBER_EXPRESSION => last_identifier_child(node),
        _ => None,
    }
}

// -- arguments ------------------------------------------------------------

/// The argument nodes (`JS_ARGUMENT`) of the call at `node`.
pub fn iter_arguments(node: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> {
    call_at(node)
        .and_then(|call| direct_child(&call, JS_ARGUMENT_LIST))
        .into_iter()
        .flat_map(|list| list.children().filter(|child| child.kind() == JS_ARGUMENT))
}

pub fn arguments(node: &SyntaxNode) -> Vec<SyntaxNode> {
    iter_arguments(node).collect()
}

pub fn argument(node: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    iter_arguments(node).nth(index)
}

/// The expression node inside argument `index`.
pub fn argument_expression(node: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    argument(node, index)?.first_child()
}

/// The first string literal inside argument `index`.
pub fn string_argument(node: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    argument(node, index)?
        .descendants()
        .find(|node| node.kind() == JS_STRING)
}

/// The first object literal inside argument `index`.
pub fn object_argument(node: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    argument(node, index)?
        .descendants()
        .find(|node| node.kind() == JS_OBJECT)
}

// -- strings --------------------------------------------------------------

pub fn string_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, JS_STRING)
}

/// The content of the string literal at `node` without its quotes (a missing
/// closing quote is tolerated). Empty when `node` is not inside a string.
pub fn string_value(node: &SyntaxNode) -> String {
    let Some(string) = string_at(node) else {
        return String::new();
    };
    let Some(token) = string
        .descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
        .find(|token| matches!(token.kind(), TK_STRING | TK_TEMPLATE))
    else {
        return String::new();
    };
    let text = token.text();
    let Some(quote @ ('\'' | '"' | '`')) = text.chars().next() else {
        return text.to_owned();
    };
    let inner = &text[1..];
    inner.strip_suffix(quote).unwrap_or(inner).to_owned()
}

/// The index of the call argument containing the string at `node`.
pub fn string_argument_index(node: &SyntaxNode) -> Option<usize> {
    let string = string_at(node)?;
    let call = call_at(&string)?;
    iter_arguments(&call).position(|argument| contains(&argument, &string))
}

/// Whether `node` is the string at `argument_index` of one of the named calls.
pub fn string_in_call(node: &SyntaxNode, argument_index: usize, names: &[&str]) -> bool {
    if string_at(node).is_none() || string_argument_index(node) != Some(argument_index) {
        return false;
    }
    call_name(node).is_some_and(|name| names.contains(&name.as_str()))
}

// -- exports and imports --------------------------------------------------

pub fn export_defaults(root: &SyntaxNode) -> Vec<SyntaxNode> {
    nodes(root, &[JS_EXPORT_DEFAULT])
}

pub fn export_default_expression(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, JS_EXPORT_DEFAULT)?.first_child()
}

/// The module path of the first import statement binding `binding` (any
/// import when `binding` is empty).
pub fn import_path(root: &SyntaxNode, binding: &str) -> Option<String> {
    root.descendants()
        .filter(|node| node.kind() == JS_IMPORT_STATEMENT)
        .filter(|statement| binding.is_empty() || has_identifier(statement, binding))
        .find_map(|statement| {
            statement
                .descendants()
                .find(|node| node.kind() == JS_STRING)
        })
        .map(|string| string_value(&string))
}

/// The path of the first `import('...')` call with a string argument.
pub fn dynamic_import_path(root: &SyntaxNode) -> Option<String> {
    iter_calls(root, &["import"])
        .find_map(|call| string_argument(&call, 0))
        .map(|path| string_value(&path))
}

// -- objects and arrays ---------------------------------------------------

/// The properties and methods of an object literal.
pub fn properties(object: &SyntaxNode) -> Vec<SyntaxNode> {
    iter_properties(object).collect()
}

fn iter_properties(object: &SyntaxNode) -> impl Iterator<Item = SyntaxNode> {
    (object.kind() == JS_OBJECT)
        .then(|| {
            object
                .children()
                .filter(|child| matches!(child.kind(), JS_PROPERTY | JS_METHOD))
        })
        .into_iter()
        .flatten()
}

/// The property or method named `name` of an object literal.
pub fn property(object: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    iter_properties(object).find(|property| property_name(property).as_deref() == Some(name))
}

/// The closest property containing `node`.
pub fn property_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, JS_PROPERTY)
}

/// The name of a property or method (string keys unquoted, modifiers and
/// comments ignored).
pub fn property_name(node: &SyntaxNode) -> Option<String> {
    let name = property_name_node(node)?;
    Some(match name.kind() {
        JS_STRING => string_value(&name),
        JS_IDENTIFIER => identifier_token_text(&name),
        _ => trimmed_text(&name),
    })
}

pub fn property_name_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    if !matches!(node.kind(), JS_PROPERTY | JS_METHOD) {
        return None;
    }
    node.children()
        .find(|child| matches!(child.kind(), JS_IDENTIFIER | JS_STRING))
}

/// The value expression of a property (its second child node).
pub fn property_value(node: &SyntaxNode) -> Option<SyntaxNode> {
    if node.kind() != JS_PROPERTY {
        return None;
    }
    node.children().nth(1)
}

pub fn method_name(node: &SyntaxNode) -> Option<String> {
    if node.kind() != JS_METHOD {
        return None;
    }
    property_name(node)
}

pub fn array_items(array: &SyntaxNode) -> Vec<SyntaxNode> {
    if array.kind() != JS_ARRAY {
        return Vec::new();
    }
    array.children().collect()
}

pub fn object_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, JS_OBJECT)
}

// -- member expressions ---------------------------------------------------

pub fn member_expression_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, JS_MEMBER_EXPRESSION)
}

/// The name of a direct Vue instance member (`this.<name>`) at `node`. Nested
/// access such as `this.repository.search` only matches on `repository`.
/// The name is empty for an incomplete `this.`.
pub fn this_member(node: &SyntaxNode) -> Option<String> {
    let member = member_expression_at(node)?;
    let text = compact_node_expression(&member).replace("?.", ".");
    let name = text.strip_prefix("this.")?;
    if name.contains(['.', '[', '(', ')']) {
        return None;
    }
    Some(name.to_owned())
}

/// The identifier node of the direct `this.<name>` member at `node`.
pub fn this_member_name_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    let member = member_expression_at(node)?;
    this_member(&member)?;
    last_identifier_child(&member)
}

/// A direct member accessed from a Shopware store lookup.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StoreMember {
    /// The store id passed to `Store.get`.
    pub store: String,
    /// The accessed member; empty for an incomplete `Store.get('x').`.
    pub member: String,
}

/// Matches `Shopware.Store.get('<store>').<member>` and `Store.get(...)`.
/// Nested access is conservative: in `Store.get('session').currentUser.id`,
/// `currentUser` matches while `id` does not.
pub fn store_member(node: &SyntaxNode) -> Option<StoreMember> {
    let member = member_expression_at(node)?;
    let mut children = member.children();
    let receiver = children.next()?;
    if receiver.kind() != JS_CALL_EXPRESSION {
        return None;
    }
    let name = call_name(&receiver)?;
    if name != "Shopware.Store.get" && name != "Store.get" {
        return None;
    }
    let store = string_argument(&receiver, 0)
        .map(|string| string_value(&string))
        .unwrap_or_default();
    let member = children
        .filter(|child| child.kind() == JS_IDENTIFIER)
        .last()
        .map(|child| trimmed_text(&child))
        .unwrap_or_default();
    (!store.is_empty()).then_some(StoreMember { store, member })
}

// -- identifiers ----------------------------------------------------------

/// The identifier text of the identifier node at `node`.
pub fn identifier_text(node: &SyntaxNode) -> Option<String> {
    ancestor_or_self(node, JS_IDENTIFIER).map(|identifier| identifier_token_text(&identifier))
}

fn identifier_token_text(identifier: &SyntaxNode) -> String {
    identifier
        .children_with_tokens()
        .filter_map(NodeOrToken::into_token)
        .find(|token| matches!(token.kind(), TK_IDENTIFIER | TK_KEYWORD))
        .map_or_else(|| trimmed_text(identifier), |token| token.text().to_owned())
}

// -- helpers --------------------------------------------------------------

fn last_identifier_child(node: &SyntaxNode) -> Option<SyntaxNode> {
    node.children()
        .filter(|child| child.kind() == JS_IDENTIFIER)
        .last()
}

fn direct_child(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.children().find(|child| child.kind() == kind)
}

fn ancestor_or_self(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|node| node.kind() == kind)
}

fn contains(parent: &SyntaxNode, child: &SyntaxNode) -> bool {
    parent.text_range().contains_range(child.text_range())
}

fn trimmed_text(node: &SyntaxNode) -> String {
    node.text().to_string().trim().to_owned()
}

fn has_identifier(node: &SyntaxNode, name: &str) -> bool {
    node.descendants()
        .any(|child| child.kind() == JS_IDENTIFIER && trimmed_text(&child) == name)
        || node
            .descendants_with_tokens()
            .filter_map(NodeOrToken::into_token)
            .any(|token| token.kind() == TK_IDENTIFIER && token.text() == name)
}

/// Node text without outer whitespace and, when it may contain trivia,
/// without any trivia tokens.
fn compact_node_expression(node: &SyntaxNode) -> String {
    let text = node.text().to_string();
    let trimmed = text.trim();
    if !may_contain_trivia(trimmed) {
        return trimmed.to_owned();
    }
    let mut result = String::with_capacity(trimmed.len());
    for token in node
        .descendants_with_tokens()
        .filter_map(NodeOrToken::into_token)
    {
        if !token.kind().is_trivia() {
            result.push_str(token.text());
        }
    }
    result
}

fn may_contain_trivia(text: &str) -> bool {
    let bytes = text.as_bytes();
    bytes.iter().enumerate().any(|(index, byte)| match byte {
        b' ' | b'\t' | 0x0C | b'\r' | b'\n' => true,
        b'/' => matches!(bytes.get(index + 1), Some(b'/' | b'*')),
        _ => false,
    })
}

#[cfg(test)]
mod tests {
    use swlsp_syntax::{SyntaxNodeExt, TextSize};

    use super::*;
    use crate::parse;

    fn root(source: &str) -> SyntaxNode {
        parse(source).root()
    }

    fn node_at(root: &SyntaxNode, offset: usize) -> SyntaxNode {
        root.node_at(TextSize::from(offset as u32))
            .expect("node at offset")
    }

    #[test]
    fn component_call_queries() {
        let root = root("Component.extend('child', 'parent', { props: { title: String } });");
        let calls = calls(&root, &["Component.extend"]);
        assert_eq!(calls.len(), 1);
        let call = &calls[0];
        assert_eq!(string_value(&string_argument(call, 0).unwrap()), "child");
        assert_eq!(string_value(&string_argument(call, 1).unwrap()), "parent");

        let object = object_argument(call, 2).unwrap();
        let props = property(&object, "props").unwrap();
        assert!(property_name_node(&props).is_some());
        assert_eq!(property_value(&props).unwrap().kind(), JS_OBJECT);
        assert_eq!(arguments(call).len(), 3);

        let mut iterator = iter_arguments(call);
        for expected in 0..3 {
            assert_eq!(iterator.next(), argument(call, expected));
        }
        assert!(iterator.next().is_none());
        assert!(argument(call, 3).is_none());
        assert_eq!(argument_expression(call, 2).unwrap().kind(), JS_OBJECT);
    }

    #[test]
    fn node_index_matches_whole_tree_queries() {
        let root = root(
            "
const alias = Shopware.Utils;
alias.format.date(value);
Shopware.Component.register('sw-card', {});
",
        );
        let index = NodeIndex::new(&root);
        assert_eq!(
            nodes(&root, &[JS_VARIABLE_DECLARATION]),
            index.nodes(&[JS_VARIABLE_DECLARATION])
        );
        assert_eq!(
            nodes(&root, &[JS_VARIABLE_DECLARATION, JS_CALL_EXPRESSION]),
            index.nodes(&[JS_VARIABLE_DECLARATION, JS_CALL_EXPRESSION])
        );
        assert_eq!(calls(&root, &[]), index.calls(&[]));
        assert_eq!(calls(&root, &[]).len(), 2);
        let named = ["Shopware.Component.register"];
        assert_eq!(calls(&root, &named), index.calls(&named));
        assert_eq!(calls(&root, &named).len(), 1);
        assert_eq!(
            calls(&root, &[]),
            iter_calls(&root, &[]).collect::<Vec<_>>()
        );
        assert_eq!(
            calls(&root, &named),
            iter_calls(&root, &named).collect::<Vec<_>>()
        );
        assert_eq!(
            calls(&root, &[]),
            index.iter_calls(&[]).cloned().collect::<Vec<_>>()
        );
        assert_eq!(iter_calls(&root, &[]).take(1).count(), 1);
        assert!(index.nodes(&[]).is_empty());
    }

    #[test]
    fn export_and_import_queries() {
        let root = root(
            "import template from './x.html.twig'; export default { template, emits: ['save'] };",
        );
        assert_eq!(
            import_path(&root, "template").as_deref(),
            Some("./x.html.twig")
        );
        assert_eq!(import_path(&root, "").as_deref(), Some("./x.html.twig"));
        assert_eq!(import_path(&root, "missing"), None);
        let exports = export_defaults(&root);
        assert_eq!(exports.len(), 1);
        let object = export_default_expression(&exports[0]).unwrap();
        assert_eq!(object.kind(), JS_OBJECT);
        let emits = property_value(&property(&object, "emits").unwrap()).unwrap();
        assert_eq!(string_value(&array_items(&emits)[0]), "save");
    }

    #[test]
    fn dynamic_import() {
        let root = root("export default { template: () => import('./x.html.twig') };");
        assert_eq!(dynamic_import_path(&root).as_deref(), Some("./x.html.twig"));
    }

    #[test]
    fn string_cursor_context() {
        let source = "Component.extend('child', 'parent')";
        let root = root(source);
        let node = node_at(&root, source.len() - 4);
        assert_eq!(string_value(&node), "parent");
        assert_eq!(string_argument_index(&node), Some(1));
        assert_eq!(call_name(&node).as_deref(), Some("Component.extend"));
        assert!(string_in_call(&node, 1, &["Component.extend"]));
        assert!(!string_in_call(&node, 0, &["Component.extend"]));
        assert!(!string_in_call(&node, 1, &["Component.register"]));
    }

    #[test]
    fn unclosed_string_value() {
        let root = root("Component.extend('child', 'sw-par");
        let call = &calls(&root, &[])[0];
        assert_eq!(string_value(&string_argument(call, 1).unwrap()), "sw-par");
    }

    #[test]
    fn this_member_context() {
        let source = "this.repository.search()";
        let root = root(source);
        let repository = node_at(&root, source.find("repository").unwrap() + 1);
        assert_eq!(this_member(&repository).as_deref(), Some("repository"));
        assert_eq!(
            identifier_text(&this_member_name_node(&repository).unwrap()).as_deref(),
            Some("repository")
        );
        let search = node_at(&root, source.find("search").unwrap() + 1);
        assert_eq!(this_member(&search), None);
        assert_eq!(this_member_name_node(&search), None);

        let incomplete = "this.";
        let root = super::tests::root(incomplete);
        assert_eq!(
            this_member(&node_at(&root, incomplete.len() - 1)).as_deref(),
            Some("")
        );
    }

    #[test]
    fn call_method_name_for_fluent_calls() {
        let root = root(
            "Application.addServiceProvider('one', factory)
        .addServiceProvider('two', factory);
Shopware.Service().register('three', factory);",
        );
        let methods: Vec<_> = calls(&root, &[])
            .iter()
            .filter_map(call_method_name)
            .collect();
        assert!(methods.iter().any(|method| method == "addServiceProvider"));
        assert!(methods.iter().any(|method| method == "register"));
    }

    #[test]
    fn call_name_ignores_leading_comments() {
        let root = root(
            "
// eslint-disable-next-line
Module.register('sw-product', {});
/* keep this call discoverable */
Shopware.Component.register('sw-card', {});",
        );
        assert_eq!(calls(&root, &["Module.register"]).len(), 1);
        assert_eq!(calls(&root, &["Shopware.Component.register"]).len(), 1);
    }

    #[test]
    fn call_name_with_trivia() {
        let root = root("\n    Shopware /* keep */ . Component . register('sw-card', {});\n");
        let call = &calls(&root, &[])[0];
        assert_eq!(
            call_name(call).as_deref(),
            Some("Shopware.Component.register")
        );
        let identifier = member_name_node(&call_callee(call).unwrap()).unwrap();
        assert_eq!(identifier_text(&identifier).as_deref(), Some("register"));
    }

    #[test]
    fn call_name_preserves_trivia_inside_computed_member_literals() {
        let root = root(r#"registry["a b"].handlers["https://example.test"]();"#);
        let calls = calls(&root, &[]);
        assert_eq!(calls.len(), 1);
        assert_eq!(
            call_name(&calls[0]).as_deref(),
            Some(r#"registry["a b"].handlers["https://example.test"]"#)
        );
    }

    #[test]
    fn property_name_ignores_leading_comments_and_method_modifiers() {
        let root = root(
            "({ methods: {
        // eslint-disable-next-line
        async validate(value: string): Promise<void> {},
    } })",
        );
        let objects = nodes(&root, &[JS_OBJECT]);
        assert!(objects.len() >= 2);
        let methods_property = property(&objects[0], "methods").unwrap();
        let methods = properties(&property_value(&methods_property).unwrap());
        assert_eq!(methods.len(), 1);
        assert_eq!(property_name(&methods[0]).as_deref(), Some("validate"));
        assert_eq!(method_name(&methods[0]).as_deref(), Some("validate"));
        assert_eq!(property_at(&methods[0]), Some(methods_property.clone()));
        assert_eq!(
            object_at(&methods[0]),
            Some(property_value(&methods_property).unwrap())
        );
    }

    #[test]
    fn store_member_context() {
        let source = "Shopware.Store.get('session').currentUser.id";
        let root = root(source);
        let current_user = node_at(&root, source.find("currentUser").unwrap() + 1);
        assert_eq!(
            store_member(&current_user),
            Some(StoreMember {
                store: "session".into(),
                member: "currentUser".into()
            })
        );
        let id = node_at(&root, source.rfind("id").unwrap() + 1);
        assert_eq!(store_member(&id), None);

        let incomplete = "Store.get('context').";
        let root = super::tests::root(incomplete);
        assert_eq!(
            store_member(&node_at(&root, incomplete.len() - 1)),
            Some(StoreMember {
                store: "context".into(),
                member: String::new()
            })
        );
    }
}
