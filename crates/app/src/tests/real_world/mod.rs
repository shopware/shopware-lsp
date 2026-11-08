//! The opt-in real-world suite (port of Go's `integration`-tagged tests in
//! `internal/app`). Run with
//! `cargo test -p swlsp-app --features integration --release -- --nocapture`
//! and `SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/shopware`.
#![allow(dead_code, unused_imports)]

mod fixture;
pub use fixture::*;

// Go real_world_helpers_*_test.go and real_world_indexing_main_test.go.
mod helpers_fixture;
mod helpers_request;
mod helpers_snapshot;
mod helpers_workspace;
mod indexing_main;

// Go framework_profile_integration_test.go, index_profile_test.go,
// request_latency_integration_test.go, admin_markup_audit_test.go,
// diagnostics_audit_test.go.
mod admin_markup_audit;
mod diagnostics_audit;
mod framework_profile;
mod index_profile;
mod request_latency;
