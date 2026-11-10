//! The text report of `check`: findings in one of two layouts and a
//! summary.

use std::fmt::Write as _;
use std::time::Duration;

use swlsp_uriutil::filepath;

use crate::CliError;
use crate::session::{DiagnosticOutput, diagnostic_severity_value};
use crate::terminal::{BLUE, BOLD, DIM, GREEN, HEADER, RED, Style, YELLOW, short_duration};
use crate::workspace_commands::{code_text, severity_label};
use swlsp_textutil::number::grouped;

/// `check --format`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Format {
    /// Findings grouped below their file, in aligned columns.
    Pretty,
    /// One `path:line:column: severity: message [code]` line per finding,
    /// which editors and terminals link to the position.
    Concise,
}

impl Format {
    /// The named format; without a name, pretty on a terminal and concise
    /// elsewhere.
    pub(crate) fn parse(value: Option<&str>, terminal: bool) -> Result<Self, CliError> {
        match value.map(str::trim) {
            None | Some("") if terminal => Ok(Self::Pretty),
            None | Some("") => Ok(Self::Concise),
            Some("pretty") => Ok(Self::Pretty),
            Some("concise") => Ok(Self::Concise),
            Some(other) => Err(CliError::usage(format!(
                "unknown check format {other:?}: expected pretty or concise"
            ))),
        }
    }
}

/// Paths as the report shows them: relative to the current directory when
/// below it (so a terminal can open them), otherwise relative to the
/// workspace root, otherwise as they are.
#[derive(Debug, Clone, Default)]
pub(crate) struct DisplayPaths {
    bases: Vec<String>,
}

impl DisplayPaths {
    pub(crate) fn new(root: &str) -> Self {
        let current = std::env::current_dir()
            .ok()
            .map(|path| path.to_string_lossy().into_owned());
        Self::below(current.as_deref(), root)
    }

    pub(crate) fn below(current: Option<&str>, root: &str) -> Self {
        Self {
            bases: current
                .into_iter()
                .chain([root])
                .filter(|base| !base.is_empty())
                .map(str::to_owned)
                .collect(),
        }
    }

    pub(crate) fn display(&self, uri: &str) -> String {
        let path = swlsp_uriutil::path(uri).unwrap_or_else(|_| uri.to_owned());
        let parent = format!("..{}", filepath::SEPARATOR);
        self.bases
            .iter()
            .find_map(|base| {
                filepath::rel(base, &path).ok().filter(|relative| {
                    relative != "." && relative != ".." && !relative.starts_with(&parent)
                })
            })
            .unwrap_or(path)
    }
}

const SEVERITIES: [(&str, &str, &str); 4] = [
    ("error", "errors", RED),
    ("warning", "warnings", YELLOW),
    ("info", "info", BLUE),
    ("hint", "hints", DIM),
];

/// The report of one `check` run, rendered file by file.
pub(crate) struct Report {
    format: Format,
    style: Style,
    paths: DisplayPaths,
    counts: [usize; 4],
    files: usize,
}

impl Report {
    pub(crate) fn new(format: Format, style: Style, paths: DisplayPaths) -> Self {
        Self {
            format,
            style,
            paths,
            counts: [0; 4],
            files: 0,
        }
    }

    /// The text for the findings of one file (empty without findings).
    pub(crate) fn file(&mut self, findings: &[DiagnosticOutput]) -> String {
        let Some(first) = findings.first() else {
            return String::new();
        };
        self.files += 1;
        let mut findings: Vec<&DiagnosticOutput> = findings.iter().collect();
        findings.sort_by_key(|finding| {
            let start = finding.diagnostic.range.start;
            (start.line, start.character)
        });
        for finding in &findings {
            self.counts[severity_index(finding)] += 1;
        }
        let path = self.paths.display(&first.uri);
        match self.format {
            Format::Pretty => self.pretty(&path, &findings),
            Format::Concise => self.concise(&path, &findings),
        }
    }

    fn concise(&self, path: &str, findings: &[&DiagnosticOutput]) -> String {
        let mut text = String::new();
        for finding in findings {
            let diagnostic = &finding.diagnostic;
            let (_, _, color) = SEVERITIES[severity_index(finding)];
            let label = severity_label(diagnostic_severity_value(diagnostic)).to_lowercase();
            let _ = writeln!(
                text,
                "{}:{}:{}: {}: {}{}",
                path,
                diagnostic.range.start.line + 1,
                diagnostic.range.start.character + 1,
                self.style.paint(color, &label),
                diagnostic.message,
                self.style.paint(
                    DIM,
                    &code(finding)
                        .map(|code| format!(" [{code}]"))
                        .unwrap_or_default()
                ),
            );
            for related in finding.related.iter().flatten() {
                let _ = writeln!(
                    text,
                    "  {}:{}:{}: {}",
                    self.paths.display(&related.location.uri),
                    related.location.range.start.line + 1,
                    related.location.range.start.character + 1,
                    related.message
                );
            }
        }
        text
    }

