//! Go `invokable_command_migration_codeaction.go`: ports the Symfony 7.3
//! `CommandToInvokableIntention` as a conservative whole-document rewrite.
//! It is offered only when `configure()` consists entirely of statically
//! convertible `addArgument()`/`addOption()` calls.

use std::collections::{BTreeMap, HashMap, HashSet};

use swlsp_console::{Input, legacy_command_inputs};
use swlsp_language::LanguageId;
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context, TextDocument};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ASSIGNMENT_EXPRESSION, PHP_BLOCK, PHP_CAST_EXPRESSION, PHP_CLASS_DECLARATION,
    PHP_EXPRESSION_STATEMENT, PHP_EXTENDS_CLAUSE, PHP_FUNCTION_CALL, PHP_MEMBER_ACCESS,
    PHP_MEMBER_CALL, PHP_NAME, PHP_SCOPED_ACCESS, PHP_SCOPED_CALL, PHP_VARIABLE,
};
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_syntax::{Kind, SyntaxNode, TextRange};

use crate::command_invoke_parameter::CONSOLE_COMMAND_CLASS;
use crate::shared::{
    PhpSourceReplacement, apply_php_source_replacements, child_tokens, end_of,
    escape_php_single_quoted, offset_range, php_block_delimiters, php_class_constructor,
    php_class_is_readonly, php_class_name_used_outside, php_class_qualifier, php_line_indentation,
    php_member_indent_unit, request_document_path, start_of, token_start, trim_backslashes,
    trimmed_end, trimmed_start,
};

const INVOKABLE_COMMAND_CLASS: &str = "Symfony\\Component\\Console\\Command\\InvokableCommand";
const COMMAND_ARGUMENT_ATTRIBUTE: &str = "Symfony\\Component\\Console\\Attribute\\Argument";
const COMMAND_OPTION_ATTRIBUTE: &str = "Symfony\\Component\\Console\\Attribute\\Option";
const COMMAND_INPUT_INTERFACE: &str = "Symfony\\Component\\Console\\Input\\InputInterface";
const COMMAND_OUTPUT_INTERFACE: &str = "Symfony\\Component\\Console\\Output\\OutputInterface";

pub struct InvokableCommandMigrationCodeActionProvider {
    php_index: Option<PhpIndex>,
}

impl InvokableCommandMigrationCodeActionProvider {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }
}

impl ActionProvider for InvokableCommandMigrationCodeActionProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(php_index) = self.php_index.as_ref() else {
            return Vec::new();
        };
        let (Some(document), Some(root), Some(node)) = (
            request.document.as_ref(),
            request.root.as_ref(),
            request.node.as_ref(),
        ) else {
            return Vec::new();
        };
        if ctx.is_cancelled() || document.language != Some(LanguageId::Php) {
            return Vec::new();
        }
        if php_index.find_class(INVOKABLE_COMMAND_CLASS).is_none() {
            return Vec::new();
        }
        migration_action(request, document, root, node)
            .map(|action| vec![action])
            .unwrap_or_default()
    }
}

