//! What the Admin API actions of a controller take and answer.
//!
//! The contract is read from the controller alone:
//!
//! - the route gives the path, its placeholders and the HTTP method;
//! - a `#[MapRequestPayload]` or `#[MapQueryString]` parameter gives the
//!   body or query as the properties of its class;
//! - reads from a `Request` or a data bag (`$request->request->get('id')`,
//!   `$data->getInt('limit')`) give one key each, typed by the getter or a
//!   cast. A key is required unless it is read with a default, tested with
//!   `has()` or read as a boolean. A request or bag that is handed on, or
//!   read with a key that is not a literal, may hold more: its shape is
//!   open;
//! - the returned responses give the answer: the inferred type of what a
//!   `JsonResponse` is built from, nothing for an empty response, a file
//!   for a download.

use std::collections::BTreeSet;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::{Document, Flags, Snapshot, Symbol, SymbolKind, Visibility};
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

use super::php_semantic::Run;
use super::util::{ranges_touch, trim_php_name};
use crate::api_service::{Field, Shape};
use crate::diagnostics::common::{local_call_argument, short_php_name, unwrap_php_parentheses};

const REQUEST_CLASS: &str = "Symfony\\Component\\HttpFoundation\\Request";
const JSON_RESPONSE_CLASS: &str = "Symfony\\Component\\HttpFoundation\\JsonResponse";
const REQUEST_BAG_CLASS: &str = "Shopware\\Core\\Framework\\Validation\\DataBag\\RequestDataBag";
const QUERY_BAG_CLASS: &str = "Shopware\\Core\\Framework\\Validation\\DataBag\\QueryDataBag";
const CRITERIA_CLASS: &str = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\Criteria";
const STRUCT_CLASS: &str = "Shopware\\Core\\Framework\\Struct\\Struct";
const COLLECTION_CLASS: &str = "Shopware\\Core\\Framework\\Struct\\Collection";
/// Where Shopware encodes a struct as its properties.
const STRUCT_ENCODERS: &[&str] = &[
    STRUCT_CLASS,
    "Shopware\\Core\\Framework\\Struct\\JsonSerializableTrait",
];
/// Serialized as an object of all their fields, which no property declares.
const RECORD_CLASSES: &[&str] = &[
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\Entity",
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult",
    "Shopware\\Core\\Framework\\Struct\\ArrayStruct",
    "ArrayObject",
];
/// Responses that send a file.
const FILE_RESPONSES: &[&str] = &["BinaryFileResponse", "StreamedResponse"];
/// How deep the properties of nested classes are followed.
const MAX_CLASS_DEPTH: usize = 4;

/// The Admin API actions of one controller class.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct ApiController {
    /// The class name without its namespace.
    pub class_name: String,
    pub actions: Vec<ApiAction>,
}

/// One action: a route of the `api` or `administration` scope below
/// `/api/`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct ApiAction {
    /// The PHP method.
    pub method: String,
    pub route_name: String,
    /// The full path, `/api/_action/…`.
    pub path: String,
    /// `get`, `post`, …
    pub verb: String,
    /// The placeholders of the path, in order.
    pub path_parameters: Vec<String>,
    pub query: Option<Shape>,
    pub body: Option<Shape>,
    /// The class a `#[MapQueryString]` query is mapped onto.
    pub query_class: Option<String>,
    /// The class a `#[MapRequestPayload]` body is mapped onto.
    pub body_class: Option<String>,
    pub response: Shape,
}

/// The Admin API actions `class` declares; `None` when it has none.
pub(crate) fn api_controller(
    semantic: &Document,
    snapshot: &Snapshot,
    class: &SyntaxNode,
) -> Option<ApiController> {
    let small_entities = BTreeSet::new();
    let run = Run::new(semantic, snapshot, &small_entities);
    let actions = run.api_actions(class);
    (!actions.is_empty()).then(|| ApiController {
        class_name: phpquery::class_name(class),
        actions,
    })
}

/// Where the keys read from a request end up.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Target {
    Body,
    Query,
}

/// The keys read from the body or the query.
#[derive(Default)]
struct Reads {
    fields: Vec<Field>,
    open: bool,
}

