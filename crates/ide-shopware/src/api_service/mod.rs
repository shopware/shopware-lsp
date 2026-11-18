//! Typed Administration API services generated from PHP controllers.
//!
//! [`crate::diagnostics::api_controller`] reads what the Admin
//! API actions of a controller take and answer; [`generate`] writes the
//! TypeScript service that calls them, and [`ApiServiceProvider`]
//! offers it on the controller class.

mod action;
pub(crate) mod generate;
mod shape;

pub use action::{
    ApiServiceProvider, ApiServiceRequest, ApiServiceResponse, CREATE_API_SERVICE_COMMAND,
    GENERATE_API_SERVICE_ACTION,
};
pub(crate) use shape::{Field, Shape};

#[cfg(test)]
mod tests;
