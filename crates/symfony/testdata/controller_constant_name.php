<?php declare(strict_types=1);

namespace App\Controller;

use App\Seo\ProductPageSeoUrlRoute;

class ProductController
{
    #[Route(
        path: '/detail/{productId}',
        name: ProductPageSeoUrlRoute::ROUTE_NAME,
        methods: ['GET']
    )]
    public function index(): void
    {
    }
}
