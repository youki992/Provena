#!/usr/bin/env python3
"""Small, dependency-light adapters for the useful ARL reconnaissance routines.

The original ARL application couples these routines to Flask, Celery and MongoDB.
This module keeps the scanning behavior stateless so CyberStrike can expose each
operation as an ordinary security tool.  It deliberately caps port enumeration
at Top 1000; full-port scanning is not supported here.
"""

from __future__ import annotations

import argparse
import base64
import concurrent.futures
import difflib
import json
import os
import random
import re
import secrets
import shutil
import socket
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any, Iterable
from urllib.parse import quote, urljoin, urlparse


ROOT = Path(__file__).resolve().parent
DICT_ROOT = ROOT / "arl-dicts"
DOMAIN_WORDLIST = DICT_ROOT / "domain_2w.txt"
FILE_WORDLIST = DICT_ROOT / "file_top_2000.txt"
WEBAPP_RULES = DICT_ROOT / "webapp.json"

ARL_TOP_PORTS = [
    80, 443, 8443, 8080, 8081, 8888, 8089, 5000, 5001, 8085, 800,
    81, 9000, 88, 8001, 8090,
]
PORT_SERVICE_FALLBACK = {
    21: "ftp", 22: "ssh", 23: "telnet", 25: "smtp", 53: "dns",
    80: "http", 110: "pop3", 111: "rpcbind", 143: "imap", 443: "https",
    445: "microsoft-ds", 465: "smtps", 587: "submission", 993: "imaps",
    995: "pop3s", 1433: "ms-sql-s", 1521: "oracle", 3306: "mysql",
    3389: "ms-wbt-server", 5432: "postgresql", 5672: "amqp", 5900: "vnc",
    6379: "redis", 8000: "http-alt", 8080: "http-proxy", 8443: "https-alt",
    9000: "http-alt", 9200: "elasticsearch", 11211: "memcached", 27017: "mongodb",
}
VALID_LABEL = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$")


def emit(value: dict[str, Any]) -> None:
    print(json.dumps(value, ensure_ascii=False, sort_keys=True))


def load_words(path: Path, limit: int | None = None) -> list[str]:
    words: list[str] = []
    with path.open("r", encoding="utf-8", errors="ignore") as stream:
        for raw in stream:
            word = raw.strip()
            if not word or word.startswith("#"):
                continue
            if word not in words:
                words.append(word)
            if limit and len(words) >= limit:
                break
    return words


def normalize_domain(value: str) -> str:
    candidate = value.strip()
    if "://" in candidate:
        candidate = urlparse(candidate).hostname or ""
    candidate = candidate.split("/", 1)[0].split(":", 1)[0].strip(".").lower()
    try:
        candidate = candidate.encode("idna").decode("ascii")
    except UnicodeError as exc:
        raise ValueError(f"invalid domain: {value}") from exc
    if not candidate or any(not VALID_LABEL.match(label) for label in candidate.split(".")):
        raise ValueError(f"invalid domain: {value}")
    return candidate


def resolve_ips(host: str) -> list[str]:
    try:
        rows = socket.getaddrinfo(host, None, type=socket.SOCK_STREAM)
    except (OSError, socket.gaierror):
        return []
    result = {row[4][0] for row in rows if row[4]}
    return sorted(result)


def wildcard_ips(domain: str, probes: int = 2) -> set[str]:
    found: set[str] = set()
    for _ in range(probes):
        probe = f"arl-{secrets.token_hex(6)}.{domain}"
        found.update(resolve_ips(probe))
    return found


