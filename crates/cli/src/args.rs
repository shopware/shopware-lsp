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
    after_help = "With no command, the stdio language server is started.",
    example = "shopware-lsp check src",
    example = "shopware-lsp check --fix src",
    example = "shopware-lsp info"
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
    /// Report problems in files and directories, and fix them with --fix
    #[usage(
        name = "check",
        help_heading = "Analyze",
        display_order = 1,
        example = "shopware-lsp check src",
        example = "shopware-lsp check --fix custom/plugins/MyPlugin",
        example = "shopware-lsp check --only php.unusedImport --fix src"
    )]
    Check(Check),
    /// Build or refresh the workspace index
    #[usage(name = "index", help_heading = "Analyze", display_order = 2)]
    Index(Index),
    /// Show how the project is detected and configured
    #[usage(
        name = "info",
        help_heading = "Analyze",
        display_order = 3,
        example = "shopware-lsp info",
        example = "shopware-lsp info --stats"
    )]
    Info(Info),
    /// List the index caches on this machine, or clear them
    #[usage(
        name = "cache",
        help_heading = "Analyze",
        display_order = 4,
        example = "shopware-lsp cache",
        example = "shopware-lsp cache clear",
        example = "shopware-lsp cache clear --stale"
    )]
    Cache(Cache),
    /// Run the Shopware language server (the default)
    #[usage(name = "serve", help_heading = "Servers", display_order = 5)]
    Serve(Serve),
    /// Run the Shopware MCP server
    #[usage(name = "mcp", help_heading = "Servers", display_order = 6)]
    Mcp,
    /// Ask the language server what an editor would ask, for debugging and scripts
    #[usage(
        name = "lsp",
        help_heading = "Debugging",
        display_order = 7,
        example = "shopware-lsp lsp hover src/Controller.php:24:18",
        example = "shopware-lsp lsp rename -d src/Controller.php:24:18 NewName"
    )]
    Lsp(Lsp),
    /// Print version information
    #[usage(name = "version", help_heading = "About", display_order = 8)]
    Version,
    /// Print license and third-party notices
    #[usage(name = "licenses", help_heading = "About", display_order = 9)]
    Licenses,
}

#[derive(Debug, Args)]
pub(crate) struct Lsp {
    #[usage(subcommand)]
    pub request: LspCommand,
}

/// One request to the language server.
#[derive(Debug, Subcommands)]
pub(crate) enum LspCommand {
    /// Show hover information at a position
    #[usage(name = "hover", help_heading = "At a position", display_order = 10)]
    Hover(At),
    /// Show definitions at a position
    #[usage(
        name = "definition",
        help_heading = "At a position",
        display_order = 11
    )]
    Definition(At),
    /// Show implementations at a position
    #[usage(
        name = "implementation",
        help_heading = "At a position",
        display_order = 12
    )]
    Implementation(At),
    /// Show references at a position
    #[usage(
        name = "references",
        help_heading = "At a position",
        display_order = 13
    )]
    References(At),
    /// Show completion items at a position
    #[usage(
        name = "completion",
        help_heading = "At a position",
        display_order = 14
    )]
    Completion(At),
    /// Show signature help at a position
    #[usage(name = "signature", help_heading = "At a position", display_order = 15)]
    Signature(At),
    /// Show incoming and outgoing calls
    #[usage(
        name = "call-hierarchy",
        help_heading = "At a position",
        display_order = 16
    )]
    CallHierarchy(At),
    /// Show supertypes and subtypes
    #[usage(
        name = "type-hierarchy",
        help_heading = "At a position",
        display_order = 17
    )]
    TypeHierarchy(At),
    /// Show document highlights at a position
    #[usage(
        name = "highlights",
        help_heading = "At a position",
        display_order = 18
    )]
    Highlights(At),
    /// Show expandable selection ranges
    #[usage(
        name = "selection-ranges",
        help_heading = "At a position",
        display_order = 19
    )]
    SelectionRanges(At),
    /// Show linked editing ranges
    #[usage(
        name = "linked-editing",
        help_heading = "At a position",
        display_order = 20
    )]
    LinkedEditing(At),
    /// Show a document outline
    #[usage(name = "symbols", help_heading = "For a file", display_order = 21)]
    Symbols(File),
    /// Show folding ranges for a document
    #[usage(
        name = "folding-ranges",
        help_heading = "For a file",
        display_order = 22
    )]
    FoldingRanges(File),
    /// Show document links
    #[usage(name = "links", help_heading = "For a file", display_order = 23)]
    Links(File),
    /// Show semantic tokens
    #[usage(
        name = "semantic-tokens",
        help_heading = "For a file",
        display_order = 24
    )]
    SemanticTokens(File),
    /// Show inlay hints
    #[usage(name = "inlay-hints", help_heading = "For a file", display_order = 25)]
    InlayHints(File),
    /// Show code lenses
    #[usage(name = "codelens", help_heading = "For a file", display_order = 26)]
    Codelens(File),
    /// Show document colors
    #[usage(name = "colors", help_heading = "For a file", display_order = 27)]
    Colors(File),
    /// Search workspace symbols
    #[usage(
        name = "workspace-symbol",
        help_heading = "For the workspace",
        display_order = 28
    )]
    WorkspaceSymbol(WorkspaceSymbol),
    /// List or apply code actions
    #[usage(name = "codeaction", help_heading = "Changes", display_order = 29)]
    Codeaction(Codeaction),
    /// Preview or apply a rename
    #[usage(name = "rename", help_heading = "Changes", display_order = 30)]
    Rename(Rename),
    /// Execute a Shopware custom command (lists them without a method)
    #[usage(name = "execute", help_heading = "Raw requests", display_order = 31)]
    Execute(Execute),
    /// Send an arbitrary LSP request
    #[usage(name = "request", help_heading = "Raw requests", display_order = 32)]
    Request(Request),
    /// Print the supported CLI and language API
    #[usage(name = "api-json", help_heading = "Raw requests", display_order = 33)]
    ApiJson,
}

