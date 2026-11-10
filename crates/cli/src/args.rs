//! The command-line interface, declared with `usage-rs`.
//!
//! GNU-style flags (`--root DIR`, `--json`, `-v`); global flags are accepted
//! before or after the command. Help (`-h`, `--help`, `help <command>`) and
//! parse errors are generated from these declarations; [`crate::Runner`]
//! routes them to its writers. With no command the stdio language server
//! starts, which is how editors launch it (they append `--stdio` and
//! `--clientProcessId=<pid>`).

use usage_rs::{Args, Cli, Subcommands};

/// Shopware LSP provides editor features and command-line project analysis.
#[derive(Debug, Cli)]
#[usage(
    bin = "shopware-lsp",
    after_help = "With no command, the stdio language server is started."
)]
pub(crate) struct ShopwareLsp {
    /// Workspace root (defaults to the current directory)
    #[usage(long, global, value_name = "DIR")]
    pub root: Option<String>,
    /// Emit machine-readable JSON where supported
    #[usage(long, global)]
    pub json: bool,
    /// Verbose progress output
    #[usage(short = 'v', long, global)]
    pub verbose: bool,
    /// Color output: auto (terminals only), always, never
    #[usage(long, global, value_name = "WHEN")]
    pub color: Option<String>,
    /// Allow workspace commands outside detected Shopware or Symfony projects
    #[usage(long, global)]
    pub allow_unsupported_project: bool,
    /// Write a CPU profile (pprof) to this file; needs the cpu-profile build feature
    #[usage(long, global, value_name = "FILE", help_heading = "Profiling")]
    pub profile_cpu: Option<String>,
    /// Create this file; heap profiles need an external tool such as heaptrack
    #[usage(long, global, value_name = "FILE", help_heading = "Profiling")]
    pub profile_mem: Option<String>,
    /// Create this file; runtime traces have no Rust equivalent
    #[usage(long, global, value_name = "FILE", help_heading = "Profiling")]
    pub profile_trace: Option<String>,
    /// Use standard input/output transport (appended by language clients)
    #[usage(long, global, hide)]
    pub stdio: bool,
    /// Parent language-client process ID (appended by language clients)
    #[usage(long = "clientProcessId", global, hide, value_name = "PID")]
    pub client_process_id: Option<i64>,
    /// Print version information
    #[usage(short = 'V', long = "version")]
    pub version: bool,
    #[usage(subcommand)]
    pub command: Option<Command>,
}

