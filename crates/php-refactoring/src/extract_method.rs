use std::collections::HashSet;

use swlsp_parser_php::query::{self, MethodSelection};
use swlsp_parser_php::syntax::*;
use swlsp_rewrite::Builder;
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};

use crate::extraction_flow::{Env, ExtractionFlow};
use crate::extraction_reindent::reindent_extraction;
use crate::extraction_returns::{control_return_call, reindent_control_returns};
use crate::extraction_scope::extraction_available;
use crate::{Cancelled, Extraction, Result, check, newline_of, source_indent};

/// Checks inherited and trait methods in the shared semantic snapshot. The
/// class is `None` when extracting from a plain function.
pub type MethodNameAvailable<'a> = &'a dyn Fn(Option<&SyntaxNode>, &str) -> bool;

#[derive(Default)]
pub(crate) struct MethodFlow {
    pub control_returns: Vec<SyntaxNode>,
    pub value_return: bool,
    pub void_return: bool,
    pub inputs: Vec<String>,
    pub outputs: Vec<String>,
    pub references: HashSet<String>,
    pub returns: bool,
    pub bare_return: bool,
}

/// Extracts consecutive method (or function) statements into a new private
/// method (or function). Supports structured control flow, multiple live
/// outputs and early returns; unknown call arguments are conservatively
/// treated as possible reference writes.
pub fn extract_method(
    root: &SyntaxNode,
    source: &str,
    selection: TextRange,
    available: Option<MethodNameAvailable<'_>>,
    cancelled: Cancelled<'_>,
) -> Result<Option<Extraction>> {
    check(cancelled)?;
    let Some(selected) = query::select_method_statements(root, source, selection, cancelled) else {
        return Ok(None);
    };
    if !method_scope_safe(&selected.method, cancelled) {
        return Ok(None);
    }
    let Some(flow) = analyze_method_flow(&selected, cancelled) else {
        check(cancelled)?;
        return Ok(None);
    };
    let base_name = if selected.class.is_none() {
        "extractedFunction"
    } else {
        "extractedMethod"
    };
    let mut name = base_name.to_owned();
    let lower_source = source.to_lowercase();
    let mut suffix = 2;
    while lower_source.contains(&name.to_lowercase())
        || available.is_some_and(|available| !available(selected.class.as_ref(), &name))
    {
        check(cancelled)?;
        // An unavailable semantic hierarchy must not cause an unbounded
        // name search.
        if suffix > 1000 {
            return Ok(None);
        }
        name = format!("{base_name}{suffix}");
        suffix += 1;
    }
    build_method_extraction(source, &selected, &flow, name).map(Some)
}

fn analyze_method_flow(selected: &MethodSelection, cancelled: Cancelled<'_>) -> Option<MethodFlow> {
    let (base, references) = extraction_available(selected);
    let mut f = ExtractionFlow::new(
        base,
        references,
        query::declaration_is_static(&selected.method),
        cancelled,
    );
    let mut env = Env::new();
    let (returns, ok) = f.statements(&selected.statements, &mut env, 0);
    if !ok {
        return None;
    }
    let mut flow = MethodFlow {
        returns,
        ..MethodFlow::default()
    };
    for ret in &f.returns {
        if ret.first_child().is_none() {
            flow.void_return = true;
        } else {
            flow.value_return = true;
        }
    }
    flow.bare_return = flow.void_return && !flow.value_return;
    if !f.returns.is_empty() && (!returns || flow.void_return && flow.value_return) {
        flow.control_returns = f.returns.clone();
        // Tagged early returns cannot retain a newly created local in the
        // caller on every path. Its destructor could otherwise run too soon.
        if f.retained.iter().any(|name| !f.base.contains(name)) {
            return None;
        }
    }
    if !returns {
        let (start, end) = (selected.range.start(), selected.range.end());
        let mut emitted = HashSet::new();
        for element in selected.method.descendants_with_tokens() {
            if cancelled() {
                return None;
            }
            let NodeOrToken::Token(token) = element else {
                continue;
            };
            let token_start = token.text_range().start();
            if token_start >= start && token_start < end {
                continue;
            }
            let names = match token.kind() {
                TK_VARIABLE => vec![token.text().to_owned()],
                TK_STRING => query::string_variables(&token).unwrap_or_default(),
                _ => continue,
            };
            for name in names {
                // Reads before the selection also matter when extracting a
                // loop body: the next iteration can consume a value written
                // here.
                if !f.writes.contains(&name) || emitted.contains(&name) || name == "$this" {
                    continue;
                }
                if !env.contains(&name) && !f.read(&name, &mut env) {
                    return None;
                }
                if !f.base.contains(&name) && !env.contains(&name) {
                    return None;
                }
                emitted.insert(name.clone());
                flow.outputs.push(name);
            }
        }
        for name in f.retained.clone() {
            if flow.outputs.contains(&name) {
                continue;
            }
            if !env.contains(&name) && !f.read(&name, &mut env) {
                return None;
            }
            flow.outputs.push(name);
        }
    }
    flow.inputs = f.inputs;
    flow.references = f.references;
    Some(flow)
}

