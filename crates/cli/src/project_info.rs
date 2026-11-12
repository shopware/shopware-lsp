//! The supported-project gate.

use crate::{CliError, Runner};

impl Runner {
    /// Fails unless `root` looks like a Shopware or Symfony project (or
    /// `--allow-unsupported-project` is set).
    pub(crate) fn require_supported_project(&self, root: &str) -> Result<(), CliError> {
        match self.unsupported_project(root)? {
            Some(reason) => Err(CliError::fail(reason)),
            None => Ok(()),
        }
    }

    /// Why `root` is not served: it has no Shopware or Symfony markers and
    /// `--allow-unsupported-project` is not set. `None` for a supported
    /// root. A detection that fails is an error, not an unsupported
    /// project.
    pub(crate) fn unsupported_project(&self, root: &str) -> Result<Option<String>, CliError> {
        if self.allow_unsupported_project {
            return Ok(None);
        }
        let result = swlsp_projectdetect::detect(root)
            .map_err(|error| CliError::fail(format!("detect project type: {error}")))?;
        if result.supported {
            return Ok(None);
        }
        Ok(Some(format!(
            "unsupported project root {root}: no Shopware or Symfony project markers found; add {} or pass --allow-unsupported-project",
            swlsp_projectconfig::PROJECT_RELATIVE_PATH
        )))
    }
}
