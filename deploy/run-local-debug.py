#!/usr/bin/env python3
"""Run the debug binary with one explicitly selected TOML configuration."""

import os
from pathlib import Path
import json
import plistlib
import sys


if __name__ == "__main__":
    root = Path(__file__).resolve().parent.parent
    environment = dict(os.environ)
    environment.setdefault("CONFIG_FILE", str(root / "configs/config.toml"))
    binary = root / "bin/twc-approval-go-bridge"
    args = sys.argv[1:]
    if not args:
        args = ["server"]
    active = root / "bin/local-service/active.json"
    if sys.platform == "darwin" and args[0] in {"server", "start", "run"} and active.exists():
        release = json.loads(active.read_text())
        app = Path(release["app_path"]).resolve()
        if not app.is_relative_to((root / "bin/local-service/releases").resolve()):
            raise RuntimeError("local application is outside this project's release directory")
        agent_path = Path.home() / "Library/LaunchAgents/com.yingqing.twc-approval-debug.plist"
        agent = plistlib.loads(agent_path.read_bytes())
        command = ["/usr/bin/open", "-W", "-g", str(app),
                   "--env", "CONFIG_FILE=" + environment["CONFIG_FILE"],
                   "--stdout", agent["StandardOutPath"], "--stderr", agent["StandardErrorPath"],
                   "--args", *args]
        os.execve(command[0], command, environment)
    os.execve(binary, [str(binary), *args], environment)
