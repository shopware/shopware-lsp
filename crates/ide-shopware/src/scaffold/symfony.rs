//! Symfony class and service-configuration scaffolds.

use std::collections::HashMap;
use std::path::Path;
use swlsp_textutil::html_escape;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_lsp::Context;
use swlsp_php_project::Model;
use swlsp_uriutil::file_uri;

use crate::scaffold::paths::{self, FileError, Stat};
use crate::scaffold::{Provider, Result, errorf, snake_case};

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Request {
    pub kind: String,
    #[serde(rename = "directoryUri")]
    pub directory_uri: String,
    pub name: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Response {
    #[serde(rename = "fileUri")]
    pub file_uri: String,
    pub content: String,
    pub language: String,
    #[serde(rename = "className", skip_serializing_if = "String::is_empty")]
    pub class_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub namespace: String,
}

impl Provider {
    pub fn create(&self, ctx: &Context, args: Option<&Value>) -> Result<Response> {
        let Some(php) = &self.php else {
            return Err(errorf!("symfony scaffold generator is unavailable"));
        };
        ctx.check()?;
        let params: Request = crate::scaffold::decode_args(
            args,
            "missing scaffold request",
            "invalid scaffold request",
        )?;
        let directory = swlsp_uriutil::path(&params.directory_uri)
            .map_err(|error| errorf!("resolve scaffold directory: {error}"))?;
        let directory = self.validated_directory(&directory)?;
        let kind = params.kind.trim().to_lowercase();
        if !is_php_scaffold(&kind) && !is_service_scaffold(&kind) {
            return Err(errorf!("unsupported Symfony scaffold kind {:?}", kind));
        }
        if is_service_scaffold(&kind) {
            return self.service_scaffold(&directory, &kind, &params.name);
        }
        let class_name = apply_scaffold_suffix(params.name.trim(), scaffold_class_suffix(&kind));
        if !valid_php_class_name(&class_name) {
            return Err(errorf!("invalid PHP class name {:?}", params.name));
        }
        let namespace = namespace_for_directory(php.project().as_deref(), &directory)?;
        let content = self.php_scaffold(&kind, &namespace, &class_name)?;
        let target = paths::join(&[&directory, &format!("{class_name}.php")]);
        ensure_scaffold_target_available(&target)?;
        if !swlsp_parser_php::parse(&content).errors.is_empty() {
            return Err(errorf!("generated PHP scaffold is invalid"));
        }
        Ok(Response {
            file_uri: file_uri(&target),
            content,
            language: "php".to_owned(),
            class_name,
            namespace,
        })
    }

    pub(crate) fn validated_directory(&self, path: &str) -> Result<String> {
        let root = paths::abs(&self.root)
            .map_err(|error| errorf!("cannot resolve workspace root: {error}"))?;
        let directory = paths::abs(path)
            .map_err(|error| errorf!("cannot resolve scaffold directory: {error}"))?;
        let canonical_root = paths::resolved_directory_path(&root);
        let canonical_directory = paths::resolved_directory_path(&directory);
        if paths::rel_within(&canonical_root, &canonical_directory).is_none() {
            return Err(errorf!(
                "scaffold directory {:?} is outside the workspace",
                path
            ));
        }
        match paths::stat(&directory) {
            Stat::Exists(metadata) if metadata.is_dir() => Ok(directory),
            Stat::Exists(_) => Err(errorf!("scaffold target is not a directory")),
            Stat::Missing => Err(errorf!("scaffold directory {directory} does not exist")),
            Stat::Failed(error) => Err(FileError::new("inspect", &directory, error).into()),
        }
    }

    fn php_scaffold(&self, kind: &str, namespace: &str, class_name: &str) -> Result<String> {
        Ok(match kind {
            "command" => self.command_template(namespace, class_name),
            "controller" => self.controller_template(namespace, class_name),
            "twig-extension" => self.twig_extension_template(namespace, class_name),
            "compiler-pass" => compiler_pass_scaffold_template(namespace, class_name),
            "kernel-test" => kernel_test_template(namespace, class_name),
            "web-test" => web_test_template(namespace, class_name),
            _ => {
                return Err(errorf!("unsupported Symfony scaffold kind {:?}", kind));
            }
        })
    }

    fn find_class(&self, name: &str) -> bool {
        self.php
            .as_ref()
            .is_some_and(|php| php.find_class(name).is_some())
    }

    fn command_template(&self, namespace: &str, class_name: &str) -> String {
        let command_name = format!(
            "{}:{}",
            self.command_prefix(namespace),
            snake_case(class_name.strip_suffix("Command").unwrap_or(class_name))
        );
        let header = php_header(namespace);
        match self.command_template_style(namespace) {
            "invokable" => format!(
                r"{header}use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Style\SymfonyStyle;

#[AsCommand(name: '{command_name}', description: 'Add a description')]
final class {class_name}
{{
    public function __invoke(SymfonyStyle $io): int
    {{
        return Command::SUCCESS;
    }}
}}
"
            ),
            "attribute" => format!(
                r"{header}use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

#[AsCommand(name: '{command_name}', description: 'Add a description')]
final class {class_name} extends Command
{{
    protected function execute(InputInterface $input, OutputInterface $output): int
    {{
        return Command::SUCCESS;
    }}
}}
"
            ),
            "property" => format!(
                r"{header}use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

final class {class_name} extends Command
{{
    protected static $defaultName = '{command_name}';
    protected static $defaultDescription = 'Add a description';

    protected function execute(InputInterface $input, OutputInterface $output): int
    {{
        return Command::SUCCESS;
    }}
}}
"
            ),
            _ => format!(
                r"{header}use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

final class {class_name} extends Command
{{
    protected function configure(): void
    {{
        $this
            ->setName('{command_name}')
            ->setDescription('Add a description');
    }}

    protected function execute(InputInterface $input, OutputInterface $output): int
    {{
        return Command::SUCCESS;
    }}
}}
"
            ),
        }
    }

    fn command_template_style(&self, namespace: &str) -> &'static str {
        if self.find_class("Symfony\\Component\\Console\\Command\\InvokableCommand")
            && !self.namespace_uses_execute_commands(namespace)
        {
            return "invokable";
        }
        if self.find_class("Symfony\\Component\\Console\\Attribute\\AsCommand") {
            return "attribute";
        }
        if let Some(php) = &self.php {
            for property in php.properties("Symfony\\Component\\Console\\Command\\Command") {
                if property
                    .name
                    .as_str()
                    .trim_start_matches('$')
                    .eq_ignore_ascii_case("defaultName")
                {
                    return "property";
                }
            }
        }
        "configure"
    }

    fn namespace_uses_execute_commands(&self, namespace: &str) -> bool {
        let Some(php) = &self.php else {
            return false;
        };
        let prefix = format!("{}\\", namespace.trim_matches('\\')).to_lowercase();
        let snapshot = php.semantic_snapshot();
        for class in php.class_symbols_view().iter() {
            let fully_qualified = class.fully_qualified.as_str();
            if !fully_qualified.to_lowercase().starts_with(&prefix) {
                continue;
            }
            if !snapshot.is_subtype_of(
                fully_qualified,
                "Symfony\\Component\\Console\\Command\\Command",
            ) {
                continue;
            }
            for method in php.methods(fully_qualified) {
                if method.name.as_str().eq_ignore_ascii_case("execute")
                    && method.container == class.id
                {
                    return true;
                }
            }
        }
        false
    }

    fn command_prefix(&self, namespace: &str) -> String {
        let mut counts: HashMap<String, usize> = HashMap::new();
        if let Some(console) = &self.console {
            let commands = console.get_commands().unwrap_or_default();
            let namespace_prefix = format!("{}\\", namespace.trim_matches('\\')).to_lowercase();
            for command in commands {
                if !command
                    .class
                    .trim_matches('\\')
                    .to_lowercase()
                    .starts_with(&namespace_prefix)
                {
                    continue;
                }
                if let Some(separator) = command.name.find(':')
                    && separator > 0
                {
                    *counts
                        .entry(command.name[..separator].to_owned())
                        .or_default() += 1;
                }
            }
        }
        let mut best = String::new();
        let mut best_count = 0;
        for (prefix, count) in counts {
            if count > best_count || count == best_count && (best.is_empty() || prefix < best) {
                best = prefix;
                best_count = count;
            }
        }
        if best.is_empty() {
            "app".to_owned()
        } else {
            best
        }
    }

    fn controller_template(&self, namespace: &str, class_name: &str) -> String {
        let base = class_name.strip_suffix("Controller").unwrap_or(class_name);
        let path = format!("/{}", snake_case(base).replace('_', "-"));
        let template = snake_case(base);
        let (route_namespace, route_syntax) =
            if self.find_class("Symfony\\Component\\Routing\\Attribute\\Route") {
                ("Attribute", format!("#[Route('{path}')]"))
            } else {
                (
                    "Annotation",
                    format!("/**\n     * @Route(\"{path}\")\n     */"),
                )
            };
        format!(
            r"{}use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\Routing\{route_namespace}\Route;

final class {class_name} extends AbstractController
{{
    {route_syntax}
    public function index(): Response
    {{
        return $this->render('{template}/index.html.twig');
    }}
}}
",
            php_header(namespace)
        )
    }

    fn twig_extension_template(&self, namespace: &str, class_name: &str) -> String {
        let header = php_header(namespace);
        if self.find_class("Twig\\Attribute\\AsTwigFunction") {
            return format!(
                r"{header}use Twig\Attribute\AsTwigFilter;
use Twig\Attribute\AsTwigFunction;

final class {class_name}
{{
    #[AsTwigFilter('my_filter')]
    public function myFilter(string $value): string
    {{
        return $value;
    }}

    #[AsTwigFunction('my_function')]
    public function myFunction(): string
    {{
        return 'Hello from Twig';
    }}
}}
"
            );
        }
        format!(
            r"{header}use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;

final class {class_name} extends AbstractExtension
{{
    public function getFunctions(): array
    {{
        return [
            new TwigFunction('my_function', $this->myFunction(...)),
        ];
    }}

    public function getFilters(): array
    {{
        return [
            new TwigFilter('my_filter', $this->myFilter(...)),
        ];
    }}

    public function myFunction(): string
    {{
        return 'Hello from Twig';
    }}

    public function myFilter(string $value): string
    {{
        return $value;
    }}
}}
"
        )
    }

    fn service_scaffold(&self, directory: &str, kind: &str, name: &str) -> Result<Response> {
        let mut name = name.trim().to_owned();
        if name.is_empty() {
            name = "services".to_owned();
        }
        if !valid_file_name(&name) {
            return Err(errorf!("invalid service configuration name {:?}", name));
        }
        let extension = kind.strip_prefix("services-").unwrap_or(kind);
        let current_ext = paths::ext(&name).to_owned();
        if current_ext.eq_ignore_ascii_case(&format!(".{extension}")) {
            name.truncate(name.len() - current_ext.len());
        }
        let target = paths::join(&[directory, &format!("{name}.{extension}")]);
        ensure_scaffold_target_available(&target)?;
        let model = self.php.as_ref().and_then(|php| php.project());
        let (namespace, source_root) = primary_project_psr4(model.as_deref(), directory)?;
        let mut relative_source = "../src/".to_owned();
        if !source_root.is_empty()
            && let Ok(relative) = paths::rel(directory, &source_root)
        {
            relative_source = paths::to_slash(&relative);
            if relative_source == "." {
                relative_source = "./".to_owned();
            } else {
                relative_source = format!("{}/", relative_source.trim_end_matches('/'));
            }
        }
        let content = match extension {
            "yaml" => {
                let content = yaml_services_template(&namespace, &relative_source);
                if !swlsp_parser_yaml::parse(&content).errors.is_empty() {
                    return Err(errorf!("generated YAML service scaffold is invalid"));
                }
                content
            }
            "xml" => {
                let content = xml_services_template(&namespace, &relative_source);
                if !swlsp_parser_xml::parse(&content).errors.is_empty() {
                    return Err(errorf!("generated XML service scaffold is invalid"));
                }
                content
            }
            "php" => {
                let content = php_services_template(&namespace, &relative_source);
                if !swlsp_parser_php::parse(&content).errors.is_empty() {
                    return Err(errorf!("generated PHP service scaffold is invalid"));
                }
                content
            }
            _ => {
                return Err(errorf!("unsupported service scaffold kind {:?}", kind));
            }
        };
        Ok(Response {
            file_uri: file_uri(&target),
            content,
            language: extension.to_owned(),
            class_name: String::new(),
            namespace: String::new(),
        })
    }
}

