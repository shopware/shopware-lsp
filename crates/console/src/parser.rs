use std::collections::{BTreeMap, HashMap, HashSet};

use swlsp_indexer::ParsedFile;
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_ARRAY, PHP_CLASS_CONST_DECLARATION, PHP_MEMBER_ACCESS, PHP_MEMBER_CALL, PHP_NAME,
    PHP_SCOPED_ACCESS, PHP_STRING, TK_COMMA, TK_EQUALS,
};
use swlsp_parser_xml::query as xml;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, Tree};

use crate::equal_fold;
use crate::model::{Command, Input, InputKind};

pub(crate) fn is_php_command_candidate(source: &str) -> bool {
    [
        "AsCommand",
        "$defaultName",
        "->setName(",
        "->addArgument(",
        "->addOption(",
        "#[Argument",
        "#[Option",
    ]
    .iter()
    .any(|needle| source.contains(needle))
}

/// Command declarations of an already parsed PHP document. On-demand
/// features use the open document's tree so unsaved names stay current.
pub fn parse_php_commands_tree(file_path: &str, tree: Option<&Tree>) -> Vec<Command> {
    tree.map(|tree| parse_php_commands_root(file_path, &tree.root()))
        .unwrap_or_default()
}

fn parse_php_commands_root(file_path: &str, root: &SyntaxNode) -> Vec<Command> {
    let namespace = query::namespace(root);
    let mut commands = Vec::new();
    for class in query::classes(root) {
        let class_name = query::class_name(&class);
        if class_name.is_empty() {
            continue;
        }
        let fqn = if namespace.is_empty() {
            class_name
        } else {
            format!("{namespace}\\{class_name}")
        };
        let (class_arguments, class_options) = class_inputs(&class, file_path);
        let constants = class_string_constants(&class);

        let attributed: Vec<Command> = command_attributes(&class)
            .iter()
            .flat_map(|attribute| {
                commands_from_attribute(
                    attribute,
                    &constants,
                    &fqn,
                    "",
                    &class_arguments,
                    &class_options,
                    file_path,
                )
            })
            .collect();
        if attributed.is_empty() {
            commands.extend(traditional_commands(
                &class,
                &fqn,
                &class_arguments,
                &class_options,
                file_path,
            ));
        } else {
            commands.extend(attributed);
        }

        for method in query::methods(&class) {
            let visibility = query::declaration_visibility(&method);
            if visibility == "private" || visibility == "protected" {
                continue;
            }
            let (arguments, options) = attributed_inputs(&method, file_path);
            let method_name = query::method_name(&method);
            for attribute in command_attributes(&method) {
                commands.extend(commands_from_attribute(
                    &attribute,
                    &constants,
                    &fqn,
                    &method_name,
                    &arguments,
                    &options,
                    file_path,
                ));
            }
        }
    }
    commands
}

fn command_attributes(node: &SyntaxNode) -> Vec<SyntaxNode> {
    query::attributes(node)
        .into_iter()
        .filter(|attribute| attribute_has_name(attribute, "AsCommand"))
        .collect()
}

fn commands_from_attribute(
    attribute: &SyntaxNode,
    constants: &HashMap<String, String>,
    class: &str,
    method: &str,
    arguments: &[Input],
    options: &[Input],
    path: &str,
) -> Vec<Command> {
    let name_node = call_argument(attribute, "name", Some(0));
    let primary = command_name_expression_value(name_node.as_ref(), constants);
    let Some(name_node) = name_node.filter(|_| !primary.is_empty()) else {
        return Vec::new();
    };
    let mut names = vec![(primary.clone(), name_node.clone())];
    if let Some(aliases) = call_argument(attribute, "aliases", Some(2))
        && aliases.kind() == PHP_ARRAY
    {
        for item in query::array_items(&aliases) {
            let Some(expression) = query::array_item_value(&item) else {
                continue;
            };
            let alias = command_name_expression_value(Some(&expression), constants);
            if !alias.is_empty() {
                names.push((alias, expression));
            }
        }
    }
    let description =
        string_expression_value(call_argument(attribute, "description", Some(1)).as_ref());
    names
        .into_iter()
        .map(|(name, node)| Command {
            name,
            canonical: primary.clone(),
            description: description.clone(),
            class: class.to_owned(),
            method: method.to_owned(),
            file: path.to_owned(),
            range: node.range_trimmed_trivia(),
            arguments: arguments.to_vec(),
            options: options.to_vec(),
        })
        .collect()
}

