use std::collections::HashMap;

use swlsp_syntax::TextRange;

use crate::{ContainerTwigComponent, parse_xml_twig_components, parse_xml_twig_globals};

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

#[test]
fn parse_xml_twig_globals_reads_add_global_calls() {
    let source = r#"<container>
  <services>
    <service id="twig">
      <call method="addGlobal">
        <argument>app</argument>
        <argument type="service" id="App\Twig\AppVariable"/>
      </call>
      <call method="addGlobal">
        <argument>site_name</argument>
        <argument>Shop</argument>
      </call>
    </service>
  </services>
</container>"#;
    let globals = parse_xml_twig_globals("/project/var/cache/Container.xml", source);
    assert_eq!(globals.len(), 2);
    assert_eq!(globals[0].name, "app");
    assert_eq!(globals[0].service_id, "App\\Twig\\AppVariable");
    assert_eq!(slice(source, globals[0].range), "app");
    assert_eq!(globals[1].name, "site_name");
    assert_eq!(globals[1].value, "Shop");
    assert_eq!(
        usize::from(globals[1].range.start()),
        source.find("site_name").unwrap()
    );
}

#[test]
fn parse_xml_twig_components_reads_factory_metadata() {
    let source = r#"<container>
  <services>
    <service id="ux.twig_component.component_factory">
      <argument><argument key="nested">ignored</argument></argument>
      <argument/>
      <argument/>
      <argument/>
      <argument type="collection">
        <argument key="Shop:Card" type="collection">
          <argument key="class">\App\Twig\Components\ShopCard\</argument>
          <argument key="template">components/shop/Card.html.twig</argument>
          <argument key="mount" type="collection">
            <argument key="template">wrong/nested.html.twig</argument>
          </argument>
        </argument>
        <argument key="OuterName" type="collection">
          <argument key="key">Renamed</argument>
          <argument key="template">components/Renamed.html.twig</argument>
        </argument>
        <argument key="DynamicCard" type="collection">
          <argument key="class">App\Twig\Components\DynamicCard</argument>
          <argument key="template_from_method">getTemplate</argument>
        </argument>
      </argument>
      <argument type="collection">
        <argument key="App\Twig\Components\Renamed">Renamed</argument>
        <argument key="\App\Twig\Components\ClassOnly\">ClassOnly</argument>
      </argument>
    </service>
  </services>
</container>"#;
    let path = "/project/var/cache/dev/Container.xml";
    let components = parse_xml_twig_components(path, source);
    assert_eq!(components.len(), 4);
    let by_name: HashMap<&str, &ContainerTwigComponent> = components
        .iter()
        .inspect(|component| assert_eq!(component.path, path))
        .map(|component| (component.name.as_str(), component))
        .collect();
    let shop = by_name["Shop:Card"];
    assert_eq!(shop.class, "App\\Twig\\Components\\ShopCard");
    assert_eq!(shop.template, "components/shop/Card.html.twig");
    assert_eq!(slice(source, shop.name_range), "Shop:Card");
    assert_eq!(
        slice(source, shop.class_range),
        r"\App\Twig\Components\ShopCard\"
    );
    assert_eq!(
        slice(source, shop.template_range),
        "components/shop/Card.html.twig"
    );
    let renamed = by_name["Renamed"];
    assert_eq!(renamed.class, "App\\Twig\\Components\\Renamed");
    assert_eq!(renamed.template, "components/Renamed.html.twig");
    assert_eq!(slice(source, renamed.name_range), "Renamed");
    let dynamic = by_name["DynamicCard"];
    assert!(dynamic.template.is_empty());
    assert_eq!(dynamic.template_from_method, "getTemplate");
    let class_only = by_name["ClassOnly"];
    assert_eq!(class_only.class, "App\\Twig\\Components\\ClassOnly");
    assert!(class_only.template.is_empty());
}

#[test]
fn parse_xml_twig_components_ignores_unrelated_or_incomplete_services() {
    let source = r#"<container><services>
  <service id="other">
    <argument/><argument/><argument/><argument/>
    <argument><argument key="Wrong"/></argument>
  </service>
  <service id="ux.twig_component.component_factory">
    <argument/><argument/><argument/><argument/>
  </service>
</services></container>"#;
    assert!(parse_xml_twig_components("container.xml", source).is_empty());
}