fn valid_file_name(name: &str) -> bool {
    !name.is_empty()
        && name
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'.' | b'-'))
}

fn psr4_mappings(model: &Model, directory: &str) -> Result<Vec<(String, String)>> {
    let mappings = model
        .psr4_mappings_for_directory(Path::new(directory))
        .map_err(|error| errorf!("{error}"))?;
    Ok(mappings
        .into_iter()
        .map(|mapping| {
            (
                mapping.namespace,
                mapping.root.to_string_lossy().into_owned(),
            )
        })
        .collect())
}

pub(crate) fn namespace_for_directory(model: Option<&Model>, directory: &str) -> Result<String> {
    let Some(model) = model else {
        return Err(errorf!("no Composer project model is available"));
    };
    let canonical_directory = paths::resolved_directory_path(directory);
    let mut candidates: Vec<(String, String)> = Vec::new();
    for (namespace, root) in psr4_mappings(model, directory)? {
        let root = paths::resolved_directory_path(&paths::clean(&root));
        if paths::rel_within(&root, &canonical_directory).is_none() {
            continue;
        }
        candidates.push((namespace.trim_matches('\\').to_owned(), root));
    }
    if candidates.is_empty() {
        return Err(errorf!("no Composer PSR-4 namespace maps to {directory}"));
    }
    candidates.sort_by(|left, right| {
        right
            .1
            .len()
            .cmp(&left.1.len())
            .then_with(|| left.0.cmp(&right.0))
    });
    let (selected_namespace, selected_root) = &candidates[0];
    let relative = paths::rel(selected_root, &canonical_directory).unwrap_or_default();
    let mut namespace = selected_namespace.clone();
    if relative != "." && !relative.is_empty() {
        namespace.push('\\');
        namespace.push_str(&paths::to_slash(&relative).replace('/', "\\"));
    }
    Ok(namespace.trim_matches('\\').to_owned())
}