def run_subdomains(args: argparse.Namespace) -> int:
    domain = normalize_domain(args.domain)
    wordlist = Path(args.wordlist) if args.wordlist else DOMAIN_WORDLIST
    if not wordlist.is_file():
        raise FileNotFoundError(f"subdomain wordlist not found: {wordlist}")
    words = load_words(wordlist, args.limit)
    wildcard = wildcard_ips(domain)
    candidates = []
    for word in words:
        if any(not VALID_LABEL.match(label) for label in word.split(".")):
            continue
        candidates.append(f"{word.lower()}.{domain}")

    found = 0
    completed = 0
    progress_step = max(100, min(1000, max(1, len(candidates) // 20)))
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, min(args.concurrency, 128))) as pool:
        future_map = {pool.submit(resolve_ips, candidate): candidate for candidate in candidates}
        for future in concurrent.futures.as_completed(future_map):
            candidate = future_map[future]
            ips = future.result()
            completed += 1
            if not ips or (wildcard and set(ips).issubset(wildcard)):
                if completed % progress_step != 0 and completed != len(candidates):
                    continue
            else:
                emit({"domain": candidate, "type": "A/AAAA", "ips": ips, "wildcard_filtered": False})
                found += 1
            if completed % progress_step == 0 or completed == len(candidates):
                emit({"progress": "arl_subdomain_scan", "completed": completed,
                      "total": len(candidates), "results": found})
                sys.stdout.flush()
    emit({"summary": "arl_subdomain_scan", "base_domain": domain, "candidates": len(candidates),
          "wildcard_ips": sorted(wildcard), "results": found})
    return 0


def parse_ports(spec: str) -> list[int]:
    values: set[int] = set()
    for item in spec.split(","):
        item = item.strip()
        if not item:
            continue
        if "-" in item:
            start_text, end_text = item.split("-", 1)
            start, end = int(start_text), int(end_text)
            if start > end:
                start, end = end, start
            if end - start + 1 > 1000:
                raise ValueError("ARL port scan is capped at 1000 ports; full-port scanning is disabled")
            values.update(range(start, end + 1))
        else:
            values.add(int(item))
    if not values or len(values) > 1000 or any(port < 1 or port > 65535 for port in values):
        raise ValueError("ports must contain 1-65535 and no more than 1000 ports")
    return sorted(values)


def service_name(port: int) -> str:
    try:
        return socket.getservbyport(port, "tcp")
    except OSError:
        return PORT_SERVICE_FALLBACK.get(port, "")


def normalize_target(value: str) -> str:
    target = value.strip()
    if "://" in target:
        target = urlparse(target).hostname or ""
    target = target.split("/", 1)[0]
    if not target or any(char in target for char in " \t\r\n;|&`$"):
        raise ValueError(f"invalid target: {value}")
    return target


def tcp_probe(target: str, port: int, timeout: float) -> dict[str, Any] | None:
    try:
        with socket.create_connection((target, port), timeout=timeout):
            return {"target": target, "port": port, "protocol": "tcp", "service": service_name(port)}
    except (OSError, socket.timeout):
        return None


def run_tcp_ports(target: str, ports: Iterable[int], timeout: float, concurrency: int) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, min(concurrency, 128))) as pool:
        futures = [pool.submit(tcp_probe, target, port, timeout) for port in ports]
        for future in concurrent.futures.as_completed(futures):
            item = future.result()
            if item:
                result.append(item)
    return sorted(result, key=lambda item: item["port"])


def vscanplus_path() -> Path | None:
    candidate = ROOT.parent / "vscanplus" / "vscan-windows-amd64.exe"
    return candidate if candidate.is_file() else None


def run_process_with_heartbeat(command: list[str], label: str, scan_timeout_minutes: float) -> tuple[int, str, str]:
    """Run a long scanner while emitting heartbeats so the caller knows it is alive."""
    emit({"progress": label, "status": "started"})
    sys.stdout.flush()
    process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               text=True, errors="replace")
    stopped = threading.Event()

    def heartbeat() -> None:
        started = time.monotonic()
        while not stopped.wait(30):
            if process.poll() is not None:
                return
            emit({"progress": label, "status": "running",
                  "elapsed_seconds": int(time.monotonic() - started)})
            sys.stdout.flush()

    thread = threading.Thread(target=heartbeat, name="arl-scan-heartbeat", daemon=True)
    thread.start()
    try:
        timeout = scan_timeout_minutes * 60 if scan_timeout_minutes > 0 else None
        stdout, stderr = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        process.kill()
        stdout, stderr = process.communicate()
        raise TimeoutError(f"{label} exceeded scan_timeout_minutes={scan_timeout_minutes}") from exc
    finally:
        stopped.set()
        thread.join(timeout=1)
    return process.returncode, stdout, stderr


