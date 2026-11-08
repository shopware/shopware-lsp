#!/usr/bin/env python3
"""Generate a synthetic Symfony UX project for benchmarking the Twig
component and Live component features, which the Shopware corpus lacks.

    ux_fixture.py OUTPUT_DIR [--components 300]

Every component `CompN` is a live component with props, an action, a
listener for `eventN` and `emit` calls to other components' events; its
template and page templates use `<twig:...>`, `component()` and
`data-live-*` attributes that reference other components, actions and events.
The layout is deterministic so Go and Rust see the same project.
"""

from __future__ import annotations

import argparse
import json
import shutil
from pathlib import Path


def component_php(index: int, count: int) -> str:
    other = (index * 7 + 3) % count
    other2 = (index * 13 + 5) % count
    return f"""<?php

declare(strict_types=1);

namespace App\\Twig\\Components;

use Symfony\\UX\\LiveComponent\\Attribute\\AsLiveComponent;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveAction;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveArg;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveListener;
use Symfony\\UX\\LiveComponent\\Attribute\\LiveProp;
use Symfony\\UX\\LiveComponent\\ComponentToolsTrait;
use Symfony\\UX\\LiveComponent\\DefaultActionTrait;

#[AsLiveComponent]
final class Comp{index}
{{
    use ComponentToolsTrait;
    use DefaultActionTrait;

    #[LiveProp(writable: true)]
    public string $query = '';

    #[LiveProp]
    public int $page = 1;

    public string $title = 'Component {index}';

    #[LiveAction]
    public function save(#[LiveArg] int $itemId, #[LiveArg('mode')] string $mode = 'full'): void
    {{
        $this->emit('event{other}', ['itemId' => $itemId]);
        $this->emitUp('event{other2}', ['mode' => $mode]);
    }}

    #[LiveAction]
    public function reset(): void
    {{
        $this->page = 1;
        $this->emitSelf('event{index}');
    }}

    #[LiveListener('event{index}')]
    public function refresh(#[LiveArg('itemId')] int $itemId = 0, #[LiveArg] string $mode = ''): void
    {{
        $this->page = $itemId;
    }}
}}
"""


def component_twig(index: int, count: int) -> str:
    child = (index + 1) % count
    other = (index * 7 + 3) % count
    return f"""<div{{{{ attributes }}}}>
    <h2>{{{{ title }}}} {{{{ query }}}} {{{{ page }}}}</h2>
    <input data-model="query" value="{{{{ query }}}}">
    <button data-action="live#action" data-live-action-param="save" data-live-item-id-param="{{{{ page }}}}">Save</button>
    <button data-action="live#action" data-live-action-param="reset">Reset</button>
    <button data-action="live#emit" data-live-event-param="event{other}" data-live-item-id-param="1">Notify</button>
    <twig:Comp{child} query="{{{{ query }}}}" :page="page" />
    {{{{ component('Comp{other}', {{ query: query, page: 2 }}) }}}}
</div>
"""


def page_twig(index: int, count: int) -> str:
    lines = ["{% extends 'base.html.twig' %}", "", "{% block body %}"]
    for offset in range(8):
        target = (index * 11 + offset * 17) % count
        lines.append(f'    <twig:Comp{target} query="page {index}" :page="{offset}" />')
        lines.append(f"    {{{{ component('Comp{(target + 1) % count}', {{ query: 'x' }}) }}}}")
        lines.append(
            f'    <button data-action="live#emit" data-live-event-param="event{(target + 2) % count}">Go</button>'
        )
    lines.append("{% endblock %}")
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("output")
    parser.add_argument("--components", type=int, default=300)
    parser.add_argument("--pages", type=int, default=100)
    args = parser.parse_args()
    root = Path(args.output)
    if root.exists():
        shutil.rmtree(root)
    (root / "src/Twig/Components").mkdir(parents=True)
    (root / "templates/components").mkdir(parents=True)
    (root / "templates/pages").mkdir(parents=True)
    (root / "config/packages").mkdir(parents=True)
    (root / "composer.json").write_text(json.dumps({
        "name": "bench/ux-fixture",
        "type": "project",
        "require": {
            "php": ">=8.2",
            "symfony/framework-bundle": "^7.2",
            "symfony/ux-twig-component": "^2.22",
            "symfony/ux-live-component": "^2.22",
        },
        "autoload": {"psr-4": {"App\\": "src/"}},
    }, indent=4) + "\n")
    (root / "config/packages/twig_component.yaml").write_text(
        "twig_component:\n"
        "    anonymous_template_directory: 'components/'\n"
        "    defaults:\n"
        "        App\\Twig\\Components\\: 'components/'\n"
    )
    (root / "templates/base.html.twig").write_text(
        "<!DOCTYPE html>\n<html><body>{% block body %}{% endblock %}</body></html>\n"
    )
    for index in range(args.components):
        (root / f"src/Twig/Components/Comp{index}.php").write_text(component_php(index, args.components))
        (root / f"templates/components/Comp{index}.html.twig").write_text(component_twig(index, args.components))
    for index in range(args.pages):
        (root / f"templates/pages/page{index}.html.twig").write_text(page_twig(index, args.components))
    print(f"wrote {args.components} components and {args.pages} pages to {root}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
