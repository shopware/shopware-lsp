//! The entity a Criteria in Administration code is searched on.
//!
//! A Criteria object carries no entity: the entity is the one of the
//! repository it is handed to. This follows that hand-over inside one file,
//! so a field path can be resolved against the right definition.

use std::collections::{BTreeMap, BTreeSet};

use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax::{
    JS_CALL_EXPRESSION, JS_EXPRESSION_STATEMENT, JS_FUNCTION, JS_IDENTIFIER, JS_MEMBER_EXPRESSION,
    JS_METHOD, JS_PROPERTY, JS_VARIABLE_DECLARATION, TK_OPERATOR,
};
use swlsp_syntax::{SyntaxNode, rowan::NodeOrToken};

use crate::context::{JsEntityReferenceKind, js_entity_reference_at, js_field_reference_prefix};
use crate::index::{Definition, resolve_association_path};

/// How many `const inner = outer.getAssociation(...)` hops are followed.
const MAX_ALIAS_DEPTH: usize = 4;
/// Repository methods that take the Criteria.
const SEARCH_METHODS: &[&str] = &["search", "searchIds", "get", "aggregate"];

/// The entity a field path starts at.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct JsCriteriaRoot {
    pub entity: String,
    /// Associations already entered through `getAssociation('…')` before the
    /// path in the string begins.
    pub associations: Vec<String>,
}

/// The definition whose fields the path segment at `offset` names: the
/// searched entity, followed through the associations before the cursor.
pub fn js_criteria_definition<'a>(
    definitions: &'a [Definition],
    node: &SyntaxNode,
    offset: u32,
) -> Option<&'a Definition> {
    let root = js_criteria_root(node)?;
    let mut typed = js_field_reference_prefix(node, offset);
    // A path may start with the name of the entity it is resolved on.
    if root.associations.is_empty() && typed.first() == Some(&root.entity) {
        typed.remove(0);
    }
    let mut path = root.associations;
    path.append(&mut typed);
    resolve_association_path(definitions, &root.entity, &path)
}

/// What holds the Criteria a field path belongs to.
#[derive(Debug, PartialEq, Eq)]
enum Holder {
    /// A local variable (`criteria.addFilter(…)`).
    Local(String),
    /// A component member (`this.criteria.addFilter(…)`).
    Member(String),
    /// Nothing named, as in `new Criteria().addFilter(…)`.
    Unnamed,
}

/// The entity the Criteria field path at `node` is resolved against. `None`
/// when the file does not show which repository receives the Criteria.
pub fn js_criteria_root(node: &SyntaxNode) -> Option<JsCriteriaRoot> {
    let string = jsquery::string_at(node)?;
    let root = string.ancestors().last()?;
    let scope = string
        .ancestors()
        .find(|ancestor| matches!(ancestor.kind(), JS_METHOD | JS_FUNCTION));
    let (holder, associations) = criteria_holder(&string, scope.as_ref().unwrap_or(&root));

    let calls = jsquery::nodes(&root, &[JS_CALL_EXPRESSION]);
    let repositories = repositories(&calls);
    let searches: Vec<Search> = calls
        .iter()
        .filter_map(|call| search(call, &repositories))
        .collect();
    let scope_range = scope.as_ref().map(SyntaxNode::text_range);
    let in_scope = |search: &&Search| {
        scope_range.is_none_or(|range| range.contains_range(search.call.text_range()))
    };
    let method = scope.as_ref().and_then(jsquery::property_name);

    // The Criteria is built inside the search call itself.
    let inline = searches
        .iter()
        .filter(|search| search.call.text_range().contains_range(string.text_range()))
        .min_by_key(|search| search.call.text_range().len())
        .map(|search| search.entity.clone());
    let held = || match &holder {
        Holder::Local(name) => single(
            searches
                .iter()
                .filter(in_scope)
                .filter(|search| search.locals.contains(name))
                .map(|search| search.entity.as_str()),
        ),
        Holder::Member(name) => searched_member(&searches, name),
        Holder::Unnamed => None,
    };
    // The enclosing method returns the Criteria, and a search takes it.
    let returned = || searched_member(&searches, method.as_deref()?);
    // `productCriteria` beside `productRepository`: the Criteria is handed
    // over in a template, the names pair them.
    let paired = || {
        let name = match &holder {
            Holder::Member(name) => name.as_str(),
            _ => method.as_deref()?,
        };
        let repository = format!("{}Repository", name.strip_suffix("Criteria")?);
        single(repositories.get(&repository)?.iter().map(String::as_str))
    };
    let entity = inline.or_else(held).or_else(returned).or_else(paired)?;
    Some(JsCriteriaRoot {
        entity,
        associations,
    })
}

