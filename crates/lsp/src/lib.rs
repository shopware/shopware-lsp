//! The LSP core (port of Go `internal/lsp`): JSON-RPC transport and ordered
//! dispatch, open-document snapshots, provider interfaces and fan-out,
//! layered configuration, diagnostics scheduling, typed inspections with
//! bound quick fixes, and validated code-action edits.
//!
//! # Transport
//!
//! Framing and message types come from `tower-lsp-server`, driven by a small
//! Tokio runtime at the I/O edge while analysis stays synchronous. The
//! transport calls a raw `tower::Service` once per incoming message in
//! arrival order; the service handles `$/cancelRequest` itself and enqueues
//! everything else onto one worker thread that executes requests and
//! document notifications strictly in arrival order (so a `didChange` can
//! never race a completion). Cancellation is a token checked cooperatively by
//! providers, not a dropped future, and diagnostics/indexing run on
//! lifecycle-managed background threads. The typed `LanguageServer` trait is
//! not used: it would deserialize into its own types and drop fields those
//! types do not model. Blocking streams (stdio, sockets, in-process pipes)
//! reach the runtime through [`async_io`].
//!
//! # Positions
//!
//! Providers work with byte offsets into immutable snapshots. UTF-16
//! conversion happens only at the protocol boundary through the snapshot's
//! [`swlsp_syntax::LineIndex`].

pub mod async_io;
mod capabilities;
mod client;
mod code_action;
mod comment_suppression;
mod configuration;
mod context;
mod diagnostics;
mod document;
mod features;
mod fix_all;
pub mod fixture;
mod inspection;
mod providers;
mod server;
mod transport;
mod vue_context;
mod workspace;
mod workspace_diagnostics;

pub use client::{CLIENT_PROTOCOL_VERSION, PresentationProfile};
pub use code_action::{
    DocumentError, WorkspaceEditHost, add_document_replacement, is_stale_error,
    resolve_optional_document,
};
pub use comment_suppression::comment_suppression_reader;
pub use configuration::{
    ConfigurationCatalog, ConfigurationDiagnostic, ConfigurationInspection, ConfigurationIssue,
    ConfigurationReloadResult,
};
pub use context::{
    BoxError, CancellationToken, Cancelled, Context, RpcError, codes, is_cancellation,
};
pub use diagnostics::{DIAGNOSTICS_DEBOUNCE, InspectionAudit, protocol_range, text_range};
pub use document::{
    DidClose, DidOpenOrChange, DocumentManager, DocumentObserver, SyntaxContext, TextDocument,
    parse_path,
};
pub use inspection::{
    BoundFix, CommandQuickFix, DiagnosticId, DocumentResolver, DocumentSnapshot, FixContext, FixId,
    FixPresentation, FixResolution, Inspection, InspectionDefinition, InspectionScope,
    InspectionSpec, Problem, ProblemDefinition, ProblemReporter, QuickFix, RewriteQuickFix,
    SuppressionReader, Suppressions, bind_fix,
};
pub use providers::*;
pub use server::{Server, WeakServer};
pub use transport::CallError;
pub use vue_context::effective_syntax_language;
pub use workspace::{WorkspaceFactory, WorkspaceRuntime};

#[cfg(test)]
mod tests;