    fn pretty(&self, path: &str, findings: &[&DiagnosticOutput]) -> String {
        /// Longer messages do not push the code column of the others out.
        const MESSAGE_COLUMN: usize = 72;
        let position = |finding: &DiagnosticOutput| {
            let start = finding.diagnostic.range.start;
            format!("{}:{}", start.line + 1, start.character + 1)
        };
        let first_line = |finding: &DiagnosticOutput| -> usize {
            let message = finding.diagnostic.message.trim_end();
            message.lines().next().unwrap_or("").chars().count()
        };
        let position_width = findings
            .iter()
            .map(|finding| position(finding).len())
            .max()
            .unwrap_or(0);
        let message_width = findings
            .iter()
            .filter(|finding| code(finding).is_some())
            .map(|finding| first_line(finding))
            .filter(|width| *width <= MESSAGE_COLUMN)
            .max()
            .unwrap_or(0);
        let label_width = 7;
        let continuation = " ".repeat(2 + position_width + 2 + label_width + 2);
        let mut text = format!("\n{}\n", self.style.paint(HEADER, path));
        for finding in findings {
            let (label, _, color) = SEVERITIES[severity_index(finding)];
            let message = finding.diagnostic.message.trim_end();
            let mut lines = message.lines();
            let head = lines.next().unwrap_or("");
            let _ = write!(
                text,
                "  {}  {}  {head}",
                self.style
                    .paint(DIM, &format!("{:>position_width$}", position(finding))),
                self.style.paint(color, &format!("{label:<label_width$}")),
            );
            if let Some(code) = code(finding) {
                let padding = message_width.saturating_sub(first_line(finding));
                let _ = write!(
                    text,
                    "{}  {}",
                    " ".repeat(padding),
                    self.style.paint(DIM, &code)
                );
            }
            text.push('\n');
            for line in lines {
                let _ = writeln!(text, "{continuation}{line}");
            }
            for related in finding.related.iter().flatten() {
                let line = format!(
                    "↳ {}:{}:{}: {}",
                    self.paths.display(&related.location.uri),
                    related.location.range.start.line + 1,
                    related.location.range.start.character + 1,
                    related.message
                );
                let _ = writeln!(text, "{continuation}{}", self.style.paint(DIM, &line));
            }
        }
        text
    }

    /// Whether anything was reported.
    pub(crate) fn has_findings(&self) -> bool {
        self.files > 0
    }

    /// The closing line: counts per severity, the files they are in, the
    /// files checked and the time it took. `style` is that of the stream
    /// the summary goes to.
    pub(crate) fn summary(&self, style: Style, checked: usize, elapsed: Duration) -> String {
        let files = |count: usize| {
            let noun = if count == 1 { "file" } else { "files" };
            format!("{} {noun}", grouped(count))
        };
        let took = style.paint(DIM, &format!("({})", short_duration(elapsed)));
        let pretty = self.format == Format::Pretty;
        if !self.has_findings() {
            let text = format!("No problems in {}", files(checked));
            return if pretty {
                format!(
                    "{} {} {took}\n",
                    style.paint(GREEN, "✓"),
                    style.paint(BOLD, &text)
                )
            } else {
                format!("{text} {took}\n")
            };
        }
        let mut worst = None;
        let counts: Vec<String> = SEVERITIES
            .iter()
            .zip(self.counts)
            .filter(|(_, count)| *count > 0)
            .map(|((singular, plural, color), count)| {
                worst.get_or_insert(*color);
                let noun = if count == 1 { singular } else { plural };
                style.paint(color, &format!("{} {noun}", grouped(count)))
            })
            .collect();
        let text = format!(
            "{} in {} of {} {took}\n",
            counts.join(", "),
            grouped(self.files),
            files(checked)
        );
        if pretty {
            format!("\n{} {text}", style.paint(worst.unwrap_or(RED), "✖"))
        } else {
            format!("Found {text}")
        }
    }
}

/// The index into [`SEVERITIES`]; a missing severity reads as a warning.
fn severity_index(finding: &DiagnosticOutput) -> usize {
    (diagnostic_severity_value(&finding.diagnostic).clamp(1, 4) - 1) as usize
}

fn code(finding: &DiagnosticOutput) -> Option<String> {
    match finding.diagnostic.code.as_ref() {
        None | Some(serde_json::Value::Null) => None,
        Some(code) => Some(code_text(code)),
    }
}
