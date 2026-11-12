//! PHP values exposed to Twig templates by controllers.

use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    ERROR, PHP_ARRAY, PHP_ARRAY_ACCESS, PHP_ASSIGNMENT_EXPRESSION, PHP_BINARY_EXPRESSION,
    PHP_FUNCTION_CALL, PHP_MEMBER_CALL, PHP_METHOD_DECLARATION, PHP_NAME, PHP_PARENTHESIZED,
    PHP_RETURN_STATEMENT, PHP_SCOPED_CALL, PHP_STRING, PHP_VARIABLE,
};
use swlsp_php_semantic::Document;
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::FoldAsciiMatcher;
use swlsp_textutil::lowercase;

use crate::{PhpIndex, lock};

const MAX_TWIG_CONTEXT_DEPTH: usize = 2;

static CANDIDATE_MATCHER: LazyLock<FoldAsciiMatcher> =
    LazyLock::new(|| FoldAsciiMatcher::new(&["render", "template", "stream"]));

/// A persisted byte range.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct TwigRange {
    pub start: u32,
    pub end: u32,
}

impl From<TextRange> for TwigRange {
    fn from(range: TextRange) -> Self {
        TwigRange {
            start: range.start().into(),
            end: range.end().into(),
        }
    }
}

/// One PHP value exposed to a Twig template. `ty` is canonical PHP type
/// syntax (`unknown` when not inferred).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigTemplateVariable {
    pub template: String,
    pub name: String,
    #[serde(rename = "type")]
    pub ty: String,
    pub file: String,
    pub range: TwigRange,
}

/// The variables one PHP file passes to one template.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct TwigTemplateContext {
    pub template: String,
    pub variables: Vec<TwigTemplateVariable>,
    /// Whether the file passes variables the analysis cannot list: a
    /// context held in a parameter, spread from a call, or built by code
    /// it does not follow.
    #[serde(default)]
    pub open: bool,
}

/// Per-generation cache of merged template variables.
#[derive(Default)]
pub(crate) struct TwigCache {
    revision: u64,
    templates: HashMap<String, Vec<TwigTemplateVariable>>,
}

const UNKNOWN: &str = "unknown";

pub(crate) fn extract_twig_template_contexts(
    path: &str,
    root: &SyntaxNode,
    source: &str,
    document: &Document,
) -> Vec<TwigTemplateContext> {
    if !CANDIDATE_MATCHER.contains_str(source) {
        return Vec::new();
    }
    let mut contexts: HashMap<String, Collector<'_>> = HashMap::new();
    fn collector_for<'a, 'm>(
        contexts: &'m mut HashMap<String, Collector<'a>>,
        template: &str,
        path: &'a str,
        document: &'a Document,
        scope: Option<SyntaxNode>,
    ) -> Option<&'m mut Collector<'a>> {
        let template = normalize_twig_template_name(template);
        if template.is_empty() {
            return None;
        }
        Some(
            contexts
                .entry(template.clone())
                .or_insert_with(|| Collector {
                    template,
                    path,
                    document,
                    scope,
                    variables: HashMap::new(),
                    visited: HashSet::new(),
                    open: false,
                }),
        )
    }

    query::visit(root, &[PHP_MEMBER_CALL, PHP_FUNCTION_CALL], |call| {
        let Some((template, context)) = twig_render_arguments(call) else {
            return true;
        };
        let scope = query::function_like_at(call);
        if let Some(collector) =
            collector_for(&mut contexts, &template, path, document, scope.clone())
        {
            collector.scope = scope;
            collector.collect(Some(&context), 0);
        }
        true
    });

    query::visit(root, &[PHP_METHOD_DECLARATION], |method| {
        for template in method_template_names(root, method) {
            let Some(collector) = collector_for(
                &mut contexts,
                &template,
                path,
                document,
                Some(method.clone()),
            ) else {
                continue;
            };
            collector.scope = Some(method.clone());
            query::visit(method, &[PHP_RETURN_STATEMENT], |statement| {
                if query::function_like_at(statement).as_ref() == Some(method) {
                    collector.collect(statement.first_child().as_ref(), 0);
                }
                true
            });
        }
        true
    });

    let mut result: Vec<TwigTemplateContext> = contexts
        .into_values()
        .map(|collector| {
            let mut variables: Vec<TwigTemplateVariable> =
                collector.variables.into_values().collect();
            sort_by_lower_name(&mut variables);
            TwigTemplateContext {
                template: collector.template,
                variables,
                open: collector.open,
            }
        })
        .collect();
    result.sort_by(|left, right| left.template.cmp(&right.template));
    result
}