fn primary_project_psr4(model: Option<&Model>, directory: &str) -> Result<(String, String)> {
    let Some(model) = model else {
        return Ok(("App\\".to_owned(), String::new()));
    };
    let mut entries: Vec<(String, String)> = psr4_mappings(model, directory)?
        .into_iter()
        .map(|(namespace, root)| {
            let mut namespace = namespace.trim_matches('\\').to_owned();
            if !namespace.is_empty() {
                namespace.push('\\');
            }
            (namespace, paths::clean(&root))
        })
        .collect();
    entries.sort_by(|left, right| {
        let left_test = left.1.to_lowercase().contains("test");
        let right_test = right.1.to_lowercase().contains("test");
        left_test
            .cmp(&right_test)
            .then_with(|| left.1.len().cmp(&right.1.len()))
            .then_with(|| left.0.cmp(&right.0))
    });
    Ok(entries
        .into_iter()
        .next()
        .unwrap_or_else(|| ("App\\".to_owned(), String::new())))
}

pub(crate) fn ensure_scaffold_target_available(target: &str) -> Result<()> {
    match paths::stat(target) {
        Stat::Exists(_) => Err(errorf!("scaffold file already exists: {target}")),
        Stat::Missing => Ok(()),
        Stat::Failed(error) => Err(FileError::new("inspect", target, error).into()),
    }
}

