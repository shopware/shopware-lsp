//! Symfony code actions and commands.

use std::sync::Arc;

use crate::analytics;
use crate::codeaction;
use crate::commands;
use swlsp_ide::{Registrar, Services};

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    let php = || s.php();
    server.action(Arc::new(codeaction::EventListenerContextProvider::new(
        php(),
    )));
    server.action(Arc::new(codeaction::ServiceTagCodeActionProvider::new(
        php(),
    )));
    server.action(Arc::new(
        codeaction::XmlServiceSuggestionCodeActionProvider::new(Some(s.services.clone()), php()),
    ));
    server.action(Arc::new(
        codeaction::PropertyServiceCodeActionProvider::new(php(), Some(s.services.clone())),
    ));
    // Offers its generators as code actions and runs them as commands.
    let generators = Arc::new(codeaction::SymfonyGeneratorProvider::new(
        php(),
        Some(s.services.clone()),
    ));
    server.action(generators.clone());
    server.command(generators);
    server.action(Arc::new(
        codeaction::InvokableCommandMigrationCodeActionProvider::new(php()),
    ));
    server.action(Arc::new(
        codeaction::CommandInvokeParameterCodeActionProvider::new(php()),
    ));
    server.action(Arc::new(codeaction::RouteAttributeCodeActionProvider::new(
        php(),
    )));
    server.action(Arc::new(
        codeaction::RouteActionParameterCodeActionProvider::new(),
    ));

    server.command(Arc::new(commands::ConsoleCatalogProvider::new(
        s.console.clone(),
        Some(&s.root),
    )));
    server.command(Arc::new(analytics::RouteCatalogProvider::new(
        &s.root,
        Some(s.routes.clone()),
        Some(s.services.clone()),
        php(),
        Some(s.twig.clone()),
    )));
    server.command(Arc::new(analytics::ServiceLocatorProvider::new(
        Some(s.services.clone()),
        php(),
    )));
    server.command(Arc::new(analytics::ProfilerCatalogProvider::new(
        &s.root,
        php(),
        Some(s.twig.clone()),
    )));
}
