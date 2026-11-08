//! The `shopware-lsp` command line (port of Go `internal/cli`): the stdio
//! language server, the MCP server, workspace commands (`index`, `stats`,
//! `check`), read-only feature commands and previewing refactors.
//!
//! Every workspace command drives the production LSP session (an in-process
//! [`swlsp_app::Application`] spoken to over JSON-RPC), so the editor, CLI
//! and MCP share one analysis path. CLI and MCP positions are one-based and
//! converted at this boundary.

mod config_order;
mod difflib;
mod edit;
mod features_read;
mod features_write;
pub mod goflag;
pub mod gofmt;
pub mod gojson;
mod logger;
mod mcp;
pub mod ordered;
mod project_info;
mod serve;
mod session;
mod target;
mod workspace_commands;

use std::io::{self, Read, Write};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

use serde::Serialize;

use crate::goflag::FlagSet;
use crate::gojson::write_json;
use crate::ordered::Node;

pub use edit::{EditMode, apply_protocol_text_edits, apply_workspace_edit};
pub use logger::{LogSink, install_logger, set_log_sink};
pub use session::{CliDocument, CliSession, SessionOptions};
pub use target::{PositionTarget, code_action_selection, parse_position_target};
pub use workspace_commands::resolve_check_files_with_exclusions;

/// Process options (Go `cli.Options`).
#[derive(Debug, Clone, Default)]
pub struct Options {
    pub version: String,
    pub license: String,
    pub third_party_notices: String,
}

/// A cancellation flag shared by a command and its session (Go context).
#[derive(Debug, Clone, Default)]
pub struct Cancel(Arc<AtomicBool>);

impl Cancel {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn cancel(&self) {
        self.0.store(true, Ordering::Release);
    }

    pub fn is_cancelled(&self) -> bool {
        self.0.load(Ordering::Acquire)
    }
}

/// A command failure. `exit` marks Go's `*exitError`, which `Run` returns
/// unwrapped; other errors are prefixed with the command name.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CliError {
    pub code: i32,
    pub message: String,
    pub exit: bool,
    pub canceled: bool,
}

impl CliError {
    pub fn fail(message: impl Into<String>) -> Self {
        Self {
            code: 1,
            message: message.into(),
            exit: false,
            canceled: false,
        }
    }

    pub fn usage(message: impl Into<String>) -> Self {
        Self {
            code: 2,
            message: message.into(),
            exit: true,
            canceled: false,
        }
    }

    pub fn exit(code: i32, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            exit: true,
            canceled: false,
        }
    }

    pub fn canceled() -> Self {
        Self {
            code: 1,
            message: "context canceled".into(),
            exit: false,
            canceled: true,
        }
    }

    fn wrap(self, prefix: &str) -> Self {
        Self {
            message: format!("{prefix}: {}", self.message),
            ..self
        }
    }
}

impl std::fmt::Display for CliError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for CliError {}

impl From<io::Error> for CliError {
    fn from(error: io::Error) -> Self {
        CliError::fail(error.to_string())
    }
}

/// The process exit code for a `run` result (Go `ExitCode`).
pub fn exit_code(result: &Result<(), CliError>) -> i32 {
    match result {
        Ok(()) => 0,
        Err(error) => error.code,
    }
}

/// A cloneable, thread-safe writer (stdout, stderr or a test buffer).
#[derive(Clone)]
pub struct Output(Arc<Mutex<Box<dyn Write + Send>>>);

impl Output {
    pub fn new(writer: impl Write + Send + 'static) -> Self {
        Self(Arc::new(Mutex::new(Box::new(writer))))
    }

    pub fn stdout() -> Self {
        Self::new(io::stdout())
    }

    pub fn stderr() -> Self {
        Self::new(io::stderr())
    }
}

impl Write for Output {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        let mut writer = self
            .0
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        writer.write_all(buf)?;
        writer.flush()?;
        Ok(buf.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        self.0
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .flush()
    }
}

/// An in-memory [`Output`] for tests.
#[derive(Clone, Default)]
pub struct Buffer(Arc<Mutex<Vec<u8>>>);

impl Buffer {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn output(&self) -> Output {
        Output::new(self.clone())
    }

    pub fn contents(&self) -> String {
        String::from_utf8_lossy(&self.0.lock().unwrap_or_else(|p| p.into_inner())).into_owned()
    }
}

