#!/usr/bin/env python3
"""Run the supervised debug process with one explicitly selected JSON document."""

import os
from pathlib import Path
import sys


if __name__ == "__main__":
    root = Path(__file__).resolve().parent.parent
    environment = dict(os.environ)
    environment.setdefault("CONFIG_FILE", str(root / "configs/config.toml"))
    binary = root / "bin/twc-approval-go-bridge"
    args = sys.argv[1:]
    if not args:
        args = ["server"]
    os.execve(binary, [str(binary), *args], environment)
