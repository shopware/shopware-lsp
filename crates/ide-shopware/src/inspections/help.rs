//! What to do about the Shopware PHP diagnostics: how to resolve a finding,
//! what each rule deliberately leaves alone, and how to mark a finding as
//! intended. Shown by the configuration catalog and the MCP tool
//! `shopware_explain_diagnostic`.
//!
//! Keep an entry in step with its rule: the catalog test fails for a code
//! no inspection declares.

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

pub(crate) const STORE_REVIEW: &[RuleHelp] = &[
    help(
        "shopware.php.superglobal",
        "Read the value from the Symfony Request the controller or subscriber receives \
         ($request->query, $request->request, $request->files).",
        "$_SERVER, $_ENV, $_COOKIE and $_SESSION are not reported.",
        "",
    ),
    help(
        "shopware.php.function.disallowed",
        "Remove the call. For diagnostics use a PSR logger; to stop a request throw an \
         exception or return a response.",
        "Methods and namespaced functions with the same name are not reported.",
        "",
    ),
    help(
        "shopware.php.function.debug_output",
        "Remove the call, or log the value. To format a value into a string pass true as the \
         second argument of print_r() or var_export().",
        "print_r($value, true) and var_export($value, true) return a string and are not reported.",
        "",
    ),
    help(
        "shopware.php.session.function",
        "Use the session of the request: $request->getSession().",
        "",
        "",
    ),
    help(
        "shopware.php.glob_brace",
        "Call glob() once per alternative and merge the results; GLOB_BRACE does not exist on \
         every platform (Alpine/musl).",
        "",
        "",
    ),
    help(
        "shopware.php.tls.verification_disabled",
        "Keep certificate and host verification on. For a private CA configure its \
         certificate (CURLOPT_CAINFO, the `cafile` stream option) instead of disabling the check.",
        "Only literal option values are judged; a value from a variable is not reported.",
        "",
    ),
    help(
        "shopware.php.crypto.predictable_salt",
        "Do not pass a salt: password_hash() generates one. Replace crypt() with password_hash().",
        "A salt that is not a string literal is not reported.",
        "",
    ),
    help(
        "shopware.php.crypto.weak_key",
        "Use at least 2048 bits for RSA keys.",
        "Only a literal private_key_bits value is judged.",
        "",
    ),
    help(
        "shopware.php.cookie.insecure",
        "Pass secure: true (the sixth argument, or the `secure` key of the options array).",
        "A call whose options come from a variable is not reported.",
        "",
    ),
    help(
        "shopware.php.cookie.symfony_insecure",
        "Pass secure: true to new Cookie() or Cookie::create(), or chain ->withSecure().",
        "A creation followed by ->withSecure() or ->withSecure(true) in the same chain is \
         not reported.",
        "",
    ),
    help(
        "shopware.php.foreign_key_checks.disabled",
        "Leave FOREIGN_KEY_CHECKS alone in a migration or plugin update: delete or migrate \
         rows in dependency order, children before parents.",
        "Only strings inside update() of a MigrationStep or Plugin subclass are reported; \
         updateDestructive() is not.",
        "",
    ),
    help(
        "shopware.php.repository.in_loop",
        "Collect the IDs first and run one search or one write with all of them outside the \
         loop; for writes pass all payloads to a single upsert().",
        "The expression a foreach iterates over and the initializer of a for loop run once \
         and are not reported.",
        "",
    ),
    help(
        "shopware.php.repository.unbounded_search",
        "Restrict the Criteria: pass IDs, add a filter or setLimit(), or iterate with \
         RepositoryIterator. When only IDs or a count are needed use searchIds() or an aggregation.",
        "searchIds(), a Criteria created with IDs, one that had any method called on it or \
         was passed to another function, and one received as a parameter are not reported. \
         Loading a small fixed table in full (languages, currencies) is a legitimate reason to \
         suppress the finding.",
        "",
    ),
    help(
        "shopware.php.internal.class_extension",
        "Do not extend the class. Decorate the service through its abstract class or \
         interface, or subscribe to the events it dispatches.",
        "",
        "",
    ),
    help(
        "shopware.php.internal.method_call",
        "Call the public API that wraps the method; internal methods change without notice \
         between minor versions.",
        "Calls from the package that declares the method, and from the Shopware namespace \
         itself, are not reported.",
        "",
    ),
    help(
        "shopware.php.internal.function_call",
        "Use the public API instead; internal functions change without notice.",
        "Calls from the package that declares the function are not reported.",
        "",
    ),
    help(
        "shopware.php.decorator.concrete_extension",
        "Extend the abstract class named in the message and implement getDecorated(), so \
         other decorators of the same service keep working.",
        "",
        "",
    ),
    help(
        "shopware.php.session.constructor",
        "Read the session in the method that needs it; a constructor runs when the service \
         is built, possibly before a request exists, and starts the session for every request.",
        "",
        "",
    ),
    help(
        "shopware.php.session.payment_handler",
        "Carry the state in the order transaction (custom fields) or in the request data \
         bag; payment handlers also run without a storefront session (Store API, headless).",
        "",
        "",
    ),
    help(
        "shopware.php.session.store_api",
        "Store API routes are stateless: take the value from the request or the \
         SalesChannelContext, and keep state in the cart or the customer.",
        "",
        "",
    ),
    help(
        "shopware.php.scheduled_task.interval",
        "Return at least 300 seconds from getDefaultInterval(). For work that must run more \
         often dispatch a message when the triggering event happens.",
        "Only a literal return value is judged.",
        "",
    ),
    help(
        "shopware.php.user.store_token",
        "Do not read the store token of an administration user; it is the merchant's \
         credential for the Shopware Store.",
        "",
        "",
    ),
    help(
        "shopware.php.context.default",
        "Use the Context the caller already has: controllers and subscribers receive one, \
         and services should take it as a parameter. In console commands and scheduled tasks \
         use Context::createCLIContext().",
        "Not reported when the installed Shopware has no Context::createCLIContext() yet. \
         Tests may create a default context; suppress it there or switch the rule off for \
         the test directory.",
        "",
    ),
    help(
        "shopware.php.system_config.sales_channel",
        "Pass $context->getSalesChannelId() as the second argument, so a value overridden \
         for the sales channel is used.",
        "Reported only inside a function that has a SalesChannelContext parameter. A string \
         variable as the second argument is accepted.",
        "",
    ),
    help(
        "shopware.php.system_config.constructor",
        "Keep the SystemConfigService in a property and read the value in the method that \
         uses it, with the sales channel ID of the current context.",
        "",
        "",
    ),
    help(
        "shopware.php.listener.route_condition",
        "Subscribe to the event Shopware dispatches for that route, named in the message \
         (for example frontend.home.page.request), and drop the route check.",
        "A listener that compares the route with === (acts on one route but does not return \
         for the others) is not reported.",
        "",
    ),
    help(
        "shopware.php.response.empty",
        "Return content, or state that there is none: new Response(null, \
         Response::HTTP_NO_CONTENT), or a JsonResponse with data.",
        "Status codes 204, 301, 302, 303, 304, 307 and 308 need no body. Arguments passed \
         through a spread are not judged.",
        "",
    ),
    help(
        "shopware.php.filesystem.local_write",
        "Write through a Flysystem filesystem service (shopware.filesystem.private or \
         .public), which also works with remote storage and several app servers. For a \
         temporary file build the path from sys_get_temp_dir().",
        "A path that mentions sys_get_temp_dir(), tempnam(), or a name containing temp or \
         tmp, and the standard streams, are not reported. Console commands that generate \
         files into the project (scaffolding) are a legitimate reason to suppress the finding.",
        "",
    ),
    help(
        "shopware.php.filesystem.getcwd",
        "Inject the %kernel.project_dir% parameter and build the path from it.",
        "",
        "",
    ),
    help(
        "shopware.php.state.translated_name",
        "Compare getTechnicalName() with the technical name of the state (for example \
         OrderTransactionStates::STATE_PAID).",
        "Printing the name and comparing two names with each other are not reported.",
        "",
    ),
    help(
        "shopware.php.message.entity_payload",
        "Put the ID in the message and load the entity in the handler. For a sales channel \
         context carry the sales channel ID and the context token, or the values the handler \
         needs.",
        "Only properties and constructor parameters of AsyncMessageInterface messages are \
         checked. A Context is not reported.",
        "",
    ),
    help(
        "shopware.php.route.get_mutation",
        "Move the write into a route that accepts POST, PATCH or DELETE. For a link sent by \
         mail, let the GET route show a confirmation page whose form posts the change.",
        "create() is not reported (logging a visit), nor are routes that also accept another \
         method.",
        "",
    ),
    help(
        "shopware.php.route.acl_missing",
        "Add the privileges the action needs to the route: defaults: ['_acl' => \
         ['my_entity:update']]. Use privileges that exist; the Administration role must \
         grant them.",
        "Routes with an _acl default on the method or the class, routes without a methods \
         list, GET-only routes, and routes outside the api route scope are not reported. A \
         missing _acl is not proof of a missing check: writes through a repository with the \
         caller's context are checked by the DAL.",
        "Declare that the route needs no privilege of its own with an empty list: \
         defaults: ['_acl' => []]. Shopware treats it like no _acl.",
    ),
    help(
        "shopware.php.catch.empty",
        "Handle the failure, log it, or rethrow. When ignoring it is right, catch the \
         specific exception.",
        "A catch of a specific exception class is not reported.",
        "Put a comment in the block that says why the failure can be ignored.",
    ),
    help(
        "shopware.criteria.id-filter",
        "Pass the IDs to the constructor: new Criteria($ids). A quick fix does this when \
         the Criteria is created in the same function.",
        "",
        "",
    ),
];

