#!/usr/bin/env python3
"""Execute a bundled scanner for the current operating system.

Tool YAML files use this tiny launcher so Windows and Linux can share one
configuration without relying on a globally installed binary or a fixed PATH.
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parent / "bin"


def platform_name() -> str:
    if os.name == "nt":
        return "windows-amd64"
    if sys.platform.startswith("linux"):
        return "linux-amd64"
    raise SystemExit(f"unsupported platform: {sys.platform}")


def candidates(tool: str, platform: str) -> list[Path]:
    suffix = ".exe" if platform.startswith("windows") else ""
    direct = ROOT / tool / platform / f"{tool}{suffix}"
    values = [direct]
    if tool == "dddd":
        values = [ROOT / "dddd" / platform / f"dddd{suffix}"]
    elif tool == "nmap" and platform.startswith("windows"):
        values = [Path(os.environ.get("ProgramFiles", r"C:\Program Files")) / "Nmap" / "nmap.exe",
                  Path(os.environ.get("ProgramFiles(x86)", r"C:\Program Files (x86)")) / "Nmap" / "nmap.exe",
                  ROOT / "nmap" / platform / "nmap.exe"]
    elif tool == "katana":
        values = [ROOT / "katana" / platform / f"katana{suffix}",
                  ROOT / "dddd" / platform / "WIHscan-1.0" / f"katana{suffix}"]
    # Backward compatibility with the original Windows-only layout.
    if platform.startswith("windows") and tool != "nmap":
        values.append(ROOT / tool / f"{tool}.exe")
    return values


def normalize_args(tool: str, platform: str, args: list[str]) -> list[str]:
    """Apply safe platform defaults without overriding explicit privileges."""
    if tool != "nmap" or not platform.startswith("windows"):
        return args
    lowered = {value.lower() for value in args}
    privileged_scan = bool(lowered.intersection({"--privileged", "-ss", "-su", "-o", "-a"}))
    if "--unprivileged" in lowered or privileged_scan:
        return args
    # Some Windows/virtualized environments expose no usable Ethernet
    # adapter to Nmap. Explicitly select connect/unprivileged behavior so
    # ordinary service scans do not fail on a synthetic device such as unk1.
    return ["--unprivileged", *args]


def main() -> int:
    if len(sys.argv) < 2:
        raise SystemExit("usage: bundled_tool.py <amass|ffuf|gau|subfinder|dddd|katana|waybackurls|nmap|paramspider> [args...]")
    tool = sys.argv[1].strip().lower()
    if tool not in {"amass", "ffuf", "gau", "subfinder", "dddd", "katana", "waybackurls", "nmap", "paramspider"}:
        raise SystemExit(f"unsupported bundled tool: {tool}")

    platform = platform_name()
    binary = next((path for path in candidates(tool, platform) if path.is_file()), None)
    if binary is None:
        checked = ", ".join(str(path) for path in candidates(tool, platform))
        raise SystemExit(f"bundled {tool} binary not found for {platform}; checked: {checked}")

    # dddd discovers config/, pocs/ and the bundled Katana relative to its CWD.
    completed = subprocess.run([str(binary), *normalize_args(tool, platform, sys.argv[2:])], cwd=binary.parent, check=False)
    return completed.returncode


if __name__ == "__main__":
    raise SystemExit(main())
