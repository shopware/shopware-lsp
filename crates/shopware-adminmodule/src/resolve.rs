//! A module description and its entity made into what is written: every
//! default derived, everything in its order.

use std::collections::{BTreeMap, BTreeSet};

use crate::config::{self, Align, Component, Direction, Labels, NumberType, Props};
use crate::entity::{Entity, EntityField, Kind};

/// The locale every text is derived in and the others fall back to.
pub const MAIN_LOCALE: &str = "en-GB";
const DEFAULT_LOCALES: [&str; 2] = ["de-DE", MAIN_LOCALE];

/// The card fields derived from the entity are put in.
pub const DEFAULT_CARD: &str = "general";

/// The distance between derived positions, so that something can be put
/// between two neighbours.
const POSITION_STEP: i64 = 10;

/// Something in the description that does not fit the entity, by the keys
/// that lead to it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Problem {
    pub path: Vec<String>,
    pub message: String,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Module {
    /// The name of the module and the prefix of its components
    /// (`acme-review`).
    pub name: String,
    pub entity: String,
    pub locales: Vec<String>,
    pub title: Labels,
    pub description: Labels,
    pub icon: String,
    pub color: String,
    /// The property that names a record, when the entity has one.
    pub label_field: Option<String>,
    /// Whether a field of the entity is translated: the pages get a
    /// language switch.
    pub translated: bool,
    pub navigation: Option<Navigation>,
    pub acl: Option<Acl>,
    pub listing: Listing,
    pub detail: Detail,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Navigation {
    pub parent: String,
    pub position: i64,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Acl {
    pub parent: Option<String>,
    /// The entities a viewer reads besides the one of the module.
    pub read: Vec<String>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Listing {
    pub managed: bool,
    pub limit: u32,
    pub sort_by: String,
    pub descending: bool,
    pub columns: Vec<Column>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Column {
    pub property: String,
    pub label: Labels,
    pub sortable: bool,
    pub align: Option<Align>,
    /// `string`, `number` or `boolean` when the cell is edited in place.
    pub inline_edit: Option<&'static str>,
    /// The column that links to the detail page.
    pub primary: bool,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Detail {
    pub managed: bool,
    pub cards: Vec<Card>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Card {
    pub name: String,
    pub title: Labels,
    pub fields: Vec<Field>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Field {
    /// The key of the field in the description: the property of the entity.
    pub name: String,
    /// The property the input writes: the foreign key of an association.
    pub model: String,
    pub label: Labels,
    pub help_text: Labels,
    pub placeholder: Labels,
    pub required: bool,
    pub translated: bool,
    pub input: Input,
    pub props: Props,
}

#[derive(Debug, Clone, PartialEq)]
pub enum Input {
    Text,
    Textarea,
    TextEditor,
    Number(NumberType),
    Switch,
    Date,
    Datetime,
    Select(Vec<Choice>),
    EntitySelect {
        entity: String,
        label_property: String,
    },
    Custom {
        name: String,
    },
}

#[derive(Debug, Clone, PartialEq)]
pub struct Choice {
    pub value: String,
    pub label: Labels,
}

/// `createdAt` as `Created at`, `acme_review` as `Acme review`.
pub fn humanize(name: &str) -> String {
    let mut words = String::new();
    let mut previous_lower = false;
    for character in name.chars() {
        if matches!(character, '_' | '-' | '.') {
            words.push(' ');
            previous_lower = false;
            continue;
        }
        if character.is_uppercase() && previous_lower {
            words.push(' ');
        }
        previous_lower = character.is_lowercase() || character.is_ascii_digit();
        words.extend(character.to_lowercase());
    }
    let mut characters = words.trim().chars();
    match characters.next() {
        Some(first) => first.to_uppercase().chain(characters).collect(),
        None => String::new(),
    }
}

/// A module name as Shopware wants it: lower-case words joined by dashes,
/// at least two of them.
pub fn valid_module_name(name: &str) -> bool {
    let word = |word: &str| {
        word.starts_with(|first: char| first.is_ascii_lowercase())
            && word
                .chars()
                .all(|character| character.is_ascii_lowercase() || character.is_ascii_digit())
    };
    name.split('-').count() >= 2 && name.split('-').all(word)
}

/// `label` for every locale: the configured text, else the one of the main
/// locale, else `derived`.
fn labels(configured: &Labels, derived: &str, locales: &[String]) -> Labels {
    let main = configured
        .get(MAIN_LOCALE)
        .map_or(derived, String::as_str)
        .to_owned();
    locales
        .iter()
        .map(|locale| {
            let text = configured.get(locale).cloned().unwrap_or(main.clone());
            (locale.clone(), text)
        })
        .collect()
}

/// Like [`labels`] for a text nothing is derived for: empty unless set.
fn optional_labels(configured: &Labels, locales: &[String]) -> Labels {
    if configured.is_empty() {
        return Labels::new();
    }
    labels(configured, "", locales)
}

fn in_list(field: &EntityField) -> bool {
    !field.read_only
        && matches!(
            field.kind,
            Kind::String
                | Kind::Int
                | Kind::Float
                | Kind::Bool
                | Kind::Date
                | Kind::DateTime
                | Kind::Enum
        )
        || field.kind == Kind::Timestamp && field.name == "createdAt"
}

/// The input of a field in a form, `None` when a form has none for it.
fn derived_input(field: &EntityField) -> Option<Input> {
    if field.read_only {
        return None;
    }
    Some(match field.kind {
        Kind::String if !field.choices.is_empty() => Input::Select(Vec::new()),
        Kind::String | Kind::Enum => Input::Text,
        Kind::LongText if field.allow_html => Input::TextEditor,
        Kind::LongText => Input::Textarea,
        Kind::Int => Input::Number(NumberType::Int),
        Kind::Float => Input::Number(NumberType::Float),
        Kind::Bool => Input::Switch,
        Kind::Date => Input::Date,
        Kind::DateTime => Input::Datetime,
        Kind::ManyToOne if !field.foreign_key.is_empty() && !field.target_entity.is_empty() => {
            Input::EntitySelect {
                entity: field.target_entity.clone(),
                label_property: field.target_label.clone(),
            }
        }
        Kind::ManyToOne | Kind::Timestamp | Kind::Other => return None,
    })
}

struct Resolver<'a> {
    name: &'a str,
    module: &'a config::Module,
    entity: &'a Entity,
    locales: Vec<String>,
    problems: Vec<Problem>,
}

impl Resolver<'_> {
    fn problem(&mut self, path: &[&str], message: String) {
        let mut full = vec!["modules".to_owned(), self.name.to_owned()];
        full.extend(path.iter().map(|part| (*part).to_owned()));
        self.problems.push(Problem {
            path: full,
            message,
        });
    }

    fn unknown_field(&mut self, path: &[&str], name: &str) {
        self.problem(
            path,
            format!("Entity \"{}\" has no field \"{name}\"", self.entity.name),
        );
    }

    fn label_field(&mut self) -> Option<String> {
        if let Some(name) = &self.module.label_field {
            if self.entity.field(name).is_none() {
                self.unknown_field(&["labelField"], name);
                return None;
            }
            return Some(name.clone());
        }
        let strings = || {
            self.entity
                .fields
                .iter()
                .filter(|field| field.kind == Kind::String)
        };
        ["name", "title", "label", "technicalName"]
            .iter()
            .find_map(|wanted| strings().find(|field| field.name == *wanted))
            .or_else(|| strings().next())
            .map(|field| field.name.clone())
    }

    fn listing(&mut self, label_field: Option<&str>) -> Listing {
        let listing = &self.module.listing;
        for name in listing.columns.keys() {
            if self.entity.field(name).is_none() {
                self.unknown_field(&["listing", "columns", name], name);
            }
        }
        let auto = listing.auto.unwrap_or(true);
        let mut columns: Vec<(i64, Column)> = Vec::new();
        for (index, field) in self.entity.fields.iter().enumerate() {
            let configured = listing.columns.get(&field.name);
            let enabled = match configured {
                Some(column) => column.enable.unwrap_or(true),
                None => auto && in_list(field),
            };
            if !enabled {
                continue;
            }
            let default = config::Column::default();
            let column = configured.unwrap_or(&default);
            let editable = column.inline_edit.unwrap_or(false);
            let inline_edit = match field.kind {
                _ if !editable => None,
                Kind::String => Some("string"),
                Kind::Int | Kind::Float => Some("number"),
                Kind::Bool => Some("boolean"),
                _ => {
                    self.problem(
                        &["listing", "columns", &field.name, "inlineEdit"],
                        format!("Field \"{}\" cannot be edited in a list", field.name),
                    );
                    None
                }
            };
            let position = column
                .position
                .unwrap_or((index as i64 + 1) * POSITION_STEP);
            columns.push((
                position,
                Column {
                    property: field.name.clone(),
                    label: labels(&column.label, &humanize(&field.name), &self.locales),
                    sortable: column
                        .sortable
                        .unwrap_or(field.kind != Kind::ManyToOne && field.kind != Kind::Other),
                    align: column.align.or(match field.kind {
                        Kind::Int | Kind::Float => Some(Align::Right),
                        _ => None,
                    }),
                    inline_edit,
                    primary: false,
                },
            ));
        }
        columns
            .sort_by(|left, right| (left.0, &left.1.property).cmp(&(right.0, &right.1.property)));
        let mut columns: Vec<Column> = columns.into_iter().map(|(_, column)| column).collect();
        // The label field links to the record, else the first column.
        let primary = columns
            .iter()
            .position(|column| Some(column.property.as_str()) == label_field)
            .unwrap_or(0);
        if let Some(column) = columns.get_mut(primary) {
            column.primary = true;
        }
        let (sort_by, descending) = match &listing.sorting {
            Some(sorting) => {
                if self.entity.field(&sorting.field).is_none() {
                    self.unknown_field(&["listing", "sorting", "field"], &sorting.field);
                }
                (
                    sorting.field.clone(),
                    sorting.direction == Some(Direction::Descending),
                )
            }
            None => match self.entity.field("createdAt") {
                Some(_) => ("createdAt".to_owned(), true),
                None => (
                    columns
                        .first()
                        .map_or_else(|| "id".to_owned(), |column| column.property.clone()),
                    false,
                ),
            },
        };
        Listing {
            managed: listing.managed.unwrap_or(true),
            limit: listing.limit.unwrap_or(25),
            sort_by,
            descending,
            columns,
        }
    }

    fn field(
        &mut self,
        card: &str,
        field: &EntityField,
        configured: &config::Field,
    ) -> Option<Field> {
        let path = ["detail", "cards", card, "fields", field.name.as_str()];
        let derived = derived_input(field);
        let mut props = Props::new();
        let input = match &configured.component {
            None => derived,
            Some(component) => {
                let (input, given) = match component {
                    Component::Text { props } => (Input::Text, props),
                    Component::Textarea { props } => (Input::Textarea, props),
                    Component::TextEditor { props } => (Input::TextEditor, props),
                    Component::Number { number_type, props } => (
                        Input::Number(number_type.unwrap_or(match field.kind {
                            Kind::Float => NumberType::Float,
                            _ => NumberType::Int,
                        })),
                        props,
                    ),
                    Component::Switch { props } => (Input::Switch, props),
                    Component::Date { props } => (Input::Date, props),
                    Component::Datetime { props } => (Input::Datetime, props),
                    Component::Select { options, props } => {
                        let mut choices: Vec<(i64, Choice)> = options
                            .iter()
                            .enumerate()
                            .map(|(index, (value, option))| {
                                (
                                    option
                                        .position
                                        .unwrap_or((index as i64 + 1) * POSITION_STEP),
                                    Choice {
                                        value: value.clone(),
                                        label: labels(
                                            &option.label,
                                            &humanize(value),
                                            &self.locales,
                                        ),
                                    },
                                )
                            })
                            .collect();
                        choices.sort_by(|left, right| {
                            (left.0, &left.1.value).cmp(&(right.0, &right.1.value))
                        });
                        (
                            Input::Select(choices.into_iter().map(|(_, choice)| choice).collect()),
                            props,
                        )
                    }
                    Component::EntitySelect {
                        entity,
                        label_property,
                        props,
                    } => (
                        Input::EntitySelect {
                            entity: entity.clone().unwrap_or(field.target_entity.clone()),
                            label_property: label_property
                                .clone()
                                .unwrap_or(field.target_label.clone()),
                        },
                        props,
                    ),
                    Component::Custom { name, props } => {
                        (Input::Custom { name: name.clone() }, props)
                    }
                };
                props = given.clone();
                Some(input)
            }
        };
        let Some(mut input) = input else {
            self.problem(
                &path,
                format!(
                    "Field \"{}\" has no input a form can be generated with; name one with \"component\"",
                    field.name
                ),
            );
            return None;
        };
        match &mut input {
            Input::Select(choices) if choices.is_empty() => {
                *choices = field
                    .choices
                    .iter()
                    .map(|value| Choice {
                        value: value.clone(),
                        label: labels(&Labels::new(), &humanize(value), &self.locales),
                    })
                    .collect();
                if choices.is_empty() {
                    self.problem(
                        &[&path[..], &["component"]].concat(),
                        format!("The select of \"{}\" has no options", field.name),
                    );
                }
            }
            Input::EntitySelect {
                entity,
                label_property,
            } => {
                if entity.is_empty() {
                    self.problem(
                        &[&path[..], &["component"]].concat(),
                        format!("The entity select of \"{}\" names no entity", field.name),
                    );
                }
                if label_property.is_empty() {
                    *label_property = "id".to_owned();
                }
            }
            _ => {}
        }
        // An association is written through its foreign key.
        let model = match (&input, field.kind) {
            (Input::EntitySelect { .. }, Kind::ManyToOne) if !field.foreign_key.is_empty() => {
                field.foreign_key.clone()
            }
            _ => field.name.clone(),
        };
        Some(Field {
            name: field.name.clone(),
            model,
            label: labels(&configured.label, &humanize(&field.name), &self.locales),
            help_text: optional_labels(&configured.help_text, &self.locales),
            placeholder: optional_labels(&configured.placeholder, &self.locales),
            required: configured.required.unwrap_or(field.required),
            translated: field.translated,
            input,
            props,
        })
    }

    fn detail(&mut self) -> Detail {
        let detail = &self.module.detail;
        // The card each field is named in; a field belongs to one card.
        let mut placed: BTreeMap<&str, &str> = BTreeMap::new();
        for (card, configured) in &detail.cards {
            for name in configured.fields.keys() {
                if self.entity.field(name).is_none() {
                    self.unknown_field(&["detail", "cards", card, "fields", name], name);
                } else if let Some(other) = placed.insert(name, card) {
                    self.problem(
                        &["detail", "cards", card, "fields", name],
                        format!("Field \"{name}\" is in card \"{other}\" already"),
                    );
                }
            }
        }
        let auto = detail.auto.unwrap_or(true);
        // The foreign key of an association with an input has none itself.
        let foreign_keys: BTreeSet<&str> = self
            .entity
            .fields
            .iter()
            .filter(|field| field.kind == Kind::ManyToOne)
            .map(|field| field.foreign_key.as_str())
            .collect();
        let mut names: Vec<&str> = detail.cards.keys().map(String::as_str).collect();
        if !names.contains(&DEFAULT_CARD) {
            names.push(DEFAULT_CARD);
        }
        let mut cards: Vec<(i64, Card)> = Vec::new();
        for (card_index, card) in names.into_iter().enumerate() {
            let default = config::Card::default();
            let configured = detail.cards.get(card).unwrap_or(&default);
            if configured.enable == Some(false) {
                continue;
            }
            let mut fields: Vec<(i64, Field)> = Vec::new();
            for (index, field) in self.entity.fields.iter().enumerate() {
                let named = configured.fields.get(&field.name);
                let derived = auto
                    && card == DEFAULT_CARD
                    && !placed.contains_key(field.name.as_str())
                    && !foreign_keys.contains(field.name.as_str())
                    && derived_input(field).is_some();
                let enabled = match named {
                    Some(named) => named.enable.unwrap_or(true),
                    None => derived,
                };
                if !enabled {
                    continue;
                }
                let default = config::Field::default();
                let named = named.unwrap_or(&default);
                let position = named.position.unwrap_or((index as i64 + 1) * POSITION_STEP);
                if let Some(field) = self.field(card, field, named) {
                    fields.push((position, field));
                }
            }
            if fields.is_empty() {
                continue;
            }
            fields.sort_by(|left, right| (left.0, &left.1.name).cmp(&(right.0, &right.1.name)));
            let position = configured.position.unwrap_or(match card {
                DEFAULT_CARD => POSITION_STEP,
                _ => (card_index as i64 + 2) * POSITION_STEP,
            });
            cards.push((
                position,
                Card {
                    name: card.to_owned(),
                    title: labels(&configured.title, &humanize(card), &self.locales),
                    fields: fields.into_iter().map(|(_, field)| field).collect(),
                },
            ));
        }
        cards.sort_by(|left, right| (left.0, &left.1.name).cmp(&(right.0, &right.1.name)));
        Detail {
            managed: detail.managed.unwrap_or(true),
            cards: cards.into_iter().map(|(_, card)| card).collect(),
        }
    }
}

/// Keys that become the name of a Twig block, a snippet or a CSS class.
fn valid_key(name: &str) -> bool {
    name.starts_with(|first: char| first.is_ascii_alphabetic())
        && name
            .chars()
            .all(|character| character.is_ascii_alphanumeric() || character == '_')
}

/// Resolves the module `name` against its entity. The module is usable
/// only without problems.
pub fn resolve(name: &str, module: &config::Module, entity: &Entity) -> (Module, Vec<Problem>) {
    let mut locales: Vec<String> = module.locales.keys().cloned().collect();
    if locales.is_empty() {
        locales = DEFAULT_LOCALES.map(str::to_owned).to_vec();
    }
    if !locales.iter().any(|locale| locale == MAIN_LOCALE) {
        locales.push(MAIN_LOCALE.to_owned());
        locales.sort();
    }
    let mut resolver = Resolver {
        name,
        module,
        entity,
        locales,
        problems: Vec::new(),
    };
    if !valid_module_name(name) {
        resolver.problem(
            &[],
            format!(
                "Module name \"{name}\" must be lower-case words joined by dashes, with a prefix: \"acme-review\""
            ),
        );
    }
    for card in module.detail.cards.keys() {
        if !valid_key(card) {
            resolver.problem(
                &["detail", "cards", card],
                format!("Card name \"{card}\" may hold letters, digits and underscores only"),
            );
        }
    }
    let label_field = resolver.label_field();
    let listing = resolver.listing(label_field.as_deref());
    let detail = resolver.detail();
    let navigation = (module.navigation.enable != Some(false)).then(|| Navigation {
        parent: module
            .navigation
            .parent
            .clone()
            .unwrap_or_else(|| "sw-extension".to_owned()),
        position: module.navigation.position.unwrap_or(100),
    });
    let acl = (module.acl.enable != Some(false)).then(|| {
        let mut read: Vec<String> = detail
            .cards
            .iter()
            .flat_map(|card| &card.fields)
            .filter_map(|field| match &field.input {
                Input::EntitySelect { entity, .. } => Some(entity.clone()),
                _ => None,
            })
            .filter(|target| *target != entity.name)
            .collect();
        read.sort();
        read.dedup();
        Acl {
            parent: module.acl.parent.clone().or_else(|| {
                let parent = navigation.as_ref()?.parent.as_str();
                Some(
                    match parent {
                        "sw-catalogue" => "catalogues",
                        "sw-order" => "orders",
                        "sw-customer" => "customers",
                        "sw-content" => "content",
                        "sw-marketing" => "marketing",
                        "sw-settings" => "settings",
                        _ => return None,
                    }
                    .to_owned(),
                )
            }),
            read,
        }
    });
    let locales = resolver.locales.clone();
    let title = humanize(&entity.name);
    let resolved = Module {
        name: name.to_owned(),
        entity: entity.name.clone(),
        title: labels(&module.title, &title, &locales),
        description: labels(&module.description, &title, &locales),
        icon: module
            .icon
            .clone()
            .unwrap_or_else(|| "regular-cube".to_owned()),
        color: module.color.clone().unwrap_or_else(|| "#189EFF".to_owned()),
        label_field,
        translated: entity.translated(),
        navigation,
        acl,
        listing,
        detail,
        locales,
    };
    (resolved, resolver.problems)
}
