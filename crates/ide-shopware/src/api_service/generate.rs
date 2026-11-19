//! The TypeScript source of an Administration API service.

use swlsp_textutil::{lowercase_char, uppercase_first};

use super::shape::{Shape, is_identifier};
use crate::diagnostics::{ApiAction, ApiController};
use crate::scaffold::snake_case;

/// Members of `ApiService` that a generated method must not replace.
const RESERVED_METHODS: &[&str] = &[
    "constructor",
    "getApiBasePath",
    "getBasicHeaders",
    "handleResponse",
    "parseJsonApiData",
    "getVersionHeader",
    "makeQueryParams",
    "apiEndpoint",
    "httpClient",
    "client",
    "loginService",
    "endpoint",
    "contentType",
    "type",
    "name",
];
/// Methods `ApiService` had in older Shopware versions, which an extension
/// may still be built against.
const LEGACY_METHODS: &[&str] = &[
    "getList",
    "getById",
    "updateById",
    "deleteAssociation",
    "create",
    "delete",
    "clone",
    "versionize",
    "mergeVersion",
];
/// Names the generated file uses itself.
const RESERVED_TYPES: &[&str] = &["ApiService", "HttpClient", "LoginService", "Blob", "Record"];
const MAX_LINE: usize = 120;

/// The type aliases of a service, in the order they are first used.
#[derive(Default)]
pub(crate) struct Types {
    aliases: Vec<(String, String)>,
    /// Names the file already uses for something else.
    taken: Vec<String>,
}

impl Types {
    pub(crate) fn with_taken(taken: Vec<String>) -> Types {
        Types {
            aliases: Vec::new(),
            taken,
        }
    }

    /// The type of `shape`: inline when it fits on a line, otherwise an
    /// alias. The alias is called `preferred` (the PHP class a payload is
    /// mapped onto) when that name is free or already stands for the same
    /// type, else `fallback`, numbered when that is taken as well.
    fn name(&mut self, shape: &Shape, preferred: Option<&str>, fallback: &str) -> String {
        let text = shape.typescript(0);
        if !text.contains('\n') {
            return text;
        }
        let numbered = (2..).map(|number| format!("{fallback}{number}"));
        let candidates = preferred
            .filter(|name| !RESERVED_TYPES.contains(name))
            .map(str::to_owned)
            .into_iter()
            .chain([fallback.to_owned()])
            .chain(numbered);
        for candidate in candidates {
            match self.aliases.iter().find(|(name, _)| *name == candidate) {
                Some((_, known)) if *known == text => return candidate,
                Some(_) => {}
                None if self.taken.contains(&candidate) => {}
                None => {
                    self.aliases.push((candidate.clone(), text));
                    return candidate;
                }
            }
        }
        text
    }

    pub(crate) fn source(&self) -> String {
        self.aliases
            .iter()
            .map(|(name, text)| format!("export type {name} = {text};\n\n"))
            .collect()
    }
}

/// Where the service is generated for.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Flavour {
    /// The Administration itself: relative imports, registered by its
    /// directory.
    Core,
    /// An extension: the globals of `Shopware`, registered by the file.
    Extension,
}

/// The names of a generated service.
pub(crate) struct ServiceNames {
    /// `SwagExampleApiService`
    pub class: String,
    /// `swagExampleApiService`
    pub service: String,
    /// `swag-example`
    pub endpoint: String,
}

impl ServiceNames {
    pub(crate) fn of(controller: &ApiController) -> ServiceNames {
        let base = controller
            .class_name
            .strip_suffix("Controller")
            .filter(|base| !base.is_empty())
            .unwrap_or(&controller.class_name);
        let base = base
            .strip_suffix("Api")
            .filter(|base| !base.is_empty())
            .unwrap_or(base);
        let mut service = String::with_capacity(base.len() + 10);
        let mut characters = base.chars();
        service.extend(characters.next().map(lowercase_char));
        service.extend(characters);
        service.push_str("ApiService");
        ServiceNames {
            class: format!("{base}ApiService"),
            service,
            endpoint: snake_case(base).replace('_', "-"),
        }
    }

