//! `project-info` and the supported-project gate (Go `project_info.go`).

use crate::{CliError, Runner};

pub(crate) fn run_project_info(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    if !args.is_empty() {
        return Err(CliError::usage("project-info takes no arguments"));
    }
    let root = runner.workspace_root()?;
    let result =
        swlsp_projectdetect::detect(&root).map_err(|error| CliError::fail(error.to_string()))?;
    if runner.json {
        return runner.write_json(&result);
    }
    let mut lines = vec![format!("Project type: {}", result.kind.as_str())];
    if result.evidence.is_empty() {
        lines.push("Supported: no".into());
        lines.push("Evidence: none".into());
    } else {
        lines.push("Supported: yes".into());
        lines.push("Evidence:".into());
        for evidence in &result.evidence {
            lines.push(format!("  {}: {}", evidence.path, evidence.reason));
        }
    }
    runner.write_text(&format!("{}\n", lines.join("\n")))
}

impl Runner {
    /// Go `requireSupportedProject`.
    pub(crate) fn require_supported_project(&self, root: &str) -> Result<(), CliError> {
        if self.allow_unsupported_project {
            return Ok(());
        }
        let result = swlsp_projectdetect::detect(root)
            .map_err(|error| CliError::fail(format!("detect project type: {error}")))?;
        if result.supported {
            return Ok(());
        }
        Err(CliError::fail(format!(
            "unsupported project root {root}: no Shopware or Symfony project markers found; add {} or pass -allow-unsupported-project",
            swlsp_projectconfig::PROJECT_RELATIVE_PATH
        )))
    }
}
