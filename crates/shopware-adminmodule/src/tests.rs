use crate::config::{ConfigError, parse};
use crate::resolve::{Input, Module, Problem};
use crate::{Entity, EntityField, GeneratedFile, Kind, Style, render, resolve};

fn field(name: &str, kind: Kind) -> EntityField {
    EntityField {
        name: name.to_owned(),
        kind,
        ..EntityField::default()
    }
}

fn review() -> Entity {
    Entity {
        name: "acme_review".to_owned(),
        fields: vec![
            field("id", Kind::Other),
            EntityField {
                required: true,
                translated: true,
                ..field("title", Kind::String)
            },
            EntityField {
                allow_html: true,
                translated: true,
                ..field("text", Kind::LongText)
            },
            field("rating", Kind::Int),
            field("active", Kind::Bool),
            EntityField {
                choices: vec!["open".to_owned(), "done".to_owned()],
                ..field("status", Kind::String)
            },
            field("productId", Kind::Other),
            EntityField {
                foreign_key: "productId".to_owned(),
                target_entity: "product".to_owned(),
                target_label: "name".to_owned(),
                ..field("product", Kind::ManyToOne)
            },
            EntityField {
                read_only: true,
                ..field("score", Kind::Float)
            },
            field("publishedAt", Kind::DateTime),
            field("createdAt", Kind::Timestamp),
            field("updatedAt", Kind::Timestamp),
        ],
    }
}

fn rendered(source: &str, style: Style) -> Vec<GeneratedFile> {
    let config = parse(source).unwrap();
    let (name, module) = config.modules.iter().next().unwrap();
    let (module, problems) = resolve(name, module, &review());
    assert_eq!(problems, [], "problems");
    render(&module, style)
}

const FULL: &str = r##"
version: 1
modules:
  acme-review:
    entity: acme_review
    title: { en-GB: Reviews, de-DE: Bewertungen }
    icon: regular-star
    color: "#ff68b4"
    labelField: title
    navigation: { parent: sw-catalogue, position: 50 }
    locales: { en-GB: {}, de-DE: {}, fr-FR: {} }
    listing:
      limit: 50
      sorting: { field: rating, direction: DESC }
      columns:
        rating: { position: 5, inlineEdit: true, label: { de-DE: Sterne } }
        createdAt: { enable: false }
        status: { sortable: false, align: center }
    detail:
      cards:
        general:
          title: { en-GB: Basics }
          fields:
            rating:
              position: 1
              required: true
              helpText: { en-GB: One to five }
              component: { type: number, props: { min: 1, max: 5, step: 1, fillDigits: true, suffix: stars } }
            score: { enable: false }
        content:
          position: 20
          fields:
            text: { component: { type: textarea }, placeholder: { en-GB: Write } }
            status:
              component:
                type: select
                options:
                  open: { label: { en-GB: Open, de-DE: Offen } }
                  closed: { position: 1 }
            product: { component: { type: entity-select, labelProperty: productNumber } }
            active: { component: { type: custom, name: acme-toggle } }
"##;

/// Every generated file is read without a syntax error by the parser of
/// its language.
#[test]
fn generated_files_have_no_syntax_errors() {
    let minimal = "modules:\n  acme-review:\n    entity: acme_review\n";
    let unmanaged = "modules:\n  acme-review:\n    entity: acme_review\n    acl: { enable: false }\n    navigation: { enable: false }\n    listing: { managed: false }\n    detail: { managed: false }\n";
    for source in [minimal, FULL, unmanaged] {
        for style in [Style::Meteor, Style::Classic] {
            for file in rendered(source, style) {
                let parse = swlsp_language::LanguageId::from_path(&file.path)
                    .and_then(|language| language.parse(&file.content))
                    .unwrap_or_else(|| panic!("no parser for {}", file.path));
                let errors: Vec<String> = parse
                    .errors
                    .iter()
                    .map(|error| error.message().to_owned())
                    .collect();
                assert_eq!(
                    errors,
                    Vec::<String>::new(),
                    "{}\n{}",
                    file.path,
                    file.content
                );
                assert!(file.content.ends_with('\n') && !file.content.ends_with("\n\n"));
                assert!(
                    !file.content.lines().any(|line| line.ends_with(' ')),
                    "{}",
                    file.path
                );
            }
        }
    }
}

fn resolved(source: &str) -> (Module, Vec<Problem>) {
    let config = parse(source).unwrap();
    let (name, module) = config.modules.iter().next().unwrap();
    resolve(name, module, &review())
}

fn content(files: &[GeneratedFile], path: &str) -> String {
    files
        .iter()
        .find(|file| file.path == path)
        .unwrap_or_else(|| panic!("no file {path}"))
        .content
        .clone()
}

