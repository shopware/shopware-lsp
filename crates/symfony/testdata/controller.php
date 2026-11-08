<?php

namespace App\Controller\Frontend\Account;

use Symfony\Component\Routing\Annotation\Route;
use Symfony\Component\HttpFoundation\Response;

#[Route(
    name: "frontend.account.address.page",
    path: "/account/address"
)]
class AddressController
{
    #[Route(name: "frontend.account.address.create", path: "/create")]
    public function createAddress(): Response
    {
        // Method implementation
    }
}