#[derive(Debug, Subcommands)]
pub(crate) enum Command {
    /// Run the Shopware language server (the default)
    #[usage(name = "serve")]
    Serve(Serve),
    /// Run the Shopware MCP server
    #[usage(name = "mcp")]
    Mcp,
    /// Print version information
    #[usage(name = "version")]
    Version,
    /// Print the supported CLI and language API
    #[usage(name = "api-json")]
    ApiJson,
    /// Print license and third-party notices
    #[usage(name = "licenses")]
    Licenses,
    /// Validate and print the effective project configuration
    #[usage(name = "config")]
    Config,
    /// Detect the workspace project type
    #[usage(name = "project-info")]
    ProjectInfo,
    /// Build or refresh the workspace index
    #[usage(name = "index")]
    Index(Index),
    /// Print indexing, workspace, cache, and memory statistics
    #[usage(name = "stats")]
    Stats,
    /// Show diagnostics for files and directories
    #[usage(name = "check")]
    Check(Check),
    /// Show completion items at a position
    #[usage(name = "completion")]
    Completion(At),
    /// Show definitions at a position
    #[usage(name = "definition")]
    Definition(At),
    /// Show implementations at a position
    #[usage(name = "implementation")]
    Implementation(At),
    /// Show references at a position
    #[usage(name = "references")]
    References(At),
    /// Show hover information at a position
    #[usage(name = "hover")]
    Hover(At),
    /// Show signature help at a position
    #[usage(name = "signature")]
    Signature(At),
    /// Show incoming and outgoing calls
    #[usage(name = "call-hierarchy")]
    CallHierarchy(At),
    /// Show supertypes and subtypes
    #[usage(name = "type-hierarchy")]
    TypeHierarchy(At),
    /// Show a document outline
    #[usage(name = "symbols")]
    Symbols(File),
    /// Search workspace symbols
    #[usage(name = "workspace-symbol")]
    WorkspaceSymbol(WorkspaceSymbol),
    /// Show document highlights at a position
    #[usage(name = "highlights")]
    Highlights(At),
    /// Show folding ranges for a document
    #[usage(name = "folding-ranges")]
    FoldingRanges(File),
    /// Show document links
    #[usage(name = "links")]
    Links(File),
    /// Show semantic tokens
    #[usage(name = "semtok")]
    Semtok(File),
    /// Show inlay hints
    #[usage(name = "inlay-hints")]
    InlayHints(File),
    /// Show code lenses
    #[usage(name = "codelens")]
    Codelens(File),
    /// Show expandable selection ranges
    #[usage(name = "selection-ranges")]
    SelectionRanges(At),
    /// Show linked editing ranges
    #[usage(name = "linked-editing")]
    LinkedEditing(At),
    /// Show document colors
    #[usage(name = "colors")]
    Colors(File),
    /// List or apply code actions
    #[usage(name = "codeaction")]
    Codeaction(Codeaction),
    /// Preview or apply a rename
    #[usage(name = "rename")]
    Rename(Rename),
    /// Execute a Shopware custom command (lists them without a method)
    #[usage(name = "execute")]
    Execute(Execute),
    /// Send an arbitrary LSP request
    #[usage(name = "request")]
    Request(Request),
}

#[derive(Debug, Default, Args)]
pub(crate) struct Serve {
    /// TCP address or unix;<path> for one remote client
    #[usage(long, value_name = "ADDR")]
    pub listen: Option<String>,
    /// Timeout waiting for a remote client, such as 30s or 2m
    #[usage(long, value_name = "DURATION")]
    pub listen_timeout: Option<String>,
    /// Write server logs to this file
    #[usage(long, value_name = "FILE")]
    pub logfile: Option<String>,
    /// Serve runtime profiling endpoints on this address
    #[usage(long, value_name = "ADDR")]
    pub debug: Option<String>,
    /// Write raw JSON-RPC traffic to stderr
    #[usage(long)]
    pub rpc_trace: bool,
}

#[derive(Debug, Args)]
pub(crate) struct Index {
    /// Clear file hashes and rebuild all indexes
    #[usage(long)]
    pub force: bool,
}

#[derive(Debug, Args)]
pub(crate) struct Check {
    /// Minimum severity: hint, info, warning, error (defaults to project configuration)
    #[usage(long, value_name = "LEVEL")]
    pub severity: Option<String>,
    /// Exit unsuccessfully when this severity or higher is reported (defaults to project configuration)
    #[usage(long, value_name = "LEVEL")]
    pub fail_on: Option<String>,
    /// Maximum concurrent diagnostic requests (default: CPU count, at most 4)
    #[usage(long, value_name = "COUNT")]
    pub workers: Option<i64>,
    /// Output layout: pretty (grouped by file, the default on a terminal) or concise (one line per finding)
    #[usage(long, value_name = "FORMAT")]
    pub format: Option<String>,
    /// Apply the quick fixes that need no decision and report what is left
    #[usage(long)]
    pub fix: bool,
    /// Files or directories to check
    #[usage(arg, required, value_name = "PATH")]
    pub paths: Vec<String>,
}

/// A file position.
#[derive(Debug, Args)]
pub(crate) struct At {
    /// File position: file[:line[:column]], one-based
    #[usage(value_name = "POSITION")]
    pub position: String,
}

/// A document.
#[derive(Debug, Args)]
pub(crate) struct File {
    /// The document
    #[usage(value_name = "FILE")]
    pub file: String,
}