#[test]
fn a_module_is_derived_from_its_entity() {
    let (module, problems) = resolved("modules:\n  acme-review:\n    entity: acme_review\n");
    assert_eq!(problems, []);
    assert_eq!(module.label_field.as_deref(), Some("title"));
    assert!(module.translated);
    assert_eq!(module.locales, ["de-DE", "en-GB"]);
    assert_eq!(module.title["en-GB"], "Acme review");
    // Scalar fields are columns; long texts, associations and what the
    // server writes are not. The label field links to the record.
    let columns: Vec<(&str, bool)> = module
        .listing
        .columns
        .iter()
        .map(|column| (column.property.as_str(), column.primary))
        .collect();
    assert_eq!(
        columns,
        [
            ("title", true),
            ("rating", false),
            ("active", false),
            ("status", false),
            ("publishedAt", false),
            ("createdAt", false),
        ]
    );
    assert_eq!(
        (module.listing.sort_by.as_str(), module.listing.descending),
        ("createdAt", true)
    );
    // One card with every field a form has an input for; the association
    // stands for its foreign key.
    let [card] = module.detail.cards.as_slice() else {
        panic!("{:?}", module.detail.cards);
    };
    let fields: Vec<(&str, &str)> = card
        .fields
        .iter()
        .map(|field| (field.name.as_str(), field.model.as_str()))
        .collect();
    assert_eq!(
        fields,
        [
            ("title", "title"),
            ("text", "text"),
            ("rating", "rating"),
            ("active", "active"),
            ("status", "status"),
            ("product", "productId"),
            ("publishedAt", "publishedAt"),
        ]
    );
    assert_eq!(card.fields[1].input, Input::TextEditor);
    assert!(card.fields[0].required && card.fields[0].translated);
    let acl = module.acl.unwrap();
    assert_eq!((acl.parent, acl.read), (None, vec!["product".to_owned()]));
}

#[test]
fn the_description_overrides_what_is_derived() {
    let (module, problems) = resolved(FULL);
    assert_eq!(problems, []);
    assert_eq!(module.locales, ["de-DE", "en-GB", "fr-FR"]);
    // A locale without a text takes the one of the main locale.
    assert_eq!(module.title["fr-FR"], "Reviews");
    let columns: Vec<&str> = module
        .listing
        .columns
        .iter()
        .map(|column| column.property.as_str())
        .collect();
    assert_eq!(
        columns,
        ["rating", "title", "active", "status", "publishedAt"]
    );
    assert_eq!(module.listing.columns[0].inline_edit, Some("number"));
    assert_eq!(module.listing.columns[0].label["de-DE"], "Sterne");
    assert_eq!(module.listing.columns[0].label["en-GB"], "Rating");
    let cards: Vec<(&str, Vec<&str>)> = module
        .detail
        .cards
        .iter()
        .map(|card| {
            (
                card.name.as_str(),
                card.fields
                    .iter()
                    .map(|field| field.name.as_str())
                    .collect(),
            )
        })
        .collect();
    // A field named in a card leaves the default one.
    assert_eq!(
        cards,
        [
            ("general", vec!["rating", "title", "publishedAt"]),
            ("content", vec!["text", "active", "status", "product"]),
        ]
    );
    assert_eq!(module.acl.unwrap().parent.as_deref(), Some("catalogues"));
    assert_eq!(module.navigation.unwrap().position, 50);
}

#[test]
fn without_auto_only_what_is_named_is_generated() {
    let (module, problems) = resolved(
        "modules:\n  acme-review:\n    entity: acme_review\n    listing:\n      auto: false\n      columns: { rating: {} }\n    detail:\n      auto: false\n      cards: { main: { fields: { title: {} } } }\n",
    );
    assert_eq!(problems, []);
    assert_eq!(module.listing.columns.len(), 1);
    let cards: Vec<&str> = module
        .detail
        .cards
        .iter()
        .map(|card| card.name.as_str())
        .collect();
    assert_eq!(cards, ["main"]);
}

#[test]
fn what_does_not_fit_the_entity_is_a_problem() {
    let (_, problems) = resolved(
        "modules:\n  Review:\n    entity: acme_review\n    labelField: nope\n    listing:\n      sorting: { field: nope }\n      columns:\n        text: { inlineEdit: true }\n    detail:\n      cards:\n        a-b: {}\n        one: { fields: { title: {}, score: {}, status: { component: { type: select, options: {} } } } }\n        two: { fields: { title: {} } }\n",
    );
    let found: Vec<(String, &str)> = problems
        .iter()
        .map(|problem| (problem.path[2..].join("."), problem.message.as_str()))
        .collect();
    assert_eq!(
        found,
        [
            (
                String::new(),
                "Module name \"Review\" must be lower-case words joined by dashes, with a prefix: \"acme-review\""
            ),
            (
                "detail.cards.a-b".to_owned(),
                "Card name \"a-b\" may hold letters, digits and underscores only"
            ),
            (
                "labelField".to_owned(),
                "Entity \"acme_review\" has no field \"nope\""
            ),
            (
                "listing.columns.text.inlineEdit".to_owned(),
                "Field \"text\" cannot be edited in a list"
            ),
            (
                "listing.sorting.field".to_owned(),
                "Entity \"acme_review\" has no field \"nope\""
            ),
            (
                "detail.cards.two.fields.title".to_owned(),
                "Field \"title\" is in card \"one\" already"
            ),
            (
                "detail.cards.one.fields.score".to_owned(),
                "Field \"score\" has no input a form can be generated with; name one with \"component\""
            ),
        ]
    );
}