impl Reads {
    fn add(&mut self, name: String, shape: Shape, optional: bool) {
        if let Some(field) = self.fields.iter_mut().find(|field| field.name == name) {
            if field.shape == Shape::Unknown {
                field.shape = shape;
            }
            field.optional |= optional;
            return;
        }
        self.fields.push(Field {
            name,
            shape,
            optional,
        });
    }

    fn shape(self) -> Option<Shape> {
        (self.open || !self.fields.is_empty()).then_some(Shape::Object {
            fields: self.fields,
            open: self.open,
        })
    }
}

/// What an action takes.
struct Request {
    query: Option<Shape>,
    body: Option<Shape>,
    query_class: Option<String>,
    body_class: Option<String>,
}

/// A parameter that holds request data.
struct Source {
    variable: String,
    kind: SourceKind,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum SourceKind {
    Request,
    Bag(Target),
}

impl Run<'_> {
    fn api_actions(&self, class: &SyntaxNode) -> Vec<ApiAction> {
        let class_routes: Vec<SyntaxNode> = class
            .children()
            .filter(|child| child.kind() == PHP_ATTRIBUTE_GROUP)
            .flat_map(|group| phpquery::nodes(&group, &[PHP_ATTRIBUTE]))
            .filter(|attribute| self.is_route(attribute))
            .collect();
        let class_path = class_routes
            .iter()
            .find_map(|route| self.route_argument(route, "path", 0))
            .map(|(_, path)| path)
            .unwrap_or_default();
        let mut actions = Vec::new();
        for method in phpquery::methods(class) {
            let Some(route) = phpquery::attributes(&method)
                .into_iter()
                .find(|attribute| self.is_route(attribute))
            else {
                continue;
            };
            let api = class_routes
                .iter()
                .chain(std::iter::once(&route))
                .flat_map(|route| self.route_scopes(route))
                // The Administration's own scope is answered below `/api/`
                // as well.
                .any(|scope| scope == "api" || scope == "administration");
            let Some((_, path)) = self.route_argument(&route, "path", 0) else {
                continue;
            };
            let path = format!(
                "/{}",
                [class_path.as_str(), path.as_str()]
                    .iter()
                    .flat_map(|part| part.split('/'))
                    .filter(|segment| !segment.is_empty())
                    .collect::<Vec<_>>()
                    .join("/")
            );
            if !api || !path.starts_with("/api/") {
                continue;
            }
            let mut verbs = self.route_methods(&route);
            if verbs.is_empty() {
                verbs = class_routes
                    .iter()
                    .flat_map(|route| self.route_methods(route))
                    .collect();
            }
            let verb = verbs
                .first()
                .map_or_else(|| "post".to_owned(), |verb| lowercase(verb));
            let route_name = self
                .route_argument(&route, "name", 1)
                .map(|(_, name)| name)
                .unwrap_or_default();
            let path_parameters = swlsp_symfony::Route {
                path: path.clone(),
                ..Default::default()
            }
            .parameters();
            let request = self.request_shapes(&method, &verb, &path_parameters);
            actions.push(ApiAction {
                method: phpquery::method_name(&method),
                route_name,
                path,
                verb,
                path_parameters,
                query: request.query,
                body: request.body,
                query_class: request.query_class,
                body_class: request.body_class,
                response: self.response_shape(&method),
            });
        }
        actions
    }

    fn method_symbol(&self, method: &SyntaxNode) -> Option<&Symbol> {
        let name = phpquery::direct_child(method, PHP_NAME)?.range_trimmed_trivia();
        self.semantic.symbols.iter().find(|symbol| {
            symbol.kind == SymbolKind::Method && ranges_touch(name, symbol.selection_range)
        })
    }