    pub(crate) fn file_name(&self) -> String {
        format!("{}.api.service.ts", self.endpoint)
    }
}

pub(crate) fn service_source(controller: &ApiController, flavour: Flavour) -> String {
    let names = ServiceNames::of(controller);
    let mut types = Types::default();
    let methods = methods_source(&controller.actions, flavour, &mut types, &mut Vec::new()).source;
    let mut source = String::new();
    match flavour {
        Flavour::Core => source.push_str(
            "import type { HttpClient } from 'src/core/factory/http-client.types';\n\
             import ApiService from '../api.service';\n\
             import type { LoginService } from '../login.service';\n\n",
        ),
        Flavour::Extension => source.push_str(
            "const { ApiService } = Shopware.Classes;\n\n\
             type HttpClient = ConstructorParameters<typeof ApiService>[0];\n\
             type LoginService = ConstructorParameters<typeof ApiService>[1];\n\n",
        ),
    }
    source.push_str(&types.source());
    source.push_str(&format!(
        "/**\n * Gateway to the Admin API actions of {controller}.\n */\n\
         export default class {class} extends ApiService {{\n    \
         constructor(httpClient: HttpClient, loginService: LoginService, apiEndpoint = '{endpoint}') {{\n        \
         super(httpClient, loginService, apiEndpoint);\n        \
         this.name = '{service}';\n    }}\n\n{methods}}}\n",
        controller = controller.class_name,
        class = names.class,
        endpoint = names.endpoint,
        service = names.service,
    ));
    if flavour == Flavour::Extension {
        source.push_str(&format!(
            "\nShopware.Application.addServiceProvider('{service}', (container) => {{\n    \
             const initContainer = Shopware.Application.getContainer('init');\n\n    \
             return new {class}(initContainer.httpClient, container.loginService);\n}});\n\n\
             declare global {{\n    interface ServiceContainer {{\n        \
             {service}: {class};\n    }}\n}}\n",
            service = names.service,
            class = names.class,
        ));
    }
    source
}

/// Generated methods and the names they got.
pub(crate) struct Methods {
    pub source: String,
    pub names: Vec<String>,
}

/// One method per action, separated by an empty line. `used` holds the
/// member names that are taken and gains the new ones.
pub(crate) fn methods_source<'a>(
    actions: impl IntoIterator<Item = &'a ApiAction>,
    flavour: Flavour,
    types: &mut Types,
    used: &mut Vec<String>,
) -> Methods {
    let mut methods = Methods {
        source: String::new(),
        names: Vec::new(),
    };
    for action in actions {
        if !is_identifier(&action.method) {
            continue;
        }
        let reserved = RESERVED_METHODS.contains(&action.method.as_str())
            || (flavour == Flavour::Extension && LEGACY_METHODS.contains(&action.method.as_str()));
        let with_verb = format!("{}{}", action.method, uppercase_first(&action.verb));
        let numbered = (2..).map(|number| format!("{with_verb}{number}"));
        let Some(name) = (!reserved)
            .then(|| action.method.clone())
            .into_iter()
            .chain([with_verb.clone()])
            .chain(numbered)
            .find(|name| !used.contains(name))
        else {
            continue;
        };
        used.push(name.clone());
        if !methods.source.is_empty() {
            methods.source.push('\n');
        }
        methods
            .source
            .push_str(&method_source(action, &name, types));
        methods.names.push(name);
    }
    methods
}

