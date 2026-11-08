use super::*;

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

#[test]
fn yaml_service_argument_references_cases() {
    let source = r"services:
  app.consumer:
    class: App\Consumer
    arguments:
      - scalar
      - '@app.logger'
      - '@?app.optional'
  app.named:
    class: App\Named
    arguments:
      $logger: '@app.logger'
      2: '@app.third'
  app.factory:
    class: App\FactoryProduct
    factory: ['@factory', create]
    arguments: ['@app.ignored']
    calls:
      - [setLogger, ['@app.logger']]
  app.called:
    class: App\Called
    calls:
      - setLogger: [scalar, '@app.logger']
";
    let references = yaml_service_argument_references(&swlsp_parser_yaml::parse(source).root());
    assert_eq!(references.len(), 6);
    assert_eq!(references[0].owner_service_id, "app.consumer");
    assert_eq!(references[0].owner_class, r"App\Consumer");
    assert_eq!(references[0].service_id, "app.logger");
    assert_eq!(references[0].parameter_index, 1);
    assert_eq!(slice(source, references[0].range), "@app.logger");
    assert_eq!(references[1].service_id, "app.optional");
    assert_eq!(references[2].parameter_name, "$logger");
    assert_eq!(references[2].parameter_index, -1);
    assert_eq!(references[3].parameter_index, 2);
    assert_eq!(references[4].method_name, "setLogger");
    assert_eq!(references[4].parameter_index, 0);
    assert_eq!(references[5].method_name, "setLogger");
    assert_eq!(references[5].parameter_index, 1);
}

#[test]
fn yaml_service_method_references_cases() {
    let source = r"services:
  app.called:
    class: App\Called
    calls:
      - [setLogger, ['@app.logger']]
      - initialize: []
    tags:
      - { name: kernel.event_listener, method: onKernelRequest }
  app.factory:
    class: App\Product
    factory: ['@app.builder', create]
  app.static_factory:
    class: App\Product
    factory: 'App\Factory::build'
  app.legacy_factory:
    class: App\Product
    factory_service: app.builder
    factory_method: legacyCreate
";
    let references = yaml_service_method_references(&swlsp_parser_yaml::parse(source).root());
    assert_eq!(references.len(), 6);
    assert_eq!(references[0].owner_service_id, "app.called");
    assert_eq!(references[0].owner_class, r"App\Called");
    assert_eq!(references[0].method_name, "setLogger");
    assert_eq!(slice(source, references[0].range), "setLogger");
    assert_eq!(references[1].method_name, "initialize");
    assert_eq!(references[2].method_name, "onKernelRequest");
    assert_eq!(references[3].method_name, "create");
    assert!(references[3].factory);
    assert_eq!(references[3].receiver(), ("app.builder", ""));
    assert_eq!(references[4].method_name, "build");
    assert_eq!(references[4].receiver(), ("", r"App\Factory"));
    assert_eq!(slice(source, references[4].range), "build");
    assert_eq!(references[5].method_name, "legacyCreate");
    assert_eq!(references[5].receiver().0, "app.builder");
}

#[test]
fn yaml_service_method_reference_at_includes_empty_scalars() {
    for (source, needle) in [
        (
            "services:\n  app.called:\n    class: App\\Called\n    calls:\n      - ['', []]\n",
            "''",
        ),
        (
            "services:\n  app.called:\n    class: App\\Called\n    calls:\n      - [, []]\n",
            "[,",
        ),
        (
            "services:\n  app.factory:\n    class: App\\Product\n    factory: ['@app.builder', '']\n",
            "''",
        ),
        (
            "services:\n  app.factory:\n    class: App\\Product\n    factory: 'app.builder:'\n",
            ":'",
        ),
    ] {
        let offset = (source.find(needle).unwrap() + 1) as u32;
        let reference =
            yaml_service_method_reference_at(&swlsp_parser_yaml::parse(source).root(), offset)
                .unwrap_or_else(|| panic!("{source}"));
        assert!(reference.method_name.is_empty());
    }
}

#[test]
fn xml_service_argument_references_cases() {
    let source = r#"<container><services>
  <service id="app.consumer" class="App\Consumer">
    <argument>scalar</argument>
    <argument type="service" id="app.logger"/>
    <argument type="service" id="app.named" key="$named"/>
    <argument type="service" id="app.third" key="3"/>
  </service>
  <service id="app.factory" class="App\FactoryProduct">
    <factory service="factory"/>
    <argument type="service" id="app.ignored"/>
    <call method="setLogger">
      <argument type="service" id="app.logger"/>
    </call>
  </service>
  <service id="app.called" class="App\Called">
    <call method="setLogger">
      <argument>scalar</argument>
      <argument type="service" id="app.logger"/>
    </call>
  </service>
</services></container>"#;
    let references = xml_service_argument_references(&swlsp_parser_xml::parse(source).root());
    assert_eq!(references.len(), 5);
    assert_eq!(references[0].service_id, "app.logger");
    assert_eq!(references[0].parameter_index, 1);
    assert_eq!(slice(source, references[0].range), "app.logger");
    assert_eq!(references[1].parameter_name, "$named");
    assert_eq!(references[1].parameter_index, -1);
    assert_eq!(references[2].parameter_index, 3);
    assert_eq!(references[3].method_name, "setLogger");
    assert_eq!(references[3].parameter_index, 0);
    assert_eq!(references[4].method_name, "setLogger");
    assert_eq!(references[4].parameter_index, 1);
}

#[test]
fn xml_service_method_references_cases() {
    let source = r#"<container><services>
  <service id="app.called" class="App\Called">
    <call method="setLogger"/>
    <call method="initialize"/>
    <tag name="kernel.event_listener" method="onKernelRequest"/>
  </service>
  <service id="app.factory" class="App\Product">
    <factory service="app.builder" method="create"/>
  </service>
  <service id="app.static_factory" class="App\Product">
    <factory class="App\Factory" method="build"/>
  </service>
  <service id="app.legacy_factory" class="App\Product"
           factory-service="app.builder" factory-method="legacyCreate"/>
</services></container>"#;
    let references = xml_service_method_references(&swlsp_parser_xml::parse(source).root());
    assert_eq!(references.len(), 6);
    assert_eq!(references[0].owner_service_id, "app.called");
    assert_eq!(references[0].owner_class, r"App\Called");
    assert_eq!(references[0].method_name, "setLogger");
    assert_eq!(slice(source, references[0].range), "setLogger");
    assert_eq!(references[1].method_name, "initialize");
    assert_eq!(references[2].method_name, "onKernelRequest");
    assert_eq!(references[3].receiver(), ("app.builder", ""));
    assert_eq!(references[4].receiver(), ("", r"App\Factory"));
    assert_eq!(references[5].receiver().0, "app.builder");
}

#[test]
fn xml_service_method_reference_at_includes_empty_attributes() {
    let source = "<container><services>\n  <service id=\"app.called\" class=\"App\\Called\">\n    <call method=\"\"/>\n  </service>\n</services></container>";
    let offset = (source.find("method=\"\"").unwrap() + "method=\"".len()) as u32;
    let reference =
        xml_service_method_reference_at(&swlsp_parser_xml::parse(source).root(), offset).unwrap();
    assert!(reference.method_name.is_empty());
}

#[test]
fn numeric_index_matches_atoi() {
    assert_eq!(numeric_index("3"), 3);
    assert_eq!(numeric_index("+4"), 4);
    assert_eq!(numeric_index("-1"), -1);
    assert_eq!(numeric_index("-0"), 0);
    assert_eq!(numeric_index(""), -1);
    assert_eq!(numeric_index("x"), -1);
}