def run_external_top_ports(target: str, top_ports: int, timeout: float, scan_timeout_minutes: float) -> list[dict[str, Any]]:
    scanner = vscanplus_path()
    if scanner:
        command = [str(scanner), "-s", "c", "-np", "-silent", "-nc", "-json",
                   "-top-ports", str(top_ports), "-rate", "300", "-c", "25", "-host", target]
        return_code, stdout, _stderr = run_process_with_heartbeat(
            command, f"arl_port_scan:vscanplus:top{top_ports}", scan_timeout_minutes)
        if return_code == 0:
            rows: list[dict[str, Any]] = []
            for line in stdout.splitlines():
                try:
                    item = json.loads(line)
                except json.JSONDecodeError:
                    continue
                port = item.get("port") or item.get("port_id")
                if str(port).isdigit():
                    rows.append({"target": item.get("ip") or target, "port": int(port),
                                 "protocol": item.get("protocol", "tcp"),
                                 "service": item.get("service", item.get("service_name", "")),
                                 "source": "vscanplus"})
            if rows:
                return sorted(rows, key=lambda item: item["port"])

    nmap = shutil.which("nmap")
    if nmap:
        return_code, stdout, _stderr = run_process_with_heartbeat(
            [nmap, "-sT", "-Pn", "-n", "--top-ports", str(top_ports), target],
            f"arl_port_scan:nmap:top{top_ports}", scan_timeout_minutes)
        if return_code == 0:
            rows = []
            for line in stdout.splitlines():
                match = re.match(r"^\s*(\d+)/tcp\s+open\s+(\S+)", line)
                if match:
                    rows.append({"target": target, "port": int(match.group(1)),
                                 "protocol": "tcp", "service": match.group(2), "source": "nmap"})
            return rows

    raise RuntimeError("Top 100/1000 scan requires the bundled VscanPlus or nmap")


def run_ports(args: argparse.Namespace) -> int:
    target = normalize_target(args.target)
    if args.ports:
        rows = run_tcp_ports(target, parse_ports(args.ports), args.timeout, args.concurrency)
    else:
        top_ports = args.top_ports or 10
        if top_ports not in (10, 100, 1000):
            raise ValueError("top_ports must be one of 10, 100, or 1000")
        if top_ports == 10:
            rows = run_tcp_ports(target, ARL_TOP_PORTS, args.timeout, args.concurrency)
        else:
            rows = run_external_top_ports(target, top_ports, args.timeout, args.scan_timeout_minutes)
    for item in rows:
        emit(item)
    emit({"summary": "arl_port_scan", "target": target, "open_ports": len(rows),
          "full_port_scan": False})
    return 0


def title_from_body(body: str) -> str:
    match = re.search(r"<title[^>]*>(.*?)</title>", body, flags=re.I | re.S)
    return re.sub(r"\s+", " ", match.group(1)).strip() if match else ""