fn method_source(action: &ApiAction, name: &str, types: &mut Types) -> String {
    let type_prefix = uppercase_first(name);
    let mut parameters: Vec<String> = action
        .path_parameters
        .iter()
        .map(|parameter| format!("{parameter}: string"))
        .collect();
    let has_body = matches!(action.verb.as_str(), "post" | "put" | "patch");
    let body = action.body.as_ref().filter(|_| has_body);
    if let Some(body) = body {
        let ty = types.name(
            body,
            action.body_class.as_deref(),
            &format!("{type_prefix}Payload"),
        );
        parameters.push(format!("payload: {ty}"));
    }
    if let Some(query) = &action.query {
        let ty = types.name(
            query,
            action.query_class.as_deref(),
            &format!("{type_prefix}Query"),
        );
        // Every key may be left out: then so may the whole object.
        let optional = match query {
            Shape::Object { fields, .. } => fields.iter().all(|field| field.optional),
            _ => true,
        };
        parameters.push(if optional {
            format!("params: {ty} = {{}}")
        } else {
            format!("params: {ty}")
        });
    }
    parameters.push("additionalHeaders: Record<string, string> = {}".to_owned());
    let file = action.response == Shape::Blob;
    let response = if action.response == Shape::Void {
        "void".to_owned()
    } else {
        types.name(&action.response, None, &format!("{type_prefix}Response"))
    };

    let mut config = Vec::new();
    if action.query.is_some() {
        config.push("params".to_owned());
    }
    config.push("headers: this.getBasicHeaders(additionalHeaders)".to_owned());
    if file {
        config.push("responseType: 'blob'".to_owned());
    }
    let mut arguments = vec![request_path(&action.path)];
    if has_body {
        arguments.push(if body.is_some() { "payload" } else { "null" }.to_owned());
    }
    let config_source = |indent: &str| {
        format!(
            "{{\n{}{indent}}}",
            config
                .iter()
                .map(|entry| format!("{indent}    {entry},\n"))
                .collect::<String>()
        )
    };
    let prefix = if action.response == Shape::Void {
        "        await "
    } else {
        "        const response = await "
    };
    let method = format!("this.httpClient.{}(", action.verb);
    let first_line = format!("{prefix}{method}{}, {{", arguments.join(", "));
    let call = if first_line.len() > MAX_LINE {
        // One argument per line.
        let mut call = format!("{method}\n");
        for argument in &arguments {
            call.push_str(&format!("            {argument},\n"));
        }
        call.push_str(&format!(
            "            {},\n        )",
            config_source("            ")
        ));
        call
    } else {
        arguments.push(config_source("        "));
        format!("{method}{})", arguments.join(", "))
    };

    let mut source = format!(
        "    /**\n     * {} {}\n     */\n",
        action.verb.to_uppercase(),
        action.path
    );
    let signature = format!(
        "    public async {name}({}): Promise<{response}> {{\n",
        parameters.join(", ")
    );
    if signature.len() > MAX_LINE {
        source.push_str(&format!("    public async {name}(\n"));
        for parameter in &parameters {
            source.push_str(&format!("        {parameter},\n"));
        }
        source.push_str(&format!("    ): Promise<{response}> {{\n"));
    } else {
        source.push_str(&signature);
    }
    match &action.response {
        Shape::Void => source.push_str(&format!("        await {call};\n")),
        Shape::Blob => source.push_str(&format!(
            "        const response = await {call};\n\n        return response.data as Blob;\n"
        )),
        Shape::Unknown => source.push_str(&format!(
            "        const response = await {call};\n\n        \
             return ApiService.handleResponse(response);\n"
        )),
        _ => source.push_str(&format!(
            "        const response = await {call};\n\n        \
             return ApiService.handleResponse(response) as {response};\n"
        )),
    }
    source.push_str("    }\n");
    source
}

/// The path as `httpClient` takes it: below `/api`, placeholders filled
/// from the parameters of the same name.
fn request_path(path: &str) -> String {
    let path = path.strip_prefix("/api").unwrap_or(path);
    if !path.contains('{') {
        return format!("'{path}'");
    }
    let mut text = String::from("`");
    let mut rest = path;
    while let Some(open) = rest.find('{') {
        text.push_str(&rest[..open]);
        let inner = &rest[open + 1..];
        let close = inner.find('}').unwrap_or(inner.len());
        let name: String = inner[..close]
            .trim_start_matches('!')
            .chars()
            .take_while(|character| character.is_ascii_alphanumeric() || *character == '_')
            .collect();
        text.push_str(&format!("${{{name}}}"));
        rest = inner.get(close + 1..).unwrap_or("");
    }
    text.push_str(rest);
    text.push('`');
    text
}