pub(crate) const DAL_DEFINITION: &[RuleHelp] = &[
    help(
        "shopware.dal.entity_property.missing",
        "Add a property to the entity class with the name of the field (the second argument \
         of most fields, the first of association fields), plus a getter and a setter.",
        "Definitions without their own getEntityClass() hydrate ArrayEntity and are not \
         checked. Fields without a name argument (CustomFields, ReferenceVersionField, \
         TranslationsAssociationField) are not checked.",
        "",
    ),
    help(
        "shopware.dal.entity_property.readonly",
        "Remove readonly from the property; the DAL assigns it after constructing the entity.",
        "",
        "",
    ),
    help(
        "shopware.dal.entity_property.private",
        "Make the property protected; the entity hydrator cannot write private properties.",
        "",
        "",
    ),
    help(
        "shopware.dal.entity_property.getter_missing",
        "Add a getter named get, is or has followed by the property name.",
        "",
        "",
    ),
    help(
        "shopware.dal.entity_property.setter_missing",
        "Add a setter named set followed by the property name.",
        "Fields flagged Runtime or Computed need no setter.",
        "",
    ),
];

pub(crate) const ANNOUNCED: &[RuleHelp] = &[
    help(
        "shopware.migration.announced.internal",
        "Stop using the class or method before the version in the message: it leaves the \
         public API then. Look for the replacement in its deprecation note or the upgrade guide.",
        "Code in the package that declares it, and code marked @deprecated tag:<version> for \
         the same version, is not reported.",
        "Mark the class or function that goes away with the change: @deprecated tag:<version>.",
    ),
    help(
        "shopware.migration.announced.final",
        "Stop extending the class; decorate the service or use its extension points. There \
         is no declaration that works before and after the change.",
        "",
        "Mark the subclass @deprecated tag:<version> when it is removed with the change.",
    ),
    help(
        "shopware.migration.announced.abstract",
        "Implement the method in the subclass now; an implementation is valid before and \
         after the change.",
        "A subclass that inherits an implementation from a class in between is not reported.",
        "",
    ),
    help(
        "shopware.migration.announced.visibility",
        "Stop calling the method or reading the property from outside the scope it will be \
         visible in.",
        "Access from a scope that stays allowed (the class itself, or a subclass for \
         protected) is not reported.",
        "",
    ),
    help(
        "shopware.migration.announced.parameter.removed",
        "Stop passing the argument; the method ignores it from the version in the message.",
        "",
        "",
    ),
    help(
        "shopware.migration.announced.parameter.renamed",
        "Pass the argument by position instead of by name: no name is valid before and \
         after the change.",
        "",
        "",
    ),
    help(
        "shopware.migration.announced.parameter.required",
        "Pass the new argument by position now, after the existing ones; Shopware already \
         accepts it.",
        "Calls with a spread argument are not judged.",
        "",
    ),
    help(
        "shopware.migration.announced.parameter.type",
        "Pass a value of the announced type.",
        "Only values that can never fit are reported: an int|string value for a parameter \
         narrowed to int is not.",
        "",
    ),
    help(
        "shopware.migration.announced.property.readonly",
        "Stop assigning the property from outside its class; pass the value to the \
         constructor or use the method that sets it.",
        "",
        "",
    ),
    help(
        "shopware.migration.announced.property.type",
        "Assign a value of the announced type.",
        "Only values that can never fit are reported.",
        "",
    ),
    help(
        "shopware.migration.announced.property.redeclared",
        "Remove the redeclaration and use the inherited property; no declaration is valid \
         before and after the change.",
        "",
        "",
    ),
    help(
        "shopware.migration.announced.type.widened",
        "Handle the announced type where the value is used: check for null before \
         dereferencing or passing it on, or use ?->.",
        "A use that already accepts the wider type is not reported, nor one after a check \
         such as !== null.",
        "",
    ),
];