impl Write for Buffer {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.0
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .extend_from_slice(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

pub type Input = Box<dyn Read + Send>;

type CommandFn = fn(&Runner, &[String]) -> Result<(), CliError>;

#[derive(Clone, Serialize)]
pub struct CommandDefinition {
    pub name: &'static str,
    pub summary: &'static str,
    pub usage: &'static str,
    #[serde(skip)]
    run: CommandFn,
}

/// The command-line runner (Go `cli.Runner`).
pub struct Runner {
    options: Options,
    pub(crate) root: String,
    pub(crate) json: bool,
    pub(crate) verbose: bool,
    pub(crate) allow_unsupported_project: bool,
    pub(crate) cancel: Cancel,
    input: Mutex<Option<Input>>,
    pub(crate) out: Output,
    pub(crate) err_out: Output,
}

impl Runner {
    pub fn new(mut options: Options) -> Self {
        if options.version.is_empty() {
            options.version = "dev".into();
        }
        Self {
            options,
            root: String::new(),
            json: false,
            verbose: false,
            allow_unsupported_project: false,
            cancel: Cancel::new(),
            input: Mutex::new(None),
            out: Output::new(io::sink()),
            err_out: Output::new(io::sink()),
        }
    }

    pub fn version(&self) -> &str {
        &self.options.version
    }

    /// Takes the input stream (once).
    pub(crate) fn take_input(&self) -> Input {
        self.input
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .take()
            .unwrap_or_else(|| Box::new(io::empty()))
    }

    /// Runs one command line (Go `Runner.Run`).
    pub fn run(
        &mut self,
        cancel: &Cancel,
        args: &[String],
        input: Input,
        out: Output,
        err_out: Output,
    ) -> Result<(), CliError> {
        self.cancel = cancel.clone();
        *self.input.lock().unwrap_or_else(|p| p.into_inner()) = Some(input);
        self.out = out;
        self.err_out = err_out.clone();
        install_logger();
        set_log_sink(LogSink::Writer(err_out.clone()));

        let mut global = FlagSet::new("shopware-lsp");
        global.string(
            "root",
            "",
            "workspace root (defaults to the current directory)",
        );
        global.bool("json", false, "emit machine-readable JSON where supported");
        global.bool("v", false, "verbose progress output");
        global.bool(
            "allow-unsupported-project",
            false,
            "allow workspace commands outside detected Shopware or Symfony projects",
        );
        // Conventional stdio-server flags appended by language clients.
        global.bool("stdio", false, "use standard input/output transport");
        global.int("clientProcessId", 0, "parent language-client process ID");
        global.bool("h", false, "show help");
        global.bool("help", false, "show help");
        global.string("profile.cpu", "", "write a CPU profile to this file");
        global.string(
            "profile.mem",
            "",
            "create this file (heap profiles need an external tool such as heaptrack)",
        );
        global.string(
            "profile.trace",
            "",
            "create this file (Go runtime traces have no Rust equivalent)",
        );
        let mut err_writer = err_out.clone();
        if let Err(error) = global.parse(args, &mut err_writer) {
            return Err(CliError::usage(error.0));
        }
        self.root = global.get_string("root");
        self.json = global.get_bool("json");
        self.verbose = global.get_bool("v");
        self.allow_unsupported_project = global.get_bool("allow-unsupported-project");
        let show_help = global.get_bool("h") || global.get_bool("help");
        let profiles = Profiles::start(
            &global.get_string("profile.cpu"),
            &global.get_string("profile.trace"),
            &global.get_string("profile.mem"),
            &mut err_writer,
        )?;

        let mut rest: Vec<String> = global.args().to_vec();
        let mut name = "serve".to_owned();
        if !rest.is_empty() {
            name = rest.remove(0);
        }
        if show_help {
            name = "help".into();
            rest.clear();
        }
        if name == "-h" || name == "--help" {
            name = "help".into();
        }
        let result = self.dispatch(&name, &rest);
        match profiles.stop() {
            Ok(()) => result,
            Err(profile_error) => {
                let mut err_out = self.err_out.clone();
                let _ = writeln!(err_out, "profile: {profile_error}");
                let message = format!("write profile: {profile_error}");
                Err(match result {
                    Ok(()) => CliError::fail(message),
                    Err(error) => CliError {
                        message: format!("{}\n{message}", error.message),
                        ..error
                    },
                })
            }
        }
    }

    fn dispatch(&self, name: &str, rest: &[String]) -> Result<(), CliError> {
        let commands = commands();
        let Some(command) = commands.iter().find(|command| command.name == name) else {
            return Err(CliError::usage(format!(
                "unknown command {}; run 'shopware-lsp help'",
                gofmt::go_quote(name)
            )));
        };
        if name != "serve" && !self.verbose {
            set_log_sink(LogSink::Discard);
        }
        match (command.run)(self, rest) {
            Ok(()) => Ok(()),
            Err(error) if error.exit => Err(error),
            Err(error) => Err(error.wrap(name)),
        }
    }

    pub(crate) fn write_json<T: Serialize + ?Sized>(&self, value: &T) -> Result<(), CliError> {
        let mut out = self.out.clone();
        write_json(&mut out, value).map_err(CliError::from)
    }

    pub(crate) fn write_text(&self, text: &str) -> Result<(), CliError> {
        let mut out = self.out.clone();
        out.write_all(text.as_bytes()).map_err(CliError::from)
    }

    pub(crate) fn write_err(&self, text: &str) -> Result<(), CliError> {
        let mut out = self.err_out.clone();
        out.write_all(text.as_bytes()).map_err(CliError::from)
    }

    /// The absolute, cleaned workspace root (Go `workspaceRoot`).
    pub(crate) fn workspace_root(&self) -> Result<String, CliError> {
        let mut root = self.root.clone();
        if root.is_empty() {
            root = std::env::current_dir()
                .map_err(|error| CliError::fail(format!("current directory: {error}")))?
                .to_string_lossy()
                .into_owned();
        }
        let absolute = swlsp_uriutil::filepath::abs(&root)
            .map_err(|error| CliError::fail(format!("resolve workspace root: {error}")))?;
        let metadata = std::fs::metadata(&absolute).map_err(|error| {
            CliError::fail(format!(
                "workspace root: {}",
                gofmt::path_error("stat", &absolute, &error)
            ))
        })?;
        if !metadata.is_dir() {
            return Err(CliError::fail(format!(
                "workspace root is not a directory: {absolute}"
            )));
        }
        Ok(swlsp_uriutil::filepath::clean(&absolute))
    }

    pub(crate) fn session_options(&self, root: &str, start_index: bool) -> SessionOptions {
        SessionOptions {
            root: root.to_owned(),
            version: self.options.version.clone(),
            err_out: Some(self.err_out.clone()),
            start_index,
            allow_unsupported_project: self.allow_unsupported_project,
            watch_files: false,
            editor_configuration: None,
        }
    }

    /// Opens an indexed production session (Go `connect`).
    pub(crate) fn connect(&self) -> Result<CliSession, CliError> {
        let root = self.workspace_root()?;
        self.require_supported_project(&root)?;
        if self.verbose {
            self.write_err(&format!("Initializing workspace {root}...\n"))?;
        }
        let mut session = CliSession::new(&self.cancel, self.session_options(&root, true))?;
        let started = std::time::Instant::now();
        let result = session.wait_for_index()?;
        session.initial_index = result;
        if self.verbose {
            let mut nanos = (result.time_in_seconds * 1e9) as i64;
            if nanos <= 0 {
                nanos = started.elapsed().as_nanos() as i64;
            }
            self.write_err(&format!(
                "Workspace ready in {}\n",
                gofmt::format_duration(gofmt::round_duration(nanos, 1_000_000))
            ))?;
        }
        Ok(session)
    }

    /// Opens a session without indexing (Go `connectWithoutIndex`).
    pub(crate) fn connect_without_index(&self) -> Result<CliSession, CliError> {
        let root = self.workspace_root()?;
        self.require_supported_project(&root)?;
        CliSession::new(&self.cancel, self.session_options(&root, false))
    }
}

pub fn commands() -> Vec<CommandDefinition> {
    macro_rules! command {
        ($name:expr, $summary:expr, $usage:expr, $run:expr) => {
            CommandDefinition {
                name: $name,
                summary: $summary,
                usage: $usage,
                run: $run,
            }
        };
    }
    vec![
        command!(
            "serve",
            "run the Shopware language server",
            "serve [server-flags]",
            serve::run_serve
        ),
        command!("mcp", "run the Shopware MCP server", "mcp", mcp::run_mcp),
        command!(
            "version",
            "print version information",
            "version",
            run_version
        ),
        command!("help", "print command usage", "help [command]", run_help),
        command!(
            "api-json",
            "print the supported CLI and language API",
            "api-json",
            run_api_json
        ),
        command!(
            "licenses",
            "print license and third-party notices",
            "licenses",
            run_licenses
        ),
        command!(
            "config",
            "validate and print the effective project configuration",
            "config",
            run_config
        ),
        command!(
            "project-info",
            "detect the workspace project type",
            "project-info",
            project_info::run_project_info
        ),
        command!(
            "index",
            "build or refresh the workspace index",
            "index [-force]",
            workspace_commands::run_index
        ),
        command!(
            "stats",
            "print indexing, workspace, cache, and memory statistics",
            "stats",
            workspace_commands::run_stats
        ),
        command!(
            "check",
            "show diagnostics for files and directories",
            "check [-severity level] [-fail-on level] [-workers count] <file-or-directory>...",
            workspace_commands::run_check
        ),
        command!(
            "completion",
            "show completion items at a position",
            "completion <file[:line[:column]]>",
            features_read::run_completion
        ),
        command!(
            "definition",
            "show definitions at a position",
            "definition <file[:line[:column]]>",
            features_read::run_definition
        ),
        command!(
            "implementation",
            "show implementations at a position",
            "implementation <file[:line[:column]]>",
            features_read::run_implementation
        ),
        command!(
            "references",
            "show references at a position",
            "references <file[:line[:column]]>",
            features_read::run_references
        ),
        command!(
            "hover",
            "show hover information at a position",
            "hover <file[:line[:column]]>",
            features_read::run_hover
        ),
        command!(
            "signature",
            "show signature help at a position",
            "signature <file[:line[:column]]>",
            features_read::run_signature
        ),
        command!(
            "call-hierarchy",
            "show incoming and outgoing calls",
            "call-hierarchy <file[:line[:column]]>",
            features_read::run_call_hierarchy
        ),
        command!(
            "type-hierarchy",
            "show supertypes and subtypes",
            "type-hierarchy <file[:line[:column]]>",
            features_read::run_type_hierarchy
        ),
        command!(
            "symbols",
            "show a document outline",
            "symbols <file>",
            features_read::run_symbols
        ),
        command!(
            "workspace-symbol",
            "search workspace symbols",
            "workspace-symbol [--fresh] <query>",
            features_read::run_workspace_symbols
        ),
        command!(
            "highlights",
            "show document highlights at a position",
            "highlights <file[:line[:column]]>",
            features_read::run_highlights
        ),
        command!(
            "folding-ranges",
            "show folding ranges for a document",
            "folding-ranges <file>",
            features_read::run_folding_ranges
        ),
        command!(
            "links",
            "show document links",
            "links <file>",
            features_read::run_links
        ),
        command!(
            "semtok",
            "show semantic tokens",
            "semtok <file>",
            features_read::run_semantic_tokens
        ),
        command!(
            "inlay-hints",
            "show inlay hints",
            "inlay-hints <file>",
            features_read::run_inlay_hints
        ),
        command!(
            "codelens",
            "show code lenses",
            "codelens <file>",
            features_read::run_code_lens
        ),
        command!(
            "selection-ranges",
            "show expandable selection ranges",
            "selection-ranges <file[:line[:column]]>",
            features_read::run_selection_ranges
        ),
        command!(
            "linked-editing",
            "show linked editing ranges",
            "linked-editing <file[:line[:column]]>",
            features_read::run_linked_editing
        ),
        command!(
            "colors",
            "show document colors",
            "colors <file>",
            features_read::run_colors
        ),
        command!(
            "codeaction",
            "list or apply code actions",
            "codeaction [-kind kind] [-title regexp] [-exec] [-w|-d] <position>",
            features_write::run_code_action
        ),
        command!(
            "rename",
            "preview or apply a rename",
            "rename [-w|-d] <position> <new-name>",
            features_write::run_rename
        ),
        command!(
            "execute",
            "execute a Shopware custom command",
            "execute <method> [json-arguments]",
            features_write::run_execute
        ),
        command!(
            "request",
            "send an arbitrary LSP request",
            "request <method> [json-params]",
            features_write::run_request
        ),
    ]
}

fn sorted_commands() -> Vec<CommandDefinition> {
    let mut commands = commands();
    commands.sort_by(|left, right| left.name.cmp(right.name));
    commands
}

fn run_config(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    if !args.is_empty() {
        return Err(CliError::usage("config takes no arguments"));
    }
    let session = runner.connect_without_index()?;
    let configuration = session.configuration.clone();
    let _ = session.close();
    if runner.json {
        return runner.write_json(&configuration.raw);
    }
    // Go writes a map, so the keys come out sorted.
    runner.write_json(&Node::Object(vec![
        ("effective".into(), configuration.effective),
        ("path".into(), configuration.path),
        ("scopes".into(), configuration.scopes),
    ]))
}

fn run_help(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    let commands = sorted_commands();
    if let Some(topic) = args.first() {
        let Some(command) = commands.iter().find(|command| command.name == topic) else {
            return Err(CliError::exit(
                2,
                format!("unknown help topic {}", gofmt::go_quote(topic)),
            ));
        };
        return runner.write_text(&format!(
            "{}\n\nUsage:\n  shopware-lsp [global-flags] {}\n",
            command.summary, command.usage
        ));
    }
    let mut lines: Vec<String> = vec![
        "Shopware LSP provides editor features and command-line project analysis.".into(),
        String::new(),
        "Usage:".into(),
        "  shopware-lsp [global-flags] [command] [command-flags]".into(),
        String::new(),
        "Commands:".into(),
    ];
    for command in &commands {
        lines.push(format!("  {:<18} {}", command.name, command.summary));
    }
    lines.extend(
        [
            "",
            "Global flags:",
            "  -root PATH          workspace root (default: current directory)",
            "  -json               machine-readable output",
            "  -v                  verbose progress output",
            "  -allow-unsupported-project",
            "                       allow workspace commands for an unknown project type",
            "  -profile.cpu FILE   write CPU profile",
            "  -profile.mem FILE   write heap profile",
            "  -profile.trace FILE write runtime trace",
            "",
            "With no command, the stdio language server is started.",
        ]
        .map(str::to_owned),
    );
    runner.write_text(&format!("{}\n", lines.join("\n")))
}

fn run_version(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    if !args.is_empty() {
        return Err(CliError::usage("version takes no arguments"));
    }
    if runner.json {
        // Go map[string]string: sorted keys.
        return runner.write_json(&serde_json::json!({
            "name": "shopware-lsp",
            "version": runner.options.version,
        }));
    }
    runner.write_text(&format!("shopware-lsp {}\n", runner.options.version))
}

const LSP_METHODS: &[&str] = &[
    "callHierarchy/incomingCalls",
    "callHierarchy/outgoingCalls",
    "codeAction/resolve",
    "codeLens/resolve",
    "textDocument/codeAction",
    "textDocument/codeLens",
    "textDocument/colorPresentation",
    "textDocument/completion",
    "textDocument/definition",
    "textDocument/diagnostic",
    "textDocument/documentColor",
    "textDocument/documentHighlight",
    "textDocument/documentLink",
    "textDocument/documentSymbol",
    "textDocument/foldingRange",
    "textDocument/hover",
    "textDocument/implementation",
    "textDocument/inlayHint",
    "textDocument/linkedEditingRange",
    "textDocument/prepareCallHierarchy",
    "textDocument/prepareTypeHierarchy",
    "textDocument/references",
    "textDocument/rename",
    "textDocument/selectionRange",
    "textDocument/semanticTokens/full",
    "textDocument/signatureHelp",
    "typeHierarchy/subtypes",
    "typeHierarchy/supertypes",
    "workspace/symbol",
    "workspace/willRenameFiles",
    "shopware/configuration/catalog",
    "shopware/configuration/effective",
    "shopware/configuration/reload",
    "workspace/didChangeConfiguration",
];

fn run_api_json(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    if !args.is_empty() {
        return Err(CliError::usage("api-json takes no arguments"));
    }
    let mut languages = std::collections::BTreeMap::<String, Vec<String>>::new();
    let mut extensions: Vec<(String, swlsp_language::LanguageId)> = Vec::new();
    for language in swlsp_language::LanguageId::ALL {
        for extension in language.extensions() {
            extensions.push((format!(".{extension}"), language));
        }
    }
    extensions.sort();
    for (extension, language) in extensions {
        languages
            .entry(language.as_str().to_owned())
            .or_default()
            .push(extension);
    }
    #[derive(Serialize)]
    struct Api<'a> {
        #[serde(rename = "commands")]
        commands: Vec<CommandDefinition>,
        #[serde(rename = "customCommandDiscovery")]
        custom_command_discovery: &'a str,
        languages: std::collections::BTreeMap<String, Vec<String>>,
        #[serde(rename = "lspMethods")]
        lsp_methods: &'a [&'a str],
        name: &'a str,
        version: &'a str,
    }
    runner.write_json(&Api {
        commands: sorted_commands(),
        custom_command_discovery: "shopware-lsp execute",
        languages,
        lsp_methods: LSP_METHODS,
        name: "shopware-lsp",
        version: &runner.options.version,
    })
}

