use swlsp_uriutil::file_uri;

use super::*;
use crate::php_common::test_support::*;

#[test]
fn container_constant_definition_for_yaml_and_xml() {
    let root = tempfile::tempdir().unwrap();
    let php_path = root.path().join("src/Mode.php");
    let php_source = r"<?php
namespace App;
class Mode {
    public const ACTIVE = 'active';
}
enum State {
    case ACTIVE;
}
";
    write(&php_path, php_source);
    let php = php_index(&root.path().join("cache"));
    index(&php, &p(&php_path), php_source);
    let provider = ContainerConstantDefinitionProvider::new(Some(php));

    let yaml = file_uri(&p(&root.path().join("config/services.yaml")));
    let xml = file_uri(&p(&root.path().join("config/services.xml")));
    for (uri, source) in [
        (&yaml, r"value: !php/const App\Mode::ACTIVE"),
        (&yaml, r"value: !php/enum App\State::ACTIVE->value"),
        (&yaml, r"value: !php/enum App\State"),
        (
            &xml,
            r#"<container><services><service id="app">
<argument type="constant">App\Mode::ACTIVE</argument>
</service></services></container>"#,
        ),
    ] {
        let document = document(uri, source);
        let target = if source.contains("ACTIVE") {
            "ACTIVE"
        } else {
            "State"
        };
        let offset = index_of(source, target) + 2;
        let locations = definitions(&provider, &request_at(&document, offset));
        assert_eq!(locations.len(), 1, "{uri}: {source}");
        assert_eq!(locations[0].uri, file_uri(&p(&php_path)));
        assert_eq!(location_text(php_source, &locations[0]), target);
    }
}