fn is_service_scaffold(kind: &str) -> bool {
    matches!(kind, "services-yaml" | "services-xml" | "services-php")
}

fn is_php_scaffold(kind: &str) -> bool {
    matches!(
        kind,
        "command" | "controller" | "twig-extension" | "compiler-pass" | "kernel-test" | "web-test"
    )
}

fn scaffold_class_suffix(kind: &str) -> &'static str {
    match kind {
        "command" => "Command",
        "controller" => "Controller",
        "kernel-test" | "web-test" => "Test",
        _ => "",
    }
}

fn apply_scaffold_suffix(name: &str, suffix: &str) -> String {
    if !suffix.is_empty() && !name.to_lowercase().ends_with(&suffix.to_lowercase()) {
        return format!("{name}{suffix}");
    }
    name.to_owned()
}

fn valid_php_class_name(name: &str) -> bool {
    !name.is_empty()
        && name.bytes().enumerate().all(|(index, current)| {
            current >= 0x80
                || current == b'_'
                || current.is_ascii_alphabetic()
                || index > 0 && current.is_ascii_digit()
        })
}

fn php_header(namespace: &str) -> String {
    let header = "<?php\n\ndeclare(strict_types=1);\n\n";
    if namespace.is_empty() {
        return header.to_owned();
    }
    format!("{header}namespace {namespace};\n\n")
}

