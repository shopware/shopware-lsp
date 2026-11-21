//! The JavaScript of a module: its registration, its roles and the
//! components of its pages.

use super::{Names, header, js, options_name};
use crate::config::Align;
use crate::render::Style;
use crate::resolve::Input;

pub(super) fn index_js(names: &Names) -> String {
    let module = names.module;
    let mut lines = Vec::new();
    header(&mut lines);
    if module.acl.is_some() {
        lines.push("import './acl';".to_owned());
        lines.push(String::new());
    }
    for (component, managed) in [
        (names.list(), module.listing.managed),
        (names.detail(), module.detail.managed),
    ] {
        let directory = if managed { "./page" } else { "../page" };
        lines.push(format!(
            "Shopware.Component.register('{component}', () => import('{directory}/{component}'));"
        ));
    }
    let privilege = |role: &str| match module.acl {
        Some(_) => format!(
            "                privilege: {},\n",
            js(&names.privilege(role))
        ),
        None => String::new(),
    };
    let index = names.route("index");
    lines.push(format!(
        "
Shopware.Module.register({name}, {{
    type: 'plugin',
    name: {name},
    title: {title},
    description: {description},
    color: {color},
    icon: {icon},
    entity: {entity},

    routes: {{
        index: {{
            component: {list},
            path: 'index',
            meta: {{
{viewer}            }},
        }},
        detail: {{
            component: {detail},
            path: 'detail/:id',
            meta: {{
                parentPath: {index},
{viewer}            }},
            props: {{
                default(route) {{
                    return {{ entityId: route.params.id }};
                }},
            }},
        }},
        create: {{
            component: {detail},
            path: 'create',
            meta: {{
                parentPath: {index},
{creator}            }},
        }},
    }},",
        name = js(&module.name),
        title = js(&names.snippet("general.title")),
        description = js(&names.snippet("general.description")),
        color = js(&module.color),
        icon = js(&module.icon),
        entity = js(&module.entity),
        list = js(&names.list()),
        detail = js(&names.detail()),
        index = js(&index),
        viewer = privilege("viewer"),
        creator = privilege("creator"),
    ));
    if let Some(navigation) = &module.navigation {
        let privilege = match module.acl {
            Some(_) => format!(
                "\n            privilege: {},",
                js(&names.privilege("viewer"))
            ),
            None => String::new(),
        };
        lines.push(format!(
            "
    navigation: [
        {{
            id: {id},
            label: {label},
            color: {color},
            icon: {icon},
            path: {path},
            parent: {parent},
            position: {position},{privilege}
        }},
    ],",
            id = js(&module.name),
            label = js(&names.snippet("general.title")),
            color = js(&module.color),
            icon = js(&module.icon),
            path = js(&index),
            parent = js(&navigation.parent),
            position = navigation.position,
        ));
    }
    lines.push("});".to_owned());
    lines.join("\n") + "\n"
}

pub(super) fn acl_js(names: &Names) -> Option<String> {
    let acl = names.module.acl.as_ref()?;
    let entity = &names.module.entity;
    let mut lines = Vec::new();
    header(&mut lines);
    let read: Vec<String> = std::iter::once(entity)
        .chain(&acl.read)
        .map(|entity| format!("                {},", js(&format!("{entity}:read"))))
        .collect();
    let parent = match &acl.parent {
        Some(parent) => js(parent),
        None => "null".to_owned(),
    };
    lines.push(format!(
        "Shopware.Service('privileges').addPrivilegeMappingEntry({{
    category: 'permissions',
    parent: {parent},
    key: {key},
    roles: {{
        viewer: {{
            privileges: [
{read}
            ],
            dependencies: [],
        }},
        editor: {{
            privileges: [{update}],
            dependencies: [{viewer}],
        }},
        creator: {{
            privileges: [{create}],
            dependencies: [{viewer}, {editor}],
        }},
        deleter: {{
            privileges: [{delete}],
            dependencies: [{viewer}],
        }},
    }},
}});",
        key = js(entity),
        read = read.join("\n"),
        update = js(&format!("{entity}:update")),
        create = js(&format!("{entity}:create")),
        delete = js(&format!("{entity}:delete")),
        viewer = js(&names.privilege("viewer")),
        editor = js(&names.privilege("editor")),
    ));
    Some(lines.join("\n") + "\n")
}