#[derive(Debug, Args)]
pub(crate) struct Cache {
    #[usage(subcommand)]
    pub command: Option<CacheCommand>,
}

#[derive(Debug, Subcommands)]
pub(crate) enum CacheCommand {
    /// Delete the cache of this project, or with a flag those of others
    #[usage(name = "clear")]
    Clear(CacheClear),
}

#[derive(Debug, Args)]
pub(crate) struct CacheClear {
    /// Delete the caches that are outdated or whose project is gone
    #[usage(long, conflicts = "all")]
    pub stale: bool,
    /// Delete the caches of all projects
    #[usage(long)]
    pub all: bool,
    /// Show what would be deleted
    #[usage(long)]
    pub dry_run: bool,
}

#[derive(Debug, Args)]
pub(crate) struct Info {
    /// Also index the workspace and show index, cache, and memory statistics
    #[usage(long)]
    pub stats: bool,
    /// Only detect the project: fast, and the workspace is not opened
    #[usage(long, conflicts = "stats")]
    pub project_only: bool,
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
    /// Report and fix only these rules: a rule ID or a prefix such as twig.html.* (repeatable, comma-separated)
    #[usage(long, value_name = "RULE")]
    pub only: Vec<String>,
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
    /// The name the command is invoked with; a request below `lsp` goes
    /// by its own name.
    pub(crate) fn name(&self) -> &'static str {
        match self {
            Command::Check(_) => "check",
            Command::Index(_) => "index",
            Command::Info(_) => "info",
            Command::Cache(_) => "cache",
            Command::Serve(_) => "serve",
            Command::Mcp => "mcp",
            Command::Lsp(lsp) => lsp.request.name(),
            Command::Version => "version",
            Command::Licenses => "licenses",
        }
    }
}

impl LspCommand {
    pub(crate) fn name(&self) -> &'static str {
        match self {
            LspCommand::Hover(_) => "hover",
            LspCommand::Definition(_) => "definition",
            LspCommand::Implementation(_) => "implementation",
            LspCommand::References(_) => "references",
            LspCommand::Completion(_) => "completion",
            LspCommand::Signature(_) => "signature",
            LspCommand::CallHierarchy(_) => "call-hierarchy",
            LspCommand::TypeHierarchy(_) => "type-hierarchy",
            LspCommand::Highlights(_) => "highlights",
            LspCommand::SelectionRanges(_) => "selection-ranges",
            LspCommand::LinkedEditing(_) => "linked-editing",
            LspCommand::Symbols(_) => "symbols",
            LspCommand::FoldingRanges(_) => "folding-ranges",
            LspCommand::Links(_) => "links",
            LspCommand::SemanticTokens(_) => "semantic-tokens",
            LspCommand::InlayHints(_) => "inlay-hints",
            LspCommand::Codelens(_) => "codelens",
            LspCommand::Colors(_) => "colors",
            LspCommand::WorkspaceSymbol(_) => "workspace-symbol",
            LspCommand::Codeaction(_) => "codeaction",
            LspCommand::Rename(_) => "rename",
            LspCommand::Execute(_) => "execute",
            LspCommand::Request(_) => "request",
            LspCommand::ApiJson => "api-json",
        }
    }
}
