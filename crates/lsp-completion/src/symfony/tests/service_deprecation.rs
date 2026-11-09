//! Tests for service deprecation details in completion.

use std::sync::Arc;

use swlsp_symfony::ServiceIndex;

use super::*;
use crate::symfony::ServiceCompletionProvider;

#[test]
fn service_completions_mark_deprecated_services_and_classes() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        &dir.path().join("php"),
        &[(
            "/project/Types.php",
            r"<?php
namespace App;
class Modern {}
/** @deprecated Use Modern instead. */
class Legacy {}
",
        )],
    );
    let services =
        Arc::new(ServiceIndex::new(&p(dir.path()), dir.path().join("services"), None).unwrap());
    services.set_php_index(Some(php.clone()));
    index_file(
        services.as_ref(),
        "/project/services.yaml",
        r"services:
  app.modern:
    class: App\Modern
  app.legacy:
    class: App\Legacy
    deprecated: 'The %service_id% service is deprecated; use app.modern.'
",
    );
    let provider = ServiceCompletionProvider::new(Some(services), Some(php));
    let items = provider.service_completion_items("@", "");
    let legacy = require_completion(&items, "app.legacy");
    assert!(legacy.deprecated);
    assert_eq!(legacy.insert_text, "@app.legacy");
    assert_eq!(legacy.detail, "Deprecated Symfony service");
    let documentation = legacy.documentation.unwrap().value;
    assert!(documentation.contains("app.legacy"));
    assert!(!documentation.contains("%service_id%"));
    assert!(!require_completion(&items, "app.modern").deprecated);

    let classes = provider.class_completion_items();
    let legacy = require_completion(&classes, r"App\Legacy");
    assert!(legacy.deprecated);
    assert_eq!(legacy.detail, "Deprecated PHP type");
    assert!(!require_completion(&classes, r"App\Modern").deprecated);
}