/// Sorts by lower-cased name. Names that collide only in case keep a
/// deterministic order by their exact spelling.
fn sort_by_lower_name(variables: &mut [TwigTemplateVariable]) {
    variables.sort_by(|left, right| {
        lowercase(&left.name)
            .cmp(&lowercase(&right.name))
            .then_with(|| left.name.cmp(&right.name))
    });
}

fn twig_render_arguments(call: &SyntaxNode) -> Option<(String, SyntaxNode)> {
    let name = lowercase(&query::call_method_name(call));
    let context_index = match name.as_str() {
        "render" | "renderview" | "renderstorefront" | "stream" | "htmltemplate"
        | "texttemplate" => 1,
        "renderblock" | "renderblockview" => 2,
        _ => return None,
    };
    let template_node = argument_expression(call, &["view", "name", "template"], 0)?;
    if template_node.kind() != PHP_STRING {
        return None;
    }
    let template = query::string_value(&template_node);
    if !lowercase(&template).ends_with(".twig") {
        return None;
    }
    let context = argument_expression(call, &["parameters", "context"], context_index)?;
    Some((template, context))
}

fn argument_expression(call: &SyntaxNode, names: &[&str], fallback: usize) -> Option<SyntaxNode> {
    for (index, argument) in query::arguments(call).enumerate() {
        let name = lowercase(&query::argument_name(&argument));
        if names.contains(&name.as_str()) {
            return query::argument_expression(call, index);
        }
    }
    let argument = query::argument(call, fallback)?;
    if !query::argument_name(&argument).is_empty() {
        return None;
    }
    query::argument_expression(call, fallback)
}

fn method_template_names(root: &SyntaxNode, method: &SyntaxNode) -> Vec<String> {
    let mut templates = Vec::new();
    for attribute in query::attributes(method) {
        let name = query::direct_child(&attribute, PHP_NAME)
            .map(|name| query::name_value(&name))
            .unwrap_or_default();
        if !swlsp_textutil::eq_ignore_case(last_name_segment(&name), "Template") {
            continue;
        }
        match argument_expression(&attribute, &["template"], 0) {
            Some(node) if node.kind() == PHP_STRING => templates.push(query::string_value(&node)),
            _ => {
                let guessed = guessed_controller_template(root, method);
                if !guessed.is_empty() {
                    templates.push(guessed);
                }
            }
        }
    }
    let mut unique = HashSet::new();
    templates
        .into_iter()
        .map(|template| normalize_twig_template_name(&template))
        .filter(|template| !template.is_empty() && unique.insert(template.clone()))
        .collect()
}

/// Symfony's conventional template name for a controller method with an
/// empty `#[Template]` attribute.
pub fn guessed_controller_template(root: &SyntaxNode, method: &SyntaxNode) -> String {
    let Some(class) = query::class_at(method) else {
        return String::new();
    };
    let class_name = query::class_name(&class);
    let mut class_name = class_name
        .strip_suffix("Controller")
        .unwrap_or(&class_name)
        .to_owned();
    if class_name.is_empty() {
        return String::new();
    }
    let namespace = query::namespace(root);
    if let Some(marker) = namespace.find("Controller\\") {
        class_name = format!(
            "{}\\{class_name}",
            &namespace[marker + "Controller\\".len()..]
        );
    }
    let class_name = class_name.trim_matches('\\');
    let method_name = query::method_name(method);
    let method_name = method_name.strip_suffix("Action").unwrap_or(&method_name);
    if method_name == "__invoke" {
        return format!("{}.html.twig", snake_path(class_name));
    }
    format!(
        "{}/{}.html.twig",
        snake_path(class_name),
        snake_case(method_name)
    )
}

