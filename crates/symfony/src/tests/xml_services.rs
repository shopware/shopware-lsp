use crate::parse_xml_services;

struct Case {
    name: &'static str,
    xml: &'static str,
    services: usize,
    aliases: usize,
    parameters: usize,
    tags: &'static [(&'static str, &'static [&'static str])],
}

const CASES: &[Case] = &[
    Case {
        name: "Basic service",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1" />
</container>"#,
        services: 1,
        aliases: 0,
        parameters: 0,
        tags: &[],
    },
    Case {
        name: "Service with tags",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1">
        <tag name="app.tag" />
    </service>
</container>"#,
        services: 1,
        aliases: 0,
        parameters: 0,
        tags: &[("app.service1", &["app.tag"])],
    },
    Case {
        name: "Service with multiple tags",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1">
        <tag name="app.tag1" />
        <tag name="app.tag2" />
    </service>
</container>"#,
        services: 1,
        aliases: 0,
        parameters: 0,
        tags: &[("app.service1", &["app.tag1", "app.tag2"])],
    },
    Case {
        name: "Multiple services",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1" />
    <service id="app.service2" class="App\Service\Service2" />
</container>"#,
        services: 2,
        aliases: 0,
        parameters: 0,
        tags: &[],
    },
    Case {
        name: "Services with aliases",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1" />
    <alias id="app.alias1" service="app.service1" />
</container>"#,
        services: 1,
        aliases: 1,
        parameters: 0,
        tags: &[],
    },
    Case {
        name: "Complex XML with services, tags, and aliases",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service id="app.service1" class="App\Service\Service1">
        <tag name="app.tag1" />
        <tag name="app.tag2" />
    </service>
    <service id="app.service2" class="App\Service\Service2">
        <tag name="app.tag3" />
    </service>
    <alias id="app.alias1" service="app.service1" />
    <alias id="app.alias2" service="app.service2" />
</container>"#,
        services: 2,
        aliases: 2,
        parameters: 0,
        tags: &[
            ("app.service1", &["app.tag1", "app.tag2"]),
            ("app.service2", &["app.tag3"]),
        ],
    },
    Case {
        name: "Symfony namespaced XML with nested services",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container xmlns="http://symfony.com/schema/dic/services"
    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
    xsi:schemaLocation="http://symfony.com/schema/dic/services
        https://symfony.com/schema/dic/services/services-1.0.xsd">

    <services>
        <!-- Default configuration for services in *this* file -->
        <defaults autowire="true" autoconfigure="true"/>

        <!-- makes classes in src/ available to be used as services -->
        <!-- this creates a service per class whose id is the fully-qualified class name -->
        <prototype namespace="App\" resource="../src/" exclude="../src/{DependencyInjection,Entity,Kernel.php}"/>

        <service id="App\Service\SiteUpdateManager">
            <argument key="$adminEmail">manager@example.com</argument>
        </service>

        <service id="bla">
            <argument type="service" id=""/>
        </service>
    </services>
</container>"#,
        services: 2,
        aliases: 0,
        parameters: 0,
        tags: &[],
    },
    Case {
        name: "Container with parameters",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <parameters>
        <parameter key="database_host">localhost</parameter>
        <parameter key="database_port">3306</parameter>
        <parameter key="database_name">app</parameter>
    </parameters>
    <service id="app.service1" class="App\Service\Service1" />
</container>"#,
        services: 1,
        aliases: 0,
        parameters: 3,
        tags: &[],
    },
    Case {
        name: "Container with parameter and value attribute",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <parameters>
        <parameter key="app.debug" value="true" />
    </parameters>
</container>"#,
        services: 0,
        aliases: 0,
        parameters: 1,
        tags: &[],
    },
    Case {
        name: "Container with service reference parameter",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <parameters>
        <parameter key="app.manager" type="service" id="app.service.manager" />
    </parameters>
    <service id="app.service.manager" class="App\Service\Manager" />
</container>"#,
        services: 1,
        aliases: 0,
        parameters: 1,
        tags: &[],
    },
    Case {
        name: "Non-service XML - HTML document",
        xml: r#"<!DOCTYPE html>
<html>
<head>
    <title>Test HTML</title>
</head>
<body>
    <h1>This is not a service file</h1>
    <p>Just a regular HTML document</p>
</body>
</html>"#,
        services: 0,
        aliases: 0,
        parameters: 0,
        tags: &[],
    },
    Case {
        name: "XML without container tag",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<config>
    <parameters>
        <parameter name="test">value</parameter>
    </parameters>
</config>"#,
        services: 0,
        aliases: 0,
        parameters: 0,
        tags: &[],
    },
    Case {
        name: "Empty XML",
        xml: "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<container>\n</container>",
        services: 0,
        aliases: 0,
        parameters: 0,
        tags: &[],
    },
    Case {
        name: "Services with missing attributes",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?>
<container>
    <service />
    <service id="" />
    <service class="App\Service\MissingId" />
    <alias />
    <alias id="missing.service.reference" />
</container>"#,
        services: 0,
        aliases: 0,
        parameters: 0,
        tags: &[],
    },
    Case {
        name: "Malformed XML",
        xml: r#"<?xml version="1.0" encoding="UTF-8" ?><container><service></container>"#,
        services: 0,
        aliases: 0,
        parameters: 0,
        tags: &[],
    },
];