/// What every migration finding shares.
const MIGRATION_SCOPE: &str = "Reported only when the Shopware target version is known \
                               (shopware.targetVersion, or shopware/core in composer.lock) and \
                               has reached the version in the message.";

pub(crate) const MIGRATION: &[RuleHelp] = &[
    help(
        "shopware.migration.api.class.rename",
        "Use the new class name; the quick fix \"Rename class\" rewrites the use statement \
         and the references.",
        "Short-name uses of an imported class are not reported separately, only the import. \
         A grouped use statement is reported without a quick fix.",
        "",
    ),
    help(
        "shopware.migration.api.method.rename",
        "Call the new method name; a quick fix renames the call.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.api.static_method.rename",
        "Call the replacement the message names; a quick fix rewrites the call.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.api.constant.rename",
        "Read the constant from the class the message names; a quick fix moves the reference.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.api.property",
        "Call the getter the message names in place of reading the property; a quick fix \
         does it.",
        "An assignment to the property is reported without a quick fix.",
        "",
    ),
    help(
        "shopware.migration.api.exception_factory",
        "Create the exception through the static factory the message names; a quick fix \
         replaces the `new` expression.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.annotation.route_defaults",
        "Move the annotation into the defaults of @Route (_captcha, _loginRequired, \
         _routeScope); a quick fix does it.",
        "A @RouteScope whose scopes are not a literal list is reported without a quick fix.",
        "",
    ),
    help(
        "shopware.migration.cart_behavior.permission",
        "Check $behavior->hasPermission(CheckoutPermissions::SKIP_CART_PERSISTENCE) \
         instead; a quick fix rewrites the call and adds the import.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.context_metadata.state",
        "Set the indexing flag with $context->addState(); a quick fix rewrites the call.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.entity_extension.entity_name",
        "Implement getEntityName() returning the entity name (Definition::ENTITY_NAME) and, \
         from 6.7, remove getDefinitionClass(); a quick fix does it when the definition class \
         can be read from getDefinitionClass().",
        "",
        "",
    ),
    help(
        "shopware.migration.entity_search_result.get_entities",
        "Call the collection method on $result->getEntities(), and iterate or count \
         getEntities(); a quick fix inserts the call.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.faker.property_call",
        "Call the Faker formatter as a method ($faker->name()); a quick fix adds the parentheses.",
        "",
        "",
    ),
    help(
        "shopware.migration.message_handler.subscriber",
        "Replace AbstractMessageHandler with a handler class marked #[AsMessageHandler] \
         whose __invoke() takes the message; a quick fix converts the class.",
        "A class that has both handle() and __invoke() is reported without a quick fix.",
        "",
    ),
    help(
        "shopware.migration.price_definition.filterable",
        "Implement FilterableInterface on the price definition; a quick fix adds it.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.product_stream.enrich_criteria",
        "Call $builder->enrichCriteria($criteria, ...) in place of adding the result of \
         buildFilters(); a quick fix rewrites the common patterns and marks the others with a \
         TODO comment.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.reflection_helper.native",
        "Use PHP's ReflectionMethod, ReflectionProperty or ReflectionClass directly; a \
         quick fix rewrites the call.",
        "Calls with named or spread arguments are reported without a quick fix.",
        "",
    ),
    help(
        "shopware.migration.reverse_proxy.ban_all",
        "Implement banAll() on the gateway; a quick fix adds an implementation that bans \
         everything.",
        "",
        "",
    ),
    help(
        "shopware.migration.scheduled_task.exception_logger",
        "Take a Psr\\Log\\LoggerInterface in the constructor and pass it as the second \
         argument of parent::__construct(); a quick fix does both.",
        "Handlers without a constructor of their own are not reported.",
        "",
    ),
    help(
        "shopware.migration.arguments.remove",
        "Remove the argument or parameter the message names; a quick fix does it.",
        MIGRATION_SCOPE,
        "",
    ),
    help(
        "shopware.migration.arguments.add_default",
        "Pass the argument explicitly, with the value the message names, so the behavior \
         stays the same when the default changes; a quick fix adds it.",
        "Not reported when the argument is already passed, by position or by name.",
        "",
    ),
    help(
        "shopware.migration.declaration.interface_to_abstract",
        "Extend the abstract class the message names in place of implementing the \
         interface, and use it in type declarations; a quick fix does it.",
        "A class that already extends another class is reported without a quick fix.",
        "",
    ),
    help(
        "shopware.migration.declaration.parameter.add",
        "Add the parameter to the overriding method, as the message spells it; a quick fix \
         inserts it.",
        "Constructors and methods with a variadic parameter are not reported.",
        "",
    ),
    help(
        "shopware.migration.declaration.type",
        "Declare the type the message names on the overriding method; a quick fix sets it \
         and imports its classes.",
        "An override that declares something other than today's inherited type is left alone.",
        "",
    ),
    help(
        "shopware.migration.thumbnail.generate",
        "Call generate() with a MediaCollection; a quick fix wraps the single media entity.",
        MIGRATION_SCOPE,
        "",
    ),
];

