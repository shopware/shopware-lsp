//! `format`: runs the document formatters of the language server over files.
//! Like the other commands that change files it previews a unified diff and
//! writes only with `-w`.

use std::collections::BTreeMap;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp_protocol::{TextEdit, WorkspaceEdit};

use crate::args::Format;
use crate::edit::{EditMode, apply_protocol_text_edits, apply_workspace_edit};
use crate::session::language_for;
use crate::workspace_commands::{display_workspace_path, resolve_check_files_with_exclusions};
use crate::{CliError, Runner};

/// The languages a formatter is registered for. Other files below a
/// directory target are not opened at all.
const FORMATTED_LANGUAGES: &[LanguageId] = &[
    LanguageId::Php,
    LanguageId::Twig,
    LanguageId::Json,
    LanguageId::JavaScript,
    LanguageId::Scss,
];

pub(crate) fn run_format(runner: &Runner, flags: &Format) -> Result<(), CliError> {
    let indent_width = flags.indent_width.unwrap_or(4);
    if !(1..=16).contains(&indent_width) {
        return Err(CliError::usage(
            "format --indent-width must be between 1 and 16",
        ));
    }
    let session = runner.connect()?;
    let paths: Vec<String> = resolve_check_files_with_exclusions(
        &runner.cancel,
        &session.root,
        &flags.paths,
        &session.configuration.exclude,
    )?
    .into_iter()
    .filter(|path| {
        language_for(path).is_some_and(|language| FORMATTED_LANGUAGES.contains(&language))
    })
    .collect();
    if paths.is_empty() {
        let _ = session.close();
        return Err(CliError::fail(
            "no file to format: only PHP, Twig, JSON, JavaScript, TypeScript and SCSS files have a formatter",
        ));
    }

    let mode = EditMode {
        write: flags.write,
        diff: !flags.write && !flags.check && !runner.json,
    };
    let mut changed: Vec<String> = Vec::new();
    let mut out = runner.out.clone();
    for path in &paths {
        if runner.cancel.is_cancelled() {
            return Err(CliError::cancelled());
        }
        let edits = session.with_document(path, |document| {
            let edits: Option<Vec<TextEdit>> = session.call(
                "textDocument/formatting",
                json!({
                    "textDocument": {"uri": document.uri},
                    "options": {
                        "tabSize": indent_width,
                        "insertSpaces": !flags.use_tabs,
                        "insertFinalNewline": true,
                    },
                }),
            )?;
            let edits = edits.unwrap_or_default();
            let formatted = apply_protocol_text_edits(&document.source, &edits)
                .map_err(|error| CliError::fail(format!("format {path}: {error}")))?;
            Ok((formatted != document.source).then_some((document.uri.clone(), edits)))
        })?;
        let Some((uri, edits)) = edits else {
            continue;
        };
        let name = display_workspace_path(&session.root, path);
        if mode.write || mode.diff {
            let edit = WorkspaceEdit {
                changes: BTreeMap::from([(uri, edits)]),
                ..WorkspaceEdit::default()
            };
            // The preview is the diff; a written file is named.
            let mut sink = Vec::new();
            apply_workspace_edit(
                if mode.diff { &mut out } else { &mut sink },
                Some(&edit),
                mode,
            )?;
        }
        if !mode.diff && !runner.json {
            runner.write_text(&format!("{name}\n"))?;
        }
        changed.push(name);
    }
    session.close()?;

    if runner.json {
        runner.write_json(&json!({
            "written": flags.write,
            "files": paths.len(),
            "changed": changed,
        }))?;
    } else {
        let files = |count: usize| if count == 1 { "file" } else { "files" };
        let summary = match (changed.len(), flags.write) {
            (0, _) => format!("{} {} already formatted\n", paths.len(), files(paths.len())),
            (count, true) => format!(
                "Formatted {count} of {} {}\n",
                paths.len(),
                files(paths.len())
            ),
            (count, false) => format!(
                "{count} of {} {} would be reformatted; pass --write to format\n",
                paths.len(),
                files(paths.len())
            ),
        };
        runner.write_err(&summary)?;
    }
    // Without --write a file that is not formatted is a failure, for CI.
    if !flags.write && !changed.is_empty() {
        return Err(CliError::exit(1, ""));
    }
    Ok(())
}