    /// The query and the body of an action.
    fn request_shapes(
        &self,
        method: &SyntaxNode,
        verb: &str,
        path_parameters: &[String],
    ) -> Request {
        // A request without a body sends its data in the query.
        let default_target = if matches!(verb, "get" | "delete" | "head") {
            Target::Query
        } else {
            Target::Body
        };
        let mut query = Reads::default();
        let mut body = Reads::default();
        let mut mapped_query = None;
        let mut mapped_body = None;
        let class_name = |ty: &Type| {
            (ty.kind() == TypeKind::Object && !ty.name().is_empty())
                .then(|| short_php_name(ty.name()).to_owned())
        };
        let mut sources: Vec<Source> = Vec::new();
        let symbol = self.method_symbol(method);
        for node in phpquery::parameters(method) {
            let name = phpquery::parameter_name(&node);
            let name = name.trim_start_matches('$');
            if path_parameters.iter().any(|parameter| parameter == name) {
                continue;
            }
            let parameter = symbol.and_then(|symbol| {
                symbol
                    .parameters
                    .iter()
                    .find(|parameter| parameter.name.trim_start_matches('$') == name)
            });
            let ty = parameter.map(|parameter| parameter.ty.clone());
            let written = phpquery::parameter_type(&node);
            let written = short_php_name(written.trim_start_matches('?'));
            let attributes: Vec<String> = phpquery::attributes(&node)
                .iter()
                .map(|attribute| short_php_name(&phpquery::attribute_name(attribute)).to_owned())
                .collect();
            let has = |attribute: &str| attributes.iter().any(|name| name == attribute);
            let is = |class: &str| match &ty {
                Some(ty) if ty.kind() == TypeKind::Object && !ty.name().is_empty() => {
                    self.snapshot.is_subtype_of(ty.name(), class)
                        || (self.snapshot.classes(ty.name()).is_empty()
                            && written == short_php_name(class))
                }
                _ => written == short_php_name(class),
            };
            // A resolver of its own builds the object from a request
            // that need not look like it.
            let resolved = |attribute: &str, position: usize| {
                phpquery::attributes(&node)
                    .iter()
                    .filter(|node| short_php_name(&phpquery::attribute_name(node)) == attribute)
                    .any(|node| local_call_argument(node, "resolver", position).is_some())
            };
            let mapped = |custom: bool, ty: &Type| {
                if custom {
                    (
                        Shape::Object {
                            fields: Vec::new(),
                            open: true,
                        },
                        None,
                    )
                } else {
                    (self.payload_shape(ty), class_name(ty))
                }
            };
            if has("MapRequestPayload") {
                let custom = resolved("MapRequestPayload", 3);
                mapped_body = ty.as_ref().map(|ty| mapped(custom, ty));
            } else if has("MapQueryString") {
                let custom = resolved("MapQueryString", 2);
                mapped_query = ty.as_ref().map(|ty| mapped(custom, ty));
            } else if has("MapQueryParameter") {
                let shape = ty.as_ref().map_or(Shape::Unknown, |ty| {
                    self.type_shape(ty, false, &mut Vec::new())
                });
                let optional = phpquery::parameter_optional(&node);
                query.add(name.to_owned(), without_null(shape), optional);
            } else if is(REQUEST_CLASS) {
                sources.push(Source {
                    variable: name.to_owned(),
                    kind: SourceKind::Request,
                });
            } else if is(QUERY_BAG_CLASS) {
                sources.push(Source {
                    variable: name.to_owned(),
                    kind: SourceKind::Bag(Target::Query),
                });
            } else if is(REQUEST_BAG_CLASS) {
                sources.push(Source {
                    variable: name.to_owned(),
                    kind: SourceKind::Bag(Target::Body),
                });
            } else if is(CRITERIA_CLASS) {
                // The criteria is built from the whole body.
                body.open = true;
            }
        }
        if !sources.is_empty() {
            self.collect_reads(method, &sources, default_target, &mut query, &mut body);
        }
        let (query, query_class) = match mapped_query {
            Some((shape, class)) => (Some(shape), class),
            None => (query.shape(), None),
        };
        let (body, body_class) = match mapped_body {
            Some((shape, class)) => (Some(shape), class),
            None => (body.shape(), None),
        };
        Request {
            query,
            body,
            query_class,
            body_class,
        }
    }