#[test]
fn the_format_refuses_what_it_does_not_know() {
    // A file that does not parse describes nothing, not "no modules".
    assert!(parse("modules: {\"acme-review\":{\"entity\":\"acme_review\"}}\n}").is_err());
    assert_eq!(parse("").unwrap().version, 1);
    assert_eq!(
        parse("version: 2\n"),
        Err(ConfigError::Version { found: 2 })
    );
    for source in [
        "modules: { acme-review: { entity: a, unknown: 1 } }",
        "modules: { acme-review: { listing: { columns: [title] } } }",
        "modules: { acme-review: { detail: { cards: { c: { fields: { f: { component: { type: slider } } } } } } } }",
        "modules: { acme-review: { detail: { cards: { c: { fields: { f: { component: { type: text, rows: 2 } } } } } } } }",
    ] {
        assert!(
            matches!(parse(source), Err(ConfigError::Decode(_))),
            "{source}"
        );
    }
}

#[test]
fn the_pages_follow_the_style_and_the_roles() {
    let minimal = "modules:\n  acme-review:\n    entity: acme_review\n";
    let meteor = rendered(minimal, Style::Meteor);
    let classic = rendered(minimal, Style::Classic);
    let detail = "generated/page/acme-review-detail/acme-review-detail.html.twig";
    assert!(
        content(&meteor, detail)
            .contains("<mt-text-field\n                        v-model=\"item.title\"")
    );
    assert!(
        content(&classic, detail)
            .contains("<sw-text-field\n                        v-model:value=\"item.title\"")
    );
    let script = "generated/page/acme-review-detail/index.js";
    assert!(
        content(&meteor, script)
            .contains("Shopware.Store.get('context').resetLanguageToDefault();")
    );
    assert!(
        content(&classic, script)
            .contains("Shopware.State.commit('context/resetLanguageToDefault');")
    );
    // Every field sits in a block named after its card and itself.
    assert!(
        content(&meteor, detail)
            .contains("{% block acme_review_detail_card_general_field_published_at %}")
    );

    // Without roles nothing asks the access control, and no file declares
    // them; a page taken over lies outside the generated directory.
    let open = rendered(
        "modules:\n  acme-review:\n    entity: acme_review\n    acl: { enable: false }\n    listing: { managed: false }\n",
        Style::Meteor,
    );
    let paths: Vec<(&str, bool)> = open
        .iter()
        .map(|file| (file.path.as_str(), file.once))
        .collect();
    assert_eq!(
        paths,
        [
            ("generated/index.js", false),
            (
                "generated/page/acme-review-detail/acme-review-detail.html.twig",
                false
            ),
            ("generated/page/acme-review-detail/index.js", false),
            ("generated/snippet/de-DE.json", false),
            ("generated/snippet/en-GB.json", false),
            ("page/acme-review-list/acme-review-list.html.twig", true),
            ("page/acme-review-list/index.js", true),
        ]
    );
    assert!(open.iter().all(|file| !file.content.contains("acl")), "acl");
    assert!(content(&open, "generated/index.js").contains("import('../page/acme-review-list')"));
}

#[test]
fn snippets_hold_every_text_in_sorted_order() {
    let files = rendered(FULL, Style::Meteor);
    let german: serde_json::Value =
        serde_json::from_str(&content(&files, "generated/snippet/de-DE.json")).unwrap();
    let module = &german["acme-review"];
    assert_eq!(module["general"]["title"], "Bewertungen");
    assert_eq!(module["list"]["columns"]["rating"], "Sterne");
    assert_eq!(module["list"]["buttonCreate"], "Hinzufügen");
    assert_eq!(module["detail"]["cards"]["general"]["title"], "Basics");
    assert_eq!(
        module["detail"]["fields"]["rating"]["helpText"],
        "One to five"
    );
    assert_eq!(
        module["detail"]["fields"]["status"]["options"]["open"],
        "Offen"
    );
    assert_eq!(
        module["detail"]["fields"]["status"]["options"]["closed"],
        "Closed"
    );
    assert_eq!(
        german["sw-privileges"]["permissions"]["acme_review"]["label"],
        "Bewertungen"
    );
    let english = content(&files, "generated/snippet/en-GB.json");
    assert!(english.find("\"detail\"").unwrap() < english.find("\"general\"").unwrap());
}