#[test]
fn parse_xml_services_cases() {
    for case in CASES {
        let (services, parameters) = parse_xml_services("test.xml", case.xml);
        let aliases = services
            .iter()
            .filter(|service| !service.alias_target.is_empty())
            .count();
        assert_eq!(
            services.len() - aliases,
            case.services,
            "{}: services",
            case.name
        );
        assert_eq!(aliases, case.aliases, "{}: aliases", case.name);
        assert_eq!(
            parameters.len(),
            case.parameters,
            "{}: parameters",
            case.name
        );
        for (id, tags) in case.tags {
            let service = services
                .iter()
                .find(|service| service.id == *id)
                .unwrap_or_else(|| panic!("{}: service {id}", case.name));
            for tag in *tags {
                assert!(service.tags.contains_key(*tag), "{}: tag {tag}", case.name);
            }
            assert_eq!(service.tags.len(), tags.len(), "{}: tag count", case.name);
        }
    }
}

#[test]
fn parse_xml_deprecated_service_metadata() {
    let (services, _) = parse_xml_services(
        "services.xml",
        r#"<container><services>
<service id="legacy" class="App\Legacy" deprecated="Use App\Modern instead"/>
<service id="flagged" class="App\Flagged" deprecated="true"/>
<service id="active" class="App\Active" deprecated="false"/>
<service id="compiled.legacy" class="App\CompiledLegacy">
  <deprecated package="app/package" version="2.0">The "%service_id%" service is deprecated.</deprecated>
</service>
<service id="compiled.alias" alias="modern.service">
  <deprecated package="app/package" version="2.0">Replace %alias_id% with modern.service.</deprecated>
</service>
<alias id="legacy.alias" service="modern.service" deprecated="Use modern.service"/>
</services></container>"#,
    );
    assert_eq!(services.len(), 6);
    assert!(services[0].deprecated);
    assert_eq!(services[0].deprecation, "Use App\\Modern instead");
    assert!(!services[0].deprecated_range.is_empty());
    assert!(services[1].deprecated);
    assert!(services[1].deprecation.is_empty());
    assert!(!services[2].deprecated);
    assert!(services[3].deprecated);
    assert_eq!(
        services[3].deprecation,
        r#"The "%service_id%" service is deprecated."#
    );
    assert!(!services[3].deprecated_range.is_empty());
    assert!(services[4].deprecated);
    assert_eq!(services[4].alias_target, "modern.service");
    assert!(services[4].class.is_empty());
    assert!(services[5].deprecated);
    assert_eq!(services[5].deprecation, "Use modern.service");
}