    /// The keys the method reads from its request parameters.
    fn collect_reads(
        &self,
        method: &SyntaxNode,
        sources: &[Source],
        default_target: Target,
        query: &mut Reads,
        body: &mut Reads,
    ) {
        let source_of = |node: &SyntaxNode| {
            (node.kind() == PHP_VARIABLE)
                .then(|| phpquery::variable_name(node))
                .and_then(|name| {
                    let name = name.trim_start_matches('$').to_owned();
                    sources.iter().find(|source| source.variable == name)
                })
        };
        let Some(block) = phpquery::direct_child(method, PHP_BLOCK) else {
            return;
        };
        // A request or bag passed to another call may be read there.
        for variable in phpquery::nodes(&block, &[PHP_VARIABLE]) {
            let Some(source) = source_of(&variable) else {
                continue;
            };
            if variable
                .parent()
                .is_none_or(|parent| !matches!(parent.kind(), PHP_ARGUMENT | PHP_NAMED_ARGUMENT))
            {
                continue;
            }
            match source.kind {
                SourceKind::Bag(Target::Query) => query.open = true,
                SourceKind::Bag(Target::Body) => body.open = true,
                SourceKind::Request if default_target == Target::Query => query.open = true,
                SourceKind::Request => body.open = true,
            }
        }
        for call in phpquery::nodes(&block, &[PHP_MEMBER_CALL]) {
            let Some(receiver) = phpquery::call_receiver(&call) else {
                continue;
            };
            let receiver = unwrap_php_parentheses(&receiver);
            let getter = lowercase(&phpquery::call_method_name(&call));
            let target = if let Some(source) = source_of(&receiver) {
                match source.kind {
                    SourceKind::Bag(target) => target,
                    // `$request->get()` looks in the query and the body.
                    SourceKind::Request if getter == "get" => default_target,
                    SourceKind::Request => {
                        if matches!(getter.as_str(), "toarray" | "getcontent") {
                            body.open = true;
                        }
                        continue;
                    }
                }
            } else if let Some(target) = self.request_bag_target(&receiver, &source_of) {
                target
            } else {
                continue;
            };
            let reads = match target {
                Target::Body => &mut *body,
                Target::Query => &mut *query,
            };
            let Some((shape, optional)) = getter_shape(&getter) else {
                // `all()`, `keys()`, iteration: every key is read.
                if !matches!(getter.as_str(), "set" | "remove" | "add" | "replace") {
                    reads.open = true;
                }
                continue;
            };
            let key = local_call_argument(&call, "key", 0);
            let Some(key) = self
                .expression_strings(key.as_ref())
                .into_iter()
                .next()
                .filter(|key| !key.is_empty())
            else {
                reads.open = true;
                continue;
            };
            let has_default = phpquery::arguments(&call).count() > 1;
            let shape = match (shape, cast_shape(&call)) {
                (Shape::Unknown, Some(cast)) => cast,
                (shape, _) => shape,
            };
            reads.add(key, shape, optional || has_default);
        }
    }