/// The entity of the searches that take the component member `name`.
fn searched_member(searches: &[Search], name: &str) -> Option<String> {
    single(
        searches
            .iter()
            .filter(|search| search.members.iter().any(|member| member == name))
            .map(|search| search.entity.as_str()),
    )
}

/// What holds the Criteria the string belongs to, and the associations
/// entered between that holder and the string.
fn criteria_holder(string: &SyntaxNode, scope: &SyntaxNode) -> (Holder, Vec<String>) {
    let mut associations = Vec::new();
    let Some(mut base) = owning_call(string).and_then(|call| chain_base(&call, &mut associations))
    else {
        return (Holder::Unnamed, associations);
    };
    for _ in 0..MAX_ALIAS_DEPTH {
        let Some(name) = local_name(&base) else {
            let holder = jsquery::this_member(&base).map_or(Holder::Unnamed, Holder::Member);
            return (holder, associations);
        };
        let Some(initializer) = association_alias(scope, &name) else {
            return (Holder::Local(name), associations);
        };
        let mut outer = association_segments(&initializer);
        let next = chain_base(&initializer, &mut outer);
        outer.append(&mut associations);
        associations = outer;
        match next {
            Some(next) => base = next,
            None => return (Holder::Unnamed, associations),
        }
    }
    (Holder::Unnamed, associations)
}

/// The Criteria method call the string is an argument of, skipping the
/// static `Criteria.equals(…)` style factories around it.
fn owning_call(string: &SyntaxNode) -> Option<SyntaxNode> {
    let mut call = jsquery::call_at(string)?;
    while jsquery::call_name(&call).is_some_and(|name| name.starts_with("Criteria.")) {
        call = jsquery::call_at(&call.parent()?)?;
    }
    Some(call)
}

/// Walks a fluent chain from `call` to the expression it starts at,
/// prepending the associations of every `getAssociation` on the way.
fn chain_base(call: &SyntaxNode, associations: &mut Vec<String>) -> Option<SyntaxNode> {
    let mut current = call.clone();
    loop {
        let callee = current.first_child()?;
        if callee.kind() != JS_MEMBER_EXPRESSION {
            return None;
        }
        let object = callee.first_child()?;
        if object.kind() != JS_CALL_EXPRESSION {
            return Some(object);
        }
        let mut outer = association_segments(&object);
        outer.append(associations);
        *associations = outer;
        current = object;
    }
}

/// The path of a `….getAssociation('a.b')` call, empty for any other call.
fn association_segments(call: &SyntaxNode) -> Vec<String> {
    if jsquery::call_method_name(call).as_deref() != Some("getAssociation") {
        return Vec::new();
    }
    jsquery::string_argument(call, 0)
        .map(|string| jsquery::string_value(&string))
        .unwrap_or_default()
        .split('.')
        .filter(|segment| !segment.is_empty())
        .map(str::to_owned)
        .collect()
}

fn local_name(expression: &SyntaxNode) -> Option<String> {
    (expression.kind() == JS_IDENTIFIER)
        .then(|| jsquery::identifier_text(expression))
        .flatten()
        .filter(|name| name != "this")
}

/// The initializer of `const <name> = <criteria>.getAssociation(…)` in `scope`.
fn association_alias(scope: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    jsquery::nodes(scope, &[JS_VARIABLE_DECLARATION])
        .iter()
        .filter_map(|declaration| declared_value(declaration, name))
        .find(|value| {
            value.kind() == JS_CALL_EXPRESSION
                && jsquery::call_method_name(value).as_deref() == Some("getAssociation")
        })
}

/// The value assigned to `name` in a variable declaration.
fn declared_value(declaration: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    let mut children = declaration.children();
    while let Some(child) = children.next() {
        if child.kind() == JS_IDENTIFIER
            && jsquery::identifier_text(&child).as_deref() == Some(name)
        {
            return children.next();
        }
    }
    None
}

