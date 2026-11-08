//! Symfony, Twig, translation and controller workspace symbols (port of
//! `symfony_symbol.go`).

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_console::Index as ConsoleIndex;
use swlsp_lsp::{Context, ProviderResult, WorkspaceSymbolProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_protocol::SymbolKind;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Symbol, SymbolKind as PhpSymbolKind, Visibility};
use swlsp_php_types::go_to_lower;
use swlsp_symfony::{
    Route, RouteIndexer, RoutePathSearchMatcher, ServiceIndex, parse_controller_reference,
    resolve_controller_reference,
};
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_translation::Index as TranslationIndex;
use swlsp_twig::TwigIndexer;
use swlsp_twigcomponent::Index as ComponentIndex;
use swlsp_uriutil::filepath;

const MAX_COLLECTED_SYMBOLS: usize = 500;
const MAX_SERVICE_SYMBOLS: usize = 150;
const MAX_CONTROLLER_SYMBOLS: usize = 150;

pub struct SymfonyWorkspaceSymbolProvider {
    services: Option<Arc<ServiceIndex>>,
    routes: Option<Arc<RouteIndexer>>,
    commands: Option<Arc<ConsoleIndex>>,
    twig: Option<Arc<TwigIndexer>>,
    components: Option<Arc<ComponentIndex>>,
    translations: Option<Arc<TranslationIndex>>,
    php: Option<PhpIndex>,
}

#[derive(Clone, Debug, Default)]
struct Candidate {
    name: String,
    container: String,
    path: String,
    range: TextRange,
    line: i64,
    kind: Option<SymbolKind>,
    score: u8,
    location_range: Option<protocol::Range>,
}

impl SymfonyWorkspaceSymbolProvider {
    pub fn new(
        services: Option<Arc<ServiceIndex>>,
        routes: Option<Arc<RouteIndexer>>,
        commands: Option<Arc<ConsoleIndex>>,
        twig: Option<Arc<TwigIndexer>>,
        components: Option<Arc<ComponentIndex>>,
        translations: Option<Arc<TranslationIndex>>,
        php: Option<PhpIndex>,
    ) -> Self {
        Self {
            services,
            routes,
            commands,
            twig,
            components,
            translations,
            php,
        }
    }
}

struct Collector {
    query: String,
    candidates: Vec<Candidate>,
}

impl Collector {
    fn add(&mut self, mut current: Candidate) {
        let Some(score) = symbol_match_score(&self.query, &[&current.name, &current.container])
        else {
            return;
        };
        if current.name.is_empty() || current.path.is_empty() {
            return;
        }
        current.score = score;
        self.candidates.push(current);
    }
}

impl WorkspaceSymbolProvider for SymfonyWorkspaceSymbolProvider {
    fn workspace_symbols(
        &self,
        ctx: &Context,
        query: &str,
    ) -> ProviderResult<Vec<protocol::SymbolInformation>> {
        let mut collector = Collector {
            query: query.trim().to_owned(),
            candidates: Vec::new(),
        };
        self.collect_services(ctx, &mut collector)?;
        self.collect_routes(ctx, &mut collector)?;
        self.collect_controllers(ctx, &mut collector)?;
        self.collect_commands(ctx, &mut collector)?;
        self.collect_twig(ctx, &mut collector)?;
        self.collect_components(ctx, &mut collector)?;
        self.collect_translations(ctx, &mut collector)?;

        let mut candidates = collector.candidates;
        candidates.sort_by(|left, right| {
            left.score
                .cmp(&right.score)
                .then_with(|| go_to_lower(&left.name).cmp(&go_to_lower(&right.name)))
                .then_with(|| left.container.cmp(&right.container))
                .then_with(|| left.path.cmp(&right.path))
                .then_with(|| left.range.start().cmp(&right.range.start()))
        });
        candidates.truncate(MAX_COLLECTED_SYMBOLS);

        let mut result = Vec::with_capacity(candidates.len());
        let mut seen = HashSet::with_capacity(candidates.len());
        let mut sources: HashMap<String, Option<LineIndex>> = HashMap::new();
        for current in candidates {
            ctx.check()?;
            let Some(location) = symbol_location(&current, &mut sources) else {
                continue;
            };
            let key = (
                go_to_lower(&current.name),
                current.container.clone(),
                location.uri.clone(),
                current.range,
            );
            if !seen.insert(key) {
                continue;
            }
            result.push(protocol::SymbolInformation {
                name: current.name,
                kind: current.kind.unwrap_or(SymbolKind::NULL),
                location,
                container_name: current.container,
                priority: 0,
            });
        }
        Ok(result)
    }
}