/// Excludes scope-dependent constructs and hidden variable reads. Simple
/// string interpolation is analyzed through native token queries.
fn method_scope_safe(method: &SyntaxNode, cancelled: Cancelled<'_>) -> bool {
    const UNSAFE_TOKENS: &[&str] = &[
        "$",
        "eval",
        "extract",
        "compact",
        "get_defined_vars",
        "unset",
        "func_get_args",
        "func_get_arg",
        "func_num_args",
        "debug_backtrace",
        "__method__",
        "__function__",
        "__line__",
        "include",
        "include_once",
        "require",
        "require_once",
        "goto",
    ];
    for element in method.descendants_with_tokens() {
        if cancelled() {
            return false;
        }
        match element {
            NodeOrToken::Node(node) => {
                if matches!(
                    node.kind(),
                    ERROR
                        | PHP_GLOBAL_STATEMENT
                        | PHP_STATIC_STATEMENT
                        | PHP_TRY_STATEMENT
                        | PHP_YIELD_EXPRESSION
                ) {
                    return false;
                }
            }
            NodeOrToken::Token(token) => {
                if token.kind() == TK_AMPERSAND
                    && token
                        .parent()
                        .is_none_or(|parent| parent.kind() != PHP_PARAMETER)
                {
                    return false;
                }
                if token.kind() == TK_STRING && query::string_variables(&token).is_none() {
                    return false;
                }
                let text = token.text();
                if UNSAFE_TOKENS
                    .iter()
                    .any(|unsafe_text| text.eq_ignore_ascii_case(unsafe_text))
                {
                    return false;
                }
            }
        }
    }
    true
}

fn build_method_extraction(
    source: &str,
    selected: &MethodSelection,
    flow: &MethodFlow,
    name: String,
) -> Result<Extraction> {
    let newline = newline_of(source);
    let mut method_indent = source_indent(
        source,
        selected.method.range_trimmed_trivia().start().into(),
    )
    .to_owned();
    let statement_indent = source_indent(source, selected.range.start().into());
    let mut unit = statement_indent
        .strip_prefix(method_indent.as_str())
        .unwrap_or(statement_indent);
    if unit.is_empty() || unit == statement_indent && !method_indent.is_empty() {
        unit = "    ";
    }
    if method_indent.is_empty()
        && let Some(class) = &selected.class
    {
        method_indent = format!(
            "{}{unit}",
            source_indent(source, class.range_trimmed_trivia().start().into())
        );
    }
    let is_static = query::declaration_is_static(&selected.method);
    let (mut call, visibility, modifier) = match &selected.class {
        None => (name.clone(), "", ""),
        Some(_) if is_static => (format!("self::{name}"), "private ", "static "),
        Some(_) => (format!("$this->{name}"), "private ", ""),
    };
    call = format!("{call}({});", flow.inputs.join(", "));
    if !flow.control_returns.is_empty() {
        call = control_return_call(source, &call, flow, newline, statement_indent);
    } else if flow.returns && flow.bare_return {
        call = format!("{call}{newline}{statement_indent}return;");
    } else if flow.returns {
        call = format!("return {call}");
    } else if !flow.outputs.is_empty() {
        call = format!("{} = {call}", flow_output(&flow.outputs));
    }
    let body_indent = format!("{method_indent}{unit}");
    let mut body = if flow.control_returns.is_empty() {
        reindent_extraction(
            source,
            selected.range,
            statement_indent,
            &body_indent,
            &selected.statements,
        )
    } else {
        reindent_control_returns(
            source,
            selected.range,
            statement_indent,
            &body_indent,
            &selected.statements,
            &flow.control_returns,
        )?
    };
    if !flow.control_returns.is_empty() {
        let output = if flow.outputs.is_empty() {
            "null".to_owned()
        } else {
            flow_output(&flow.outputs)
        };
        body.push_str(&format!("{newline}{body_indent}return [0, {output}];"));
    } else if !flow.outputs.is_empty() {
        body.push_str(&format!(
            "{newline}{body_indent}return {};",
            flow_output(&flow.outputs)
        ));
    }
    let parameters: Vec<String> = flow
        .inputs
        .iter()
        .map(|parameter| {
            if flow.references.contains(parameter) {
                format!("&{parameter}")
            } else {
                parameter.clone()
            }
        })
        .collect();
    let method = format!(
        "{newline}{newline}{method_indent}{visibility}{modifier}function {name}({}){newline}{method_indent}{{{newline}{body}{newline}{method_indent}}}",
        parameters.join(", ")
    );
    // Insert immediately after the original method, preserving its
    // surrounding class comments and the existing closing-brace indentation.
    let offset = selected.method.range_trimmed_trivia().end();
    let mut builder = Builder::new(source);
    builder.replace_range(selected.range, call)?;
    builder.insert(offset.into(), method)?;
    Ok(Extraction {
        edits: builder.finish()?,
        name,
        occurrences: 0,
    })
}

pub(crate) fn flow_output(names: &[String]) -> String {
    match names {
        [name] => name.clone(),
        _ => format!("[{}]", names.join(", ")),
    }
}