pub(crate) const PROJECT: &[RuleHelp] = &[
    help(
        "app_script.service-unavailable",
        "Use a service the hook provides, or move the script to a hook that provides it; \
         the hook is the directory of the script below Resources/scripts.",
        "Scripts of a hook the index does not know are not checked.",
        "",
    ),
    help(
        "app_script.permission-missing",
        "Add a <read> permission for the entity to the app's manifest.xml; a quick fix adds it.",
        "",
        "",
    ),
    help(
        "shopware.dal.entity-not-found",
        "Correct the entity name; a quick fix offers the close name.",
        "Reported only for a name close to an indexed entity: an unknown name without a \
         close match may be a custom entity created at runtime.",
        "Guard an optional entity with EntityDefinition.has('name'); has() is not checked.",
    ),
    help(
        "shopware.decoration.abstraction",
        "Type-hint the abstract class the message names: another extension may decorate \
         the service, and its decorator is not an instance of the concrete class.",
        "",
        "",
    ),
    help(
        "shopware.entity_snapshot.invalid",
        "Do not edit snapshot files by hand: restore the file from version control, or \
         recreate it with the entity schema tools (shopware_entity_schema_preview and \
         shopware_entity_schema_apply).",
        "",
        "",
    ),
    help(
        "shopware.entity_snapshot.graph",
        "Restore the snapshot files from version control; their parent links no longer form \
         one history.",
        "",
        "",
    ),
    help(
        "shopware.entity_snapshot.parent_missing",
        "Restore the missing parent snapshots from version control; a snapshot may only be \
         removed together with everything that follows it.",
        "",
        "",
    ),
    help(
        "shopware.entity_snapshot.reconcile",
        "Two branches each added a snapshot. Reconcile them with the entity schema tool \
         shopware_entity_schema_reconcile, which writes one snapshot following both.",
        "",
        "",
    ),
    help(
        "shopware.entity_snapshot.migration_missing",
        "Restore the migration the snapshot references; a released migration must not be \
         deleted.",
        "",
        "",
    ),
    help(
        "shopware.entity_snapshot.migration_changed",
        "Revert the change to the migration and put it into a new migration; a migration \
         that has run on an installation is not run again.",
        "",
        "",
    ),
    help(
        "shopware.entity_snapshot.schema_drift",
        "The entity definitions were changed without a snapshot and migration. Apply the \
         change through the entity schema tools (shopware_entity_schema_load, _preview, \
         _apply), which write both.",
        "",
        "",
    ),
    help(
        "shopware.store.label",
        "Add extra.label to composer.json with a de-DE and an en-GB text.",
        "Checked only for an extension that sets validation.store_compliance: true in \
         .shopware-extension.yml.",
        "",
    ),
    help(
        "shopware.store.description",
        "Add extra.description with a de-DE and an en-GB text of 150 to 185 characters each.",
        "Checked only for an extension that sets validation.store_compliance: true in \
         .shopware-extension.yml.",
        "",
    ),
    help(
        "shopware.store.manufacturer-link",
        "Add extra.manufacturerLink with a de-DE and an en-GB URL.",
        "Checked only for an extension that sets validation.store_compliance: true in \
         .shopware-extension.yml.",
        "",
    ),
    help(
        "shopware.store.support-link",
        "Add extra.supportLink with a de-DE and an en-GB URL.",
        "Checked only for an extension that sets validation.store_compliance: true in \
         .shopware-extension.yml.",
        "",
    ),
    help(
        "shopware.store.require-core",
        "Require shopware/core in composer.json with the version range the extension supports.",
        "Checked only for an extension that sets validation.store_compliance: true in \
         .shopware-extension.yml.",
        "",
    ),
    help(
        "theme.icon.missing",
        "Use the name of an icon the pack contains, or name the pack that has it: \
         {% sw_icon 'name' style {'pack': 'solid'} %}.",
        "A pack given by a variable is not checked.",
        "",
    ),
    help(
        "twig.block.deprecated",
        "Override the block the upstream deprecation note names instead; the deprecated \
         block is removed with the version in the note.",
        "",
        "",
    ),
    help(
        "twig.block.redundant_override",
        "The block repeats its parent unchanged: remove the override, or replace its body \
         with {{ parent() }}; a quick fix does the latter.",
        "",
        "",
    ),
    help(
        "twig.versioning.comment_missing",
        "Add a {# shopware-block: <hash>@<version> #} comment that records which upstream \
         block the override was written against; a quick fix adds it.",
        "Off by default. Blocks that only delegate to parent() need no comment.",
        "",
    ),
    help(
        "twig.versioning.outdated",
        "The upstream block changed since the override was written. Compare the upstream \
         block, carry the change over where needed, then update the comment; quick fixes show \
         the diff and update the comment.",
        "",
        "",
    ),
    help(
        "twig.versioning.original_missing",
        "The upstream block is gone or moved. Find where the content lives now and move the \
         override there, or delete it.",
        "Blocks without a versioning comment are not reported.",
        "",
    ),
];