/// The last segment of a `\\`- or `/`-separated name, ignoring trailing
/// separators.
fn last_name_segment(name: &str) -> &str {
    let trimmed = name.trim_end_matches(['\\', '/']);
    trimmed.rsplit(['\\', '/']).next().unwrap_or(trimmed)
}

fn snake_path(value: &str) -> String {
    value
        .split('\\')
        .map(snake_case)
        .collect::<Vec<_>>()
        .join("/")
}

fn snake_case(value: &str) -> String {
    let mut result = String::with_capacity(value.len() + 4);
    for (index, current) in value.char_indices() {
        if current.is_uppercase() && index > 0 {
            result.push('_');
        }
        if current.is_ascii() {
            result.push(current.to_ascii_lowercase());
        } else {
            let mut buffer = [0u8; 4];
            result.push_str(&lowercase(current.encode_utf8(&mut buffer)));
        }
    }
    result
}

pub(crate) fn normalize_twig_template_name(template: &str) -> String {
    let template = template.trim();
    let template = if std::path::MAIN_SEPARATOR == '/' {
        template.to_owned()
    } else {
        template.replace(std::path::MAIN_SEPARATOR, "/")
    };
    template.strip_prefix('/').unwrap_or(&template).to_owned()
}

fn direct_nodes(node: &SyntaxNode) -> Vec<SyntaxNode> {
    node.children().collect()
}

struct Collector<'a> {
    template: String,
    path: &'a str,
    document: &'a Document,
    scope: Option<SyntaxNode>,
    variables: HashMap<String, TwigTemplateVariable>,
    visited: HashSet<String>,
    /// See [`TwigTemplateContext::open`].
    open: bool,
}

