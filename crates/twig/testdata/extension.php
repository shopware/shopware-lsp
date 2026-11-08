<?php declare(strict_types=1);

namespace App\Twig;

use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;

class TwigExt extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction('test', [$this, 'test']),
            new TwigFunction('test2', $this->test(...)),
        ];
    }

    public function getFilters(): array
    {
        return [
            new TwigFilter('abs', 'abs'),
            new TwigFilter('test', [$this, 'test']),
            new TwigFilter('test2', $this->test(...)),
        ];
    }

    public function test(string $test)
    {
        return 'test';
    }   
}