impl SymfonyWorkspaceSymbolProvider {
    fn collect_controllers(&self, ctx: &Context, collector: &mut Collector) -> ProviderResult<()> {
        let Some(php) = &self.php else {
            return Ok(());
        };
        let query = collector.query.clone();
        let snapshot = php.semantic_snapshot();
        let mut count = 0;
        'classes: for class in php.class_symbols_view().iter() {
            ctx.check()?;
            if class.kind != PhpSymbolKind::Class || !is_symfony_controller_class(class) {
                continue;
            }
            let class_name = trim_backslash(&class.fully_qualified);
            for member in snapshot.members_of(&class.id) {
                if member.kind != PhpSymbolKind::Method
                    || member.visibility != Visibility::Public
                    || (member.name.starts_with("__") && &*member.name != "__invoke")
                {
                    continue;
                }
                let mut name = class_name.to_owned();
                if &*member.name != "__invoke" {
                    name.push_str("::");
                    name.push_str(&member.name);
                }
                let container = format!("Symfony controller · {class_name}");
                if symbol_match_score(&query, &[&name, &container]).is_none() {
                    continue;
                }
                collector.add(Candidate {
                    name,
                    container,
                    path: member.path.to_string(),
                    range: member.selection_range,
                    kind: Some(SymbolKind::METHOD),
                    ..Candidate::default()
                });
                count += 1;
                if count >= MAX_CONTROLLER_SYMBOLS {
                    break 'classes;
                }
            }
        }
        if count >= MAX_CONTROLLER_SYMBOLS {
            return Ok(());
        }
        let Some(routes) = &self.routes else {
            return Ok(());
        };
        for route in routes.get_routes_view()?.iter() {
            ctx.check()?;
            let Some(reference) = parse_controller_reference(&route.controller) else {
                continue;
            };
            let resolution =
                resolve_controller_reference(&reference, self.services.as_deref(), Some(php))?;
            if !resolution.method_found {
                continue;
            }
            let method = resolution.method.unwrap_or_default();
            let container = format!("Symfony controller · {}", &*method.fully_qualified);
            if symbol_match_score(&query, &[&route.controller, &container]).is_none() {
                continue;
            }
            collector.add(Candidate {
                name: route.controller.clone(),
                container,
                path: method.path.to_string(),
                range: method.selection_range,
                kind: Some(SymbolKind::METHOD),
                ..Candidate::default()
            });
        }
        Ok(())
    }

    fn collect_services(&self, ctx: &Context, collector: &mut Collector) -> ProviderResult<()> {
        let Some(services) = &self.services else {
            return Ok(());
        };
        let query = collector.query.clone();
        let mut count = 0;
        for name in services.get_all_services()? {
            ctx.check()?;
            if symbol_match_score(&query, &[&name, "Symfony service"]).is_none() {
                continue;
            }
            let Some(service) = services.get_service_by_id(&name)? else {
                continue;
            };
            if service.path.is_empty() {
                continue;
            }
            let mut container = "Symfony service".to_owned();
            if !service.class.is_empty() {
                container.push_str(" · ");
                container.push_str(&service.class);
            }
            collector.add(Candidate {
                name,
                container,
                path: service.path,
                line: i64::from(service.line),
                kind: Some(SymbolKind::OBJECT),
                ..Candidate::default()
            });
            count += 1;
            if count >= MAX_SERVICE_SYMBOLS {
                break;
            }
        }
        for parameter in services.get_all_parameters()? {
            ctx.check()?;
            collector.add(Candidate {
                name: parameter.name,
                container: "Symfony parameter".to_owned(),
                path: parameter.path,
                line: i64::from(parameter.line),
                kind: Some(SymbolKind::CONSTANT),
                ..Candidate::default()
            });
        }
        Ok(())
    }

    fn collect_routes(&self, ctx: &Context, collector: &mut Collector) -> ProviderResult<()> {
        let Some(routes) = &self.routes else {
            return Ok(());
        };
        let query = collector.query.clone();
        let path_search = query.contains('/');
        let matcher = RoutePathSearchMatcher::new(&query);
        for route in routes.get_routes_view()?.iter() {
            ctx.check()?;
            collector.add(Candidate {
                name: route.name.clone(),
                container: format!("Symfony route · {}", route_endpoint_symbol_detail(route)),
                path: route.file_path.clone(),
                line: i64::from(route.line),
                kind: Some(SymbolKind::METHOD),
                ..Candidate::default()
            });
            if path_search && matcher.matches(&route.path) {
                collector.add(Candidate {
                    name: query.clone(),
                    container: format!("Symfony route URL · {} · {}", route.path, route.name),
                    path: route.file_path.clone(),
                    line: i64::from(route.line),
                    kind: Some(SymbolKind::METHOD),
                    ..Candidate::default()
                });
            }
        }
        Ok(())
    }

    fn collect_commands(&self, ctx: &Context, collector: &mut Collector) -> ProviderResult<()> {
        let Some(commands) = &self.commands else {
            return Ok(());
        };
        for command in commands.get_commands()? {
            ctx.check()?;
            let mut container = "Symfony command".to_owned();
            if !command.description.is_empty() {
                container.push_str(" · ");
                container.push_str(&command.description);
            }
            collector.add(Candidate {
                name: command.name,
                container,
                path: command.file,
                range: command.range,
                kind: Some(SymbolKind::FUNCTION),
                ..Candidate::default()
            });
        }
        Ok(())
    }

    fn collect_twig(&self, ctx: &Context, collector: &mut Collector) -> ProviderResult<()> {
        let Some(twig) = &self.twig else {
            return Ok(());
        };
        let query = collector.query.clone();
        for name in twig.get_all_template_files()? {
            if symbol_match_score(&query, &[&name, "Twig template"]).is_none() {
                continue;
            }
            for file in twig.get_twig_files_by_rel_path(&name)? {
                collector.add(Candidate {
                    name: name.clone(),
                    container: "Twig template".to_owned(),
                    path: file.path,
                    kind: Some(SymbolKind::FILE),
                    ..Candidate::default()
                });
            }
        }
        for block in twig.get_all_template_blocks()? {
            collector.add(Candidate {
                name: block.name,
                container: "Twig block".to_owned(),
                path: block.file_path,
                range: block.range,
                line: block.line,
                kind: Some(SymbolKind::FIELD),
                ..Candidate::default()
            });
        }
        for macro_ in twig.get_all_macros()? {
            collector.add(Candidate {
                container: format!("Twig macro · {}", macro_.signature()),
                name: macro_.name,
                path: macro_.file_path,
                range: macro_.name_range,
                kind: Some(SymbolKind::FUNCTION),
                ..Candidate::default()
            });
        }
        for function in twig.get_all_twig_functions()? {
            collector.add(Candidate {
                name: function.name,
                container: "Twig function".to_owned(),
                path: function.file_path,
                line: function.line,
                kind: Some(SymbolKind::FUNCTION),
                ..Candidate::default()
            });
        }
        for filter in twig.get_all_twig_filters()? {
            collector.add(Candidate {
                name: filter.name,
                container: "Twig filter".to_owned(),
                path: filter.file_path,
                line: filter.line,
                kind: Some(SymbolKind::FUNCTION),
                ..Candidate::default()
            });
        }
        ctx.check()?;
        Ok(())
    }

    fn collect_components(&self, ctx: &Context, collector: &mut Collector) -> ProviderResult<()> {
        let Some(components) = &self.components else {
            return Ok(());
        };
        for component in components.components()? {
            ctx.check()?;
            let mut container = if component.live {
                "Live component".to_owned()
            } else {
                "Twig component".to_owned()
            };
            if !component.class.is_empty() {
                container.push_str(" · ");
                container.push_str(&component.class);
            }
            collector.add(Candidate {
                name: component.name,
                container,
                path: component.file,
                range: component.name_range,
                kind: Some(SymbolKind::CLASS),
                ..Candidate::default()
            });
        }
        Ok(())
    }

    fn collect_translations(&self, ctx: &Context, collector: &mut Collector) -> ProviderResult<()> {
        let Some(translations) = &self.translations else {
            return Ok(());
        };
        let mut domain_files = HashSet::new();
        for message in translations.messages()? {
            ctx.check()?;
            let range = protocol::Range {
                start: protocol::Position {
                    line: message.line,
                    character: message.character,
                },
                end: protocol::Position {
                    line: message.end_line,
                    character: message.end_character,
                },
            };
            let mut container = format!("Translation · {}", message.domain);
            if !message.locale.is_empty() {
                container.push_str(" · ");
                container.push_str(&message.locale);
            }
            let domain_key = (
                go_to_lower(&message.domain),
                message.locale.clone(),
                filepath::clean(&message.file),
            );
            if !message.domain.is_empty() && domain_files.insert(domain_key) {
                let mut domain_container = "Translation domain".to_owned();
                if !message.locale.is_empty() {
                    domain_container.push_str(" · ");
                    domain_container.push_str(&message.locale);
                }
                collector.add(Candidate {
                    name: message.domain.clone(),
                    container: domain_container,
                    path: message.file.clone(),
                    kind: Some(SymbolKind::NAMESPACE),
                    location_range: Some(range),
                    ..Candidate::default()
                });
            }
            collector.add(Candidate {
                name: message.key,
                container,
                path: message.file,
                kind: Some(SymbolKind::STRING),
                location_range: Some(range),
                ..Candidate::default()
            });
        }
        Ok(())
    }
}

