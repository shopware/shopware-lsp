//! The templates of the pages: Twig blocks around Vue markup.

use serde_json::Value;

use super::{Names, Style, attribute, capitalized, kebab, options_name, snake};
use crate::config::NumberType;
use crate::resolve::{Card, Field, Input};

/// Lines of a template: Twig blocks around Vue markup.
#[derive(Default)]
struct Template {
    lines: Vec<String>,
    depth: usize,
}

impl Template {
    fn line(&mut self, text: &str) {
        self.lines
            .push(format!("{}{text}", "    ".repeat(self.depth)));
    }

    /// A block, written at the indentation of what it holds.
    fn block(&mut self, name: &str, body: impl FnOnce(&mut Self)) {
        // Neighbouring blocks are set apart.
        if self
            .lines
            .last()
            .is_some_and(|line| line.ends_with("{% endblock %}"))
        {
            self.lines.push(String::new());
        }
        self.line(&format!("{{% block {name} %}}"));
        body(self);
        self.line("{% endblock %}");
    }

    fn open(&mut self, tag: &str, attributes: &[String]) {
        self.tag(tag, attributes, ">");
        self.depth += 1;
    }

    fn close(&mut self, tag: &str) {
        self.depth -= 1;
        self.line(&format!("</{tag}>"));
    }

    fn element(&mut self, tag: &str, attributes: &[String]) {
        self.tag(tag, attributes, " />");
    }

    fn tag(&mut self, tag: &str, attributes: &[String], end: &str) {
        match attributes {
            [] => self.line(&format!("<{tag}{end}")),
            [one] => self.line(&format!("<{tag} {one}{end}")),
            many => {
                self.line(&format!("<{tag}"));
                self.depth += 1;
                for attribute in many {
                    self.line(attribute);
                }
                self.depth -= 1;
                self.line(end.trim_start());
            }
        }
    }

    fn finish(self) -> String {
        self.lines.join("\n") + "\n"
    }
}

fn text(key: &str) -> String {
    format!("{{{{ $t('{key}') }}}}")
}

fn bound(name: &str, expression: &str) -> String {
    format!(":{name}=\"{}\"", attribute(expression))
}

fn plain(name: &str, value: &str) -> String {
    format!("{name}=\"{}\"", attribute(value))
}

fn button(style: Style) -> &'static str {
    match style {
        Style::Meteor => "mt-button",
        Style::Classic => "sw-button",
    }
}

fn card(style: Style) -> &'static str {
    match style {
        Style::Meteor => "mt-card",
        Style::Classic => "sw-card",
    }
}

fn language_switch(template: &mut Template, block: &str, attributes: &[String]) {
    template.block(&format!("{block}_language_switch"), |template| {
        template.open("template", &["#language-switch".to_owned()]);
        template.element("sw-language-switch", attributes);
        template.close("template");
    });
}

pub(super) fn list_twig(names: &Names) -> String {
    let module = names.module;
    let block = snake(&names.list());
    let class = names.list();
    let mut template = Template::default();
    template.line(&format!("{{% block {block} %}}"));
    template.open("sw-page", &[plain("class", &class)]);

    template.block(&format!("{block}_search_bar"), |template| {
        template.open("template", &["#search-bar".to_owned()]);
        template.element(
            "sw-search-bar",
            &[
                plain("initial-search-type", &module.entity),
                bound("initial-search", "term"),
                "@search=\"onSearch\"".to_owned(),
            ],
        );
        template.close("template");
    });

    template.block(&format!("{block}_smart_bar_header"), |template| {
        template.open("template", &["#smart-bar-header".to_owned()]);
        template.open("h2", &[]);
        template.line(&text(&names.snippet("general.title")));
        template.open(
            "span",
            &[
                "v-if=\"!isLoading && total\"".to_owned(),
                plain("class", "sw-page__smart-bar-amount"),
            ],
        );
        template.line("({{ total }})");
        template.close("span");
        template.close("h2");
        template.close("template");
    });

    if module.translated {
        language_switch(
            &mut template,
            &block,
            &["@on-change=\"onChangeLanguage\"".to_owned()],
        );
    }

    template.block(&format!("{block}_actions"), |template| {
        template.open("template", &["#smart-bar-actions".to_owned()]);
        template.block(&format!("{block}_actions_create"), |template| {
            let mut attributes = vec![
                plain("class", &format!("{class}__create")),
                plain("variant", "primary"),
                bound("disabled", &format!("!{}", names.can("creator"))),
                format!(
                    "@click=\"$router.push({{ name: '{}' }})\"",
                    names.route("create")
                ),
            ];
            if names.style == Style::Meteor {
                attributes.insert(2, plain("size", "default"));
            }
            template.open(button(names.style), &attributes);
            template.line(&text(&names.snippet("list.buttonCreate")));
            template.close(button(names.style));
        });
        template.close("template");
    });

    template.block(&format!("{block}_content"), |template| {
        template.open("template", &["#content".to_owned()]);
        template.block(&format!("{block}_grid"), |template| {
            template.open(
                "sw-entity-listing",
                &[
                    plain("class", &format!("{class}__grid")),
                    plain("identifier", &class),
                    plain("detail-route", &names.route("detail")),
                    bound("repository", "repository"),
                    bound(
                        match names.style {
                            Style::Meteor => "data-source",
                            Style::Classic => "items",
                        },
                        "items",
                    ),
                    bound("columns", "columns"),
                    bound("is-loading", "isLoading"),
                    bound("sort-by", "sortBy"),
                    bound("sort-direction", "sortDirection"),
                    bound("disable-data-fetching", "true"),
                    bound("show-selection", &names.can("deleter")),
                    bound("allow-view", &names.can("viewer")),
                    bound("allow-edit", &names.can("editor")),
                    bound("allow-inline-edit", &names.can("editor")),
                    bound("allow-delete", &names.can("deleter")),
                    "@column-sort=\"onSortColumn\"".to_owned(),
                    "@page-change=\"onPageChange\"".to_owned(),
                ],
            );
            template.block(&format!("{block}_grid_columns"), |_| {});
            template.close("sw-entity-listing");
        });
        template.close("template");
    });

    template.close("sw-page");
    template.line("{% endblock %}");
    template.finish()
}

