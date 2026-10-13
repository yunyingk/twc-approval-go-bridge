#!/usr/bin/env python3
"""Build, inspect, update, and roll back the existing macOS LaunchAgent."""

import argparse
import base64
from contextlib import contextmanager
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import math
import os
from pathlib import Path
import plistlib
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import tomllib
import urllib.request


LABEL = "com.yingqing.twc-approval-debug"
BUNDLE_ID = "com.yingqing.twc-approval-bridge"
APP_NAME = "TWC Approval Bridge.app"
VERSION_PACKAGE = "github.com/yunyingk/twc-approval-go-bridge/internal/version"


def atomic_write(path, data, mode=0o600):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix=".local-service-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as handle:
            os.fchmod(handle.fileno(), mode)
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def write_json(path, value):
    atomic_write(path, (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode())


def duration_seconds(value):
    if value == "0":
        return 0
    parts = re.findall(r"([0-9]+(?:\.[0-9]+)?)(ns|us|µs|μs|ms|s|m|h)", value)
    if not parts or "".join(number + unit for number, unit in parts) != value:
        raise ValueError("invalid runtime.shutdown_timeout")
    factors = {"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "μs": 1e-6,
               "ms": 1e-3, "s": 1, "m": 60, "h": 3600}
    return sum(float(number) * factors[unit] for number, unit in parts)


class LocalService:
    def __init__(self, root=None, agent=None):
        self.root = Path(root or Path(__file__).resolve().parent.parent).resolve()
        self.agent = Path(agent or Path.home() / "Library/LaunchAgents" / (LABEL + ".plist"))
        self.target = f"gui/{os.getuid()}/{LABEL}"
        self.domain = f"gui/{os.getuid()}"
        self.binary = self.root / "bin/twc-approval-go-bridge"
        self.releases = self.root / "bin/local-service/releases"
        self.active_file = self.root / "bin/local-service/active.json"
        self.private = self.root / "data/local-service"
        self.previous_file = self.private / "previous.json"
        self.plist = plistlib.loads(self.agent.read_bytes())
        arguments = self.plist.get("ProgramArguments", [])
        if (self.plist.get("Label") != LABEL or
                str(self.root / "deploy/run-local-debug.py") not in arguments or
                Path(self.plist.get("WorkingDirectory", "")).resolve() != self.root):
            raise RuntimeError("LaunchAgent does not belong to this checkout; refusing to change it")
        configured = self.plist.get("EnvironmentVariables", {}).get("CONFIG_FILE")
        self.config_path = Path(configured or os.environ.get("CONFIG_FILE") or
                                self.root / "configs/config.toml")
        if not self.config_path.is_absolute():
            self.config_path = self.root / self.config_path
        self.config_path = self.config_path.resolve()
        self.config = tomllib.loads(self.config_path.read_text())
        runtime = self.config.get("runtime", {})
        address = runtime.get("http_addr", ":8080")
        host, port = address.rsplit(":", 1)
        self.port = int(port)
        if host not in ("", "0.0.0.0", "127.0.0.1", "localhost", "[::1]", "[::]"):
            raise RuntimeError("local service checks require a local HTTP listener")
        self.host = "::1" if host.startswith("[") else "127.0.0.1"
        url_host = "[::1]" if self.host == "::1" else self.host
        self.url = f"http://{url_host}:{self.port}"
        self.password = runtime.get("dashboard_password", "")
        self.grace = max(20, math.ceil(duration_seconds(runtime.get("shutdown_timeout", "10s"))) + 10)

    def run(self, args, capture=True, check=True, **kwargs):
        return subprocess.run(args, cwd=self.root, check=check, text=True,
                              capture_output=capture, **kwargs)

    @contextmanager
    def lock(self):
        self.private.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.private.chmod(0o700)
        with (self.private / "operation.lock").open("a") as handle:
            os.chmod(handle.name, 0o600)
            try:
                fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RuntimeError("another local update or rollback is running") from None
            yield

    def active(self):
        if not self.active_file.exists():
            return None
        value = json.loads(self.active_file.read_text())
        for key in ("app_path", "binary_path", "control_path"):
            if not Path(value[key]).resolve().is_relative_to(self.releases.resolve()):
                raise RuntimeError("active release contains a path outside the release directory")
        return value

    def request(self, endpoint):
        request = urllib.request.Request(self.url + endpoint)
        if self.password and endpoint == "/api/status":
            token = base64.b64encode(("admin:" + self.password).encode()).decode()
            request.add_header("Authorization", "Basic " + token)
        # Local management must not send credentials through a shell proxy.
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(request, timeout=2) as response:
            return json.load(response)

    def apps(self, release=None):
        release = release or self.active()
        if not release:
            return []
        return json.loads(self.run([release["control_path"], BUNDLE_ID]).stdout)

    def binary_version(self, binary):
        return json.loads(self.run([str(binary), "version", "-json"]).stdout)

    def listener_pids(self):
        result = self.run(["lsof", "-t", "-nP", f"-iTCP:{self.port}",
                           "-sTCP:LISTEN"], check=False)
        if result.returncode not in (0, 1):
            raise RuntimeError("cannot inspect the local HTTP listener")
        return sorted({int(value) for value in result.stdout.split()})

    def owned_pid(self, pid):
        result = self.run(["ps", "-p", str(pid), "-o", "comm="], check=False)
        executable = result.stdout.strip()
        if not executable:
            return False
        path = Path(executable).resolve()
        return path == self.binary or path.is_relative_to(self.releases.resolve())

    def assert_owned_listener(self):
        pids = self.listener_pids()
        if len(pids) > 1 or any(not self.owned_pid(pid) for pid in pids):
            raise RuntimeError("HTTP port is occupied by an unrelated or duplicate process")
        return pids

    def status(self):
        value = {"service_label": LABEL, "url": self.url}
        job = self.run(["launchctl", "print", self.target], check=False)
        value["launchagent_loaded"] = job.returncode == 0
        match = re.search(r"^\s*pid = (\d+)$", job.stdout, re.MULTILINE)
        value["supervisor_pid"] = int(match[1]) if match else None
        value["backend_pids"] = self.listener_pids()
        value["source_commit"] = self.run(["git", "rev-parse", "--short", "HEAD"]).stdout.strip()
        if self.binary.exists():
            value["disk_version"] = self.binary_version(self.binary)
        try:
            value["running_version"] = self.request("/version")
            runtime = self.request("/api/status")
            value["feishu_state"] = runtime.get("feishu_state")
            value["health"] = self.request("/healthz").get("status")
        except (OSError, ValueError):
            value["health"] = "unavailable"
        value["applications"] = self.apps()
        value["rollback_available"] = self.previous_file.exists()
        return value

    def source_fingerprint(self):
        digest = hashlib.sha256()
        digest.update(self.run(["git", "rev-parse", "HEAD"]).stdout.encode())
        files = self.run(["git", "ls-files", "-z", "--cached", "--others",
                          "--exclude-standard"]).stdout.split("\0")
        for name in sorted(set(filter(None, files))):
            path = self.root / name
            digest.update(name.encode())
            digest.update(path.read_bytes() if path.is_file() else b"<deleted>")
        return digest.hexdigest()

    def checks(self):
        for args in ([sys.executable, "-B", "-m", "unittest", "discover", "-s", "scripts",
                      "-p", "test_local_service.py"],
                     ["go", "test", "./..."], ["go", "test", "-tags", "no_anthropic", "./..."],
                     ["go", "vet", "./..."]):
            print("检查:", " ".join(args), flush=True)
            self.run(args, capture=False)

    def build(self):
        fingerprint = self.source_fingerprint()
        version_text = (self.root / "internal/version/version.go").read_text()
        version = re.search(r'Version\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"', version_text).group(1)
        commit = self.run(["git", "rev-parse", "--short", "HEAD"]).stdout.strip()
        if self.run(["git", "status", "--porcelain", "--untracked-files=normal"]).stdout:
            commit += "-dirty"
        now = datetime.now(timezone.utc)
        date = now.strftime("%Y-%m-%dT%H:%M:%SZ")
        release = Path(tempfile.mkdtemp(prefix=now.strftime("%Y%m%dT%H%M%SZ-") + commit + "-",
                                       dir=self._release_directory()))
        app = release / APP_NAME
        resources = app / "Contents/Resources"
        macos = app / "Contents/MacOS"
        resources.mkdir(parents=True)
        macos.mkdir()
        binary = resources / "twc-approval-go-bridge"
        flags = "-s -w " + " ".join(f"-X {VERSION_PACKAGE}.{key}={value}" for key, value in
                                    [("Version", version), ("GitCommit", commit), ("BuildDate", date)])
        environment = dict(os.environ, CGO_ENABLED="0")
        self.run(["go", "build", "-trimpath", "-ldflags", flags, "-o", str(binary), "./cmd/server"],
                 capture=False, env=environment)
        for name, framework, output in [("launcher.m", "Foundation", macos / "twc-approval-launcher"),
                                         ("app-info.m", "AppKit", release / "app-info")]:
            self.run(["clang", "-O2", "-Wall", "-Wextra", "-Werror", "-framework", framework,
                      str(self.root / "deploy/macos" / name), "-o", str(output)], capture=False)
        shutil.copyfile(self.root / "deploy/macos/AppIcon.icns", resources / "AppIcon.icns")
        info = {"CFBundleExecutable": "twc-approval-launcher", "CFBundleIdentifier": BUNDLE_ID,
                "CFBundleName": "TWC Approval Bridge", "CFBundleDisplayName": "TWC Approval Bridge",
                "CFBundlePackageType": "APPL", "CFBundleIconFile": "AppIcon.icns",
                "CFBundleShortVersionString": version, "CFBundleVersion": version,
                "LSBackgroundOnly": True, "NSHighResolutionCapable": True,
                "TWCProjectRoot": str(self.root), "TWCBuildCommit": commit}
        atomic_write(app / "Contents/Info.plist", plistlib.dumps(info), 0o644)
        # Sign both executable images: the Go binary is execed within this app.
        for path in (binary, app):
            self.run(["codesign", "--force", "--sign", "-", "--timestamp=none", str(path)],
                     capture=False)
            self.run(["codesign", "--verify", "--deep", "--strict", str(path)], capture=False)
        metadata = {"app_path": str(app), "binary_path": str(binary),
                    "control_path": str(release / "app-info"), "version": self.binary_version(binary),
                    "source_fingerprint": fingerprint}
        if self.source_fingerprint() != fingerprint:
            raise RuntimeError("source changed during build; existing service was left running")
        write_json(release / "release.json", metadata)
        return metadata

    def _release_directory(self):
        self.releases.mkdir(parents=True, exist_ok=True)
        return self.releases

    def snapshot(self):
        backups = self.private / "backups"
        backups.mkdir(parents=True, exist_ok=True, mode=0o700)
        directory = Path(tempfile.mkdtemp(prefix="saved-", dir=backups))
        active = self.active()
        binary = Path(active["binary_path"]) if active else self.binary
        if not binary.is_file():
            raise RuntimeError("no runnable version exists to save for rollback")
        saved = directory / "twc-approval-go-bridge"
        shutil.copyfile(binary, saved)
        saved.chmod(0o700)
        state = {"directory": str(directory), "active": active,
                 "version": self.binary_version(saved)}
        atomic_write(directory / "agent.plist", self.agent.read_bytes())
        write_json(directory / "snapshot.json", state)
        return state

    def stop(self):
        pids = self.assert_owned_listener()
        apps = self.apps()
        for app in apps:
            if not Path(app["bundle_path"]).resolve().is_relative_to(self.releases.resolve()):
                raise RuntimeError("a same-identifier application belongs to another checkout")
            if app["pid"] not in pids:
                pids.append(app["pid"])
        job = self.run(["launchctl", "print", self.target], check=False)
        if job.returncode == 0:
            self.run(["launchctl", "bootout", self.target])
        # Launch Services owns the app process; unloading its waiting supervisor
        # alone does not stop it. Signal only verified project processes.
        for pid in pids:
            if self.owned_pid(pid):
                try:
                    os.kill(pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
        deadline = time.monotonic() + self.grace
        while time.monotonic() < deadline:
            if not self.listener_pids() and not self.apps():
                return
            time.sleep(0.2)
        raise RuntimeError("old service did not exit cleanly; refusing to start a second instance")

    def install(self, release):
        self.copy_binary(Path(release["binary_path"]))
        write_json(self.active_file, release)
        agent = dict(self.plist)
        environment = dict(agent.get("EnvironmentVariables", {}))
        environment["CONFIG_FILE"] = str(self.config_path)
        agent["EnvironmentVariables"] = environment
        agent["ExitTimeOut"] = max(agent.get("ExitTimeOut", 0), self.grace)
        atomic_write(self.agent, plistlib.dumps(agent), 0o644)

    def copy_binary(self, source):
        self.binary.parent.mkdir(parents=True, exist_ok=True)
        atomic_write(self.binary, source.read_bytes(), 0o755)

    def restore(self, state):
        directory = Path(state["directory"]).resolve()
        if not directory.is_relative_to((self.private / "backups").resolve()):
            raise RuntimeError("rollback snapshot is outside this project's backup directory")
        self.copy_binary(directory / "twc-approval-go-bridge")
        if state["active"]:
            write_json(self.active_file, state["active"])
        elif self.active_file.exists():
            self.active_file.unlink()
        atomic_write(self.agent, (directory / "agent.plist").read_bytes(), 0o644)

    def start(self):
        self.run(["launchctl", "bootstrap", self.domain, str(self.agent)])
        self.run(["launchctl", "kickstart", self.target])

    def wait_ready(self, expected, timeout=60):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                actual = self.request("/version")
                if any(actual.get(key) != expected.get(key) for key in ("git_commit", "build_date")):
                    raise RuntimeError("running build metadata does not match the selected release")
                if actual.get("version") != expected.get("version"):
                    raise RuntimeError("running semantic version does not match the selected release")
                if self.request("/healthz").get("status") != "ok":
                    raise OSError("health check not ready")
                if self.request("/readyz").get("status") != "ready":
                    raise OSError("readiness check not ready")
                status = self.request("/api/status")
                feishu = self.config.get("feishu", {})
                expected_state = "ready" if feishu.get("app_id") and feishu.get("app_secret") else "disabled"
                if status.get("feishu_state") != expected_state:
                    raise OSError("Feishu connection not ready")
                pids = self.assert_owned_listener()
                if len(pids) != 1:
                    raise OSError("backend PID not ready")
                active = self.active()
                if active:
                    apps = self.apps(active)
                    if (len(apps) != 1 or apps[0]["pid"] != pids[0] or
                            not apps[0]["icon_present"] or
                            Path(apps[0]["bundle_path"]).resolve() != Path(active["app_path"]).resolve()):
                        raise OSError("application identity or icon not ready")
                return {"pid": pids[0], "version": actual, "feishu_state": status.get("feishu_state")}
            except (OSError, ValueError):
                time.sleep(0.5)
        raise RuntimeError("service did not pass health, version, connection, and icon checks within 60s")

    def switch(self, previous, apply, expected):
        # Refuse an unrelated port owner before entering a mutation/recovery path.
        self.assert_owned_listener()
        try:
            self.stop()
            apply()
            self.start()
            return self.wait_ready(expected)
        except Exception as error:
            print("切换失败，恢复已保存版本。", flush=True)
            try:
                self.stop()
                self.restore(previous)
                self.start()
                restored = self.wait_ready(previous["version"])
                print("已恢复:", json.dumps(restored, ensure_ascii=False), flush=True)
            except Exception as recovery:
                raise RuntimeError("automatic recovery failed; saved snapshot: " +
                                   previous["directory"] + "; " + str(recovery)) from error
            raise

    def update(self):
        with self.lock():
            self.assert_owned_listener()
            self.checks()
            release = self.build()
            previous = self.snapshot()
            print("回滚版本:", json.dumps(previous["version"], ensure_ascii=False), flush=True)
            result = self.switch(previous, lambda: self.install(release), release["version"])
            write_json(self.previous_file, {"directory": previous["directory"]})
            write_json(self.private / "last-operation.json", {
                "operation": "update", "verified_at": datetime.now(timezone.utc).isoformat(),
                "result": result, "rollback_version": previous["version"]})
            return result

    def rollback(self):
        with self.lock():
            reference = json.loads(self.previous_file.read_text())
            directory = Path(reference["directory"]).resolve()
            if not directory.is_relative_to((self.private / "backups").resolve()):
                raise RuntimeError("invalid rollback snapshot")
            previous = json.loads((directory / "snapshot.json").read_text())
            current = self.snapshot()
            result = self.switch(current, lambda: self.restore(previous), previous["version"])
            write_json(self.previous_file, {"directory": current["directory"]})
            write_json(self.private / "last-operation.json", {
                "operation": "rollback", "verified_at": datetime.now(timezone.utc).isoformat(),
                "result": result, "rollback_version": current.get("version")})
            return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["update", "status", "rollback", "build"])
    args = parser.parse_args()
    if sys.platform != "darwin":
        parser.error("this tool only manages the local macOS LaunchAgent")
    try:
        service = LocalService()
        if args.command == "build":
            with service.lock():
                result = service.build()
        else:
            result = getattr(service, args.command)()
        print(json.dumps(result, ensure_ascii=False, indent=2))
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        # Never dump the TOML, environment, HTTP headers, or a private response.
        print("本机服务操作失败:", str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