/// A string literal, or a `self::`/`static::` constant of the same class.
fn command_name_expression_value(
    node: Option<&SyntaxNode>,
    constants: &HashMap<String, String>,
) -> String {
    let value = string_expression_value(node);
    if !value.is_empty() {
        return value;
    }
    let Some(node) =
        node.filter(|node| matches!(node.kind(), PHP_SCOPED_ACCESS | PHP_MEMBER_ACCESS))
    else {
        return String::new();
    };
    let text: String = node.text().to_string().split_whitespace().collect();
    let Some(separator) = text.rfind("::") else {
        return String::new();
    };
    if separator == 0 || separator + 2 >= text.len() {
        return String::new();
    }
    let scope = &text[..separator];
    if !equal_fold(scope, "self") && !equal_fold(scope, "static") {
        return String::new();
    }
    constants
        .get(&text[separator + 2..])
        .cloned()
        .unwrap_or_default()
}

fn class_string_constants(class: &SyntaxNode) -> HashMap<String, String> {
    let mut result = HashMap::new();
    let Some(body) = query::class_body(class) else {
        return result;
    };
    for declaration in body
        .children()
        .filter(|child| child.kind() == PHP_CLASS_CONST_DECLARATION)
    {
        let mut expect_name = false;
        let mut expect_value = false;
        let mut name = String::new();
        for element in declaration.children_with_tokens() {
            match element {
                NodeOrToken::Token(token) => {
                    if equal_fold(token.text(), "const") || token.kind() == TK_COMMA {
                        expect_name = true;
                        expect_value = false;
                        name.clear();
                    } else if token.kind() == TK_EQUALS {
                        expect_name = false;
                        expect_value = true;
                    }
                }
                NodeOrToken::Node(node) => {
                    if expect_name && node.kind() == PHP_NAME {
                        name = query::name_value(&node);
                        expect_name = false;
                        continue;
                    }
                    if !expect_value {
                        continue;
                    }
                    if !name.is_empty() && node.kind() == PHP_STRING {
                        let literal = query::string_value(&node);
                        if !literal.is_empty() {
                            result.insert(name.clone(), literal);
                        }
                    }
                    expect_value = false;
                }
            }
        }
    }
    result
}

fn traditional_commands(
    class: &SyntaxNode,
    fqn: &str,
    arguments: &[Input],
    options: &[Input],
    path: &str,
) -> Vec<Command> {
    let (names, range) = traditional_command_names(class);
    let Some(first) = names.first().cloned() else {
        return Vec::new();
    };
    names
        .into_iter()
        .map(|name| Command {
            name,
            canonical: first.clone(),
            class: fqn.to_owned(),
            file: path.to_owned(),
            range,
            arguments: arguments.to_vec(),
            options: options.to_vec(),
            ..Command::default()
        })
        .collect()
}

/// Names from `protected static $defaultName = 'a|b'` or
/// `$this->setName('a')` in `configure()`.
fn traditional_command_names(class: &SyntaxNode) -> (Vec<String>, TextRange) {
    for property in query::properties(class) {
        let declares_default_name = query::property_variables(&property)
            .iter()
            .any(|variable| query::variable_name(variable) == "defaultName");
        if !declares_default_name {
            continue;
        }
        if let Some(literal) = query::nodes(&property, &[PHP_STRING]).first() {
            return (
                split_command_names(&query::string_value(literal)),
                literal.range_trimmed_trivia(),
            );
        }
    }
    for method in query::methods(class) {
        if !equal_fold(&query::method_name(&method), "configure") {
            continue;
        }
        for call in query::nodes(&method, &[PHP_MEMBER_CALL]) {
            if query::function_like_at(&call).as_ref() != Some(&method)
                || !equal_fold(&query::call_method_name(&call), "setName")
            {
                continue;
            }
            if let Some(literal) = query::string_argument(&call, 0) {
                return (
                    split_command_names(&query::string_value(&literal)),
                    literal.range_trimmed_trivia(),
                );
            }
        }
    }
    (Vec::new(), TextRange::default())
}

fn split_command_names(value: &str) -> Vec<String> {
    value
        .split('|')
        .map(str::trim)
        .filter(|name| !name.is_empty())
        .map(str::to_owned)
        .collect()
}

