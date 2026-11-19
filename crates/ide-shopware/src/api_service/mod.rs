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
    ApiServiceProvider, ApiServiceRequest, ApiServiceResponse, GENERATE_API_SERVICE_ACTION,
    GENERATE_API_SERVICE_COMMAND,
};
pub(crate) use shape::{Field, Shape, property_key};

#[cfg(test)]
mod tests;
