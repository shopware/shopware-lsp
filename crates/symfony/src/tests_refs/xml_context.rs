use swlsp_parser_xml::query as xmlquery;

use super::*;

#[test]
fn xml_service_contexts() {
    let parse = swlsp_parser_xml::parse(
        r#"<service id="App\Service"><argument type="service" id="Other\Service"/><argument type="tagged_iterator" tag="app.handler"/><argument>%app.parameter%</argument><tag name="kernel.event_subscriber"/></service>"#,
    );
    let root = parse.root();
    let service = xmlquery::elements(&root, &["service"]).remove(0);
    assert!(xml_service_is_service_id(
        &xmlquery::attribute(&service, "id").unwrap()
    ));
    let arguments = xmlquery::child_elements(&service, &["argument"]);
    assert!(xml_service_is_service_reference(
        &xmlquery::attribute(&arguments[0], "id").unwrap()
    ));
    assert_eq!(xml_current_service_id(&arguments[0]), "App\\Service");
    assert!(xml_service_is_argument_tag(
        &xmlquery::attribute(&arguments[1], "tag").unwrap()
    ));
    assert!(xml_service_is_parameter_reference(&arguments[2]));
    assert_eq!(xml_parameter_reference_name(&arguments[2]), "app.parameter");
    let tag = xmlquery::child_element(&service, &["tag"]).unwrap();
    assert!(xml_service_is_tag_element(
        &xmlquery::attribute(&tag, "name").unwrap()
    ));
}

#[test]
fn xml_service_reference_variants() {
    let parse = swlsp_parser_xml::parse(
        r#"<container>
<services>
  <service id="decorator" class="App\Decorator" decorates="app.target">
    <factory service="@app.factory"/>
    <argument type="service" id="@?app.optional" on-invalid="ignore"/>
  </service>
  <alias id="alias" service="app.target"/>
</services>
</container>"#,
    );
    let root = parse.root();
    let service = xmlquery::elements(&root, &["service"]).remove(0);
    assert_eq!(
        xml_service_reference_name(&xmlquery::attribute(&service, "decorates").unwrap()).as_deref(),
        Some("app.target")
    );
    let factory = xmlquery::child_element(&service, &["factory"]).unwrap();
    assert_eq!(
        xml_service_reference_name(&xmlquery::attribute(&factory, "service").unwrap()).as_deref(),
        Some("app.factory")
    );
    let argument = xmlquery::child_element(&service, &["argument"]).unwrap();
    assert_eq!(
        xml_service_reference_name(&xmlquery::attribute(&argument, "id").unwrap()).as_deref(),
        Some("app.optional")
    );
    assert!(xml_service_reference_optional(&argument));
    let alias = xmlquery::elements(&root, &["alias"]).remove(0);
    assert_eq!(
        xml_service_reference_name(&xmlquery::attribute(&alias, "service").unwrap()).as_deref(),
        Some("app.target")
    );
    assert_eq!(
        xml_class_reference_name(&xmlquery::attribute(&service, "class").unwrap()).as_deref(),
        Some(r"App\Decorator")
    );
    let empty = swlsp_parser_xml::parse(r#"<argument type="service" id=""/>"#);
    let element = xmlquery::elements(&empty.root(), &["argument"]).remove(0);
    assert_eq!(
        xml_service_reference_name(&xmlquery::attribute(&element, "id").unwrap()).as_deref(),
        Some("")
    );
}