pub(super) fn list_js(names: &Names) -> String {
    let module = names.module;
    let listing = &module.listing;
    let mut lines = Vec::new();
    header(&mut lines);
    let mut columns = String::new();
    for column in &listing.columns {
        columns.push_str("                {\n");
        let mut entry = |key: &str, value: String| {
            columns.push_str(&format!("                    {key}: {value},\n"));
        };
        entry("property", js(&column.property));
        entry("dataIndex", js(&column.property));
        entry(
            "label",
            js(&names.snippet(&format!("list.columns.{}", column.property))),
        );
        if column.primary {
            entry("routerLink", js(&names.route("detail")));
            entry("primary", "true".to_owned());
        }
        if !column.sortable {
            entry("sortable", "false".to_owned());
        }
        if let Some(align) = column.align {
            entry(
                "align",
                js(match align {
                    Align::Left => "left",
                    Align::Center => "center",
                    Align::Right => "right",
                }),
            );
        }
        if let Some(kind) = column.inline_edit {
            entry("inlineEdit", js(kind));
        }
        entry("allowResize", "true".to_owned());
        columns.push_str("                },\n");
    }
    let inject = match module.acl {
        Some(_) => "['repositoryFactory', 'acl']",
        None => "['repositoryFactory']",
    };
    let language = if module.translated {
        "

        onChangeLanguage() {
            return this.getList();
        },"
    } else {
        ""
    };
    lines.push(format!(
        "import template from './{component}.html.twig';

const {{ Mixin }} = Shopware;
const {{ Criteria }} = Shopware.Data;

export default {{
    template,

    inject: {inject},

    mixins: [Mixin.getByName('listing')],

    data() {{
        return {{
            isLoading: false,
            items: null,
            limit: {limit},
            sortBy: {sort_by},
            sortDirection: '{direction}',
        }};
    }},

    metaInfo() {{
        return {{
            title: this.$createTitle(),
        }};
    }},

    computed: {{
        repository() {{
            return this.repositoryFactory.create({entity});
        }},

        columns() {{
            return this.getColumns();
        }},
    }},

    methods: {{
        getListCriteria() {{
            const criteria = new Criteria(this.page, this.limit);
            criteria.setTerm(this.term);
            criteria.addSorting(Criteria.sort(this.sortBy, this.sortDirection, this.naturalSorting));

            return criteria;
        }},

        getList() {{
            this.isLoading = true;

            return this.repository
                .search(this.getListCriteria())
                .then((result) => {{
                    this.total = result.total;
                    this.items = result;
                }})
                .finally(() => {{
                    this.isLoading = false;
                }});
        }},

        getColumns() {{
            return [
{columns}            ];
        }},{language}
    }},
}};",
        component = names.list(),
        limit = listing.limit,
        sort_by = js(&listing.sort_by),
        direction = if listing.descending { "DESC" } else { "ASC" },
        entity = js(&module.entity),
    ));
    lines.join("\n") + "\n"
}

