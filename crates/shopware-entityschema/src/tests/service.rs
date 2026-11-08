use crate::*;

#[test]
fn patch_service_configuration_formats() {
    let definition = "Acme\\Demo\\ExampleDefinition";
    let tests = [
        (
            "services.xml",
            "<container><services>\n</services></container>",
        ),
        (
            "services.yaml",
            "services:\n  _defaults:\n    autowire: true\n",
        ),
        (
            "services.php",
            "<?php\nreturn static function ($container, $services): void {\n};\n",
        ),
    ];
    for (path, source) in tests {
        let result = patch_service_configuration(path, source, definition).unwrap();
        assert!(result.contains(definition), "{path}");
        assert!(result.contains("shopware.entity.definition"), "{path}");
        let again = patch_service_configuration(path, &result, definition).unwrap();
        assert_eq!(
            again.matches("shopware.entity.definition").count(),
            1,
            "{path}"
        );
    }
}

#[test]
fn patch_service_configuration_creates_xml() {
    let result = patch_service_configuration("services.xml", "", "Acme\\DemoDefinition").unwrap();
    assert!(result.contains("<service id=\"Acme\\DemoDefinition\">"));
}

#[test]
fn patch_service_configuration_creates_yaml() {
    let result = patch_service_configuration("services.yaml", "", "Acme\\DemoDefinition").unwrap();
    assert_eq!(
        result,
        "services:\n  Acme\\DemoDefinition:\n    tags:\n      - { name: shopware.entity.definition }\n"
    );
}

#[test]
fn patch_yaml_service_stays_inside_services_root() {
    let source = "services:\n  existing: ~\n\nwhen@test:\n  services: {}\n";
    let result =
        patch_service_configuration("services.yaml", source, "Acme\\DemoDefinition").unwrap();
    assert!(result.find("Acme\\DemoDefinition").unwrap() < result.find("when@test:").unwrap());
}

#[test]
fn remove_service_configuration_formats() {
    let definition = "Acme\\Demo\\ExampleDefinition";
    let tests = [
        (
            "services.xml",
            "<?xml version=\"1.0\"?><container><services>\n  <service id=\"Acme\\Other\"/>\n  <service id=\"Acme\\Demo\\ExampleDefinition\"><tag name=\"shopware.entity.definition\"/></service>\n</services></container>",
        ),
        (
            "services.yaml",
            "services:\n  Acme\\Other: ~\n  Acme\\Demo\\ExampleDefinition:\n    tags:\n      - { name: shopware.entity.definition }\n",
        ),
        (
            "services.php",
            "<?php\nreturn static function ($container, $services): void {\n    $services->set(\\Acme\\Other::class);\n    $services->set(\\Acme\\Demo\\ExampleDefinition::class)->tag('shopware.entity.definition');\n};\n",
        ),
    ];
    for (path, source) in tests {
        let result = remove_service_configuration(path, source, definition).unwrap();
        assert!(!result.contains(definition), "{path}: {result}");
        assert!(result.contains("Acme\\Other"), "{path}");
        let again = remove_service_configuration(path, &result, definition).unwrap();
        assert_eq!(result, again, "{path}");
    }
}
