//! The contract between the composition root and the domain crates.
//!
//! IDE features are grouped by domain (`swlsp-ide-php`, `swlsp-ide-symfony`,
//! `swlsp-ide-twig`, ...). Each domain crate exports one [`Domain`] whose
//! `register` function adds everything the domain offers — providers,
//! inspections, suppression readers, observers — to a [`Registrar`], built
//! from the workspace [`Services`]. The composition root (`swlsp-app`) only
//! lists the domains and owns the [`Precedence`] between their providers.

pub mod analytics;
pub mod codeaction;
pub mod codelens;
pub mod commands;
pub mod completion;
pub mod definition;
pub mod diagnostics;
pub mod hover;
pub mod inlay;
pub mod inspections;
pub mod refactor;
pub mod reference;
mod registrar;
pub mod selection;
pub mod semantic;
mod services;
pub mod symbol;

pub use registrar::{Precedence, Registrar, Registration};
pub use services::Services;

/// One domain's contribution to the server.
pub struct Domain {
    /// A short name for logs and tests (`"twig"`).
    pub id: &'static str,
    /// Adds the domain's providers and inspections. Gating by configuration
    /// domain (`registrar.domain_enabled`) happens inside.
    pub register: fn(&Registrar<'_>, &Services),
}
