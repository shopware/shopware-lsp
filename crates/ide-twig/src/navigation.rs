//! Twig definitions, references, hovers and code lenses.

use std::sync::Arc;

use swlsp_ide::{Registrar, Services};

pub(crate) fn register(server: &Registrar<'_>, s: &Services) {
    definitions(server, s);
    references(server, s);
    hovers(server, s);
    server.code_lens(Arc::new(crate::codelens::TwigCodeLensProvider::new(Some(
        s.twig.clone(),
    ))));
    server.code_lens(Arc::new(
        crate::codelens::TwigComponentRelatedCodeLensProvider::new(
            Some(s.twig_components.clone()),
            s.php(),
        ),
    ));
}

fn definitions(server: &Registrar<'_>, s: &Services) {
    use crate::definition::*;

    let php = || s.php();
    server.definition(Arc::new(TwigEnumDefinitionProvider::new(php())));
    server.definition(Arc::new(TwigConstantDefinitionProvider::new(
        php(),
        Some(s.twig.clone()),
    )));
    server.definition(Arc::new(TwigMacroDefinitionProvider::new(Some(
        s.twig.clone(),
    ))));
    server.definition(Arc::new(TwigComponentDefinitionProvider::new(
        Some(s.twig_components.clone()),
        php(),
    )));
    server.definition(Arc::new(LiveComponentEventDefinitionProvider::new(Some(
        s.twig_components.clone(),
    ))));
    server.definition(Arc::new(TwigIncludeParameterDefinitionProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.definition(Arc::new(TwigRenderBlockDefinitionProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.definition(Arc::new(TwigDefinitionProvider::new(
        s.root.clone(),
        Some(s.twig.clone()),
        Some(s.extensions.clone()),
        php(),
    )));
}

fn references(server: &Registrar<'_>, s: &Services) {
    use crate::reference::*;

    let php = || s.php();
    server.references(Arc::new(TwigMacroReferenceProvider::new(Some(
        s.twig.clone(),
    ))));
    server.references(Arc::new(TwigTemplateReferenceProvider::new(Some(
        s.twig.clone(),
    ))));
    server.references(Arc::new(TwigConstantReferenceProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.references(Arc::new(TwigPhpReferenceProvider::new(
        Some(s.twig.clone()),
        php(),
    )));
    server.references(Arc::new(TwigComponentReferenceProvider::new(
        Some(s.twig_components.clone()),
        php(),
    )));
    server.references(Arc::new(LiveComponentEventReferenceProvider::new(Some(
        s.twig_components.clone(),
    ))));
}

fn hovers(server: &Registrar<'_>, s: &Services) {
    use crate::hover::*;

    let php = || s.php();
    let root = || s.root.clone();
    server.hover(Arc::new(TwigEnumHoverProvider::new(php())));
    server.hover(Arc::new(TwigConstantHoverProvider::new(
        php(),
        Some(s.twig.clone()),
    )));
    server.hover(Arc::new(TwigMacroHoverProvider::new(
        root(),
        Some(s.twig.clone()),
    )));
    server.hover(Arc::new(TwigComponentHoverProvider::new(
        root(),
        Some(s.twig_components.clone()),
    )));
    server.hover(Arc::new(LiveComponentEventHoverProvider::new(Some(
        s.twig_components.clone(),
    ))));
    server.hover(Arc::new(TwigIncludeParameterHoverProvider::new(
        root(),
        Some(s.twig.clone()),
        php(),
    )));
    server.hover(Arc::new(TwigRenderBlockHoverProvider::new(
        root(),
        Some(s.twig.clone()),
        php(),
    )));
    server.hover(Arc::new(TwigHoverProvider::new(
        root(),
        Some(s.extensions.clone()),
        php(),
        Some(s.twig.clone()),
    )));
    if let Some(versioning) = crate::versioning(server, s) {
        server.hover(Arc::new(TwigVersioningHoverProvider::new(Some(versioning))));
    }
}
