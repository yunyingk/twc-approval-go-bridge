#!/usr/bin/env python3
"""Explicitly load local configuration for the supervised public debug process."""

import os
from pathlib import Path
import re
import sys


def read_env(path):
    values = {}
    if not path.exists():
        return values
    for number, line in enumerate(path.read_text().splitlines(), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:]
        key, separator, value = line.partition("=")
        key = key.strip()
        if not separator or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise ValueError(f"invalid environment assignment in {path.name}:{number}")
        value = value.strip()
        if len(value) >= 2 and value[0] in "\"'" and value[-1] == value[0]:
            value = value[1:-1]
        # Treat values as data; do not evaluate shell substitutions or commands.
        values[key] = value
    return values


if __name__ == "__main__":
    root = Path(__file__).resolve().parent.parent
    environment = dict(os.environ)
    for path in (root / "configs/config.example.env", root / ".env", root / ".env.public-debug"):
        environment.update(read_env(path))
    binary = root / "bin/twc-approval-public-debug"
    os.chdir(root)
    os.execve(binary, [str(binary), *sys.argv[1:]], environment)
