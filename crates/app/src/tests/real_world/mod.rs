//! The opt-in real-world suite. Run with
//! `cargo test -p swlsp-app --features integration --release -- --nocapture`
//! and `SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/shopware`.
#![allow(dead_code, unused_imports)]

mod fixture;
pub use fixture::*;

// Shared fixture helpers and the main indexing test.
mod helpers_fixture;
mod helpers_request;
mod helpers_snapshot;
mod helpers_workspace;
mod indexing_main;

// Profiles, latency budgets and audits.
mod admin_markup_audit;
mod diagnostics_audit;
mod framework_profile;
mod index_profile;
mod request_latency;
