//! Code actions, rename, custom commands and raw requests (Go
//! `features_write.go`). Edits preview as a unified diff unless `-w` is set.

use std::io::Read;

use regex::Regex;
use serde_json::{Value, json};
use swlsp_lsp_protocol::{CodeAction, DiagnosticResult, WorkspaceEdit};

use crate::edit::{EditMode, apply_workspace_edit};
use crate::goflag::FlagSet;
use crate::gofmt::go_quote;
use crate::session::{position_params, text_document_params};
use crate::target::{code_action_selection, parse_position_target, require_one_argument};
use crate::{CliError, Runner};

pub(crate) fn matches_code_action_kind(actual: &str, requested: &str) -> bool {
    actual == requested || actual.starts_with(&format!("{requested}."))
}

pub(crate) fn action_kind(action: &CodeAction) -> String {
    action
        .kind
        .as_ref()
        .map(|kind| kind.as_str().to_owned())
        .unwrap_or_default()
}

pub(crate) fn run_code_action(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let mut flags = FlagSet::new("codeaction");
    flags.int("end-line", 0, "one-based exclusive selection end line");
    flags.int(
        "end-column",
        0,
        "one-based UTF-16 exclusive selection end column",
    );
    flags.string("kind", "", "filter by hierarchical code-action kind");
    flags.string(
        "title",
        "",
        "regular expression matched against action titles",
    );
    flags.bool("exec", false, "execute the first matching action");
    flags.bool("w", false, "write resulting edits");
    flags.bool("d", false, "print resulting edits as a unified diff");
    let mut err_out = runner.err_out.clone();
    flags
        .parse(args, &mut err_out)
        .map_err(|error| CliError::usage(error.0))?;
    let value = require_one_argument(flags.args(), "codeaction expects one file position")?;
    let title_pattern = flags.get_string("title");
    let title_matcher = if title_pattern.is_empty() {
        None
    } else {
        Some(
            Regex::new(&title_pattern)
                .map_err(|error| CliError::fail(format!("compile title expression: {error}")))?,
        )
    };
    let target = parse_position_target(&value)?;
    let selection = code_action_selection(
        target.position,
        flags.get_int("end-line"),
        flags.get_int("end-column"),
    )?;
    let kind = flags.get_string("kind");
    let session = runner.connect()?;
    let document = session.open_document(&target.path)?;
    let diagnostics: Option<DiagnosticResult> = session.call(
        "textDocument/diagnostic",
        text_document_params(&document.uri),
    )?;
    let mut params = position_params(&document.uri, target.position);
    params["range"] = json!(selection);
    let mut context = json!({"diagnostics": diagnostics.unwrap_or_default().items});
    if !kind.is_empty() {
        context["only"] = json!([kind]);
    }
    params["context"] = context;
    let actions: Option<Vec<CodeAction>> = session.call("textDocument/codeAction", params)?;
    let filtered: Option<Vec<CodeAction>> = actions.map(|actions| {
        actions
            .into_iter()
            .filter(|action| {
                kind.is_empty() || matches_code_action_kind(&action_kind(action), &kind)
            })
            .filter(|action| {
                title_matcher
                    .as_ref()
                    .is_none_or(|matcher| matcher.is_match(&action.title))
            })
            .collect()
    });
    if !flags.get_bool("exec") {
        return runner.write_json(&filtered);
    }
    let Some(mut action) = filtered.and_then(|actions| actions.into_iter().next()) else {
        return Err(CliError::fail("no matching code action"));
    };
    if let Some(disabled) = &action.disabled {
        return Err(CliError::fail(format!(
            "code action {} is disabled: {}",
            go_quote(&action.title),
            disabled.reason
        )));
    }
    if action.edit.is_none() && action.data.is_some() {
        let resolved: Option<CodeAction> = session.call(
            "codeAction/resolve",
            serde_json::to_value(&action).unwrap_or(Value::Null),
        )?;
        action = resolved.unwrap_or_default();
        if let Some(disabled) = &action.disabled {
            return Err(CliError::fail(format!(
                "code action {} is disabled: {}",
                go_quote(&action.title),
                disabled.reason
            )));
        }
    }
    if let Some(edit) = &action.edit {
        let mut out = runner.out.clone();
        return apply_workspace_edit(
            &mut out,
            Some(edit),
            EditMode {
                write: flags.get_bool("w"),
                diff: flags.get_bool("d"),
            },
        );
    }
    if let Some(command) = &action.command {
        return Err(CliError::fail(format!(
            "code action {} requires editor command {}; use the corresponding CLI execute command",
            go_quote(&action.title),
            go_quote(&command.command)
        )));
    }
    Err(CliError::fail(format!(
        "code action {} returned neither edits nor a command",
        go_quote(&action.title)
    )))
}