fn migration_action(
    request: &CodeActionRequest,
    document: &TextDocument,
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<protocol::CodeAction> {
    let class = phpquery::class_at(node)?;
    if class.kind() != PHP_CLASS_DECLARATION || php_class_is_readonly(Some(&class)) {
        return None;
    }
    let resolver = NameResolver::new(root);
    let extends = phpquery::direct_child(&class, PHP_EXTENDS_CLAUSE)?;
    let extends_names = phpquery::class_extends(&class);
    if extends_names.len() != 1
        || !equal_fold(
            trim_backslashes(&resolver.resolve(&extends_names[0])),
            CONSOLE_COMMAND_CLASS,
        )
    {
        return None;
    }
    let execute = php_own_method(&class, "execute")?;
    if php_own_method(&class, "__invoke").is_some() {
        return None;
    }
    let configure = php_own_method(&class, "configure");
    if let Some(configure) = &configure
        && !safe_invokable_command_configure(configure)
    {
        return None;
    }

    let path = request_document_path(request);
    let (arguments, options) = legacy_command_inputs(&class, &path);
    if arguments.len() + options.len() != invokable_configure_input_count(configure.as_ref()) {
        return None;
    }
    if !safe_invokable_command_defaults(&arguments, &options) {
        return None;
    }

    let mut argument_qualifier = String::new();
    let mut option_qualifier = String::new();
    let mut import_replacements = Vec::new();
    for (inputs, attribute, qualifier) in [
        (
            &arguments,
            COMMAND_ARGUMENT_ATTRIBUTE,
            &mut argument_qualifier,
        ),
        (&options, COMMAND_OPTION_ATTRIBUTE, &mut option_qualifier),
    ] {
        if inputs.is_empty() {
            continue;
        }
        let (value, edit) = php_class_qualifier(request, attribute);
        if value.is_empty() {
            return None;
        }
        *qualifier = value;
        if let Some(edit) = edit {
            import_replacements.push(php_text_edit_replacement(request, &edit)?);
        }
    }

    let mut migration = build_invokable_command_migration(
        request,
        &class,
        &execute,
        configure.as_ref(),
        &extends,
        &resolver,
        &arguments,
        &options,
        &argument_qualifier,
        &option_qualifier,
    )?;
    migration.extend(import_replacements);
    let updated = apply_php_source_replacements(document.source(), &mut migration)?;
    let updated = cleanup_invokable_command_imports(&document.uri, &updated);
    let parsed = TextDocument::new(document.uri.clone(), &updated, document.version + 1);
    if !parsed.parse_errors().is_empty() {
        return None;
    }
    Some(protocol::CodeAction {
        title: "Symfony: Migrate to invokable command".to_owned(),
        kind: Some(CodeActionKind::REFACTOR_REWRITE),
        edit: Some(protocol::WorkspaceEdit {
            changes: BTreeMap::from([(
                request.params.text_document.uri.clone(),
                vec![protocol::TextEdit::new(
                    offset_range(request, 0, document.source().len() as u32),
                    updated,
                )],
            )]),
            ..protocol::WorkspaceEdit::default()
        }),
        ..protocol::CodeAction::default()
    })
}

fn php_own_method(class: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    phpquery::methods(class)
        .into_iter()
        .find(|method| equal_fold(&phpquery::method_name(method), name))
}

fn function_like_is(call: &SyntaxNode, method: &SyntaxNode) -> bool {
    phpquery::function_like_at(call).as_ref() == Some(method)
}

fn safe_invokable_command_configure(method: &SyntaxNode) -> bool {
    let Some(block) = phpquery::direct_child(method, PHP_BLOCK) else {
        return false;
    };
    for child in block.children() {
        if child.kind() != PHP_EXPRESSION_STATEMENT {
            return false;
        }
        let mut statement_calls = 0;
        for call in phpquery::nodes(
            &child,
            &[PHP_MEMBER_CALL, PHP_SCOPED_CALL, PHP_FUNCTION_CALL],
        ) {
            if !function_like_is(&call, method) {
                continue;
            }
            if call.kind() != PHP_MEMBER_CALL || !invokable_command_call_rooted_at_this(&call) {
                return false;
            }
            match go_to_lower(&phpquery::call_method_name(&call)).as_str() {
                "addargument" | "addoption" => statement_calls += 1,
                _ => return false,
            }
        }
        if statement_calls == 0 {
            return false;
        }
    }
    true
}

fn invokable_command_call_rooted_at_this(call: &SyntaxNode) -> bool {
    let mut receiver = phpquery::call_receiver(call);
    while let Some(current) = receiver {
        let kind = current.kind();
        if kind == PHP_VARIABLE {
            return equal_fold(&phpquery::variable_name(&current), "this");
        } else if kind == PHP_MEMBER_CALL || kind == PHP_SCOPED_CALL {
            receiver = phpquery::call_receiver(&current);
        } else if kind == PHP_MEMBER_ACCESS || kind == PHP_SCOPED_ACCESS {
            receiver = current.children().next();
        } else {
            return false;
        }
    }
    false
}

fn invokable_configure_input_count(configure: Option<&SyntaxNode>) -> usize {
    let Some(configure) = configure else {
        return 0;
    };
    phpquery::nodes(configure, &[PHP_MEMBER_CALL])
        .iter()
        .filter(|call| function_like_is(call, configure))
        .filter(|call| {
            matches!(
                go_to_lower(&phpquery::call_method_name(call)).as_str(),
                "addargument" | "addoption"
            )
        })
        .count()
}

fn safe_invokable_command_defaults(arguments: &[Input], options: &[Input]) -> bool {
    arguments.iter().chain(options).all(|input| {
        let value = go_to_lower(input.default.trim());
        value.is_empty() || !(value.contains("new ") || value.contains(['(', ')', ';']))
    })
}

struct InputCall {
    call: SyntaxNode,
    replacement: SyntaxNode,
    variable_name: String,
}

#[derive(Clone, Default)]
struct CommandParameter {
    text: String,
    optional: bool,
}

#[allow(clippy::too_many_arguments)]
fn build_invokable_command_migration(
    request: &CodeActionRequest,
    class: &SyntaxNode,
    execute: &SyntaxNode,
    configure: Option<&SyntaxNode>,
    extends: &SyntaxNode,
    resolver: &NameResolver,
    arguments: &[Input],
    options: &[Input],
    argument_qualifier: &str,
    option_qualifier: &str,
) -> Option<Vec<PhpSourceReplacement>> {
    let (input_name, input_type) =
        invokable_command_interface_parameter(execute, resolver, COMMAND_INPUT_INTERFACE);
    let (output_name, output_type) =
        invokable_command_interface_parameter(execute, resolver, COMMAND_OUTPUT_INTERFACE);
    let mut input_variables: HashMap<String, String> = HashMap::new();
    let mut required: Vec<CommandParameter> = Vec::new();
    let mut optional: Vec<CommandParameter> = Vec::new();
    let mut used_names: HashSet<String> = HashSet::new();
    for input in arguments {
        let variable = invokable_command_variable_name(&input.name);
        if variable.is_empty() || !used_names.insert(go_to_lower(&variable)) {
            return None;
        }
        input_variables.insert(format!("argument:{}", input.name), variable.clone());
        let parameter = invokable_argument_parameter(input, &variable, argument_qualifier);
        if invokable_argument_optional(input) {
            optional.push(parameter);
        } else {
            required.push(parameter);
        }
    }
    for input in options {
        let variable = invokable_command_variable_name(&input.name);
        if variable.is_empty() || !used_names.insert(go_to_lower(&variable)) {
            return None;
        }
        input_variables.insert(format!("option:{}", input.name), variable.clone());
        optional.push(invokable_option_parameter(
            input,
            &variable,
            option_qualifier,
        ));
    }

    let calls = invokable_input_calls(execute, &input_name, &input_variables);
    let mut replacements = Vec::new();
    let mut replaced_ranges: Vec<TextRange> = Vec::with_capacity(calls.len());
    for item in &calls {
        if let Some(statement) = invokable_redundant_input_assignment(item) {
            replacements.push(PhpSourceReplacement {
                start: start_of(&statement),
                end: end_of(&statement),
                text: String::new(),
            });
            replaced_ranges.push(statement.text_range());
            continue;
        }
        replacements.push(PhpSourceReplacement {
            start: trimmed_start(&item.replacement),
            end: trimmed_end(&item.replacement),
            text: format!("${}", item.variable_name),
        });
        replaced_ranges.push(item.call.text_range());
    }

    let input_used = invokable_variable_used_outside_ranges(execute, &input_name, &replaced_ranges);
    let output_used = invokable_variable_used_outside_ranges(execute, &output_name, &[]);
    if input_used && !input_name.is_empty() {
        if !used_names.insert(go_to_lower(&input_name)) {
            return None;
        }
        required.insert(
            0,
            CommandParameter {
                text: format!("{input_type} ${input_name}"),
                optional: false,
            },
        );
    }
    if output_used && !output_name.is_empty() {
        if !used_names.insert(go_to_lower(&output_name)) {
            return None;
        }
        let position = usize::from(input_used);
        required.insert(
            position,
            CommandParameter {
                text: format!("{output_type} ${output_name}"),
                optional: false,
            },
        );
    }
    for parameter in phpquery::parameters(execute) {
        let parameter_name = phpquery::parameter_name(&parameter);
        let name = parameter_name.strip_prefix('$').unwrap_or(&parameter_name);
        if name.is_empty() || equal_fold(name, &input_name) || equal_fold(name, &output_name) {
            continue;
        }
        if !used_names.insert(go_to_lower(name)) {
            return None;
        }
        let candidate = CommandParameter {
            text: parameter.text().to_string().trim().to_owned(),
            optional: phpquery::parameter_optional(&parameter)
                || phpquery::parameter_variadic(&parameter),
        };
        if candidate.optional {
            optional.push(candidate);
        } else {
            required.push(candidate);
        }
    }

    required.extend(optional);
    let source = request.source();
    replacements.push(invokable_command_signature_replacement(
        source, class, execute, &required,
    )?);
    replacements.push(PhpSourceReplacement {
        start: start_of(extends),
        end: end_of(extends),
        text: String::new(),
    });
    if let Some(configure) = configure {
        replacements.push(PhpSourceReplacement {
            start: start_of(configure),
            end: end_of(configure),
            text: String::new(),
        });
    }
    replacements.extend(invokable_parent_constructor_replacements(class));
    let (command_qualifier, command_import_edit) =
        php_class_qualifier(request, CONSOLE_COMMAND_CLASS);
    let exit_constant_replacements =
        invokable_self_exit_constant_replacements(execute, &command_qualifier);
    let has_exit_constants = !exit_constant_replacements.is_empty();
    replacements.extend(exit_constant_replacements);
    if has_exit_constants && let Some(edit) = command_import_edit {
        replacements.push(php_text_edit_replacement(request, &edit)?);
    }
    Some(replacements)
}

fn split_type_names(value: &str) -> impl Iterator<Item = &str> {
    value
        .split(['?', '|', '&', '(', ')'])
        .filter(|part| !part.is_empty())
}

fn invokable_command_interface_parameter(
    method: &SyntaxNode,
    resolver: &NameResolver,
    interface_name: &str,
) -> (String, String) {
    for parameter in phpquery::parameters(method) {
        let parameter_type = phpquery::parameter_type(&parameter);
        for candidate in split_type_names(&parameter_type) {
            if equal_fold(
                trim_backslashes(&resolver.resolve(candidate)),
                interface_name,
            ) {
                let name = phpquery::parameter_name(&parameter);
                return (
                    name.strip_prefix('$').unwrap_or(&name).to_owned(),
                    parameter_type.clone(),
                );
            }
        }
    }
    (String::new(), String::new())
}

fn invokable_input_calls(
    execute: &SyntaxNode,
    input_name: &str,
    variables: &HashMap<String, String>,
) -> Vec<InputCall> {
    if input_name.is_empty() {
        return Vec::new();
    }
    let mut result = Vec::new();
    for call in phpquery::nodes(execute, &[PHP_MEMBER_CALL]) {
        if !function_like_is(&call, execute) {
            continue;
        }
        let Some(receiver) = phpquery::call_receiver(&call) else {
            continue;
        };
        if receiver.kind() != PHP_VARIABLE
            || !equal_fold(&phpquery::variable_name(&receiver), input_name)
        {
            continue;
        }
        let kind = match go_to_lower(&phpquery::call_method_name(&call)).as_str() {
            "getargument" => "argument:",
            "getoption" => "option:",
            _ => continue,
        };
        let Some(literal) = phpquery::string_argument(&call, 0) else {
            continue;
        };
        let Some(variable) = variables
            .get(&format!("{kind}{}", phpquery::string_value(&literal)))
            .filter(|variable| !variable.is_empty())
        else {
            continue;
        };
        let replacement = match call.parent() {
            Some(parent) if parent.kind() == PHP_CAST_EXPRESSION => parent,
            _ => call.clone(),
        };
        result.push(InputCall {
            call,
            replacement,
            variable_name: variable.clone(),
        });
    }
    result
}

fn invokable_redundant_input_assignment(item: &InputCall) -> Option<SyntaxNode> {
    let statement = php_ancestor_of_kind(&item.replacement, PHP_EXPRESSION_STATEMENT)?;
    if !equal_fold(
        &phpquery::assigned_variable(&statement),
        &format!("${}", item.variable_name),
    ) {
        return None;
    }
    let assignments = phpquery::nodes(&statement, &[PHP_ASSIGNMENT_EXPRESSION]);
    if assignments.len() != 1 {
        return None;
    }
    let values: Vec<SyntaxNode> = assignments[0].children().collect();
    if values.len() != 2 || values[1] != item.replacement {
        return None;
    }
    Some(statement)
}

fn invokable_variable_used_outside_ranges(
    method: &SyntaxNode,
    name: &str,
    excluded: &[TextRange],
) -> bool {
    if name.is_empty() {
        return false;
    }
    let Some(block) = phpquery::direct_child(method, PHP_BLOCK) else {
        return false;
    };
    phpquery::nodes(&block, &[PHP_VARIABLE])
        .iter()
        .any(|variable| {
            if !equal_fold(&phpquery::variable_name(variable), name) {
                return false;
            }
            let range = variable.text_range();
            !excluded
                .iter()
                .any(|excluded| range.start() >= excluded.start() && range.end() <= excluded.end())
        })
}

fn attribute_prefix(qualifier: &str, fields: &[String]) -> String {
    let mut attribute = format!("#[{qualifier}");
    if !fields.is_empty() {
        attribute.push('(');
        attribute.push_str(&fields.join(", "));
        attribute.push(')');
    }
    attribute.push_str("] ");
    attribute
}

fn invokable_argument_parameter(
    input: &Input,
    variable: &str,
    qualifier: &str,
) -> CommandParameter {
    let mut fields = Vec::new();
    if variable != input.name {
        fields.push(format!("name: '{}'", escape_php_single_quoted(&input.name)));
    }
    if !input.description.is_empty() {
        fields.push(format!(
            "description: '{}'",
            escape_php_single_quoted(&input.description)
        ));
    }
    let attribute = attribute_prefix(qualifier, &fields);
    let mut parameter_type = if input.mode.to_uppercase().contains("IS_ARRAY") {
        "array".to_owned()
    } else {
        "string".to_owned()
    };
    let optional = invokable_argument_optional(input);
    if optional {
        parameter_type = format!("?{parameter_type}");
    }
    let mut text = format!("{attribute}{parameter_type} ${variable}");
    if optional {
        let value = input.default.trim();
        let value = if value.is_empty() { "null" } else { value };
        text.push_str(&format!(" = {value}"));
    }
    CommandParameter { text, optional }
}

fn invokable_argument_optional(input: &Input) -> bool {
    !input.default.is_empty() || input.mode.to_uppercase().contains("OPTIONAL")
}

fn invokable_option_parameter(input: &Input, variable: &str, qualifier: &str) -> CommandParameter {
    let mut fields = Vec::new();
    if variable != input.name {
        fields.push(format!("name: '{}'", escape_php_single_quoted(&input.name)));
    }
    if !input.shortcut.is_empty() {
        fields.push(format!(
            "shortcut: '{}'",
            escape_php_single_quoted(&input.shortcut)
        ));
    }
    if !input.description.is_empty() {
        fields.push(format!(
            "description: '{}'",
            escape_php_single_quoted(&input.description)
        ));
    }
    let attribute = attribute_prefix(qualifier, &fields);
    let mode = input.mode.to_uppercase();
    let (mut parameter_type, mut default_value) = ("?string", "null");
    if mode.contains("VALUE_NONE") {
        parameter_type = "bool";
        default_value = "false";
    } else if mode.contains("VALUE_IS_ARRAY") || mode.contains("IS_ARRAY") {
        parameter_type = "?array";
    }
    let value = input.default.trim();
    if !value.is_empty() {
        default_value = value;
    }
    CommandParameter {
        text: format!("{attribute}{parameter_type} ${variable} = {default_value}"),
        optional: true,
    }
}

fn is_go_letter(value: char) -> bool {
    value.is_alphabetic()
}

fn is_go_digit(value: char) -> bool {
    value.is_numeric()
}

/// Go `invokableCommandVariableName`.
fn invokable_command_variable_name(name: &str) -> String {
    if name.is_empty() {
        return String::new();
    }
    let valid = name.chars().enumerate().all(|(index, value)| {
        if index == 0 {
            value == '_' || is_go_letter(value)
        } else {
            value == '_' || is_go_letter(value) || is_go_digit(value)
        }
    });
    if valid {
        return name.to_owned();
    }
    let mut builder = String::new();
    let mut upper_next = false;
    for value in name.chars() {
        if is_go_letter(value) || is_go_digit(value) || value == '_' {
            if builder.is_empty() && is_go_digit(value) {
                builder.push('_');
            }
            if upper_next && is_go_letter(value) {
                builder.push(go_to_upper_char(value));
            } else {
                builder.push(value);
            }
            upper_next = false;
            continue;
        }
        upper_next = !builder.is_empty();
    }
    if builder.is_empty() {
        return "arg".to_owned();
    }
    builder
}

/// Go `unicode.ToUpper` for one rune (simple case mapping).
fn go_to_upper_char(value: char) -> char {
    let mut upper = value.to_uppercase();
    match (upper.next(), upper.next()) {
        (Some(single), None) => single,
        _ => value,
    }
}

fn invokable_command_signature_replacement(
    source: &str,
    class: &SyntaxNode,
    method: &SyntaxNode,
    parameters: &[CommandParameter],
) -> Option<PhpSourceReplacement> {
    let start = child_tokens(method)
        .find(|token| {
            matches!(
                go_to_lower(token.text()).as_str(),
                "public" | "protected" | "private" | "function"
            )
        })
        .map_or(0, |token| token_start(&token));
    let block = phpquery::direct_child(method, PHP_BLOCK)?;
    let (open, _) = php_block_delimiters(&block);
    let open = open?;
    if start == 0 || start >= token_start(&open) {
        return None;
    }
    let method_indent = php_line_indentation(source, start);
    let parameter_indent = format!(
        "{method_indent}{}",
        php_member_indent_unit(source, Some(class), method_indent)
    );
    let mut text = String::from("public function __invoke(");
    if !parameters.is_empty() {
        text.push('\n');
        for (index, parameter) in parameters.iter().enumerate() {
            text.push_str(&parameter_indent);
            text.push_str(&parameter.text);
            if index < parameters.len() - 1 {
                text.push(',');
            }
            text.push('\n');
        }
        text.push_str(method_indent);
    }
    text.push_str("): int ");
    Some(PhpSourceReplacement {
        start,
        end: token_start(&open),
        text,
    })
}

fn invokable_parent_constructor_replacements(class: &SyntaxNode) -> Vec<PhpSourceReplacement> {
    let Some(constructor) = php_class_constructor(class) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for call in phpquery::nodes(&constructor, &[PHP_SCOPED_CALL]) {
        if !function_like_is(&call, &constructor)
            || !equal_fold(&phpquery::call_method_name(&call), "__construct")
        {
            continue;
        }
        let Some(receiver) = phpquery::call_receiver(&call) else {
            continue;
        };
        if !equal_fold(&phpquery::name_value(&receiver), "parent") {
            continue;
        }
        if let Some(statement) = php_ancestor_of_kind(&call, PHP_EXPRESSION_STATEMENT) {
            result.push(PhpSourceReplacement {
                start: start_of(&statement),
                end: end_of(&statement),
                text: String::new(),
            });
        }
    }
    result
}

fn invokable_self_exit_constant_replacements(
    execute: &SyntaxNode,
    command_qualifier: &str,
) -> Vec<PhpSourceReplacement> {
    if command_qualifier.is_empty() {
        return Vec::new();
    }
    let mut result = Vec::new();
    for access in phpquery::nodes(execute, &[PHP_MEMBER_ACCESS, PHP_SCOPED_ACCESS]) {
        let names: Vec<String> = access
            .children()
            .filter(|child| child.kind() == PHP_NAME)
            .map(|child| phpquery::name_value(&child))
            .collect();
        if names.len() != 2 || (!equal_fold(&names[0], "self") && !equal_fold(&names[0], "static"))
        {
            continue;
        }
        if matches!(
            names[1].to_uppercase().as_str(),
            "SUCCESS" | "FAILURE" | "INVALID"
        ) {
            result.push(PhpSourceReplacement {
                start: trimmed_start(&access),
                end: trimmed_end(&access),
                text: format!("{command_qualifier}::{}", names[1]),
            });
        }
    }
    result
}

fn php_ancestor_of_kind(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|current| current.kind() == kind)
}

