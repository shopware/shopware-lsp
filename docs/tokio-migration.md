# Tokio Migration Plan

Status: phases 1–3 done (runtime foundation, MCP on `rmcp`, LSP transport on
`tower-lsp-server`); phase 4 (ecosystem crates) is open. See
[Outcome](#outcome). Goal: adopt the official MCP SDK (`rmcp`) and make async
ecosystem crates usable, without giving up the server's ordering and
cancellation guarantees or rewriting the synchronous analysis core.

## Summary

Introduce Tokio at the **edges** (stdio, sockets, MCP, HTTP) and keep the
**core** (parsing, indexing, inspections, providers) synchronous on
dedicated threads, bridged explicitly. Do it in four independently shippable
phases, starting with a one-week spike that settles the open questions about
`rmcp`:

| Phase | Outcome | Needed for the MCP SDK |
|---|---|---|
| 0. Spike | `rmcp` fit confirmed, version and features pinned, contract diffs known | yes |
| 1. Runtime foundation | one runtime, blocking bridge, one cancellation model | yes |
| 2. MCP on `rmcp` | hand-rolled MCP JSON-RPC deleted | yes |
| 3. LSP transport on Tokio | async I/O for the language server, no polling loops | no |
| 4. Ecosystem crates | HTTP client, optional MCP over HTTP from the editor's process | no |

Phases 0 to 2 deliver the stated goal. Phases 3 and 4 are worth doing only
for a concrete crate or feature that needs them (see the open questions).

## Current state

| Area | Today | Where |
|---|---|---|
| LSP transport | `lsp-server` framing; a reader thread, **one ordered worker thread**, bounded crossbeam queue; `$/cancelRequest` handled on the reader; cooperative `CancellationToken` | `crates/lsp/src/transport.rs`, `crates/lsp/src/context.rs` |
| Hosting | `serve` runs the server on a thread and polls for completion and socket accepts every 25 ms | `crates/cli/src/serve.rs` |
| MCP | separate process (`shopware-lsp mcp`); **hand-rolled newline JSON-RPC** with hand-built error texts; tools dispatched synchronously; one lazily created, mutex-serialized `CliSession` | `crates/cli/src/mcp/` (about 2,700 lines), `crates/cli/src/session.rs` |
| MCP → analysis | the MCP process starts an in-process LSP server and talks to it over OS pipes with its own JSON-RPC client, the same path `check` uses | `crates/cli/src/session.rs` |
| Indexing | `notify` watcher thread, scanner and settle workers on OS threads | `crates/indexer`, `crates/php/src/settle.rs` |
| HTTP | blocking `ureq`, one call site | `crates/lsp-commands/src/twig.rs` |
| MCP tests | 15 Rust tests, the VS Code `test:mcp` suite, `make zed-contracts` | `crates/cli/src/tests/mcp.rs`, `editors/vscode/test/mcpServer.test.js`, `editors/zed` |

Two properties are deliberate and must survive (`crates/lsp/src/lib.rs`
explains why `tower-lsp-server` was rejected):

1. **Ordered dispatch.** Requests and notifications run in arrival order on one
   worker, so a `didChange` can never race a completion.
2. **Cooperative cancellation.** Analysis is CPU-bound synchronous code; it
   checks a token, it is not a future that can be dropped.

## Decision: async edges, synchronous core

- **Tokio owns I/O:** stdio, TCP and Unix sockets, MCP transports, HTTP.
- **Analysis stays synchronous** and runs on dedicated OS threads (the
  ordered LSP worker) or `spawn_blocking`, never on a Tokio worker thread.
- **One cancellation model:** `tokio_util::sync::CancellationToken` becomes
  the token behind `swlsp_lsp::CancellationToken` and the CLI's `Cancel`, so
  an async transport and synchronous analysis share it without adapters.
- **One production path stays** (`AGENTS.md`): MCP and the CLI keep driving
  the same LSP server; nothing gets a second analysis path.

Rejected:

- **Port the whole server to `tower-lsp-server`.** Handlers run concurrently,
  which breaks ordered dispatch, and request cancellation drops futures
  instead of signalling CPU-bound work.
- **`async-lsp`.** Orders notifications but runs requests concurrently by
  default, and is built on `lsp-types` 0.95 next to our own protocol crate.
  Matching today's semantics would mean writing our own layers, so it buys
  little over keeping our dispatcher.
- **Make the analysis core `async`.** It would colour about 100 crates with
  no gain: the work is CPU-bound and already parallel where it matters.
- **Keep MCP hand-rolled.** It fails the goal and keeps us tracking spec
  changes (`2026-07-28` is stateless) by hand.

## Decisions

- **LSP transport: `tower-lsp-server` 0.23.** Its `Server` (codec, framing,
  read loop, bounded concurrency) drives a **raw `tower::Service` over
  JSON-RPC requests** that forwards to `swlsp_lsp::Server::handle`, plus our
  own `Loopback` for server-to-client messages. We do not implement its typed
  `LanguageServer` trait: that would re-type every request and result
  through its LSP types and silently drop fields they do not model (the
  `experimental` capability extensions, custom result fields, the six
  `shopware/*` methods). `Service::call` runs in arrival order inside the
  transport's read loop, and enqueues on the ordered worker synchronously, so
  ordering is as strict as today. `$/cancelRequest` is handled in `call`,
  before the queue, as the reader thread does today.
- **MCP: `rmcp` 3.9** with a hand-written `ServerHandler` (tools come from
  `tools.json` and are enabled by configuration), stdio only.
- **MCP error texts** are the SDK's.
- **Responses bypass the `tower-lsp-server` writer.** The worker writes
  each response, and `Connection` each server-to-client request and
  notification, as one complete frame to the output thread that
  `tower-lsp-server`'s writer also feeds (`async_io::FrameSender`). Large
  results are serialized once and freed on the worker while the output
  writes them, and everything the worker sends reaches the client in the
  order it was sent. The request futures returned to `tower-lsp-server`
  resolve immediately with no response.

## Outcome

- The LSP and MCP servers run on Tokio at the I/O edge only; analysis,
  indexing and diagnostics remain synchronous. Blocking streams (stdio,
  sockets, `CliSession` pipes) are bridged by `crates/lsp/src/async_io.rs`.
- Responses are unchanged: the end-to-end benchmark
  (`scripts/bench/lsp_bench.py` on a Shopware checkout) reports no response
  mismatch across 7,489 requests.
- Performance: a first version answered through `tower-lsp-server`'s writer
  and was 13% slower on PHP completion, whose results reach 20 MB
  (serialization on a runtime task, fragmented 8 KiB pipe writes, and the
  result freed before the write). Writing whole frames from the worker
  removed that; small requests keep about 0.1 ms of extra latency from the
  runtime hop on input.
- `serve --listen` accepts with a Tokio listener raced against cancellation
  and `--listen-timeout` (no polling), and serves the socket as Tokio
  streams without bridge threads; `--rpc-trace` wraps either kind of
  stream.
- `CliSession` serves its in-process server over a `tokio::io::duplex` on
  a small runtime of the session, with a reader task instead of OS pipes and
  a reader thread.
- A cancelled session call sends `$/cancelRequest`, so the server stops the
  work too. Each MCP tool call has its own token, a child of the CLI's,
  cancelled by `notifications/cancelled` (rmcp's request token); a
  cancelled whole-`src` diagnostics run on Shopware frees the session at
  once instead of after its 100 seconds. A call cancelled while waiting for
  the first index keeps the session, and the next call resumes the wait.
- Every queued request before `exit` is still answered in order, although
  `tower-lsp-server` stops reading at `exit`.

## Phase 0: spike (about 1 week)

Build a throwaway `rmcp` server next to the current one and answer:

1. **Dynamic tools.** Tools come from `tools.json` and are enabled per
   configuration (`McpRuntime::tool_enabled`), so they cannot be macro-only.
   Confirm a hand-written `ServerHandler` (`list_tools`, `call_tool`) can serve
   our hand-maintained input and output schemas without `schemars`.
2. **Structured results.** Check that `structuredContent` plus the text
   fallback can be produced exactly as today.
3. **Cancellation and progress.** Map MCP `notifications/cancelled` to the
   running tool's token; check progress notifications for long indexing.
4. **Protocol versions.** Check which versions `rmcp` 3.x negotiates
   (`2025-11-25`, `2026-07-28`) against what the VS Code and Zed clients send.
5. **Contract diff.** Run `crates/cli/src/tests/mcp.rs`, `npm run test:mcp`
   and `make zed-contracts` against the spike and list every difference
   (error texts, validation messages, initialization gate).
6. **Cost.** Release binary size, clean and incremental build time, and
   `musl` and Windows builds (`make release-dry-run`).

Exit criteria: a pinned `rmcp` version and feature set (expected: `server`,
`transport-io`, no `macros` and no `schemars` unless they help), the list of
contract changes we accept, and measured size and build cost. If dynamic
tools or structured output do not fit, stop here and report.

## Phase 1: runtime foundation

- **Workspace dependencies:** `tokio` (`rt-multi-thread`, `io-std`, `io-util`,
  `net`, `sync`, `time`, `macros`) and `tokio-util`, declared once in the root
  `Cargo.toml`.
- **A small `swlsp-runtime` crate:**
  - builds the runtime (worker count, thread names);
  - `run_blocking`, a `spawn_blocking` wrapper that carries the cancellation
    token;
  - a debug assertion analysis entry points call to catch accidental use on
    a Tokio worker thread.
- **Cancellation:** re-implement `swlsp_lsp::CancellationToken` on
  `tokio_util::sync::CancellationToken`, keeping its API (`is_cancelled`,
  `child`, `wait_timeout`), so no call site changes.
- **CLI entry:** `main` stays synchronous. Only `mcp` (and `serve` after
  phase 3) start a runtime, so `check` and the feature commands keep their
  startup cost.

Exit: `make rust-check` green, no behaviour change, `crossbeam` still used
inside the LSP transport.

## Phase 2: MCP on `rmcp`

- Replace the JSON-RPC layer in `crates/cli/src/mcp/mod.rs` with an
  `rmcp::ServerHandler` over `rmcp::transport::stdio()`. The SDK takes over
  version negotiation, the initialization gate, `ping`, and cancellation and
  progress plumbing.
- Keep the tool layer: `tools.rs`, `scaffold.rs`, `schema.rs` and
  `tools.json` are unchanged and synchronous. `call_tool` runs them through
  `run_blocking` with the request's cancellation token.
- **Session:** `CliSession` stays synchronous, behind a `tokio::sync::Mutex`
  acquired before `run_blocking`, so tool calls stay serialized and
  indexing-on-first-use is unchanged.
- Delete the hand-built protocol code: error texts, `method_info`, the
  params-type messages.
- **Contracts:**
  - update `crates/cli/src/tests/mcp.rs` to the SDK's texts where they
    differ;
  - regenerate the Zed contract snapshot;
  - update `editors/vscode/test/mcpServer.test.js`;
  - document visible differences (error messages, supported protocol
    versions) in `docs/reference.md`.
- **Must not change:** tool names, input and output schemas, structured
  results, path validation and write gating (`AGENTS.md`, *CLI and MCP*).

Exit: all MCP tests and both editors' contract checks green; `tools/list`
returns identical names and schemas.

## Phase 3: LSP transport on Tokio (optional)

Worth doing when phase 4 needs it, or to remove the polling loops.
Semantics stay identical:

- **I/O:** reader and writer become Tokio tasks with an LSP header codec
  (`tokio_util::codec`), still producing `lsp_server::Message`.
- **Ordering:** the ordered worker stays **one dedicated OS thread**, fed by
  a bounded `tokio::sync::mpsc` queue (`blocking_recv` on the worker side).
  `$/cancelRequest` is still handled by the reader, before the queue.
- **Outgoing calls** (`workspace/diagnostic/refresh`, configuration
  requests) use `oneshot` channels in place of crossbeam senders.
- **`serve`:** Tokio `TcpListener` and `UnixListener` replace the
  non-blocking polling accept loop, and completion waits on a future.
- **`CliSession`:** an in-process `tokio::io::duplex` replaces OS pipes; its
  JSON-RPC client becomes async, with a blocking facade for the CLI commands.
- **Measure:** `scripts/bench/lsp_bench.py` before and after on `~/shopware`;
  request latency and throughput must not regress.

Exit: transport and server tests green, `make zed-contracts` and the VS Code
tests green, benchmark within noise.

## Phase 4: ecosystem crates

Driven by need, each its own change:

- **HTTP:** done. `ureq` was replaced by `reqwest` (its blocking client:
  the one call site, the Twig block diff's GitHub fallback, runs on the
  synchronous worker). TLS stays rustls on `ring` with the bundled Mozilla
  roots (`webpki-roots`), as with `ureq`: reqwest's default `aws-lc-rs`
  would need CMake in release builds, and its platform verifier a system
  trust store. A feature that needs async HTTP can use the same crate's
  async client.
- **MCP from the editor's process:** serve MCP over Streamable HTTP from
  `serve` (an `rmcp` feature), so MCP queries the workspace the editor has
  already indexed instead of a second process indexing it again. This is a
  product change to client registration (VS Code `mcpServerModel.ts`, Zed MCP
  configuration) and security (bind to localhost, token).
- **Watcher and indexer:** stay on threads; `notify` and the scanner gain
  nothing from async.

## Rules during and after the migration

- **No analysis on Tokio worker threads.** Synchronous work goes through
  `run_blocking` or the ordered worker; the debug assertion catches mistakes
  in tests.
- **One token type.** No new cancellation primitives; async code awaits
  `cancelled()`, synchronous code polls `is_cancelled()`.
- **Tests stay offline.** Async tests use `#[tokio::test]`; existing
  synchronous tests are unchanged.
- **Logging stays on `log`.** Moving to `tracing` is a separate decision.
- **Documentation:** the threading notes in `crates/lsp/src/lib.rs` and
  `crates/lsp/src/transport.rs` and `docs/lsp-server.md` describe the new
  model in the same change.

## Risks

| Risk | Mitigation |
|---|---|
| `rmcp` cannot serve config-dependent tools or our exact schemas | phase 0 gate; fall back to keeping our layer on Tokio stdio |
| MCP clients depend on today's error texts | contract suites in phase 0 list every difference; changes are documented |
| Blocking a Tokio worker with analysis | `run_blocking`, debug assertion, review rule |
| Ordering regressions in the LSP transport | phase 3 keeps the single ordered worker; transport tests cover order and cancellation |
| `rmcp` churn (3.x moved fast in 2026) | pin the version, keep `rmcp` confined to `crates/cli/src/mcp` |
| Binary size and build time | measured in phase 0; minimal features |
| `musl` and Windows release builds | `make release-dry-run` in phase 0 and before each phase lands |

## Effort

| Phase | Estimate |
|---|---|
| 0 | 3 to 5 days |
| 1 | 2 to 3 days |
| 2 | 1 to 2 weeks, mostly contract and test updates |
| 3 | 1 to 2 weeks |
| 4 | per feature |

## Open questions

Answered: phase 3 was done; changed MCP error texts are acceptable; stdio is
enough for now. Still open:

1. **Which other async crates are wanted?** Phase 4 items start when a
   feature needs them.
2. **Should MCP move into the editor's process** (Streamable HTTP from
   `serve`) to stop indexing the workspace twice?

Sources: [rmcp README](https://github.com/modelcontextprotocol/rust-sdk),
[rmcp 3.1.0 on docs.rs](https://docs.rs/crate/rmcp/3.1.0/source/README.md),
[tower-lsp-server 0.23.0](https://docs.rs/crate/tower-lsp-server/latest/source/README.md),
[async-lsp 0.2.4](https://docs.rs/crate/async-lsp/latest).