fn trim_backslash(value: &str) -> &str {
    value.strip_prefix('\\').unwrap_or(value)
}

fn is_symfony_controller_class(class: &Symbol) -> bool {
    let name = go_to_lower(trim_backslash(&class.fully_qualified));
    if !name.ends_with("controller") || !name.contains("\\controller\\") {
        return false;
    }
    let path = go_to_lower(&filepath::to_slash(&class.path));
    !path.contains("/test/") && !path.contains("/tests/")
}

fn route_endpoint_symbol_detail(route: &Route) -> String {
    let mut methods: Vec<String> = Vec::new();
    for method in &route.methods {
        let method = method.trim().to_uppercase();
        if method.is_empty() || methods.contains(&method) {
            continue;
        }
        methods.push(method);
    }
    let mut detail = if methods.is_empty() {
        "ANY".to_owned()
    } else {
        methods.join("|")
    };
    if !route.path.is_empty() {
        detail.push(' ');
        detail.push_str(&route.path);
    }
    let controller = short_route_controller(&route.controller);
    if !controller.is_empty() {
        detail.push_str(" · ");
        detail.push_str(&controller);
    }
    detail
}

fn short_route_controller(controller: &str) -> String {
    let controller = controller.trim();
    if controller.is_empty() {
        return String::new();
    }
    if let Some((class, method)) = controller.split_once("::") {
        return format!("{}:{method}", short_route_controller_class(class));
    }
    if let Some(offset) = controller.rfind(':')
        && offset > 0
        && offset < controller.len() - 1
    {
        return format!(
            "{}:{}",
            short_route_controller_class(&controller[..offset]),
            &controller[offset + 1..]
        );
    }
    short_route_controller_class(controller)
}