#[derive(Debug, Args)]
pub(crate) struct WorkspaceSymbol {
    /// Query a fresh session instead of the cached symbol catalog
    #[usage(long)]
    pub fresh: bool,
    /// The symbol query
    pub query: String,
}

#[derive(Debug, Args)]
pub(crate) struct Codeaction {
    /// One-based exclusive selection end line
    #[usage(long, value_name = "LINE")]
    pub end_line: Option<i64>,
    /// One-based UTF-16 exclusive selection end column
    #[usage(long, value_name = "COLUMN")]
    pub end_column: Option<i64>,
    /// Filter by hierarchical code-action kind
    #[usage(long)]
    pub kind: Option<String>,
    /// Regular expression matched against action titles
    #[usage(long, value_name = "REGEXP")]
    pub title: Option<String>,
    /// Execute the first matching action
    #[usage(long)]
    pub exec: bool,
    #[usage(flatten)]
    pub edits: EditFlags,
    /// File position: file[:line[:column]], one-based
    #[usage(value_name = "POSITION")]
    pub position: String,
}

#[derive(Debug, Args)]
pub(crate) struct Rename {
    #[usage(flatten)]
    pub edits: EditFlags,
    /// File position: file[:line[:column]], one-based
    #[usage(value_name = "POSITION")]
    pub position: String,
    /// The new name
    #[usage(value_name = "NEW_NAME")]
    pub new_name: String,
}

/// How resulting edits are applied: previewed by default.
#[derive(Debug, Args)]
pub(crate) struct EditFlags {
    /// Write resulting edits to disk
    #[usage(short = 'w', long)]
    pub write: bool,
    /// Print resulting edits as a unified diff
    #[usage(short = 'd', long)]
    pub diff: bool,
}

#[derive(Debug, Args)]
pub(crate) struct Execute {
    /// The custom command; lists available commands when omitted
    pub method: Option<String>,
    /// JSON arguments: inline JSON, - for stdin, or @file
    #[usage(value_name = "JSON")]
    pub arguments: Option<String>,
}

#[derive(Debug, Args)]
pub(crate) struct Request {
    /// The LSP method
    pub method: String,
    /// JSON parameters: inline JSON, - for stdin, or @file
    #[usage(value_name = "JSON")]
    pub params: Option<String>,
}

impl Command {
    /// The name the command is invoked with.
    pub(crate) fn name(&self) -> &'static str {
        match self {
            Command::Serve(_) => "serve",
            Command::Mcp => "mcp",
            Command::Version => "version",
            Command::ApiJson => "api-json",
            Command::Licenses => "licenses",
            Command::Config => "config",
            Command::ProjectInfo => "project-info",
            Command::Index(_) => "index",
            Command::Stats => "stats",
            Command::Check(_) => "check",
            Command::Completion(_) => "completion",
            Command::Definition(_) => "definition",
            Command::Implementation(_) => "implementation",
            Command::References(_) => "references",
            Command::Hover(_) => "hover",
            Command::Signature(_) => "signature",
            Command::CallHierarchy(_) => "call-hierarchy",
            Command::TypeHierarchy(_) => "type-hierarchy",
            Command::Symbols(_) => "symbols",
            Command::WorkspaceSymbol(_) => "workspace-symbol",
            Command::Highlights(_) => "highlights",
            Command::FoldingRanges(_) => "folding-ranges",
            Command::Links(_) => "links",
            Command::Semtok(_) => "semtok",
            Command::InlayHints(_) => "inlay-hints",
            Command::Codelens(_) => "codelens",
            Command::SelectionRanges(_) => "selection-ranges",
            Command::LinkedEditing(_) => "linked-editing",
            Command::Colors(_) => "colors",
            Command::Codeaction(_) => "codeaction",
            Command::Rename(_) => "rename",
            Command::Execute(_) => "execute",
            Command::Request(_) => "request",
        }
    }
}