fn class_inputs(class: &SyntaxNode, path: &str) -> (Vec<Input>, Vec<Input>) {
    let mut arguments = Vec::new();
    let mut options = Vec::new();
    for method in query::methods(class) {
        match query::method_name(&method).to_lowercase().as_str() {
            "configure" => {
                for call in query::nodes(&method, &[PHP_MEMBER_CALL]) {
                    if query::function_like_at(&call).as_ref() != Some(&method) {
                        continue;
                    }
                    match query::call_method_name(&call).to_lowercase().as_str() {
                        "addargument" => {
                            arguments.extend(input_from_arguments(&call, InputKind::Argument, path))
                        }
                        "addoption" => {
                            options.extend(input_from_arguments(&call, InputKind::Option, path))
                        }
                        _ => {}
                    }
                }
                for creation in query::object_creations(&method, &[]) {
                    match short_php_name(&query::object_class_name(&creation)) {
                        "InputArgument" => {
                            arguments.extend(input_from_arguments(
                                &creation,
                                InputKind::Argument,
                                path,
                            ));
                        }
                        "InputOption" => {
                            options.extend(input_from_arguments(
                                &creation,
                                InputKind::Option,
                                path,
                            ));
                        }
                        _ => {}
                    }
                }
            }
            "__invoke" => {
                let (attributed_arguments, attributed_options) = attributed_inputs(&method, path);
                arguments.extend(attributed_arguments);
                options.extend(attributed_options);
            }
            _ => {}
        }
    }
    (unique_inputs(arguments), unique_inputs(options))
}

/// `addArgument()`/`addOption()` declarations of the class's own
/// `configure()` in source order (first declaration of a name wins), for
/// refactorings that preserve declaration order.
pub fn legacy_command_inputs(class: &SyntaxNode, path: &str) -> (Vec<Input>, Vec<Input>) {
    let mut arguments = Vec::new();
    let mut options = Vec::new();
    let mut seen_arguments = HashSet::new();
    let mut seen_options = HashSet::new();
    let Some(method) = query::methods(class)
        .into_iter()
        .find(|method| equal_fold(&query::method_name(method), "configure"))
    else {
        return (arguments, options);
    };
    for call in query::nodes(&method, &[PHP_MEMBER_CALL]) {
        if query::function_like_at(&call).as_ref() != Some(&method) {
            continue;
        }
        let (kind, seen, target) = match query::call_method_name(&call).to_lowercase().as_str() {
            "addargument" => (InputKind::Argument, &mut seen_arguments, &mut arguments),
            "addoption" => (InputKind::Option, &mut seen_options, &mut options),
            _ => continue,
        };
        if let Some(input) = input_from_arguments(&call, kind, path)
            && seen.insert(input.name.clone())
        {
            target.push(input);
        }
    }
    (arguments, options)
}

/// `#[Argument]`/`#[Option]` parameters of an invokable command method.
fn attributed_inputs(method: &SyntaxNode, path: &str) -> (Vec<Input>, Vec<Input>) {
    let mut arguments = Vec::new();
    let mut options = Vec::new();
    for parameter in query::parameters(method) {
        for attribute in query::attributes(&parameter) {
            let kind = if attribute_has_name(&attribute, "Argument") {
                InputKind::Argument
            } else if attribute_has_name(&attribute, "Option") {
                InputKind::Option
            } else {
                continue;
            };
            let name_node = call_argument(&attribute, "name", None);
            let mut name = string_expression_value(name_node.as_ref());
            if name.is_empty() {
                let parameter_name = query::parameter_name(&parameter);
                name = parameter_name
                    .strip_prefix('$')
                    .unwrap_or(&parameter_name)
                    .to_owned();
            }
            if name.is_empty() {
                continue;
            }
            let range = name_node.as_ref().map_or_else(
                || parameter.range_trimmed_trivia(),
                SyntaxNodeExt::range_trimmed_trivia,
            );
            let input = Input {
                name,
                kind,
                shortcut: string_expression_value(
                    call_argument(&attribute, "shortcut", None).as_ref(),
                ),
                description: string_expression_value(
                    call_argument(&attribute, "description", None).as_ref(),
                ),
                default: parameter_default(&parameter),
                file: path.to_owned(),
                range,
                ..Input::default()
            };
            match kind {
                InputKind::Argument => arguments.push(input),
                InputKind::Option => options.push(input),
            }
        }
    }
    (unique_inputs(arguments), unique_inputs(options))
}