def murmurhash3(value: bytes, seed: int = 0) -> int:
    data = bytearray(value)
    h1 = seed & 0xFFFFFFFF
    c1, c2 = 0xcc9e2d51, 0x1b873593
    for offset in range(0, len(data) - len(data) % 4, 4):
        k1 = int.from_bytes(data[offset:offset + 4], "little")
        k1 = (k1 * c1) & 0xFFFFFFFF
        k1 = ((k1 << 15) | (k1 >> 17)) & 0xFFFFFFFF
        k1 = (k1 * c2) & 0xFFFFFFFF
        h1 ^= k1
        h1 = ((h1 << 13) | (h1 >> 19)) & 0xFFFFFFFF
        h1 = (h1 * 5 + 0xe6546b64) & 0xFFFFFFFF
    tail = data[len(data) - len(data) % 4:]
    k1 = 0
    for index, byte in enumerate(tail):
        k1 ^= byte << (8 * index)
    if tail:
        k1 = (k1 * c1) & 0xFFFFFFFF
        k1 = ((k1 << 15) | (k1 >> 17)) & 0xFFFFFFFF
        k1 = (k1 * c2) & 0xFFFFFFFF
        h1 ^= k1
    h1 ^= len(data)
    h1 ^= h1 >> 16
    h1 = (h1 * 0x85ebca6b) & 0xFFFFFFFF
    h1 ^= h1 >> 13
    h1 = (h1 * 0xc2b2ae35) & 0xFFFFFFFF
    h1 ^= h1 >> 16
    return h1 - 0x100000000 if h1 & 0x80000000 else h1


def header_rule_matches(rule_value: Any, headers: Any) -> bool:
    """Match an ARL header fingerprint against one header at a time.

    ARL's original implementation searched a concatenated header string. That
    makes a rule such as ``eHTTP`` match the ``eHTTP`` substring inside
    ``SimpleHTTP`` and produces false positives. Header names and values are
    checked independently, with alphanumeric boundaries around the rule.
    """
    needle = str(rule_value).strip()
    if not needle:
        return False
    pattern = re.compile(
        rf"(?<![A-Za-z0-9]){re.escape(needle)}(?![A-Za-z0-9])",
        flags=re.IGNORECASE,
    )
    for key, value in headers.items():
        if pattern.search(str(key)) or pattern.search(str(value)):
            return True
    return False


def alternate_http_scheme(url: str) -> str:
    """Return the same target with the opposite HTTP scheme."""
    parsed = urlparse(url)
    if parsed.scheme.lower() == "http":
        scheme = "https"
    elif parsed.scheme.lower() == "https":
        scheme = "http"
    else:
        return url
    return parsed._replace(scheme=scheme).geturl()


def is_protocol_mismatch(exc: BaseException) -> bool:
    """Identify TLS/plain-HTTP handshake failures worth a single retry.

    Ordinary request failures must not trigger a second request.  Requests
    wraps protocol errors differently by version, so inspect the exception
    chain text for the small set of signatures emitted by urllib3/OpenSSL.
    """
    message = " ".join(str(item).lower() for item in (exc, exc.__cause__, exc.__context__) if item)
    markers = (
        "wrong version number",
        "wrong_version_number",
        "unknown protocol",
        "record layer failure",
        "bad statusline",
        "badstatusline",
        "remote end closed connection without response",
        "connection aborted",
    )
    return any(marker in message for marker in markers)


def request_with_protocol_fallback(requests_module: Any, target: str, timeout: float,
                                   verify: bool, headers: dict[str, str]) -> tuple[Any, str, bool]:
    """Fetch once, retrying with the opposite scheme only after a handshake mismatch."""
    try:
        return requests_module.get(target, timeout=timeout, verify=verify,
                                   allow_redirects=True, headers=headers), target, False
    except requests_module.RequestException as exc:
        if not is_protocol_mismatch(exc):
            raise
        fallback = alternate_http_scheme(target)
        if fallback == target:
            raise
        response = requests_module.get(fallback, timeout=timeout, verify=verify,
                                       allow_redirects=True, headers=headers)
        return response, fallback, True