fn run_licenses(runner: &Runner, args: &[String]) -> Result<(), CliError> {
    if !args.is_empty() {
        return Err(CliError::usage("licenses takes no arguments"));
    }
    let mut output = format!("{}\n", runner.options.license.trim());
    if !runner.options.third_party_notices.trim().is_empty() {
        output.push_str("\n--- Third-party notices ---\n\n");
        output.push_str(runner.options.third_party_notices.trim());
        output.push('\n');
    }
    runner.write_text(&output)
}

/// Profile flags. Go writes pprof CPU/heap profiles and runtime traces.
/// With the `cpu-profile` feature on Unix, `-profile.cpu` samples the process
/// with `SIGPROF` and writes a pprof protobuf; otherwise, and for the heap and
/// trace flags, the files are created (reporting the same creation errors)
/// and left empty.
struct Profiles {
    memory: String,
    #[cfg(all(unix, feature = "cpu-profile"))]
    cpu: Option<(String, pprof::ProfilerGuard<'static>)>,
}

impl Profiles {
    fn start(
        cpu: &str,
        trace: &str,
        memory: &str,
        err_out: &mut dyn Write,
    ) -> Result<Self, CliError> {
        // The Go oracle wrote pprof heap profiles and runtime traces. The Rust
        // server keeps the flags (and creates the files, so scripts relying on
        // them keep working) but says plainly that they stay empty.
        if !memory.is_empty() {
            let _ = writeln!(
                err_out,
                "-profile.mem: heap profiles are not supported; {memory} stays empty (use heaptrack or valgrind --tool=massif)"
            );
        }
        if !trace.is_empty() {
            let _ = writeln!(
                err_out,
                "-profile.trace: runtime traces are not supported; {trace} stays empty"
            );
        }
        #[cfg(not(all(unix, feature = "cpu-profile")))]
        if !cpu.is_empty() {
            let _ = writeln!(
                err_out,
                "-profile.cpu: this build has no CPU profiler; {cpu} stays empty (build with --features cpu-profile)"
            );
        }
        if !cpu.is_empty() {
            std::fs::File::create(cpu).map_err(|error| {
                CliError::fail(format!(
                    "create CPU profile: {}",
                    gofmt::path_error("open", cpu, &error)
                ))
            })?;
        }
        if !trace.is_empty() {
            std::fs::File::create(trace).map_err(|error| {
                CliError::fail(format!(
                    "create trace: {}",
                    gofmt::path_error("open", trace, &error)
                ))
            })?;
        }
        #[cfg(all(unix, feature = "cpu-profile"))]
        let cpu = if cpu.is_empty() {
            None
        } else {
            let guard = pprof::ProfilerGuardBuilder::default()
                .frequency(
                    std::env::var("SHOPWARE_LSP_PROFILE_HZ")
                        .ok()
                        .and_then(|hz| hz.parse().ok())
                        .unwrap_or(250),
                )
                .blocklist(&["libc", "libgcc", "pthread", "vdso"])
                .build()
                .map_err(|error| CliError::fail(format!("start CPU profile: {error}")))?;
            Some((cpu.to_owned(), guard))
        };
        Ok(Self {
            memory: memory.to_owned(),
            #[cfg(all(unix, feature = "cpu-profile"))]
            cpu,
        })
    }

    fn stop(self) -> Result<(), String> {
        #[cfg(all(unix, feature = "cpu-profile"))]
        if let Some((path, guard)) = self.cpu {
            use pprof::protos::Message;
            let profile = guard
                .report()
                .build()
                .and_then(|report| report.pprof())
                .map_err(|error| format!("write CPU profile: {error}"))?;
            std::fs::write(&path, profile.encode_to_vec())
                .map_err(|error| gofmt::path_error("open", &path, &error))?;
        }
        if self.memory.is_empty() {
            return Ok(());
        }
        std::fs::File::create(&self.memory)
            .map(|_| ())
            .map_err(|error| gofmt::path_error("open", &self.memory, &error))
    }
}

#[cfg(test)]
mod tests;
