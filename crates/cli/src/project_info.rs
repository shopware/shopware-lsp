//! The supported-project gate.

use crate::{CliError, Runner};

impl Runner {
    /// Fails unless `root` looks like a Shopware or Symfony project (or
    /// `--allow-unsupported-project` is set).
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
            "unsupported project root {root}: no Shopware or Symfony project markers found; add {} or pass --allow-unsupported-project",
            swlsp_projectconfig::PROJECT_RELATIVE_PATH
        )))
    }
}