fn compiler_pass_scaffold_template(namespace: &str, class_name: &str) -> String {
    format!(
        r"{}use Symfony\Component\DependencyInjection\Compiler\CompilerPassInterface;
use Symfony\Component\DependencyInjection\ContainerBuilder;

final class {class_name} implements CompilerPassInterface
{{
    public function process(ContainerBuilder $container): void
    {{
    }}
}}
",
        php_header(namespace)
    )
}

fn kernel_test_template(namespace: &str, class_name: &str) -> String {
    format!(
        r"{}use Symfony\Bundle\FrameworkBundle\Test\KernelTestCase;

final class {class_name} extends KernelTestCase
{{
    public function testSomething(): void
    {{
        self::bootKernel();

        $container = static::getContainer();

        self::assertNotNull($container);
    }}
}}
",
        php_header(namespace)
    )
}

fn web_test_template(namespace: &str, class_name: &str) -> String {
    format!(
        r"{}use Symfony\Bundle\FrameworkBundle\Test\WebTestCase;

final class {class_name} extends WebTestCase
{{
    public function testSomething(): void
    {{
        $client = static::createClient();
        $client->request('GET', '/');

        self::assertResponseIsSuccessful();
    }}
}}
",
        php_header(namespace)
    )
}

fn yaml_services_template(namespace: &str, source: &str) -> String {
    let namespace_key = if namespace.is_empty() {
        "''"
    } else {
        namespace
    };
    let source = source.replace('\'', "''");
    format!(
        "services:
  _defaults:
    autowire: true
    autoconfigure: true

  {namespace_key}:
    resource: '{source}'
    exclude:
      - '{source}DependencyInjection/'
      - '{source}Entity/'
      - '{source}Kernel.php'
"
    )
}

fn xml_services_template(namespace: &str, source: &str) -> String {
    let namespace = html_escape(namespace);
    let source = html_escape(source);
    format!(
        r#"<?xml version="1.0" encoding="UTF-8" ?>
<container xmlns="http://symfony.com/schema/dic/services"
           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
           xsi:schemaLocation="http://symfony.com/schema/dic/services
        https://symfony.com/schema/dic/services/services-1.0.xsd">
    <services>
        <defaults autowire="true" autoconfigure="true"/>
        <prototype namespace="{namespace}" resource="{source}" exclude="{source}{{DependencyInjection,Entity,Kernel.php}}"/>
    </services>
</container>
"#
    )
}

fn php_services_template(namespace: &str, source: &str) -> String {
    let escaped_namespace = namespace.replace('\\', "\\\\");
    let escaped_source = source.replace('\'', "\\'");
    format!(
        r"<?php

declare(strict_types=1);

namespace Symfony\Component\DependencyInjection\Loader\Configurator;

return static function (ContainerConfigurator $container): void {{
    $services = $container->services()
        ->defaults()
        ->autowire()
        ->autoconfigure();

    $services->load('{escaped_namespace}', '{escaped_source}')
        ->exclude('{escaped_source}{{DependencyInjection,Entity,Kernel.php}}');
}};
"
    )
}
