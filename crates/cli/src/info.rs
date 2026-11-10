//! `info`: how the project is detected and configured, and on request
//! what the index holds.

use std::fmt::Write as _;

use serde_json::{Value, json};

use crate::args::Info;
use crate::workspace_commands::stats_report;
use crate::{CliError, Runner};

pub(crate) fn run_info(runner: &Runner, flags: &Info) -> Result<(), CliError> {
    let root = runner.workspace_root()?;
    let project =
        swlsp_projectdetect::detect(&root).map_err(|error| CliError::fail(error.to_string()))?;
    if flags.project_only {
        if runner.json {
            return runner.write_json(&json!({"project": project}));
        }
        return runner.write_text(&project_text(&root, &project));
    }
    let usable = project.supported || runner.allow_unsupported_project;
    // An unsupported project has no session to ask for its configuration.
    let configuration = if usable {
        let session = runner.connect_without_index()?;
        let configuration = session.configuration.raw.clone();
        let _ = session.close();
        Some(configuration)
    } else {
        None
    };
    let stats = if flags.stats {
        Some(stats_report(runner)?)
    } else {
        None
    };
    if runner.json {
        let mut output = json!({"project": project, "configuration": configuration});
        if let Some((stats, _)) = stats {
            output["stats"] = stats;
        }
        return runner.write_json(&output);
    }

    let mut text = project_text(&root, &project);
    text.push_str("\nConfiguration\n");
    match &configuration {
        Some(configuration) => configuration_lines(&mut text, configuration),
        None => {
            let _ = writeln!(
                text,
                "  Not loaded: no Shopware or Symfony project was found here. Add {} or pass \
                 --allow-unsupported-project.",
                swlsp_projectconfig::PROJECT_RELATIVE_PATH
            );
        }
    }
    match stats {
        Some((_, stats)) => {
            text.push_str("\nIndex\n");
            for stat in stats.lines() {
                let _ = writeln!(text, "  {stat}");
            }
        }
        None if usable => {
            text.push_str(
                "\nRun `info --stats` for index statistics, `info --json` for the complete \
                 configuration.\n",
            );
        }
        None => {}
    }
    runner.write_text(&text)
}

fn project_text(root: &str, project: &swlsp_projectdetect::DetectResult) -> String {
    let mut text = String::from("Project\n");
    field(&mut text, "Root", root);
    field(&mut text, "Type", project.kind.as_str());
    let supported = if project.supported { "yes" } else { "no" };
    field(&mut text, "Supported", supported);
    for (index, evidence) in project.evidence.iter().enumerate() {
        let label = if index == 0 { "Evidence" } else { "" };
        let value = format!("{}: {}", evidence.path, evidence.reason);
        field(&mut text, label, &value);
    }
    text
}

/// The settings people look for first; `--json` has all of them.
fn configuration_lines(text: &mut String, configuration: &Value) {
    let mut line = |label: &str, value: &str| field(text, label, value);
    let effective = &configuration["effective"];
    match configuration["path"]
        .as_str()
        .filter(|path| !path.is_empty())
    {
        Some(path) => line("File", path),
        None => line("File", "none (built-in defaults)"),
    }
    let target = effective["shopware"]["targetVersion"]
        .as_str()
        .filter(|version| !version.is_empty());
    line(
        "Shopware",
        &target.map_or_else(
            || "version of the installed Shopware".to_owned(),
            |version| format!("{version} (configured target version)"),
        ),
    );
    line(
        "Check",
        &format!(
            "reports {} and above, {}",
            effective["check"]["severity"].as_str().unwrap_or("warning"),
            match effective["check"]["failOn"].as_str().unwrap_or("off") {
                "off" | "" => "never fails".to_owned(),
                level => format!("fails on {level} and above"),
            }
        ),
    );
    for (label, section) in [("Domains off", "domains"), ("Features off", "features")] {
        let disabled: Vec<&str> = effective[section]
            .as_object()
            .into_iter()
            .flatten()
            .filter(|(_, enabled)| enabled.as_bool() == Some(false))
            .map(|(name, _)| name.as_str())
            .collect();
        if !disabled.is_empty() {
            line(label, &disabled.join(", "));
        }
    }
}

/// One aligned `Label: value` line; a continuation line has no label.
fn field(text: &mut String, label: &str, value: &str) {
    let label = if label.is_empty() {
        String::new()
    } else {
        format!("{label}:")
    };
    let _ = writeln!(text, "  {label:<15} {value}");
}