impl Collector<'_> {
    fn collect(&mut self, node: Option<&SyntaxNode>, depth: usize) {
        let Some(node) = node else {
            return;
        };
        if depth > MAX_TWIG_CONTEXT_DEPTH {
            self.open = true;
            return;
        }
        match node.kind() {
            PHP_ARRAY => self.collect_array(node, depth),
            PHP_VARIABLE => self.collect_variable(
                &query::variable_name(node),
                u32::from(node.text_range().start()),
                depth,
            ),
            PHP_FUNCTION_CALL => {
                if is_twig_array_context_function(&query::call_name(node)) {
                    let count = query::arguments(node).count();
                    for index in 0..count {
                        self.collect(query::argument_expression(node, index).as_ref(), depth);
                    }
                } else {
                    self.open = true;
                }
            }
            PHP_MEMBER_CALL | PHP_SCOPED_CALL => self.collect_local_method(node, depth),
            PHP_BINARY_EXPRESSION | PHP_PARENTHESIZED | PHP_ASSIGNMENT_EXPRESSION => {
                for child in direct_nodes(node) {
                    self.collect(Some(&child), depth);
                }
            }
            _ => self.open = true,
        }
    }

    fn collect_array(&mut self, array: &SyntaxNode, depth: usize) {
        let mut spread_next = false;
        for item in query::array_items(array) {
            let text = query::node_text(&item);
            if text.trim().starts_with("...") {
                match query::array_item_value(&item) {
                    Some(value) if value.kind() != ERROR => self.collect(Some(&value), depth),
                    _ => spread_next = true,
                }
                continue;
            }
            if spread_next {
                self.collect(query::array_item_value(&item).as_ref(), depth);
                spread_next = false;
                continue;
            }
            let Some(key) = query::array_item_key(&item) else {
                continue;
            };
            if key.kind() != PHP_STRING {
                self.open = true;
                continue;
            }
            let value = query::array_item_value(&item);
            self.add(
                &query::string_value(&key),
                value.as_ref(),
                key.range_trimmed_trivia(),
            );
        }
    }

    fn scope_start(&self) -> u32 {
        self.scope
            .as_ref()
            .map_or(0, |scope| u32::from(scope.text_range().start()))
    }

    fn collect_variable(&mut self, name: &str, before: u32, depth: usize) {
        let Some(scope) = self.scope.clone() else {
            self.open = true;
            return;
        };
        if name.is_empty() {
            self.open = true;
            return;
        }
        if !self
            .visited
            .insert(format!("{}:{name}:{depth}", self.scope_start()))
        {
            return;
        }
        // A variable nothing assigns in this function is a parameter or a
        // property: what it holds is unknown.
        let mut assigned = false;
        query::visit(&scope, &[PHP_ASSIGNMENT_EXPRESSION], |assignment| {
            if u32::from(assignment.text_range().start()) >= before
                || query::function_like_at(assignment).as_ref() != Some(&scope)
            {
                return true;
            }
            let nodes = direct_nodes(assignment);
            if nodes.len() < 2 {
                return true;
            }
            let (left, right) = (&nodes[0], &nodes[nodes.len() - 1]);
            match left.kind() {
                PHP_VARIABLE => {
                    if query::variable_name(left) == name {
                        assigned = true;
                        self.collect(Some(right), depth);
                    }
                }
                PHP_ARRAY_ACCESS => {
                    if let (base, Some(key)) = array_access_parts(left)
                        && base == name
                    {
                        assigned = true;
                        self.add(
                            &query::string_value(&key),
                            Some(right),
                            key.range_trimmed_trivia(),
                        );
                    }
                }
                _ => {}
            }
            true
        });
        if !assigned {
            self.open = true;
        }
    }

    fn collect_local_method(&mut self, call: &SyntaxNode, depth: usize) {
        // Unknown until a method of this class is found to follow.
        let was_open = std::mem::replace(&mut self.open, true);
        if depth >= MAX_TWIG_CONTEXT_DEPTH {
            return;
        }
        let Some(scope) = self.scope.clone() else {
            return;
        };
        let Some(receiver) = query::call_receiver(call) else {
            return;
        };
        if receiver.kind() == PHP_VARIABLE && query::variable_name(&receiver) != "this" {
            return;
        }
        let name = query::call_method_name(call);
        let Some(class) = query::class_at(&scope) else {
            return;
        };
        if name.is_empty() {
            return;
        }
        for method in query::methods(&class) {
            if !swlsp_textutil::eq_ignore_case(&query::method_name(&method), &name) {
                continue;
            }
            if !self
                .visited
                .insert(format!("method:{}", u32::from(method.text_range().start())))
            {
                return;
            }
            self.open = was_open;
            let previous = self.scope.replace(method.clone());
            query::visit(&method, &[PHP_RETURN_STATEMENT], |statement| {
                if query::function_like_at(statement).as_ref() == Some(&method) {
                    self.collect(statement.first_child().as_ref(), depth + 1);
                }
                true
            });
            self.scope = previous;
            return;
        }
    }

    fn add(&mut self, name: &str, value: Option<&SyntaxNode>, range: TextRange) {
        let name = name.trim();
        if name.is_empty() {
            return;
        }
        let mut type_name = UNKNOWN.to_owned();
        if let Some(value) = value {
            let inferred = self.type_for_twig_value(value, &mut HashSet::new(), 0);
            if !inferred.is_unknown() {
                type_name = inferred.to_string();
            }
        }
        if let Some(existing) = self.variables.get(name)
            && existing.ty != UNKNOWN
            && type_name == UNKNOWN
        {
            return;
        }
        self.variables.insert(
            name.to_owned(),
            TwigTemplateVariable {
                template: self.template.clone(),
                name: name.to_owned(),
                ty: type_name,
                file: self.path.to_owned(),
                range: range.into(),
            },
        );
    }

    fn type_for_twig_value(
        &self,
        value: &SyntaxNode,
        visited: &mut HashSet<String>,
        depth: usize,
    ) -> Type {
        if depth > 8 {
            return Type::UNKNOWN;
        }
        let inferred = self.document.type_of(value).ty;
        if !inferred.is_unknown() {
            return inferred;
        }
        match value.kind() {
            PHP_VARIABLE => self.type_for_assigned_variable(
                &query::variable_name(value),
                u32::from(value.text_range().start()),
                visited,
                depth + 1,
            ),
            PHP_PARENTHESIZED => {
                for child in value.children() {
                    let inferred = self.type_for_twig_value(&child, visited, depth + 1);
                    if !inferred.is_unknown() {
                        return inferred;
                    }
                }
                Type::UNKNOWN
            }
            _ => Type::UNKNOWN,
        }
    }

    fn type_for_assigned_variable(
        &self,
        name: &str,
        before: u32,
        visited: &mut HashSet<String>,
        depth: usize,
    ) -> Type {
        let Some(scope) = &self.scope else {
            return Type::UNKNOWN;
        };
        if name.is_empty() || depth > 8 {
            return Type::UNKNOWN;
        }
        if !visited.insert(format!("type:{}:{name}:{before}", self.scope_start())) {
            return Type::UNKNOWN;
        }
        let mut best: Option<(u32, SyntaxNode)> = None;
        query::visit(scope, &[PHP_ASSIGNMENT_EXPRESSION], |assignment| {
            let start = u32::from(assignment.text_range().start());
            if start >= before || query::function_like_at(assignment).as_ref() != Some(scope) {
                return true;
            }
            let nodes = direct_nodes(assignment);
            if nodes.len() < 2
                || nodes[0].kind() != PHP_VARIABLE
                || query::variable_name(&nodes[0]) != name
            {
                return true;
            }
            if best
                .as_ref()
                .is_none_or(|(best_start, _)| start > *best_start)
            {
                best = Some((start, nodes[nodes.len() - 1].clone()));
            }
            true
        });
        match best {
            Some((_, value)) => self.type_for_twig_value(&value, visited, depth + 1),
            None => Type::UNKNOWN,
        }
    }
}