/// A repository call that receives a Criteria.
struct Search {
    call: SyntaxNode,
    entity: String,
    /// Local variables passed as arguments.
    locals: Vec<String>,
    /// Component members passed as arguments (`this.x` or `this.x()`).
    members: Vec<String>,
}

fn search(call: &SyntaxNode, repositories: &BTreeMap<String, BTreeSet<String>>) -> Option<Search> {
    let method = jsquery::call_method_name(call)?;
    if !SEARCH_METHODS.contains(&method.as_str()) {
        return None;
    }
    let callee = call.first_child()?;
    if callee.kind() != JS_MEMBER_EXPRESSION {
        return None;
    }
    let object = callee.first_child()?;
    let entity = match object.kind() {
        JS_CALL_EXPRESSION => repository_entity(&object)?,
        _ => single(
            repositories
                .get(&reference_name(&object)?)?
                .iter()
                .map(String::as_str),
        )?,
    };
    let mut locals = Vec::new();
    let mut members = Vec::new();
    for argument in jsquery::iter_arguments(call) {
        let Some(mut expression) = argument.first_child() else {
            continue;
        };
        if expression.kind() == JS_CALL_EXPRESSION {
            match expression.first_child() {
                Some(callee) => expression = callee,
                None => continue,
            }
        }
        if let Some(name) = local_name(&expression) {
            locals.push(name);
        } else if let Some(name) = jsquery::this_member(&expression) {
            members.push(name);
        }
    }
    Some(Search {
        call: call.clone(),
        entity,
        locals,
        members,
    })
}

/// The entities of the repositories a file creates, by the name they are
/// held under: a computed property, a variable or an assigned member.
fn repositories(calls: &[SyntaxNode]) -> BTreeMap<String, BTreeSet<String>> {
    let mut result: BTreeMap<String, BTreeSet<String>> = BTreeMap::new();
    for call in calls {
        let (Some(entity), Some(name)) = (repository_entity(call), holder_name(call)) else {
            continue;
        };
        result.entry(name).or_default().insert(entity);
    }
    result
}

/// The entity of a `repositoryFactory.create('<entity>')` call.
fn repository_entity(call: &SyntaxNode) -> Option<String> {
    let string = jsquery::string_argument(call, 0)?;
    js_entity_reference_at(&string)
        .filter(|reference| reference.kind == JsEntityReferenceKind::Repository)
        .map(|reference| reference.name)
}

/// The name a created repository is kept under.
fn holder_name(call: &SyntaxNode) -> Option<String> {
    let mut inner = call.clone();
    for ancestor in call.ancestors().skip(1) {
        match ancestor.kind() {
            JS_VARIABLE_DECLARATION => {
                let mut name = None;
                for child in ancestor.children() {
                    if child == inner {
                        return name;
                    }
                    name = (child.kind() == JS_IDENTIFIER)
                        .then(|| jsquery::identifier_text(&child))
                        .flatten();
                }
                return None;
            }
            JS_EXPRESSION_STATEMENT => {
                if let Some(name) = assigned_name(&ancestor, &inner) {
                    return Some(name);
                }
            }
            JS_METHOD | JS_PROPERTY => return jsquery::property_name(&ancestor),
            JS_FUNCTION => return None,
            _ => {}
        }
        inner = ancestor;
    }
    None
}

/// The target of `<target> = <value>` when `value` is the assigned side.
fn assigned_name(statement: &SyntaxNode, value: &SyntaxNode) -> Option<String> {
    let mut elements = statement.children_with_tokens();
    let target = elements.next()?.into_node()?;
    if target == *value {
        return None;
    }
    let assigns = elements
        .filter_map(NodeOrToken::into_token)
        .any(|token| token.kind() == TK_OPERATOR && token.text() == "=");
    if !assigns {
        return None;
    }
    reference_name(&target)
}

/// The name of a local (`repository`) or component member (`this.repository`).
fn reference_name(expression: &SyntaxNode) -> Option<String> {
    match expression.kind() {
        JS_IDENTIFIER => local_name(expression),
        JS_MEMBER_EXPRESSION => jsquery::this_member(expression),
        _ => None,
    }
}

/// The one distinct value of `values`, if there is exactly one.
fn single<'a>(values: impl Iterator<Item = &'a str>) -> Option<String> {
    let distinct: BTreeSet<&str> = values.collect();
    (distinct.len() == 1)
        .then(|| distinct.into_iter().next().map(str::to_owned))
        .flatten()
}