pub(super) fn detail_js(names: &Names) -> String {
    let module = names.module;
    let fields = || module.detail.cards.iter().flat_map(|card| &card.fields);
    let mut lines = Vec::new();
    header(&mut lines);
    let errors: Vec<String> = fields()
        .map(|field| format!("            {},", js(&field.model)))
        .collect();
    let mut options = String::new();
    for field in fields() {
        let Input::Select(choices) = &field.input else {
            continue;
        };
        options.push_str(&format!(
            "\n        {}() {{\n            return [\n",
            options_name(&field.name)
        ));
        for choice in choices {
            options.push_str(&format!(
                "                {{ value: {}, label: this.$t({}) }},\n",
                js(&choice.value),
                js(&names.snippet(&format!(
                    "detail.fields.{}.options.{}",
                    field.name, choice.value
                ))),
            ));
        }
        options.push_str("            ];\n        },\n");
    }
    let new_title = format!("this.$t({})", js(&names.snippet("detail.titleNew")));
    let identifier = match &module.label_field {
        Some(label) => format!("this.placeholder(this.item, {}, {new_title})", js(label)),
        None => new_title,
    };
    let allow_save = match module.acl {
        Some(_) => format!(
            "if (!this.item) {{
                return false;
            }}

            return this.item.isNew()
                ? this.acl.can({})
                : this.acl.can({});",
            js(&names.privilege("creator")),
            js(&names.privilege("editor")),
        ),
        None => "return !!this.item;".to_owned(),
    };
    let reset_language = match (module.translated, names.style) {
        (false, _) => "",
        (true, Style::Meteor) => {
            "                Shopware.Store.get('context').resetLanguageToDefault();\n"
        }
        (true, Style::Classic) => {
            "                Shopware.State.commit('context/resetLanguageToDefault');\n"
        }
    };
    let language = if module.translated {
        "
        onChangeLanguage() {
            return this.loadEntity();
        },
"
    } else {
        ""
    };
    let inject = match module.acl {
        Some(_) => "['repositoryFactory', 'acl']",
        None => "['repositoryFactory']",
    };
    lines.push(format!(
        "import template from './{component}.html.twig';

const {{ Mixin }} = Shopware;
const {{ Criteria }} = Shopware.Data;
const {{ mapPropertyErrors }} = Shopware.Component.getComponentHelper();

export default {{
    template,

    inject: {inject},

    mixins: [Mixin.getByName('notification'), Mixin.getByName('placeholder')],

    props: {{
        entityId: {{
            type: String,
            required: false,
            default: null,
        }},
    }},

    shortcuts: {{
        'SYSTEMKEY+S': 'onSave',
        ESCAPE: 'onCancel',
    }},

    data() {{
        return {{
            item: null,
            isLoading: false,
            isSaveSuccessful: false,
        }};
    }},

    metaInfo() {{
        return {{
            title: this.$createTitle(this.identifier),
        }};
    }},

    computed: {{
        repository() {{
            return this.repositoryFactory.create({entity});
        }},

        identifier() {{
            return {identifier};
        }},

        allowSave() {{
            {allow_save}
        }},
{options}
        ...mapPropertyErrors('item', [
{errors}
        ]),
    }},

    watch: {{
        entityId() {{
            this.loadEntity();
        }},
    }},

    created() {{
        this.loadEntity();
    }},

    methods: {{
        getDetailCriteria() {{
            return new Criteria(1, 1);
        }},

        loadEntity() {{
            if (!this.entityId) {{
{reset_language}                this.item = this.repository.create();

                return Promise.resolve();
            }}

            this.isLoading = true;

            return this.repository
                .get(this.entityId, Shopware.Context.api, this.getDetailCriteria())
                .then((item) => {{
                    this.item = item;
                }})
                .finally(() => {{
                    this.isLoading = false;
                }});
        }},
{language}
        saveFinish() {{
            this.isSaveSuccessful = false;
        }},

        onSave() {{
            if (!this.allowSave) {{
                return Promise.resolve();
            }}

            this.isLoading = true;
            this.isSaveSuccessful = false;

            return this.repository
                .save(this.item)
                .then(() => {{
                    this.isSaveSuccessful = true;

                    if (!this.entityId) {{
                        return this.$router.push({{ name: {detail_route}, params: {{ id: this.item.id }} }});
                    }}

                    return this.loadEntity();
                }})
                .catch(() => {{
                    this.createNotificationError({{
                        message: this.$t({save_error}),
                    }});
                }})
                .finally(() => {{
                    this.isLoading = false;
                }});
        }},

        onCancel() {{
            return this.$router.push({{ name: {index_route} }});
        }},
    }},
}};",
        component = names.detail(),
        entity = js(&module.entity),
        errors = errors.join("\n"),
        detail_route = js(&names.route("detail")),
        index_route = js(&names.route("index")),
        save_error = js(&names.snippet("detail.notificationSaveError")),
    ));
    lines.join("\n") + "\n"
}
