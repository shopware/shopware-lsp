<?php

namespace Shopware\Core\Api;

use Symfony\Component\Routing\Annotation\Route;
use Symfony\Component\HttpFoundation\Response;

#[Route(path: "/api")]
class ApiController
{
    #[Route(name: "foo", path: "/foo")]
    public function foo(): Response
    {
        // Method implementation
    }
}