/// The computed property that holds the validation error of a property.
pub(super) fn error_name(property: &str) -> String {
    format!("item{}Error", capitalized(property))
}

fn passed_prop(name: &str, value: &Value) -> String {
    let name = kebab(name);
    match value {
        Value::String(value) => plain(&name, value),
        Value::Bool(true) => name,
        other => bound(&name, &other.to_string()),
    }
}

fn field_markup(template: &mut Template, names: &Names, class: &str, field: &Field) {
    let style = names.style;
    let snippet = |part: &str| names.snippet(&format!("detail.fields.{}.{part}", field.name));
    let model = |directive: &str| format!("{directive}=\"item.{}\"", field.model);
    let meteor = style == Style::Meteor;
    let (tag, mut attributes): (String, Vec<String>) = match &field.input {
        Input::Text => (
            if meteor {
                "mt-text-field"
            } else {
                "sw-text-field"
            }
            .to_owned(),
            vec![model(names.model())],
        ),
        Input::Textarea => (
            if meteor {
                "mt-textarea"
            } else {
                "sw-textarea-field"
            }
            .to_owned(),
            vec![model(names.model())],
        ),
        Input::TextEditor => (
            if meteor {
                "mt-text-editor"
            } else {
                "sw-text-editor"
            }
            .to_owned(),
            vec![model(names.model())],
        ),
        Input::Number(kind) => (
            if meteor {
                "mt-number-field"
            } else {
                "sw-number-field"
            }
            .to_owned(),
            vec![
                model(names.model()),
                plain(
                    "number-type",
                    match kind {
                        NumberType::Int => "int",
                        NumberType::Float => "float",
                    },
                ),
            ],
        ),
        Input::Switch => (
            if meteor {
                "mt-switch"
            } else {
                "sw-switch-field"
            }
            .to_owned(),
            vec![model(names.model())],
        ),
        Input::Date | Input::Datetime => (
            if meteor {
                "mt-datepicker"
            } else {
                "sw-datepicker"
            }
            .to_owned(),
            vec![
                model(names.model()),
                plain(
                    "date-type",
                    if field.input == Input::Date {
                        "date"
                    } else {
                        "datetime"
                    },
                ),
            ],
        ),
        Input::Select(_) => {
            let mut attributes = vec![
                model(names.model()),
                bound("options", &options_name(&field.name)),
            ];
            if !meteor {
                attributes.push(plain("label-property", "label"));
                attributes.push(plain("value-property", "value"));
            }
            (
                if meteor {
                    "mt-select"
                } else {
                    "sw-single-select"
                }
                .to_owned(),
                attributes,
            )
        }
        Input::EntitySelect {
            entity,
            label_property,
        } => (
            "sw-entity-single-select".to_owned(),
            vec![
                model("v-model:value"),
                plain("entity", entity),
                plain("label-property", label_property),
                "show-clearable-button".to_owned(),
            ],
        ),
        Input::Custom { name } => (name.clone(), vec![model(names.model())]),
    };
    attributes.push(plain(
        "class",
        &format!("{class}__field-{}", kebab(&field.name)),
    ));
    attributes.push(bound("label", &format!("$t('{}')", snippet("label"))));
    let takes_placeholder = matches!(
        field.input,
        Input::Text | Input::Textarea | Input::Number(_) | Input::Custom { .. }
    );
    if takes_placeholder {
        let fallback =
            (!field.placeholder.is_empty()).then(|| format!("$t('{}')", snippet("placeholder")));
        match (field.translated, fallback) {
            (true, Some(fallback)) => attributes.push(bound(
                "placeholder",
                &format!("placeholder(item, '{}', {fallback})", field.model),
            )),
            (true, None) => attributes.push(bound(
                "placeholder",
                &format!("placeholder(item, '{}')", field.model),
            )),
            (false, Some(fallback)) => attributes.push(bound("placeholder", &fallback)),
            (false, None) => {}
        }
    }
    if !field.help_text.is_empty() {
        attributes.push(bound(
            "help-text",
            &format!("$t('{}')", snippet("helpText")),
        ));
    }
    attributes.push(bound("error", &error_name(&field.model)));
    attributes.push(bound("disabled", "!allowSave"));
    if field.required {
        attributes.push("required".to_owned());
    }
    attributes.extend(
        field
            .props
            .iter()
            .map(|(name, value)| passed_prop(name, value)),
    );
    template.element(&tag, &attributes);
}