def run_fingerprint(args: argparse.Namespace) -> int:
    try:
        import requests
    except ImportError as exc:
        raise RuntimeError("fingerprint scan requires the Python requests package") from exc

    requested_target = args.target if "://" in args.target else f"http://{args.target}"
    headers = {"User-Agent": "CyberStrike-ARL/1.0"}
    response, target, protocol_fallback = request_with_protocol_fallback(
        requests, requested_target, args.timeout, not args.allow_insecure, headers)
    body = response.content
    text = body.decode("utf-8", "replace")
    title = title_from_body(text)
    favicon_hash = None
    try:
        icon = requests.get(urljoin(response.url, "/favicon.ico"), timeout=args.timeout,
                            verify=not args.allow_insecure, headers={"User-Agent": "CyberStrike-ARL/1.0"})
        if icon.status_code == 200 and len(icon.content) > 80:
            encoded = base64.b64encode(icon.content).decode("ascii")
            encoded = "\n".join(encoded[index:index + 76] for index in range(0, len(encoded), 76))
            favicon_hash = murmurhash3(encoded.encode("ascii"))
    except requests.RequestException:
        pass

    rules: dict[str, Any] = {}
    if WEBAPP_RULES.is_file():
        with WEBAPP_RULES.open("r", encoding="utf-8") as stream:
            rules = json.load(stream)
    matches = []
    body_lower, title_lower = text.lower(), title.lower()
    for name, rule in rules.items():
        evidence = []
        for value in rule.get("html", []):
            if str(value).lower() in body_lower:
                evidence.append({"field": "body", "value": value})
        for value in rule.get("headers", []):
            if header_rule_matches(value, response.headers):
                evidence.append({"field": "headers", "value": value})
        for value in rule.get("title", []):
            if str(value).lower() in title_lower:
                evidence.append({"field": "title", "value": value})
        if favicon_hash is not None and favicon_hash in rule.get("icon_hash", rule.get("favicon_hash", [])):
            evidence.append({"field": "favicon_hash", "value": favicon_hash})
        if evidence:
            matches.append({"name": name, "evidence": evidence[:8]})

    emit({"target": target, "requested_target": requested_target,
          "protocol_fallback": protocol_fallback, "final_url": response.url, "status": response.status_code,
          "title": title, "server": response.headers.get("Server", ""),
          "content_length": len(body), "favicon_hash": favicon_hash,
          "fingerprints": matches, "fingerprint_source": "ARL webapp.json"})
    return 0


def make_directory_paths(target: str, wordlist: Path, limit: int) -> list[str]:
    words = load_words(wordlist, limit)
    parsed = urlparse(target)
    host = parsed.hostname or "target"
    names = {host.split(".")[0], host.replace(".", "_")}
    suffixes = [".tar", ".tar.gz", ".zip", ".rar", ".7z", ".bz2", ".gz", "_bak.rar", ".war"]
    generated = {f"{name}{suffix}" for name in names for suffix in suffixes}
    paths = []
    for item in words + sorted(generated):
        item = item.strip().lstrip("/")
        if item and "\x00" not in item and item not in paths:
            paths.append(item)
        if len(paths) >= limit:
            break
    return paths


def fetch_path(requests_module: Any, url: str, timeout: float, verify: bool) -> dict[str, Any]:
    response = requests_module.get(url, timeout=timeout, verify=verify, allow_redirects=False,
                                   headers={"User-Agent": "CyberStrike-ARL/1.0"})
    body = response.content[:65536]
    return {"url": url, "status": response.status_code, "body": body,
            "length": len(response.content), "location": response.headers.get("Location", ""),
            "content_type": response.headers.get("Content-Type", ""),
            "title": title_from_body(body.decode("utf-8", "replace"))}