fn input_from_arguments(call: &SyntaxNode, kind: InputKind, path: &str) -> Option<Input> {
    let name_node = call_argument(call, "name", Some(0))?;
    let name = string_expression_value(Some(&name_node));
    if name.is_empty() {
        return None;
    }
    let argument = |name: &str, fallback: usize| call_argument(call, name, Some(fallback));
    let mut input = Input {
        name,
        kind,
        file: path.to_owned(),
        range: name_node.range_trimmed_trivia(),
        ..Input::default()
    };
    match kind {
        InputKind::Argument => {
            input.mode = expression_text(argument("mode", 1).as_ref());
            input.description = string_expression_value(argument("description", 2).as_ref());
            input.default = expression_text(argument("default", 3).as_ref());
        }
        InputKind::Option => {
            input.shortcut = string_expression_value(argument("shortcut", 1).as_ref());
            input.mode = expression_text(argument("mode", 2).as_ref());
            input.description = string_expression_value(argument("description", 3).as_ref());
            input.default = expression_text(argument("default", 4).as_ref());
        }
    }
    Some(input)
}

/// The named argument `name`, else the positional argument at `fallback`
/// (unless that position holds a different named argument).
fn call_argument(call: &SyntaxNode, name: &str, fallback: Option<usize>) -> Option<SyntaxNode> {
    if let Some(index) = query::arguments(call)
        .position(|argument| equal_fold(&query::argument_name(&argument), name))
    {
        return query::argument_expression(call, index);
    }
    let fallback = fallback?;
    let argument = query::argument(call, fallback)?;
    if !query::argument_name(&argument).is_empty() {
        return None;
    }
    query::argument_expression(call, fallback)
}

fn attribute_has_name(attribute: &SyntaxNode, expected: &str) -> bool {
    equal_fold(short_php_name(&query::attribute_name(attribute)), expected)
}

fn short_php_name(name: &str) -> &str {
    let name = name.strip_prefix('\\').unwrap_or(name);
    name.rsplit('\\').next().unwrap_or(name)
}

fn string_expression_value(node: Option<&SyntaxNode>) -> String {
    match node {
        Some(node) if node.kind() == PHP_STRING => query::string_value(node),
        _ => String::new(),
    }
}

fn expression_text(node: Option<&SyntaxNode>) -> String {
    node.map_or_else(String::new, |node| {
        node.text().to_string().trim().to_owned()
    })
}

fn parameter_default(parameter: &SyntaxNode) -> String {
    let text = parameter.text().to_string();
    let text = text.trim();
    text.rfind('=')
        .map_or_else(String::new, |equals| text[equals + 1..].trim().to_owned())
}

/// Unique by name (the last declaration wins), sorted by name.
fn unique_inputs(inputs: Vec<Input>) -> Vec<Input> {
    let unique: BTreeMap<String, Input> = inputs
        .into_iter()
        .map(|input| (input.name.clone(), input))
        .collect();
    unique.into_values().collect()
}

pub(crate) fn parse_xml_commands(file: &ParsedFile) -> Vec<Command> {
    let Some(tree) = file.syntax_tree() else {
        return Vec::new();
    };
    let mut commands = Vec::new();
    for service in xml::elements(&tree.root(), &["service"]) {
        let attributes = xml::attribute_values(&service);
        let class = attributes
            .get("class")
            .filter(|class| !class.is_empty())
            .or_else(|| attributes.get("id"))
            .cloned()
            .unwrap_or_default();
        for tag in xml::child_elements(&service, &["tag"]) {
            let tag_attributes = xml::attribute_values(&tag);
            let command = tag_attributes.get("command").map_or("", String::as_str);
            if tag_attributes.get("name").map(String::as_str) != Some("console.command")
                || command.is_empty()
            {
                continue;
            }
            let range = xml::attribute(&tag, "command").map_or_else(
                || tag.range_trimmed_trivia(),
                |attribute| attribute.range_trimmed_trivia(),
            );
            commands.push(Command {
                name: command.to_owned(),
                canonical: command.to_owned(),
                class: class.strip_prefix('\\').unwrap_or(&class).to_owned(),
                file: file.path().to_owned(),
                range,
                ..Command::default()
            });
        }
    }
    commands
}
