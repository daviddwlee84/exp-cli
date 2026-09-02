#!/usr/bin/env python3
"""Generate language-specific llms.txt files from the MkDocs navigation."""

from __future__ import annotations

import argparse
import os
import re
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Iterable

from mkdocs.config import load_config


ZH_TW = "zh-TW"
PENDING_MARKERS = ("Translation pending", "這個頁面尚未翻譯")
H1_RE = re.compile(r"^#\s+(.+?)\s*$")


class GenerationError(RuntimeError):
    """Raised when the bilingual source set is incomplete or invalid."""


@dataclass(frozen=True)
class NavPage:
    label: str
    source: str


@dataclass(frozen=True)
class PageContent:
    nav: NavPage
    title: str
    summary: str
    markdown: str
    url: str


def flatten_nav(items: object) -> list[NavPage]:
    pages: list[NavPage] = []

    def visit(value: object, inherited_label: str | None = None) -> None:
        if isinstance(value, str):
            if value.endswith((".md", ".markdown")):
                label = inherited_label or PurePosixPath(value).stem.replace("-", " ").title()
                pages.append(NavPage(label=label, source=value))
            return
        if isinstance(value, list):
            for child in value:
                visit(child)
            return
        if isinstance(value, dict):
            for label, child in value.items():
                if isinstance(child, str):
                    visit(child, str(label))
                else:
                    visit(child)
            return
        raise GenerationError(f"unsupported nav value: {value!r}")

    visit(items)
    if not pages:
        raise GenerationError("mkdocs nav does not contain any Markdown pages")
    sources = [page.source for page in pages]
    if len(sources) != len(set(sources)):
        raise GenerationError("mkdocs nav contains duplicate Markdown page paths")
    return pages


def translated_source(source: str, locale: str) -> str:
    if locale == "en":
        return source
    path = PurePosixPath(source)
    return str(path.with_name(f"{path.stem}.{locale}{path.suffix}"))


def page_url(site_url: str, source: str, locale: str, use_directory_urls: bool) -> str:
    path = PurePosixPath(source)
    if path.name in {"index.md", "index.markdown"}:
        relative = "" if str(path.parent) == "." else f"{path.parent.as_posix().rstrip('/')}/"
    elif use_directory_urls:
        relative = f"{path.with_suffix('').as_posix().strip('/')}/"
    else:
        relative = f"{path.with_suffix('').as_posix().strip('/')}.html"
    if locale != "en":
        relative = f"{locale}/{relative}"
    return f"{site_url.rstrip('/')}/{relative}"


def extract_title(markdown: str, fallback: str) -> str:
    for line in markdown.splitlines():
        match = H1_RE.match(line)
        if match:
            return match.group(1).strip().strip("`#")
    if fallback:
        return fallback
    raise GenerationError("page has no H1 title")


def extract_summary(markdown: str) -> str:
    paragraph: list[str] = []
    in_fence = False
    skip_admonition = False
    in_comment = False

    for raw_line in markdown.splitlines():
        stripped = raw_line.strip()
        if stripped.startswith("<!--"):
            in_comment = True
        if in_comment:
            if "-->" in stripped:
                in_comment = False
            continue
        if stripped.startswith("```"):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        if stripped.startswith("!!!") or stripped.startswith("???"):
            skip_admonition = True
            continue
        if skip_admonition:
            if not stripped or raw_line.startswith(("    ", "\t")):
                continue
            skip_admonition = False
        if not stripped:
            if paragraph:
                break
            continue
        if stripped.startswith(("#", "- ", "* ", ">", "|")):
            continue
        if re.match(r"^\d+\.\s", stripped):
            continue
        paragraph.append(stripped)

    summary = " ".join(paragraph)
    summary = re.sub(r"\[([^]]+)]\([^)]+\)", r"\1", summary)
    summary = re.sub(r"[`*_]", "", summary)
    return re.sub(r"\s+", " ", summary).strip()


def read_pages(
    docs_dir: Path,
    nav_pages: Iterable[NavPage],
    locale: str,
    site_url: str,
    use_directory_urls: bool,
) -> list[PageContent]:
    result: list[PageContent] = []
    for nav_page in nav_pages:
        localized = translated_source(nav_page.source, locale)
        path = docs_dir / localized
        if not path.is_file():
            raise GenerationError(f"missing {locale} source for {nav_page.source}: {path}")
        markdown = path.read_text(encoding="utf-8")
        if not markdown.strip():
            raise GenerationError(f"empty documentation page: {path}")
        for marker in PENDING_MARKERS:
            if marker in markdown:
                raise GenerationError(f"unfinished translation marker in {path}: {marker}")
        title = extract_title(markdown, nav_page.label)
        summary = extract_summary(markdown)
        if not summary:
            raise GenerationError(f"page has no usable summary paragraph: {path}")
        result.append(
            PageContent(
                nav=nav_page,
                title=title,
                summary=summary,
                markdown=markdown.rstrip() + "\n",
                url=page_url(site_url, nav_page.source, locale, use_directory_urls),
            )
        )
    return result


def render_index(site_name: str, description: str, locale: str, pages: list[PageContent]) -> str:
    title = site_name if locale == "en" else f"{site_name} — 繁體中文"
    lines = [f"# {title}", "", f"> {description}", "", "## Pages", ""]
    for page in pages:
        lines.append(f"- [{page.title}]({page.url}): {page.summary}")
    return "\n".join(lines).rstrip() + "\n"


def render_full(site_name: str, description: str, locale: str, pages: list[PageContent]) -> str:
    title = site_name if locale == "en" else f"{site_name} — 繁體中文"
    blocks = [f"# {title}\n\n> {description}\n"]
    for page in pages:
        blocks.append(
            f"\n---\n\n<!-- Source: {page.url} -->\n\n"
            f"{page.markdown}"
        )
    return "".join(blocks).rstrip() + "\n"


def atomic_write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(content)
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def generate(config_path: Path, site_dir: Path) -> int:
    config_path = config_path.resolve()
    config = load_config(config_file=str(config_path))
    nav_pages = flatten_nav(config.nav)
    docs_dir = Path(config.docs_dir)
    site_url = str(config.site_url or "").strip()
    if not site_url:
        raise GenerationError("mkdocs site_url is required for llms.txt generation")
    site_name = str(config.site_name)
    description = str(config.site_description or f"Documentation for {site_name}")

    for locale in ("en", ZH_TW):
        pages = read_pages(
            docs_dir=docs_dir,
            nav_pages=nav_pages,
            locale=locale,
            site_url=site_url,
            use_directory_urls=bool(config.use_directory_urls),
        )
        output_dir = site_dir if locale == "en" else site_dir / locale
        atomic_write(output_dir / "llms.txt", render_index(site_name, description, locale, pages))
        atomic_write(output_dir / "llms-full.txt", render_full(site_name, description, locale, pages))
    return len(nav_pages)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=Path("mkdocs.yml"))
    parser.add_argument("--site-dir", type=Path, default=Path("site"))
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        count = generate(args.config, args.site_dir.resolve())
    except (GenerationError, OSError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    print(f"Generated bilingual llms.txt outputs for {count} pages.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