fn is_twig_array_context_function(name: &str) -> bool {
    let name = name.trim();
    let name = lowercase(name.strip_prefix('\\').unwrap_or(name));
    matches!(
        name.as_str(),
        "array_merge" | "array_merge_recursive" | "array_replace"
    )
}

fn array_access_parts(node: &SyntaxNode) -> (String, Option<SyntaxNode>) {
    let nodes = direct_nodes(node);
    if nodes.len() < 2 || nodes[0].kind() != PHP_VARIABLE {
        return (String::new(), None);
    }
    let key = nodes[1..]
        .iter()
        .find(|candidate| candidate.kind() == PHP_STRING)
        .cloned();
    (query::variable_name(&nodes[0]), key)
}

impl PhpIndex {
    /// The merged PHP context of every portable name of one Twig file. A
    /// concrete inferred type wins over an unknown value.
    pub fn twig_template_variables<S: AsRef<str>>(
        &self,
        template_names: &[S],
    ) -> swlsp_indexer::Result<Vec<TwigTemplateVariable>> {
        let mut merged: HashMap<String, TwigTemplateVariable> = HashMap::new();
        for template in template_names {
            let template = normalize_twig_template_name(template.as_ref());
            if template.is_empty() {
                continue;
            }
            for variable in self.twig_variables_for_template(&template)? {
                let key = lowercase(&variable.name);
                let replace = merged
                    .get(&key)
                    .is_none_or(|existing| existing.ty == UNKNOWN && variable.ty != UNKNOWN);
                if replace {
                    merged.insert(key, variable);
                }
            }
        }
        let mut result: Vec<TwigTemplateVariable> = merged.into_values().collect();
        sort_by_lower_name(&mut result);
        Ok(result)
    }

    /// Whether some PHP file renders one of the templates with variables
    /// the analysis cannot list.
    pub fn twig_template_context_open<S: AsRef<str>>(&self, template_names: &[S]) -> bool {
        template_names.iter().any(|template| {
            let template = normalize_twig_template_name(template.as_ref());
            !template.is_empty()
                && self
                    .inner
                    .twig_contexts
                    .get_values_view(&template)
                    .is_ok_and(|contexts| contexts.iter().any(|context| context.open))
        })
    }