    /// `$request->request`, `$request->query` and `$request->getPayload()`.
    fn request_bag_target<'s>(
        &self,
        receiver: &SyntaxNode,
        source_of: &impl Fn(&SyntaxNode) -> Option<&'s Source>,
    ) -> Option<Target> {
        let object = receiver.children().next()?;
        if source_of(&unwrap_php_parentheses(&object))?.kind != SourceKind::Request {
            return None;
        }
        match receiver.kind() {
            PHP_MEMBER_ACCESS => {
                let name = phpquery::direct_child(receiver, PHP_NAME)?;
                match phpquery::name_value(&name).as_str() {
                    "request" => Some(Target::Body),
                    "query" => Some(Target::Query),
                    _ => None,
                }
            }
            PHP_MEMBER_CALL => eq_ignore_case(&phpquery::call_method_name(receiver), "getPayload")
                .then_some(Target::Body),
            _ => None,
        }
    }

    /// The properties a request is mapped onto.
    fn payload_shape(&self, ty: &Type) -> Shape {
        without_null(self.type_shape(ty, false, &mut Vec::new()))
    }

    /// What the action answers with: the union of its returned responses.
    fn response_shape(&self, method: &SyntaxNode) -> Shape {
        let Some(block) = phpquery::direct_child(method, PHP_BLOCK) else {
            return Shape::Unknown;
        };
        let mut shapes = Vec::new();
        for statement in phpquery::nodes(&block, &[PHP_RETURN_STATEMENT]) {
            // A return of a closure is not one of the action.
            let nested = statement
                .ancestors()
                .skip(1)
                .take_while(|ancestor| ancestor != &block)
                .any(|ancestor| {
                    matches!(
                        ancestor.kind(),
                        PHP_CLOSURE | PHP_ARROW_FUNCTION | PHP_ANONYMOUS_CLASS
                    )
                });
            if nested {
                continue;
            }
            let Some(value) = statement.children().next() else {
                continue;
            };
            let value = unwrap_php_parentheses(&value);
            // The client rejects an error response: it is no result.
            if !self.is_error_response(&value) {
                shapes.push(self.returned_shape(&value));
            }
        }
        if shapes.is_empty() {
            return Shape::Unknown;
        }
        Shape::union(shapes)
    }

    /// A response built with a status of 400 or above.
    fn is_error_response(&self, value: &SyntaxNode) -> bool {
        let status = match value.kind() {
            PHP_OBJECT_CREATION | PHP_MEMBER_CALL => local_call_argument(value, "status", 1),
            _ => None,
        };
        status.is_some_and(|status| {
            let ty = self.semantic.type_of(&unwrap_php_parentheses(&status)).ty;
            ty.kind() == TypeKind::LiteralInt
                && ty.name().parse::<u32>().is_ok_and(|code| code >= 400)
        })
    }

    fn returned_shape(&self, value: &SyntaxNode) -> Shape {
        match value.kind() {
            PHP_OBJECT_CREATION => {
                let name = phpquery::direct_child(value, PHP_NAME);
                let short = name
                    .as_ref()
                    .map(phpquery::name_value)
                    .map(|name| short_php_name(&name).to_owned())
                    .unwrap_or_default();
                let resolved = self.resolved_symbols(name.as_ref(), SymbolKind::Class);
                let json = if resolved.is_empty() {
                    short == "JsonResponse"
                } else {
                    self.names_subtype(name.as_ref(), JSON_RESPONSE_CLASS)
                };
                if json {
                    return self.data_shape(local_call_argument(value, "data", 0));
                }
                if FILE_RESPONSES.contains(&short.as_str()) {
                    return Shape::Blob;
                }
                if short == "Response" || short == "NoContentResponse" {
                    let content = local_call_argument(value, "content", 0);
                    let empty = short == "NoContentResponse"
                        || content.is_none_or(|content| {
                            let text = content.text().to_string();
                            let text = text.trim();
                            eq_ignore_case(text, "null") || text == "''" || text == "\"\""
                        });
                    if empty {
                        return Shape::Void;
                    }
                }
                Shape::Unknown
            }
            // `$this->json($data)` of Symfony's AbstractController.
            PHP_MEMBER_CALL
                if phpquery::call_method_name(value) == "json"
                    && phpquery::call_receiver(value)
                        .is_some_and(|receiver| receiver.text().to_string().trim() == "$this") =>
            {
                self.data_shape(local_call_argument(value, "data", 0))
            }
            _ => Shape::Unknown,
        }
    }

    /// The shape of what a JSON response is built from.
    fn data_shape(&self, data: Option<SyntaxNode>) -> Shape {
        let Some(data) = data.map(|data| unwrap_php_parentheses(&data)) else {
            return Shape::Void;
        };
        if eq_ignore_case(data.text().to_string().trim(), "null") {
            return Shape::Void;
        }
        let ty = self.semantic.type_of(&data).ty;
        self.type_shape(&ty, true, &mut Vec::new())
    }

    /// The JSON shape of a PHP type. `serialized` tells an object that is
    /// encoded (a response) from one that is filled (a payload).
    fn type_shape(&self, ty: &Type, serialized: bool, visiting: &mut Vec<String>) -> Shape {
        match ty.kind() {
            TypeKind::Bool | TypeKind::True | TypeKind::False => Shape::Boolean,
            TypeKind::Int | TypeKind::Float | TypeKind::LiteralInt | TypeKind::LiteralFloat => {
                Shape::Number
            }
            TypeKind::String | TypeKind::LiteralString | TypeKind::ClassString => Shape::String,
            TypeKind::Null | TypeKind::Void => Shape::Null,
            TypeKind::List | TypeKind::NonEmptyList => Shape::Array(Box::new(self.type_shape(
                &ty.argument(0),
                serialized,
                visiting,
            ))),
            TypeKind::Array | TypeKind::NonEmptyArray | TypeKind::Iterable => {
                let arguments = ty.arguments();
                let (key, value) = match arguments {
                    [key, value] => (Some(key.kind()), value.clone()),
                    [value] => (None, value.clone()),
                    _ => return Shape::Unknown,
                };
                let value = Box::new(self.type_shape(&value, serialized, visiting));
                match key {
                    Some(TypeKind::Int | TypeKind::LiteralInt) => Shape::Array(value),
                    Some(TypeKind::String | TypeKind::LiteralString) => Shape::Record(value),
                    _ if *value == Shape::Unknown => Shape::Unknown,
                    // `string[]` and `array<string>` are written for lists.
                    _ => Shape::Array(value),
                }
            }
            TypeKind::ArrayShape | TypeKind::ObjectShape => {
                let fields: Vec<Field> = ty
                    .fields()
                    .iter()
                    .map(|field| Field {
                        name: field.name.to_string(),
                        shape: self.type_shape(&field.ty, serialized, visiting),
                        optional: field.optional,
                    })
                    .collect();
                // `['a', 'b']` is a list, not an object with the keys 0 and 1.
                let list = ty.kind() == TypeKind::ArrayShape
                    && !fields.is_empty()
                    && fields
                        .iter()
                        .all(|field| field.name.bytes().all(|byte| byte.is_ascii_digit()));
                if list {
                    Shape::Array(Box::new(Shape::union(
                        fields.into_iter().map(|field| field.shape),
                    )))
                } else if fields.is_empty() && ty.kind() == TypeKind::ArrayShape {
                    Shape::Array(Box::new(Shape::Unknown))
                } else {
                    Shape::Object {
                        fields,
                        open: ty.is_open_shape(),
                    }
                }
            }
            TypeKind::Union => Shape::union(
                ty.arguments()
                    .iter()
                    .map(|member| self.type_shape(member, serialized, visiting)),
            ),
            TypeKind::Object if !ty.name().is_empty() => {
                self.class_shape(ty.name(), serialized, visiting)
            }
            _ => Shape::Unknown,
        }
    }

    fn class_shape(&self, name: &str, serialized: bool, visiting: &mut Vec<String>) -> Shape {
        let name = trim_php_name(name);
        let is = |target: &str| self.snapshot.is_subtype_of(name, target);
        if is("DateTimeInterface") {
            return Shape::String;
        }
        if RECORD_CLASSES.iter().any(|class| is(class)) {
            return Shape::Record(Box::new(Shape::Unknown));
        }
        if is(COLLECTION_CLASS) {
            return Shape::Array(Box::new(Shape::Unknown));
        }
        let key = lowercase(name);
        if visiting.len() >= MAX_CLASS_DEPTH || visiting.contains(&key) {
            return Shape::Unknown;
        }
        let Some(class) = self.snapshot.classes(name).into_iter().next() else {
            return Shape::Unknown;
        };
        if class.kind == SymbolKind::Enum {
            // A backed enum is encoded as its value.
            let value = self
                .snapshot
                .members(class.id.as_str(), "value")
                .into_iter()
                .find(|member| member.kind == SymbolKind::Property)
                .map(|member| self.type_shape(&member.ty, serialized, visiting));
            return match value {
                Some(shape @ (Shape::String | Shape::Number)) => shape,
                _ => Shape::union([Shape::String, Shape::Number]),
            };
        }
        if class.kind != SymbolKind::Class {
            return Shape::Unknown;
        }
        // A struct is encoded with its protected properties too.
        let protected = serialized && is(STRUCT_CLASS);
        visiting.push(key);
        let mut fields: Vec<Field> = Vec::new();
        let mut seen: BTreeSet<String> = BTreeSet::new();
        let mut pending = vec![class];
        while let Some(class) = pending.pop() {
            if !seen.insert(lowercase(trim_php_name(&class.fully_qualified))) || seen.len() > 16 {
                continue;
            }
            let members = self.snapshot.members_of(class.id.as_str());
            // A class that encodes itself decides its shape, not its
            // properties. Shopware's own encoding of a struct is the
            // properties.
            let own_encoding = serialized
                && !STRUCT_ENCODERS
                    .iter()
                    .any(|encoder| eq_ignore_case(trim_php_name(&class.fully_qualified), encoder));
            if own_encoding
                && let Some(encode) = members.iter().find(|member| {
                    member.kind == SymbolKind::Method
                        && eq_ignore_case(&member.name, "jsonSerialize")
                })
            {
                let shape = self.type_shape(&encode.return_type, serialized, visiting);
                visiting.pop();
                return shape;
            }
            if !serialized {
                // A payload is filled through the constructor.
                let constructor = members.iter().find(|member| {
                    member.kind == SymbolKind::Method && eq_ignore_case(&member.name, "__construct")
                });
                for parameter in constructor.iter().flat_map(|method| &method.parameters) {
                    let name = parameter.name.trim_start_matches('$').to_owned();
                    if fields.iter().any(|field| field.name == name) {
                        continue;
                    }
                    let shape = self.type_shape(&parameter.ty, serialized, visiting);
                    fields.push(Field {
                        name,
                        optional: parameter.optional || has_null(&shape),
                        shape,
                    });
                }
            }
            for member in &members {
                let visible = member.visibility == Visibility::Public
                    || (protected && member.visibility == Visibility::Protected);
                if member.kind != SymbolKind::Property
                    || member.flags.has(Flags::STATIC)
                    || !visible
                {
                    continue;
                }
                let name = member.name.trim_start_matches('$').to_owned();
                if fields.iter().any(|field| field.name == name) {
                    continue;
                }
                let shape = self.type_shape(&member.ty, serialized, visiting);
                fields.push(Field {
                    name,
                    optional: !serialized && has_null(&shape),
                    shape,
                });
            }
            for parent in class.extends().iter().chain(class.traits()) {
                pending.extend(
                    self.snapshot
                        .classes(trim_php_name(parent))
                        .into_iter()
                        .next(),
                );
            }
        }
        visiting.pop();
        Shape::Object {
            fields,
            open: false,
        }
    }
}

