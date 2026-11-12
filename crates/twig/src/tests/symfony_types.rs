//! `app.request`, `app.user` and form view variables.

use std::path::Path;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_EXPRESSION, TWIG_VAR};
use swlsp_php::PhpIndex;

use super::indexer::{index_php, php_index};
use crate::macros::direct_child;
use crate::*;

const FRAMEWORK: &str = r"<?php
namespace Symfony\Component\HttpFoundation { class Request { public string $locale; } }
namespace Symfony\Component\Security\Core\User { interface UserInterface {} }
namespace Symfony\Component\Security\Http\Attribute { #[\Attribute] class IsGranted { public function __construct(public string $attribute) {} } }
namespace Symfony\Component\Form {
    class FormError {}
    class FormErrorIterator {}
    class FormView { public array $vars = []; public ?FormView $parent = null; }
}
namespace Symfony\Bridge\Twig {
    use Symfony\Component\HttpFoundation\Request;
    use Symfony\Component\Security\Core\User\UserInterface;
    class AppVariable {
        public function getRequest(): ?Request { return null; }
        public function getUser(): ?UserInterface { return null; }
    }
}
";

const CONTROLLERS: &str = r"<?php
namespace App;

use Symfony\Component\Form\FormView;
use Symfony\Component\Security\Http\Attribute\IsGranted;

class User implements \Symfony\Component\Security\Core\User\UserInterface { public string $email; }

class PublicController
{
    public function page(FormView $form)
    {
        return $this->render('public.html.twig', ['form' => $form]);
    }

    #[IsGranted('ROLE_USER')]
    public function mixed()
    {
        return $this->render('mixed.html.twig', ['title' => 'a']);
    }

    public function open()
    {
        return $this->render('mixed.html.twig', ['title' => 'b']);
    }

    #[IsGranted('POST_EDIT')]
    public function voter()
    {
        return $this->render('voter.html.twig', ['title' => 'c']);
    }
}

#[IsGranted('IS_AUTHENTICATED_FULLY')]
class AccountController
{
    public function account()
    {
        return $this->render('account.html.twig', ['title' => 'd']);
    }
}
";

fn project(root: &Path, templates: &[(&str, &str)]) -> (PhpIndex, TwigIndexer) {
    let php = php_index(&root.join("cache"));
    index_php(&php, "/project/vendor/symfony/framework.php", FRAMEWORK);
    index_php(&php, "/project/src/Controllers.php", CONTROLLERS);
    let twig = TwigIndexer::new(root.join("cache"), None).unwrap();
    twig.set_dependencies(Some(php.clone()), None);
    for (name, source) in templates {
        let path = root.join("templates").join(name);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(&path, source).unwrap();
        twig.index(&ParsedFile::new(
            path.to_str().unwrap(),
            source.as_bytes().to_vec(),
        ))
        .unwrap();
    }
    (php, twig)
}

fn printed(root: &Path, php: &PhpIndex, twig: &TwigIndexer, name: &str) -> Vec<String> {
    let path = root.join("templates").join(name);
    let source = std::fs::read_to_string(&path).unwrap();
    let tree = swlsp_parser_twig::parse(&source).root();
    let resolver = PhpAccessResolver::new(Some(php), Some(twig)).for_document(&tree);
    twigquery::nodes(&tree, &[TWIG_VAR])
        .iter()
        .map(|print| {
            let expression = direct_child(print, TWIG_EXPRESSION);
            resolver
                .expression_type(path.to_str().unwrap(), &tree, expression.as_ref())
                .to_string()
        })
        .collect()
}

#[test]
fn app_request_and_user_follow_the_render_calls() {
    let directory = tempfile::tempdir().unwrap();
    let app = "{{ app.request }}{{ app.user }}";
    let (php, twig) = project(
        directory.path(),
        &[
            ("public.html.twig", app),
            ("mixed.html.twig", app),
            ("voter.html.twig", app),
            (
                "account.html.twig",
                "{{ app.user.email }}{% include 'menu.html.twig' %}",
            ),
            ("menu.html.twig", app),
            ("mail.html.twig", app),
        ],
    );
    let printed = |name: &str| printed(directory.path(), &php, &twig, name);
    let request = r"Symfony\Component\HttpFoundation\Request";
    let user = r"Symfony\Component\Security\Core\User\UserInterface|null";
    // A controller renders within a request; a user is not required.
    assert_eq!(printed("public.html.twig"), [request, user]);
    // One of the two actions is public.
    assert_eq!(printed("mixed.html.twig"), [request, user]);
    // A voter may grant access without a user.
    assert_eq!(printed("voter.html.twig"), [request, user]);
    // The class requires a user, and the project has one user class.
    assert_eq!(printed("menu.html.twig"), [request, r"App\User"]);
    assert_eq!(printed("account.html.twig"), ["string"]);
    // Nothing renders the template: a console command may.
    assert_eq!(
        printed("mail.html.twig"),
        [&format!("{request}|null"), user]
    );
}

#[test]
fn form_view_vars_have_the_common_variables() {
    let directory = tempfile::tempdir().unwrap();
    let (php, twig) = project(
        directory.path(),
        &[(
            "public.html.twig",
            "{{ form.vars.id }}{{ form.vars.required }}{{ form.vars.block_prefixes }}{{ form.vars.value }}{{ form.parent.vars.name }}",
        )],
    );
    assert_eq!(
        printed(directory.path(), &php, &twig, "public.html.twig"),
        ["string", "bool", "list<string>", "unknown", "string"]
    );
}

#[test]
fn access_control_makes_app_user_not_null() {
    let directory = tempfile::tempdir().unwrap();
    let (php, twig) = project(
        directory.path(),
        &[
            ("secured.html.twig", "{{ app.user }}"),
            ("open.html.twig", "{{ app.user }}"),
        ],
    );
    let controller = r"<?php
namespace App;

use Symfony\Component\Routing\Attribute\Route;

class RoutedController
{
    #[Route('/account/orders/{id}', name: 'account_orders')]
    public function orders()
    {
        return $this->render('secured.html.twig', ['title' => 'a']);
    }

    #[Route('/shop', name: 'shop')]
    #[Route('/account/shop', name: 'account_shop')]
    public function shop()
    {
        return $this->render('open.html.twig', ['title' => 'b']);
    }
}
";
    let path = "/project/src/RoutedController.php";
    index_php(&php, path, controller);
    let routes = std::sync::Arc::new(
        swlsp_symfony::RouteIndexer::new(directory.path().join("cache"), None).unwrap(),
    );
    routes
        .index(&ParsedFile::new(path, controller.as_bytes().to_vec()))
        .unwrap();
    twig.index(&ParsedFile::new(
        "/project/config/packages/security.yaml",
        b"security:\n    access_control:\n        - { path: ^/account, roles: ROLE_USER }\n"
            .to_vec(),
    ))
    .unwrap();
    twig.set_route_index(Some(routes));
    assert_eq!(twig.access_control().len(), 1);
    let printed = |name: &str| printed(directory.path(), &php, &twig, name);
    assert_eq!(printed("secured.html.twig"), [r"App\User"]);
    // One of the two routes of the action is public.
    assert_eq!(
        printed("open.html.twig"),
        [r"Symfony\Component\Security\Core\User\UserInterface|null"]
    );
}