fn card_markup(template: &mut Template, names: &Names, block: &str, class: &str, card: &Card) {
    let card_block = format!("{block}_card_{}", snake(&card.name));
    template.block(&card_block, |template| {
        let tag = self::card(names.style);
        template.open(
            tag,
            &[
                plain("class", &format!("{class}__card-{}", kebab(&card.name))),
                plain(
                    "position-identifier",
                    &format!("{class}-{}", kebab(&card.name)),
                ),
                bound(
                    "title",
                    &format!(
                        "$t('{}')",
                        names.snippet(&format!("detail.cards.{}.title", card.name))
                    ),
                ),
                bound("is-loading", "isLoading"),
            ],
        );
        for field in &card.fields {
            template.block(
                &format!("{card_block}_field_{}", snake(&field.name)),
                |template| field_markup(template, names, class, field),
            );
        }
        template.close(tag);
    });
}

pub(super) fn detail_twig(names: &Names) -> String {
    let module = names.module;
    let block = snake(&names.detail());
    let class = names.detail();
    let mut template = Template::default();
    template.line(&format!("{{% block {block} %}}"));
    template.open("sw-page", &[plain("class", &class)]);

    template.block(&format!("{block}_smart_bar_header"), |template| {
        template.open("template", &["#smart-bar-header".to_owned()]);
        template.open("h2", &[]);
        template.line("{{ identifier }}");
        template.close("h2");
        template.close("template");
    });

    if module.translated {
        language_switch(
            &mut template,
            &block,
            &[
                bound("disabled", "!entityId"),
                "@on-change=\"onChangeLanguage\"".to_owned(),
            ],
        );
    }

    template.block(&format!("{block}_actions"), |template| {
        template.open("template", &["#smart-bar-actions".to_owned()]);
        template.block(&format!("{block}_actions_cancel"), |template| {
            let mut attributes = vec![
                plain("class", &format!("{class}__cancel")),
                "@click=\"onCancel\"".to_owned(),
            ];
            if names.style == Style::Meteor {
                attributes.insert(1, plain("variant", "secondary"));
                attributes.insert(2, plain("size", "default"));
            }
            template.open(button(names.style), &attributes);
            template.line(&text("global.default.cancel"));
            template.close(button(names.style));
        });
        template.block(&format!("{block}_actions_save"), |template| {
            template.open(
                "sw-button-process",
                &[
                    plain("class", &format!("{class}__save")),
                    plain("variant", "primary"),
                    bound("is-loading", "isLoading"),
                    bound("process-success", "isSaveSuccessful"),
                    bound("disabled", "!allowSave"),
                    "@update:process-success=\"saveFinish\"".to_owned(),
                    "@click=\"onSave\"".to_owned(),
                ],
            );
            template.line(&text("global.default.save"));
            template.close("sw-button-process");
        });
        template.close("template");
    });

    template.block(&format!("{block}_content"), |template| {
        template.open("template", &["#content".to_owned()]);
        template.open("sw-card-view", &[]);
        template.open("template", &["v-if=\"item\"".to_owned()]);
        if module.translated {
            template.block(&format!("{block}_language_info"), |template| {
                template.element(
                    "sw-language-info",
                    &[bound("entity-description", "identifier")],
                );
            });
        }
        for card in &module.detail.cards {
            card_markup(template, names, &block, &class, card);
        }
        template.close("template");
        template.close("sw-card-view");
        template.close("template");
    });

    template.close("sw-page");
    template.line("{% endblock %}");
    template.finish()
}