fn php_text_edit_replacement(
    request: &CodeActionRequest,
    edit: &protocol::TextEdit,
) -> Option<PhpSourceReplacement> {
    let document = request.document.as_ref()?;
    let line_index = document.line_index();
    let start = line_index.offset_utf16(edit.range.start.line, edit.range.start.character);
    let end = line_index.offset_utf16(edit.range.end.line, edit.range.end.character);
    if start > end || end as usize > document.source().len() {
        return None;
    }
    Some(PhpSourceReplacement {
        start,
        end,
        text: edit.new_text.clone(),
    })
}

/// Go `cleanupInvokableCommandImports`: drops now-unused console imports.
fn cleanup_invokable_command_imports(uri: &str, source: &str) -> String {
    let document = TextDocument::new(uri, source, 1);
    let Some(root) = document.root() else {
        return source.to_owned();
    };
    let resolver = NameResolver::new(&root);
    let targets: HashSet<String> = [
        CONSOLE_COMMAND_CLASS,
        COMMAND_INPUT_INTERFACE,
        COMMAND_OUTPUT_INTERFACE,
        "Symfony\\Component\\Console\\Input\\InputArgument",
        "Symfony\\Component\\Console\\Input\\InputOption",
    ]
    .iter()
    .map(|target| go_to_lower(target))
    .collect();
    let mut replacements = Vec::new();
    for declaration in phpquery::use_declarations(&root) {
        let imports = parse_use_declaration(&declaration.text().to_string());
        if imports.len() != 1 || imports[0].kind != ImportKind::Class {
            continue;
        }
        let target = trim_backslashes(&imports[0].target);
        if !targets.contains(&go_to_lower(target)) {
            continue;
        }
        if php_class_name_used_outside(&root, &resolver, target, &[Some(&declaration)]) {
            continue;
        }
        replacements.push(PhpSourceReplacement {
            start: start_of(&declaration),
            end: end_of(&declaration),
            text: String::new(),
        });
    }
    if replacements.is_empty() {
        return source.to_owned();
    }
    apply_php_source_replacements(source, &mut replacements).unwrap_or_else(|| source.to_owned())
}

#[cfg(test)]
#[path = "tests/invokable_command_migration.rs"]
mod tests;