def looks_like_not_found(page: dict[str, Any], baseline: dict[str, Any]) -> bool:
    if page["status"] not in (200, 204, 301, 302, 307, 308, 401, 403, 500):
        return True
    if page["status"] == baseline["status"] and abs(page["length"] - baseline["length"]) <= 5:
        return True
    if page["title"] and page["title"].lower() in {"404", "404 not found", "not found", "error", "forbidden"}:
        return True
    if page["status"] in (301, 302, 307, 308):
        location = page["location"].lower()
        if any(token in location for token in ("/404", "not-found", "error")):
            return True
    body = page["body"]
    baseline_body = baseline["body"]
    if body and baseline_body:
        ratio = difflib.SequenceMatcher(None, body[:20000], baseline_body[:20000]).quick_ratio()
        if ratio >= 0.8:
            return True
    return False


def run_directory(args: argparse.Namespace) -> int:
    try:
        import requests
        requests.packages.urllib3.disable_warnings()
    except ImportError as exc:
        raise RuntimeError("directory scan requires the Python requests package") from exc

    target = args.target if "://" in args.target else f"http://{args.target}"
    target = target.rstrip("/") + "/"
    wordlist = Path(args.wordlist) if args.wordlist else FILE_WORDLIST
    if not wordlist.is_file():
        raise FileNotFoundError(f"directory wordlist not found: {wordlist}")
    baseline_url = urljoin(target, f"arl-not-found-{secrets.token_hex(8)}")
    baseline = fetch_path(requests, baseline_url, args.timeout, not args.allow_insecure)
    paths = make_directory_paths(target, wordlist, args.limit)
    found = 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, min(args.concurrency, 64))) as pool:
        future_map = {
            pool.submit(fetch_path, requests, urljoin(target, quote(path, safe="/._-~")),
                        args.timeout, not args.allow_insecure): path for path in paths
        }
        for future in concurrent.futures.as_completed(future_map):
            path = future_map[future]
            try:
                page = future.result()
            except requests.RequestException:
                continue
            if looks_like_not_found(page, baseline):
                continue
            emit({"path": path, "url": page["url"], "status": page["status"],
                  "content_length": page["length"], "content_type": page["content_type"],
                  "title": page["title"], "location": page["location"],
                  "source": "ARL file_top_2000"})
            found += 1
    emit({"summary": "arl_directory_scan", "target": target, "candidates": len(paths), "results": found})
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="ARL-style stateless reconnaissance helpers")
    commands = parser.add_subparsers(dest="command", required=True)

    subdomains = commands.add_parser("subdomains")
    subdomains.add_argument("--domain", required=True)
    subdomains.add_argument("--wordlist")
    subdomains.add_argument("--limit", type=int, default=20000)
    subdomains.add_argument("--concurrency", type=int, default=50)
    subdomains.set_defaults(handler=run_subdomains)

    ports = commands.add_parser("ports")
    ports.add_argument("--target", required=True)
    ports.add_argument("--ports")
    ports.add_argument("--top-ports", dest="top_ports", type=int, default=10)
    ports.add_argument("--timeout", type=float, default=1.0)
    ports.add_argument("--concurrency", type=int, default=32)
    ports.add_argument("--scan-timeout-minutes", dest="scan_timeout_minutes", type=float, default=0,
                       help="外部 Top 100/1000 扫描总时限；0 表示不设墙钟时限，只能手动取消")
    ports.set_defaults(handler=run_ports)

    fingerprint = commands.add_parser("fingerprint")
    fingerprint.add_argument("--target", required=True)
    fingerprint.add_argument("--timeout", type=float, default=15.0)
    fingerprint.add_argument("--allow-insecure", action="store_true")
    fingerprint.set_defaults(handler=run_fingerprint)

    directory = commands.add_parser("directory")
    directory.add_argument("--target", required=True)
    directory.add_argument("--wordlist")
    directory.add_argument("--limit", type=int, default=2000)
    directory.add_argument("--concurrency", type=int, default=10)
    directory.add_argument("--timeout", type=float, default=10.0)
    directory.add_argument("--allow-insecure", action="store_true")
    directory.set_defaults(handler=run_directory)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    try:
        return args.handler(args)
    except KeyboardInterrupt:
        return 130
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
