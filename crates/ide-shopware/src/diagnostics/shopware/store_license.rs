//! SPDX license expressions, as Composer accepts them in `license`.

use std::collections::HashSet;
use std::sync::LazyLock;

/// The SPDX license identifiers, lowercased. Generated from the license list
/// of `composer/spdx-licenses`.
static LICENSES: LazyLock<HashSet<String>> = LazyLock::new(|| {
    include_str!("spdx_licenses.txt")
        .lines()
        .filter(|line| !line.is_empty())
        .map(str::to_ascii_lowercase)
        .collect()
});

/// Whether `expression` is `proprietary` or an SPDX license expression:
/// identifiers (optionally ending in `+`) joined by `AND` and `OR`, with
/// `WITH <exception>` and parentheses. Exception names are not checked.
pub(crate) fn is_valid_license(expression: &str) -> bool {
    let expression = expression.trim();
    if expression.eq_ignore_ascii_case("proprietary") {
        return true;
    }
    let spaced = expression.replace(['(', ')'], " ");
    let mut expects_license = true;
    let mut after_with = false;
    for token in spaced.split_whitespace() {
        let operator = ["and", "or", "with"]
            .into_iter()
            .find(|operator| token.eq_ignore_ascii_case(operator));
        match operator {
            Some(operator) => {
                if expects_license {
                    return false;
                }
                expects_license = true;
                after_with = operator == "with";
            }
            None => {
                if !expects_license || !(after_with || is_license_id(token)) {
                    return false;
                }
                expects_license = false;
                after_with = false;
            }
        }
    }
    !expects_license
}

fn is_license_id(token: &str) -> bool {
    let id = token
        .strip_suffix('+')
        .unwrap_or(token)
        .to_ascii_lowercase();
    id.starts_with("licenseref-") || LICENSES.contains(&id)
}

#[cfg(test)]
mod tests {
    use super::is_valid_license;

    #[test]
    fn license_expressions() {
        for valid in [
            "MIT",
            "mit",
            "proprietary",
            "Proprietary",
            "GPL-2.0+",
            "(MIT OR Apache-2.0)",
            "MIT and BSD-3-Clause",
            "GPL-2.0-or-later WITH Classpath-exception-2.0",
            "LicenseRef-Acme",
        ] {
            assert!(is_valid_license(valid), "{valid}");
        }
        for invalid in [
            "",
            "Custom",
            "MIT OR",
            "OR MIT",
            "MIT Apache-2.0",
            "MIT, Apache-2.0",
        ] {
            assert!(!is_valid_license(invalid), "{invalid}");
        }
    }
}