/// What a bag getter returns, and whether its key may be missing. `None`
/// for a method that reads no single key.
fn getter_shape(getter: &str) -> Option<(Shape, bool)> {
    Some(match getter {
        "get" | "getenum" | "filter" => (Shape::Unknown, false),
        "getstring" | "getalpha" | "getalnum" | "getdigits" => (Shape::String, false),
        "getint" => (Shape::Number, false),
        "getboolean" => (Shape::Boolean, true),
        "has" => (Shape::Unknown, true),
        "all" => (Shape::Unknown, false),
        _ => return None,
    })
}

/// The type a read is cast to: `(string) $request->request->get('id')`.
fn cast_shape(call: &SyntaxNode) -> Option<Shape> {
    let cast = call
        .parent()
        .filter(|parent| parent.kind() == PHP_CAST_EXPRESSION)?;
    let name = cast
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| token.kind() == TK_IDENTIFIER)?;
    Some(match lowercase(name.text()).as_str() {
        "string" => Shape::String,
        "int" | "integer" | "float" | "double" => Shape::Number,
        "bool" | "boolean" => Shape::Boolean,
        _ => return None,
    })
}

fn has_null(shape: &Shape) -> bool {
    match shape {
        Shape::Null => true,
        Shape::Union(members) => members.contains(&Shape::Null),
        _ => false,
    }
}

/// `shape` without the `null` of a nullable type: an optional key says it.
fn without_null(shape: Shape) -> Shape {
    match shape {
        Shape::Union(members) => {
            Shape::union(members.into_iter().filter(|member| *member != Shape::Null))
        }
        shape => shape,
    }
}