    fn twig_variables_for_template(
        &self,
        template: &str,
    ) -> swlsp_indexer::Result<Vec<TwigTemplateVariable>> {
        let template = normalize_twig_template_name(template);
        if template.is_empty() {
            return Ok(Vec::new());
        }
        let revision = self.analysis_revision();
        {
            let mut cache = lock(&self.inner.twig_cache);
            if cache.revision != revision {
                cache.revision = revision;
                cache.templates = HashMap::new();
            }
            if let Some(cached) = cache.templates.get(&template) {
                return Ok(cached.clone());
            }
        }
        let contexts = self.inner.twig_contexts.get_values_view(&template)?;
        let mut variables: Vec<TwigTemplateVariable> = contexts
            .iter()
            .flat_map(|context| context.variables.iter().cloned())
            .collect();
        // Rows come back in scanner commit order, which is not stable across
        // runs; callers keep the first value per variable name, so order by
        // source file (stable, preserving each file's own order) first.
        variables.sort_by(|a, b| a.file.cmp(&b.file));
        let variables = self.enrich_unknown_twig_variables(&template, variables);
        let mut cache = lock(&self.inner.twig_cache);
        if cache.revision == revision {
            cache.templates.insert(template, variables.clone());
        }
        Ok(variables)
    }

    /// Re-analyzes the source files of unknown variables against the current
    /// workspace, so types declared in files indexed later resolve.
    fn enrich_unknown_twig_variables(
        &self,
        template: &str,
        mut variables: Vec<TwigTemplateVariable>,
    ) -> Vec<TwigTemplateVariable> {
        let mut paths: Vec<&str> = variables
            .iter()
            .filter(|variable| variable.ty == UNKNOWN && !variable.file.is_empty())
            .map(|variable| variable.file.as_str())
            .collect();
        paths.sort_unstable();
        paths.dedup();
        if paths.is_empty() {
            return variables;
        }
        let mut fresh: HashMap<(String, String), String> = HashMap::new();
        for path in paths {
            let Ok(content) = std::fs::read(path) else {
                continue;
            };
            let source = String::from_utf8_lossy(&content);
            let tree = swlsp_parser_php::parse(&source).tree;
            let root = tree.root();
            let document = self.analyze_document(path, 0, &root, tree.source());
            for context in extract_twig_template_contexts(path, &root, tree.source(), &document) {
                if normalize_twig_template_name(&context.template) != template {
                    continue;
                }
                for variable in context.variables {
                    if variable.ty == UNKNOWN {
                        continue;
                    }
                    fresh.insert((path.to_owned(), lowercase(&variable.name)), variable.ty);
                }
            }
        }
        if fresh.is_empty() {
            return variables;
        }
        for variable in &mut variables {
            if variable.ty != UNKNOWN {
                continue;
            }
            if let Some(ty) = fresh.get(&(variable.file.clone(), lowercase(&variable.name))) {
                variable.ty = ty.clone();
            }
        }
        variables
    }

    /// Every indexed controller source for one variable name, instead of
    /// merging duplicate providers.
    pub fn twig_template_variable_sources<S: AsRef<str>>(
        &self,
        name: &str,
        template_names: &[S],
    ) -> swlsp_indexer::Result<Vec<TwigTemplateVariable>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for template in template_names {
            let template = normalize_twig_template_name(template.as_ref());
            if template.is_empty() {
                continue;
            }
            result.extend(
                self.twig_variables_for_template(&template)?
                    .into_iter()
                    .filter(|variable| variable.name == name),
            );
        }
        result.sort_unstable_by(|left, right| {
            (
                &left.file,
                left.range.start,
                left.range.end,
                &left.template,
                &left.ty,
            )
                .cmp(&(
                    &right.file,
                    right.range.start,
                    right.range.end,
                    &right.template,
                    &right.ty,
                ))
        });
        Ok(result)
    }
}

#[cfg(test)]
pub(crate) fn direct_nodes_for_test(node: &SyntaxNode) -> Vec<SyntaxNode> {
    direct_nodes(node)
}
