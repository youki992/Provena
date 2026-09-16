#!/usr/bin/env python3
"""WIHscan-1.0 adapter for JavaScript endpoint and secret discovery.

WIHscan-1.0 ships a Katana binary plus a separate ``rules.yaml``.  Katana
does the crawling; this adapter owns rule loading, matching, exclusions, and
redaction so the rules file is never passed as a Katana configuration.

The adapter emits JSON Lines and never prints a matched secret.  It is
intended to be called by the CyberStrike tool runner, not as a general-purpose
shell wrapper.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable, Iterator
from urllib.parse import urlparse

try:
    import yaml
except ImportError as exc:  # pragma: no cover - exercised only on bad installs
    raise SystemExit("PyYAML is required by wihscan_js.py; install requirements.txt") from exc


ROOT = Path(__file__).resolve().parent.parent
DEFAULT_DEPTH = 2
DEFAULT_RATE_LIMIT = 10
DEFAULT_DURATION = "60s"
MAX_BODY_CHARS = 2_000_000
MAX_MATCHES = 500
MAX_SNIPPET_CHARS = 240


@dataclass(frozen=True)
class Rule:
    rule_id: str
    pattern: re.Pattern[str]


def emit(value: dict[str, Any]) -> None:
    print(json.dumps(value, ensure_ascii=False, sort_keys=True), flush=True)


def validate_target(value: str) -> str:
    target = value.strip()
    parsed = urlparse(target)
    if parsed.scheme.lower() not in {"http", "https"} or not parsed.netloc:
        raise ValueError("target must be an absolute http:// or https:// URL")
    if parsed.username or parsed.password:
        raise ValueError("target must not contain embedded credentials")
    return target


def resolve_file(env_name: str, candidates: Iterable[Path], label: str) -> Path:
    configured = os.environ.get(env_name, "").strip()
    paths = [Path(configured)] if configured else []
    paths.extend(candidates)
    for path in paths:
        if path.is_file():
            return path.resolve()
    joined = ", ".join(str(path) for path in paths)
    raise FileNotFoundError(f"{label} not found; checked: {joined}")


def find_katana() -> Path:
    return resolve_file(
        "WIHSCAN_KATANA_PATH",
        (
            ROOT / "tools" / "bin" / "katana" / "windows-amd64" / "katana.exe",
            ROOT / "tools" / "bin" / "katana" / "linux-amd64" / "katana",
            ROOT / "wihscan" / "katana.exe",
            ROOT / "wihscan" / "katana",
            ROOT.parent.parent / "WIHscan-1.0" / "katana.exe",
            ROOT.parent.parent / "WIHscan-1.0" / "katana",
            ROOT / "WIHscan-1.0" / "katana.exe",
            ROOT / "WIHscan-1.0" / "katana",
        ),
        "WIHscan Katana binary",
    ) if not shutil.which("katana") else Path(shutil.which("katana") or "katana")


def find_rules() -> Path:
    return resolve_file(
        "WIHSCAN_RULES_PATH",
        (
            ROOT / "wihscan" / "config" / "rules.yaml",
            ROOT.parent.parent / "WIHscan-1.0" / "config" / "rules.yaml",
            ROOT / "WIHscan-1.0" / "config" / "rules.yaml",
        ),
        "WIHscan rules.yaml",
    )


def load_rules(path: Path) -> tuple[list[Rule], list[dict[str, Any]]]:
    document = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    rules: list[Rule] = []
    for item in document.get("rules", []) or []:
        if not item or not item.get("enabled", False):
            continue
        rule_id = str(item.get("id", "")).strip()
        pattern = str(item.get("pattern", ""))
        if not rule_id or not pattern:
            continue
        try:
            rules.append(Rule(rule_id, re.compile(pattern)))
        except re.error as exc:
            raise ValueError(f"invalid WIHscan rule {rule_id}: {exc}") from exc
    excludes = [item for item in (document.get("exclude_rules", []) or []) if item and item.get("enabled", False)]
    return rules, excludes


def _exclude_matches(exclude: dict[str, Any], url: str, content: str, rule_id: str) -> bool:
    excluded_id = str(exclude.get("id", "")).strip()
    if excluded_id and excluded_id != rule_id:
        return False

    target = str(exclude.get("target", "")).strip()
    if target.startswith("regex:"):
        try:
            if not re.search(target[6:], url, re.I):
                return False
        except re.error:
            return False
    elif target and target not in url:
        return False

    content_rule = str(exclude.get("content", "")).strip()
    if not content_rule:
        return True
    if content_rule.startswith("regex:"):
        try:
            return bool(re.search(content_rule[6:], content, re.I))
        except re.error:
            return False
    return content_rule in content


def is_excluded(excludes: list[dict[str, Any]], url: str, content: str, rule_id: str, source_tag: str) -> bool:
    for exclude in excludes:
        source = str(exclude.get("source_tag", "")).strip()
        if source and source != source_tag:
            continue
        if _exclude_matches(exclude, url, content, rule_id):
            return True
    return False


def redact_match(rule_id: str, matched: str) -> str:
    """Keep the context useful while ensuring the matched value is not output."""
    if rule_id in {"access_key_generic", "sensitive_info_generic"}:
        # Those rules capture a key and a value; preserve the key name only.
        quoted = re.sub(r"(['\"])([0-9A-Za-z_\-=:]{8,64})\1", r"\1***REDACTED***\1", matched, count=1)
        if quoted != matched:
            return quoted
    if rule_id == "password":
        return re.sub(r"(['\"])([^'\"]{1,80})\1", r"\1***REDACTED***\1", matched, count=1)
    return "***REDACTED***"


def is_noise_match(rule_id: str, matched: str) -> bool:
    """Remove common permission/config literals caught by broad generic rules."""
    if rule_id not in {"access_key_generic", "sensitive_info_generic"}:
        return False
    quoted = re.findall(r"['\"]([^'\"]*)['\"]", matched)
    if not quoted:
        return False
    value = quoted[-1].strip().lower()
    if value in {"true", "false", "null", "undefined", "localhost", "application/json"}:
        return True
    if re.fullmatch(r"[a-z0-9_.-]+:(?:read|write|delete|execute|admin|self)", value):
        return True
    return False


def make_snippet(content: str, start: int, end: int, rule_id: str) -> tuple[str, int]:
    line = content.count("\n", 0, start) + 1
    left = max(0, start - 90)
    right = min(len(content), end + 90)
    snippet = content[left:start] + redact_match(rule_id, content[start:end]) + content[end:right]
    snippet = re.sub(r"\s+", " ", snippet).strip()
    if len(snippet) > MAX_SNIPPET_CHARS:
        snippet = snippet[:MAX_SNIPPET_CHARS] + "…"
    return snippet, line


def redact_snippet_secrets(snippet: str, rules: list[Rule]) -> str:
    """Redact neighboring findings too; context must never disclose another value."""
    for rule in rules:
        snippet = rule.pattern.sub(lambda match: redact_match(rule.rule_id, match.group(0)), snippet)
    return snippet


def scan_content(
    content: str,
    url: str,
    source_tag: str,
    rules: list[Rule],
    excludes: list[dict[str, Any]],
) -> Iterator[dict[str, Any]]:
    content = content[:MAX_BODY_CHARS]
    seen: set[str] = set()
    for rule in rules:
        if is_excluded(excludes, url, content, rule.rule_id, source_tag):
            continue
        for match in rule.pattern.finditer(content):
            if is_noise_match(rule.rule_id, match.group(0)):
                continue
            snippet, line = make_snippet(content, match.start(), match.end(), rule.rule_id)
            snippet = redact_snippet_secrets(snippet, rules)
            fingerprint = hashlib.sha256(f"{rule.rule_id}\0{url}\0{line}\0{snippet}".encode()).hexdigest()
            if fingerprint in seen:
                continue
            seen.add(fingerprint)
            yield {
                "type": "sensitive_match",
                "rule_id": rule.rule_id,
                "url": url,
                "source": source_tag,
                "line": line,
                "snippet": snippet,
            }


def nested_values(value: Any, wanted: set[str]) -> Iterator[Any]:
    if isinstance(value, dict):
        for key, item in value.items():
            if str(key).lower().replace("_", "") in wanted:
                yield item
            yield from nested_values(item, wanted)
    elif isinstance(value, list):
        for item in value:
            yield from nested_values(item, wanted)


def endpoint_from_item(item: dict[str, Any]) -> tuple[str, str, str]:
    request = item.get("request") if isinstance(item.get("request"), dict) else {}
    url = str(request.get("endpoint") or item.get("url") or "").strip()
    tag = str(request.get("tag") or item.get("tag") or "").strip().lower()
    source = str(request.get("source") or item.get("source") or "").strip()
    return url, tag, source


def iter_xhr(item: dict[str, Any]) -> Iterator[tuple[str, str]]:
    for value in nested_values(item, {"xhrrequests", "xhrrequest", "xhr"}):
        values = value if isinstance(value, list) else [value]
        for row in values:
            if isinstance(row, str):
                yield row, "GET"
            elif isinstance(row, dict):
                url = str(row.get("url") or row.get("endpoint") or row.get("requestUrl") or "").strip()
                method = str(row.get("method") or "GET").upper()
                if url:
                    yield url, method


def build_command(katana: Path, target: str, depth: int, duration: str, rate_limit: int, max_pages: int, headless: bool) -> list[str]:
    command = [
        str(katana), "-u", target, "-d", str(depth), "-jc", "-jsl", "-j", "-silent",
        "-duc", "-rl", str(rate_limit), "-ct", duration,
    ]
    if max_pages > 0:
        command.extend(["-mdp", str(max_pages)])
    if headless:
        command.extend(["-hl", "-xhr"])
    return command


def run(args: argparse.Namespace) -> int:
    target = validate_target(args.target)
    if not 1 <= args.depth <= 10:
        raise ValueError("depth must be between 1 and 10")
    if not 1 <= args.rate_limit <= 100:
        raise ValueError("rate_limit must be between 1 and 100")
    if args.max_pages < 0:
        raise ValueError("max_pages must be >= 0")

    katana = find_katana()
    rules_path = find_rules()
    rules, excludes = load_rules(rules_path)
    command = build_command(katana, target, args.depth, args.duration, args.rate_limit, args.max_pages, args.headless)

    emit({"type": "start", "tool": "wihscan_js", "target": target, "katana": str(katana), "rules": len(rules), "headless": args.headless})
    process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8", errors="replace", bufsize=1)
    javascript_urls: set[str] = set()
    endpoints: set[tuple[str, str]] = set()
    match_count = 0
    records = 0
    assert process.stdout is not None
    for line in process.stdout:
        try:
            item = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(item, dict):
            continue
        records += 1
        url, tag, source = endpoint_from_item(item)
        request = item.get("request") if isinstance(item.get("request"), dict) else {}
        attribute = str(request.get("attribute") or item.get("attribute") or "").strip().lower()
        normalized = url.split("#", 1)[0]
        path = urlparse(normalized).path.lower()
        is_js = tag == "script" or attribute == "src" and path.endswith((".js", ".mjs")) or path.endswith((".js", ".mjs"))
        if normalized and is_js:
            if normalized not in javascript_urls:
                javascript_urls.add(normalized)
                emit({"type": "javascript", "url": normalized, "source": source or None})
        elif normalized and tag == "js":
            emit({"type": "endpoint", "url": normalized, "source": source or None})

        for xhr_url, method in iter_xhr(item):
            endpoints.add((xhr_url, method))

        response = item.get("response") if isinstance(item.get("response"), dict) else {}
        body = response.get("body")
        if isinstance(body, str) and normalized:
            source_tag = "javascript" if is_js else ("page" if tag in {"", "page", "html"} else tag)
            for finding in scan_content(body, normalized, source_tag, rules, excludes):
                if match_count >= MAX_MATCHES:
                    break
                emit(finding)
                match_count += 1

    stderr = process.stderr.read() if process.stderr is not None else ""
    return_code = process.wait()
    for url, method in sorted(endpoints):
        emit({"type": "xhr", "url": url, "method": method})
    summary: dict[str, Any] = {
        "type": "summary",
        "tool": "wihscan_js",
        "target": target,
        "records": records,
        "javascript_urls": len(javascript_urls),
        "xhr_endpoints": len(endpoints),
        "sensitive_matches": match_count,
        "exit_code": return_code,
    }
    if stderr.strip():
        summary["stderr"] = stderr.strip()[-2000:]
    emit(summary)
    return 0 if return_code == 0 else return_code


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="WIHscan JS and sensitive-information adapter")
    parser.add_argument("--target", required=True, help="in-scope absolute HTTP/HTTPS URL")
    parser.add_argument("--depth", type=int, default=DEFAULT_DEPTH)
    parser.add_argument("--duration", default=DEFAULT_DURATION, help="Katana crawl duration, e.g. 30s or 2m")
    parser.add_argument("--rate-limit", type=int, default=DEFAULT_RATE_LIMIT)
    parser.add_argument("--max-pages", type=int, default=50)
    parser.add_argument("--headless", action="store_true", help="enable Katana headless mode and XHR extraction")
    return parser.parse_args(argv)


if __name__ == "__main__":
    try:
        raise SystemExit(run(parse_args()))
    except (FileNotFoundError, ValueError, OSError) as exc:
        emit({"type": "error", "tool": "wihscan_js", "error": str(exc)})
        raise SystemExit(2)
