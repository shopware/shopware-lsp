<?php declare(strict_types=1);

namespace App\Controller;

use App\Seo\RouteNames;

class ProductController
{
    #[Route(
        path: '/detail/{productId}',
        name: RouteNames::PREFIX . RouteNames::DETAIL,
        methods: ['GET']
    )]
    public function index(): void
    {
    }
}