pub(crate) fn run_rename(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let mut flags = FlagSet::new("rename");
    flags.bool("w", false, "write resulting edits");
    flags.bool("d", false, "print resulting edits as a unified diff");
    let mut err_out = runner.err_out.clone();
    flags
        .parse(args, &mut err_out)
        .map_err(|error| CliError::usage(error.0))?;
    if flags.args().len() != 2 {
        return Err(CliError::usage(
            "rename expects a file position and a new name",
        ));
    }
    let target = parse_position_target(&flags.args()[0])?;
    let session = runner.connect()?;
    let document = session.open_document(&target.path)?;
    let mut params = position_params(&document.uri, target.position);
    params["newName"] = json!(flags.args()[1]);
    let edit: Option<WorkspaceEdit> = session.call("textDocument/rename", params)?;
    let Some(edit) = edit else {
        return Err(CliError::fail("the selected symbol cannot be renamed"));
    };
    let mut out = runner.out.clone();
    apply_workspace_edit(
        &mut out,
        Some(&edit),
        EditMode {
            write: flags.get_bool("w"),
            diff: flags.get_bool("d"),
        },
    )
}

pub(crate) fn run_execute(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let session = runner.connect()?;
    if args.is_empty() {
        let commands: Option<Vec<String>> = session.call("shopware/commands", json!({}))?;
        let _ = session.close();
        return runner.write_json(&commands);
    }
    if args.len() > 2 {
        return Err(CliError::usage(
            "execute expects a method and one optional JSON argument",
        ));
    }
    let params = parse_json_argument(runner, &args[1..])?;
    let result: Value = session.call(&args[0], params)?;
    let _ = session.close();
    runner.write_json(&result)
}

pub(crate) fn run_request(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    if args.is_empty() || args.len() > 2 {
        return Err(CliError::usage(
            "request expects a method and one optional JSON parameter",
        ));
    }
    let params = parse_json_argument(runner, &args[1..])?;
    let session = runner.connect()?;
    let result: Value = session.call(&args[0], params)?;
    let _ = session.close();
    runner.write_json(&result)
}

/// Go `parseJSONArgument`: inline JSON, `-` for stdin or `@file`.
fn parse_json_argument(runner: &Runner, args: &[String]) -> Result<Value, CliError> {
    let Some(argument) = args.first() else {
        return Ok(json!({}));
    };
    let content = if argument == "-" {
        let mut content = Vec::new();
        runner
            .take_input()
            .read_to_end(&mut content)
            .map_err(|error| CliError::fail(format!("read JSON argument: {error}")))?;
        content
    } else if let Some(path) = argument.strip_prefix('@') {
        std::fs::read(path).map_err(|error| {
            CliError::fail(format!(
                "read JSON argument file: {}",
                crate::gofmt::path_error("open", path, &error)
            ))
        })?
    } else {
        argument.as_bytes().to_vec()
    };
    serde_json::from_slice(&content)
        .map_err(|error| CliError::fail(format!("parse JSON argument: {error}")))
}

#[allow(dead_code)]
fn _require(args: &[String]) -> Result<String, CliError> {
    require_one_argument(args, "")
}