fn short_route_controller_class(value: &str) -> String {
    let value = trim_backslash(value).trim();
    if let Some(offset) = value.rfind('\\')
        && offset < value.len() - 1
    {
        return value[offset + 1..].to_owned();
    }
    value.to_owned()
}

/// 0 exact, 1 prefix, 2 substring, 3 empty query; `None` without a match.
fn symbol_match_score(query: &str, values: &[&str]) -> Option<u8> {
    let query = go_to_lower(query.trim());
    if query.is_empty() {
        return Some(3);
    }
    let mut best = 4;
    for value in values {
        let value = go_to_lower(value);
        if value == query {
            best = 0;
        } else if value.starts_with(&query) {
            best = best.min(1);
        } else if value.contains(&query) {
            best = best.min(2);
        }
    }
    (best < 4).then_some(best)
}

fn symbol_location(
    current: &Candidate,
    sources: &mut HashMap<String, Option<LineIndex>>,
) -> Option<protocol::Location> {
    if current.path.is_empty() {
        return None;
    }
    let uri = swlsp_uriutil::file_uri(&current.path);
    if let Some(range) = current.location_range {
        return Some(protocol::Location { uri, range });
    }
    if !current.range.is_empty() {
        let line_index = sources
            .entry(current.path.clone())
            .or_insert_with(|| {
                std::fs::read(&current.path)
                    .ok()
                    .map(|source| LineIndex::new(String::from_utf8_lossy(&source).into_owned()))
            })
            .as_ref()?;
        return Some(protocol::Location {
            uri,
            range: swlsp_lsp::protocol_range(line_index, current.range),
        });
    }
    let mut location = protocol::Location {
        uri,
        range: protocol::Range::default(),
    };
    if current.line > 0 {
        let line = (current.line - 1) as u32;
        location.range.start.line = line;
        location.range.end.line = line;
    }
    Some(location)
}
