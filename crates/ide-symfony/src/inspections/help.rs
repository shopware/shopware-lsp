//! What to do about the Symfony diagnostics; see [`swlsp_lsp::RuleHelp`].

use swlsp_lsp::RuleHelp;

const fn help(
    code: &'static str,
    fix: &'static str,
    exceptions: &'static str,
    opt_out: &'static str,
) -> RuleHelp {
    RuleHelp {
        code,
        fix,
        exceptions,
        opt_out,
    }
}

const CONTAINER: &str = "Missing services and parameters are judged only against a compiled \
                         container (var/cache/*/…DebugContainer.xml): without one nothing is \
                         reported. Run bin/console cache:warmup once.";

pub(crate) const SYMFONY: &[RuleHelp] = &[
    help(
        "symfony.asset.missing",
        "Correct the path to an asset that exists; a quick fix offers close names.",
        "Paths built at runtime, URLs, and raw HTML paths with no close indexed asset (they \
         may be served by a dev server) are not reported.",
        "",
    ),
    help(
        "symfony.asset.package.missing",
        "Use an asset package configured under framework.assets.packages; a quick fix \
         offers close names.",
        "",
        "",
    ),
    help(
        "symfony.asset_mapper.entrypoint.missing",
        "Use an importmap entry marked entrypoint: true, or mark the entry as an entrypoint \
         in importmap.php.",
        "",
        "",
    ),
    help(
        "symfony.encore.entry.missing",
        "Use an entry name webpack.config.js adds with addEntry(); a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "symfony.vite.entry.missing",
        "Use an entry name the Vite configuration declares; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "symfony.console.command.missing",
        "Use the name of a registered command; a quick fix offers close names.",
        "Only find(), get() and has() on a console Application are checked.",
        "",
    ),
    help(
        "symfony.console.argument.missing",
        "Declare the argument in the command's configure() (addArgument) or correct the \
         name; a quick fix offers close names.",
        "Not reported for a command that declares no arguments at all.",
        "",
    ),
    help(
        "symfony.console.option.missing",
        "Declare the option in the command's configure() (addOption) or correct the name; a \
         quick fix offers close names.",
        "Not reported for a command that declares no options at all. An option's shortcut \
         counts.",
        "",
    ),
    help(
        "symfony.console.invoke.return_type",
        "Declare `: int` on __invoke(); a quick fix adds it.",
        "Only classes with #[AsCommand] are checked.",
        "",
    ),
    help(
        "symfony.console.invoke.return_value",
        "Return an exit code: Command::SUCCESS, Command::FAILURE or Command::INVALID.",
        "Not reported once __invoke() declares an int return type; the PHP analysis checks \
         the value then.",
        "",
    ),
    help(
        "symfony.constant.missing",
        "Use an existing constant (Class::NAME) or enum case in !php/const, !php/enum or \
         <argument type=\"constant\">.",
        "",
        "",
    ),
    help(
        "symfony.doctrine.table.missing",
        "Correct the table name; a quick fix offers the close name.",
        "Reported only for a name close to a table an entity definition declares; other \
         tables (migrations, third-party tables) are not known and not reported.",
        "",
    ),
    help(
        "symfony.doctrine.column.missing",
        "Correct the column name to a storage name of the entity's fields; a quick fix \
         offers the close name.",
        "Reported only for a name close to a known column of a known table.",
        "",
    ),
    help(
        "symfony.doctrine.type_class.missing",
        "Register an existing class that extends Doctrine\\DBAL\\Types\\Type; a quick fix \
         offers known type classes.",
        "",
        "",
    ),
    help(
        "symfony.doctrine.type_class.invalid",
        "Make the class extend Doctrine\\DBAL\\Types\\Type, or register one that does.",
        "",
        "",
    ),
    help(
        "symfony.service.duplicate",
        "Remove or rename one of the two definitions; the later one replaces the earlier \
         without warning.",
        "Only repeats within one file are reported.",
        "",
    ),
    help(
        "symfony.parameter.duplicate",
        "Remove or rename one of the two parameters; the later one replaces the earlier.",
        "Only repeats within one file are reported.",
        "",
    ),
    help(
        "symfony.route.duplicate",
        "Give each route its own name; the later one replaces the earlier.",
        "Only repeats within one file are reported. A name on a class-level #[Route] is a \
         prefix, not a route.",
        "",
    ),
    help(
        "symfony.route.redefined",
        "Rename one of the routes. If replacing a route of a bundle is intended, keep the \
         name and suppress the finding.",
        "Definitions in vendor and test directories neither count nor are reported: \
         redefining a bundle's route by name is the documented way to override it.",
        "",
    ),
    help(
        "symfony.php.embedded_css.invalid",
        "Correct the CSS selector; the message carries the parser's error.",
        "Strings with interpolation are not checked.",
        "",
    ),
    help(
        "symfony.php.embedded_json.invalid",
        "Correct the JSON, or build the response from an array with new JsonResponse($data).",
        "Strings with interpolation are not checked.",
        "",
    ),
    help(
        "symfony.php.embedded_xpath.invalid",
        "Correct the XPath expression; the message carries the parser's error.",
        "Strings with interpolation are not checked.",
        "",
    ),
    help(
        "symfony.event.missing",
        "Check the event name: nothing listens to it. A quick fix offers names that have \
         listeners. An event dispatched for other extensions to use needs no listener.",
        "Only dispatch sites are checked, and only once the project has listeners indexed.",
        "",
    ),
    help(
        "symfony.event.listener_method.missing",
        "Add the method to the listener class or correct its name; quick fixes create the \
         method or offer close names.",
        "",
        "",
    ),
    help(
        "symfony.messenger.message.missing",
        "Name an existing message class in `handles`; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "symfony.messenger.handler_method.missing",
        "Name a public method of the handler class; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "symfony.route.missing",
        "Use the name of an existing route; a quick fix offers close names.",
        "Names built at runtime are not checked.",
        "",
    ),
    help(
        "symfony.controller.target.missing",
        "Point the route at an existing controller class or service \
         (App\\Controller\\Foo::action).",
        "Values with parameters or placeholders are not checked.",
        "",
    ),
    help(
        "symfony.controller.method.missing",
        "Add the action to the controller or correct its name; a quick fix creates the \
         method with the route's placeholders as parameters.",
        "",
        "",
    ),
    help(
        "symfony.controller.deprecated",
        "Route to the replacement the controller's @deprecated note names.",
        "",
        "",
    ),
    help(
        "symfony.serializer.class.missing",
        "Pass an existing class name, preferably as Foo::class; a quick fix offers close names.",
        "Only string class names are checked.",
        "",
    ),
    help(
        "symfony.service.missing",
        "Use the ID of an existing service; a quick fix offers close names. A service of \
         the extension must be defined in its services configuration.",
        CONTAINER,
        "Mark an optional reference: `@?id` in YAML, on-invalid=\"null\" in XML, \
         ->nullOnInvalid() in PHP configuration. A get('id') guarded by has('id') in the same \
         file is not reported either.",
    ),
    help(
        "symfony.parameter.missing",
        "Use the name of an existing parameter; a quick fix offers close names.",
        CONTAINER,
        "A getParameter('name') guarded by hasParameter('name') in the same file is not reported.",
    ),
    help(
        "symfony.class.missing",
        "Correct the class name; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "symfony.service.deprecated",
        "Use the replacement the deprecation message names.",
        "",
        "",
    ),
    help(
        "symfony.class.deprecated",
        "Use the replacement the class's @deprecated note names.",
        "",
        "",
    ),
    help(
        "symfony.service.arguments.missing",
        "Add the missing constructor arguments, or enable autowiring for the service; a \
         quick fix adds them, using the service of the matching type where there is exactly one.",
        "Autowired, abstract, aliased and factory-built services, and services with a \
         parent, are not checked.",
        "",
    ),
    help(
        "symfony.service.argument.type",
        "Pass a service whose class is the type the parameter expects; a quick fix offers \
         compatible services.",
        "Parameters without an object type are not checked.",
        "",
    ),
    help(
        "symfony.service.named_argument.unknown",
        "Use the name of a constructor parameter ($name); a quick fix offers them.",
        "Services built by a factory are not checked.",
        "",
    ),
    help(
        "symfony.service.method.missing",
        "Add the method to the service class or correct its name; quick fixes create the \
         method or offer close names.",
        "",
        "",
    ),
    help(
        "symfony.service.tag_type",
        "Make the class implement the interface, or extend the base class, the tag \
         requires; or remove the tag.",
        "Tags without a known contract are not checked.",
        "",
    ),
    help(
        "symfony.services_xml.deprecated",
        "Convert the XML service configuration to YAML. A quick fix converts \
         Resources/config/services.xml and the XML files it imports; for an XML file \
         imported from Resources/config/services.php a quick fix converts it and points the \
         import() call at the YAML file. A file with a construct YAML cannot express \
         (on-invalid=\"null\" in a method call, a property or a collection) has to be \
         changed first.",
        "Reported for Resources/config/services.xml and for import('file.xml') calls in \
         Resources/config/services.php. Imports with a glob, a bundle path or a path built \
         at runtime, and XML loaded from elsewhere, are not reported.",
        "",
    ),
    help(
        "symfony.routes_xml.deprecated",
        "Convert the XML route configuration to YAML. A quick fix converts \
         Resources/config/routes.xml and the route files it imports; for an XML file \
         imported from Resources/config/routes.php a quick fix converts it and points the \
         import() call at the YAML file.",
        "Reported for Resources/config/routes.xml and for import('file.xml') calls in \
         Resources/config/routes.php. Imports with a glob, a bundle path or a path built at \
         runtime, and XML loaded from elsewhere, are not reported. A file with an element \
         or attribute the conversion does not know is reported without a quick fix.",
        "",
    ),
    help(
        "symfony.translation.key.missing",
        "Add the key to a translation file of the domain; quick fixes add it to an existing \
         catalogue or offer close keys.",
        "Keys built at runtime are not checked. In the messages domain a Shopware \
         storefront snippet with the key counts.",
        "",
    ),
    help(
        "symfony.translation.domain.missing",
        "Use an existing translation domain; a quick fix offers close names.",
        "",
        "",
    ),
    help(
        "symfony.validation.option.missing",
        "Use an option the constraint declares; a quick fix offers them.",
        "Only the array form new Constraint([...]) is checked.",
        "",
    ),
];
