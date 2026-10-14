#!/usr/bin/env python3
"""Manage committed Docker releases and migrate the existing Mac worker to Linux."""

import argparse
import base64
from contextlib import contextmanager
from datetime import datetime, timezone
import fcntl
import hashlib
import gzip
import importlib.util
import io
import json
import os
from pathlib import Path
import plistlib
import re
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import tomllib
import urllib.request

sys.dont_write_bytecode = True


DEFAULT_ROOT = "/home/dev/apps/twc-approval-go-bridge"
PROJECT = "twc-approval-bridge"
GO_IMAGE = "docker.m.daocloud.io/library/golang:1.24.13-alpine3.22@sha256:3641e0d9b931dc4f2f185dcd669c4679670e9277c8166a838ddb98a2d4389cb5"
RUNTIME_IMAGE = "gcr.m.daocloud.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab"
FRPC_IMAGE = "docker.m.daocloud.io/snowdreamtech/frpc@sha256:35142a90724b86f693eb38127f5a6d794e1fc5b2847c5b8497f7800889e15caf"


def atomic(path, data, mode=0o600):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=".twc-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            os.fchmod(stream.fileno(), mode)
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def write_json(path, value):
    atomic(path, (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode())


def replace_string(text, section, key, value):
    """Replace one string value while retaining existing comments and other keys."""
    lines = text.splitlines(keepends=True)
    current = ""
    matches = 0
    pattern = re.compile(r'^(\s*' + re.escape(key) + r'\s*=\s*)(?:"(?:\\.|[^"\\])*"|\x27[^\x27]*\x27)(\s*(?:#.*)?)$')
    for index, line in enumerate(lines):
        header = re.match(r"^\s*\[([^]]+)\]", line)
        if header:
            current = header.group(1)
        if current == section:
            match = pattern.match(line.rstrip("\r\n"))
            if match:
                matches += 1
                lines[index] = match.group(1) + json.dumps(value, ensure_ascii=False) + match.group(2) + "\n"
    if matches != 1:
        raise RuntimeError(f"expected one existing TOML string for {section or 'root'}.{key}")
    result = "".join(lines)
    tomllib.loads(result)
    return result


def archive_files(files):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        for name, data in files.items():
            path = Path(name)
            if path.is_absolute() or ".." in path.parts:
                raise RuntimeError("unsafe transfer path")
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = 0o600
            archive.addfile(info, io.BytesIO(data))
    return output.getvalue()


def hashes(directory):
    result = {}
    for path in sorted(Path(directory).rglob("*")):
        if path.is_symlink():
            raise RuntimeError("state transfer refuses symlinks")
        if path.is_file():
            result[str(path.relative_to(directory))] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def file_sha(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def image_contents(path):
    """Validate a single-platform OCI Docker-save archive without extracting it."""
    with tarfile.open(path) as archive:
        members = archive.getmembers()
        names = [item.name for item in members]
        if len(names) != len(set(names)) or any(
                Path(item.name).is_absolute() or ".." in Path(item.name).parts or
                not (item.isfile() or item.isdir()) for item in members):
            raise RuntimeError("unsafe or duplicate image archive member")
        def read(name, limit=16 * 1024 * 1024):
            item = archive.getmember(name)
            if not item.isfile() or item.size > limit:
                raise RuntimeError("invalid image metadata size")
            return archive.extractfile(item).read()
        entries = json.loads(read("manifest.json"))
        if len(entries) != 1 or len(entries[0].get("RepoTags", [])) != 1:
            raise RuntimeError("expected one tagged platform image")
        entry = entries[0]
        config_raw = read(entry["Config"])
        config = json.loads(config_raw)
        config_digest = "sha256:" + hashlib.sha256(config_raw).hexdigest()
        manifests = []
        for item in members:
            if item.isfile() and item.name.startswith("blobs/sha256/") and item.size <= 65536:
                try:
                    value = json.loads(read(item.name))
                except (ValueError, UnicodeDecodeError):
                    continue
                if isinstance(value, dict) and value.get("config", {}).get("digest") == config_digest and "layers" in value:
                    manifests.append((item.name, value))
        if len(manifests) != 1:
            raise RuntimeError("expected one OCI platform manifest")
        name, manifest = manifests[0]
        digest = "sha256:" + hashlib.sha256(read(name)).hexdigest()
        if name != "blobs/" + digest.replace(":", "/"):
            raise RuntimeError("platform manifest digest mismatch")
        layers, diffs = manifest["layers"], config["rootfs"]["diff_ids"]
        if len(layers) != len(diffs) or len(layers) != len(entry["Layers"]):
            raise RuntimeError("image layer list mismatch")
        for layer, diff, layer_name in zip(layers, diffs, entry["Layers"], strict=True):
            if layer_name != "blobs/" + layer["digest"].replace(":", "/"):
                raise RuntimeError("layer descriptor mismatch")
            raw = archive.extractfile(layer_name)
            if hashlib.file_digest(raw, "sha256").hexdigest() != layer["digest"].split(":", 1)[1]:
                raise RuntimeError("compressed layer digest mismatch")
            raw = archive.extractfile(layer_name)
            stream = gzip.GzipFile(fileobj=raw) if layer["mediaType"].endswith("+gzip") else raw
            if hashlib.file_digest(stream, "sha256").hexdigest() != diff.split(":", 1)[1]:
                raise RuntimeError("uncompressed layer digest mismatch")
        return {"platform": config["os"] + "/" + config["architecture"],
                "platform_manifest_digest": digest, "config_digest": config_digest,
                "diff_ids": diffs, "runtime_config": config["config"], "tag": entry["RepoTags"][0]}


def validate_artifact(directory):
    directory = Path(directory)
    manifest = json.loads((directory / "artifact.json").read_text())
    if manifest.get("schema") != 1 or not re.fullmatch(r"[a-f0-9]{40}", manifest.get("commit", "")):
        raise RuntimeError("invalid artifact release manifest")
    for name, key in [("image.tar", "archive_sha256"), ("deployment.tar.gz", "deployment_sha256")]:
        if file_sha(directory / name) != manifest.get(key):
            raise RuntimeError(name + " transport checksum mismatch")
    contents = image_contents(directory / "image.tar")
    if any(contents[key] != manifest.get(key) for key in contents):
        raise RuntimeError("image contents differ from release manifest")
    if (manifest["version"].get("git_commit") != manifest["commit"][:12] or
            manifest["version"].get("platform") != manifest["platform"] or
            not manifest["tag"].startswith("twc-approval-go-bridge:artifact-")):
        raise RuntimeError("artifact version or platform mismatch")
    with tarfile.open(directory / "deployment.tar.gz") as archive:
        members = archive.getmembers()
        if sorted(item.name for item in members) != ["compose.yml", "manager.py"] or any(
                not item.isfile() or item.size > 1024 * 1024 for item in members):
            raise RuntimeError("invalid minimal deployment bundle")
    return manifest


class Host:
    def __init__(self, root, commit=None, initial=False):
        self.root = Path(root).resolve()
        if self.root.name != "twc-approval-go-bridge" or len(self.root.parts) < 4 or os.getuid() == 0:
            raise RuntimeError("use an owned project directory and an unprivileged deployment user")
        if commit and not re.fullmatch(r"[a-f0-9]{40}", commit):
            raise RuntimeError("a complete committed revision is required")
        self.commit = commit
        self.initial = initial
        self.operations = self.root / "operations"
        self.active_path = self.operations / "active.json"
        self.previous_path = self.operations / "previous.json"
        self.operations.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.operations.chmod(0o700)
        config_path = self.root / "configs/config.toml"
        self.config = tomllib.loads(config_path.read_text()) if config_path.exists() else {}
        self.password = self.config.get("runtime", {}).get("dashboard_password", "")

    def run(self, args, capture=True, check=True, **kwargs):
        return subprocess.run(args, cwd=self.root, text=True, capture_output=capture, check=check, **kwargs)

    @contextmanager
    def lock(self):
        with (self.operations / "operation.lock").open("a") as lock:
            os.chmod(lock.name, 0o600)
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RuntimeError("another remote deployment operation is running") from None
            yield

    def active(self):
        return json.loads(self.active_path.read_text()) if self.active_path.exists() else None

    def release(self, commit):
        path = self.root / "releases" / commit / "release.json"
        release = json.loads(path.read_text())
        if (release["commit"] != commit or not release["image"].startswith("sha256:") or
                not release.get("tag", "").startswith("twc-approval-go-bridge:")):
            raise RuntimeError("invalid release metadata")
        return release

    def compose(self, release, args, capture=True, check=True, public=True):
        if not re.fullmatch(r"[a-f0-9]{40}", release["commit"]):
            raise RuntimeError("invalid Compose release reference")
        source = self.root / "releases" / release["commit"] / "source"
        compose_path = self.root / "releases" / release["commit"] / "bundle/compose.yml" if release.get("artifact") else source / "deploy/docker-compose.wsl.yml"
        env = dict(os.environ, TWC_IMAGE=release["tag"], TWC_ROOT=str(self.root),
                   TWC_UID=str(os.getuid()), TWC_GID=str(os.getgid()))
        command = ["docker", "compose", "--env-file", "/dev/null", "-p", PROJECT,
                   "-f", str(compose_path)]
        env.pop("COMPOSE_PROFILES", None)
        ingress_path = self.operations / "ingress.json"
        ingress = json.loads(ingress_path.read_text()) if ingress_path.exists() else {}
        if ingress.get("mode") in {"shared-frpc", "gateway"}:
            # Host ingress policy survives application image rollback.
            command += ["-f", str(self.operations / "shared-ingress.compose.yml")]
        elif public:
            command += ["--profile", "public-frp"]
        return self.run(command + args, capture=capture, check=check, env=env)

    def json_request(self, url):
        request = urllib.request.Request(url)
        if self.password and url.endswith("/api/status"):
            token = base64.b64encode(("admin:" + self.password).encode()).decode()
            request.add_header("Authorization", "Basic " + token)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(request, timeout=4) as response:
            return json.load(response)

    def local_url(self, release):
        address = self.compose(release, ["port", "server", "8080"]).stdout.strip()
        if not re.fullmatch(r"127\.0\.0\.1:[0-9]+", address):
            raise RuntimeError("service port must be published only on Linux localhost")
        return "http://" + address

    def ready(self, release, public=True, timeout=75):
        deadline = time.monotonic() + timeout
        reason = "not started"
        while time.monotonic() < deadline:
            try:
                base = self.local_url(release)
                actual = self.json_request(base + "/version")
                if any(actual.get(key) != release["version"].get(key) for key in ("version", "git_commit", "build_date")):
                    raise RuntimeError("running build does not match the immutable selected image")
                if self.json_request(base + "/healthz").get("status") != "ok":
                    raise OSError("health not ready")
                if self.json_request(base + "/readyz").get("status") != "ready":
                    raise OSError("readiness not ready")
                status = self.json_request(base + "/api/status")
                feishu = self.config.get("feishu", {})
                expected = "ready" if feishu.get("app_id") and feishu.get("app_secret") else "disabled"
                if status.get("feishu_state") != expected:
                    raise OSError("Feishu connection not ready")
                container = self.compose(release, ["ps", "-q", "server"]).stdout.split()
                if len(container) != 1:
                    raise OSError("expected exactly one server container")
                image = self.run(["docker", "inspect", "--format", "{{.Image}}", container[0]]).stdout.strip()
                if image != release["image"]:
                    raise RuntimeError("container image differs from the selected immutable image")
                if public:
                    remote = self.json_request(self.config["runtime"]["public_base_url"].rstrip("/") + "/version")
                    if any(remote.get(key) != actual.get(key) for key in ("git_commit", "build_date")):
                        raise OSError("public ingress still points to another build")
                return {"version": actual, "feishu_state": expected, "localhost_url": base,
                        "container": container[0], "public_verified": public}
            except (OSError, ValueError, subprocess.CalledProcessError) as error:
                reason = type(error).__name__
                time.sleep(1)
        raise RuntimeError("remote health/connection/ingress checks timed out: " + reason)

    def build_image(self):
        directory = self.root / "releases" / self.commit
        source = directory / "source"
        if (directory / "release.json").exists():
            release = self.release(self.commit)
            existing = self.run(["docker", "image", "inspect", "--format", "{{.Id}}", release["tag"]]).stdout.strip()
            if existing != release["image"]:
                raise RuntimeError("the immutable release tag no longer identifies its recorded image")
            return release
        version = re.search(r'Version\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"',
                            (source / "internal/version/version.go").read_text()).group(1)
        architecture = self.run(["docker", "info", "--format", "{{.Architecture}}"]).stdout.strip()
        architecture = {"x86_64": "amd64", "aarch64": "arm64", "amd64": "amd64", "arm64": "arm64"}.get(architecture)
        if not architecture:
            raise RuntimeError("unsupported remote build architecture")
        tag = f"twc-approval-go-bridge:{version}-{self.commit[:12]}"
        date = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        command = ["docker", "build", "--network", "host", "--file", str(source / "deploy/Dockerfile"), "--tag", tag]
        for key, value in {"GO_IMAGE": GO_IMAGE, "RUNTIME_IMAGE": RUNTIME_IMAGE,
                           "GOPROXY": "https://goproxy.cn", "VERSION": version,
                           "TARGETARCH": architecture,
                           "GIT_COMMIT": self.commit[:12], "BUILD_DATE": date}.items():
            command += ["--build-arg", key + "=" + value]
        self.run(command + [str(source)], capture=False)
        image = self.run(["docker", "image", "inspect", "--format", "{{.Id}}", tag]).stdout.strip()
        built_version = json.loads(self.run(["docker", "run", "--rm", image, "version", "-json"]).stdout)
        if built_version.get("git_commit") != self.commit[:12] or built_version.get("platform") != "linux/" + architecture:
            raise RuntimeError("candidate build metadata or platform differs from this host")
        release = {"commit": self.commit, "image": image, "tag": tag, "version": built_version}
        write_json(directory / "release.json", release)
        return release

    def build(self):
        """Builder-only: no runtime credentials, ingress or active worker required."""
        with self.lock():
            directory = self.root / "artifacts" / self.commit
            if (directory / "artifact.json").exists():
                return validate_artifact(directory)
            release = self.build_image()
            self.smoke(release)
            directory.mkdir(parents=True, exist_ok=True, mode=0o700)
            tag = "twc-approval-go-bridge:artifact-" + self.commit[:12] + "-" + release["image"].split(":", 1)[1][:12]
            self.run(["docker", "tag", release["tag"], tag])
            image_path = directory / "image.tar"
            with image_path.open("wb") as output:
                os.chmod(image_path, 0o600)
                self.run(["docker", "image", "save", "--platform", release["version"]["platform"], tag],
                         stdout=output, capture=False)
            contents = image_contents(image_path)
            source = self.root / "releases" / self.commit / "source"
            atomic(directory / "deployment.tar.gz", archive_files({
                "manager.py": (source / "scripts/remote-service.py").read_bytes(),
                "compose.yml": (source / "deploy/docker-compose.wsl.yml").read_bytes()}))
            manifest = dict(contents, schema=1, commit=self.commit, version=release["version"],
                            archive_sha256=file_sha(image_path), archive_bytes=image_path.stat().st_size,
                            deployment_sha256=file_sha(directory / "deployment.tar.gz"),
                            builder_image=release["image"])
            write_json(directory / "artifact.json", manifest)
            return validate_artifact(directory)

    def import_artifact(self, directory, expected_sha):
        """Runtime-only: verify and load finished bytes; never compile sources."""
        with self.lock():
            directory = Path(directory).resolve()
            if not directory.is_relative_to(self.operations) or file_sha(directory / "artifact.json") != expected_sha:
                raise RuntimeError("artifact manifest differs from the trusted SSH sender")
            manifest = validate_artifact(directory)
            if manifest["commit"] != self.commit or not self.config:
                raise RuntimeError("target commit or private runtime configuration missing")
            architecture = self.run(["docker", "info", "--format", "{{.Architecture}}"]).stdout.strip()
            architecture = {"x86_64": "amd64", "aarch64": "arm64"}.get(architecture, architecture)
            if manifest["platform"] != "linux/" + architecture:
                raise RuntimeError("artifact platform differs from the runtime host")
            protected = [self.active()]
            if self.previous_path.exists():
                protected.append(json.loads(self.previous_path.read_text()))
            for existing in protected:
                if (existing and existing["tag"] == manifest["tag"] and
                        existing["image"] not in {manifest["platform_manifest_digest"], manifest["config_digest"]}):
                    raise RuntimeError("artifact tag conflicts with a retained release; current worker retained")
            self.run(["docker", "load", "--input", str(directory / "image.tar")], capture=False)
            inspected = json.loads(self.run(["docker", "image", "inspect", manifest["tag"]]).stdout)[0]
            if (inspected["Id"] not in {manifest["platform_manifest_digest"], manifest["config_digest"]} or
                    inspected["RootFS"]["Layers"] != manifest["diff_ids"] or
                    any(inspected["Config"].get(key) != value for key, value in manifest["runtime_config"].items())):
                raise RuntimeError("imported image/configuration differs from the verified artifact")
            version = json.loads(self.run(["docker", "run", "--rm", inspected["Id"], "version", "-json"]).stdout)
            if version != manifest["version"]:
                raise RuntimeError("imported binary version differs from artifact")
            release = {"commit": self.commit, "image": inspected["Id"], "tag": manifest["tag"],
                       "version": version, "artifact": expected_sha,
                       "platform_manifest_digest": manifest["platform_manifest_digest"]}
            bundle = self.root / "releases" / self.commit / "bundle"
            bundle.mkdir(parents=True, exist_ok=True, mode=0o700)
            with tarfile.open(directory / "deployment.tar.gz") as archive:
                for item in archive:
                    atomic(bundle / item.name, archive.extractfile(item).read())
            self.candidate_checks(release)
            write_json(bundle.parent / "release.json", release)
            write_json(bundle.parent / "artifact.json", manifest)
            return release

    def prepare(self):
        """Legacy first Mac handoff; normal deployments import an artifact."""
        with self.lock():
            release = self.build_image()
            self.candidate_checks(release)
            return release

    def candidate_checks(self, release):
        directory = self.root / "releases" / release["commit"]
        self.compose(release, ["config", "--quiet"])
        self.smoke(release)
        self.verify_frpc(release)
        check_state = self.operations / "check-state"
        check_state.mkdir(exist_ok=True, mode=0o700)
        check = self.run(["docker", "run", "--rm", "--user", f"{os.getuid()}:{os.getgid()}",
                          "-v", str(self.root / "configs") + ":/configs:ro",
                          "-v", str(check_state) + ":/data", release["image"], "check-business-config"], check=False)
        atomic(directory / "business-check.stdout", check.stdout.encode())
        atomic(directory / "business-check.stderr", check.stderr.encode())
        if check.returncode:
            raise RuntimeError("application-identity business configuration check failed; inspect private release diagnostics")
        print("candidate_checks=passed", flush=True)

    def verify_frpc(self, release):
        ingress_path = self.operations / "ingress.json"
        if ingress_path.exists() and json.loads(ingress_path.read_text()).get("mode") == "shared-frpc":
            running = self.run(["docker", "inspect", "--format", "{{.State.Running}}", "frpc-wsl-gateway"]).stdout.strip()
            if running != "true":
                raise RuntimeError("shared WSL FRP gateway is not running")
            return
        if ingress_path.exists() and json.loads(ingress_path.read_text()).get("mode") == "gateway":
            return
        path = self.root / "configs/frpc.toml"
        config = tomllib.loads(path.read_text())
        host = self.config["runtime"]["public_base_url"].split("//", 1)[-1].split("/", 1)[0]
        proxies = config.get("proxies", [])
        if (len(proxies) != 1 or proxies[0].get("name") != "twc-approval-wsl-web" or
                proxies[0].get("customDomains") != [host] or proxies[0].get("localIP") != "server" or
                proxies[0].get("localPort") != 8080):
            raise RuntimeError("private FRP config must route only this domain to the Compose server")
        self.run(["docker", "run", "--rm", "--user", f"{os.getuid()}:{os.getgid()}",
                  "--entrypoint", "/usr/bin/frpc", "-v", str(path) + ":/etc/frp/frpc.toml:ro",
                  FRPC_IMAGE, "verify", "-c", "/etc/frp/frpc.toml"], capture=False)

    def smoke(self, release):
        directory = self.operations / ("smoke-" + release["commit"][:12])
        directory.mkdir(exist_ok=True, mode=0o700)
        (directory / "data").mkdir(exist_ok=True, mode=0o700)
        tables = {}
        for role, source, access, fields in [
            ("transactions", "webhook", "read_only", {"transaction_id": "fld_tid"}),
            ("reimbursement_details", "employee", "read_write", {"attachment": "fld_att"}),
            ("invoice_ledger", "bridge", "read_write", {"source_key": "fld_sk", "raw_json": "fld_raw"})]:
            tables[role] = {"name": role, "source": source, "access": access,
                            "base_token": "offline", "table_id": "test-" + role, "fields": fields}
        write_json(directory / "tables.json", {"version": 1, "name": "offline-smoke", "tables": tables})
        config = ('tables_file="/configs/tables.json"\n[recognition]\nprovider="disabled"\ntrigger_mode="both"\n'
                  '[review]\nprovider="seal"\ntrigger_mode="manual"\n[runtime]\nhttp_addr=":8080"\nstate_dir="/data"\n')
        atomic(directory / "config.toml", config.encode())
        name = "twc-offline-smoke-" + release["commit"][:12]
        self.run(["docker", "run", "--detach", "--name", name, "--user", f"{os.getuid()}:{os.getgid()}",
                  "-p", "127.0.0.1::8080", "-v", str(directory) + ":/configs:ro",
                  "-v", str(directory / "data") + ":/data", release["image"]])
        try:
            address = self.run(["docker", "port", name, "8080"]).stdout.strip()
            host, separator, port = address.rpartition(":")
            if host != "127.0.0.1" or not separator or not port.isdecimal() or not 1 <= int(port) <= 65535:
                raise RuntimeError("offline smoke server port is not restricted to localhost")
            for _ in range(20):
                try:
                    status = self.json_request("http://" + address + "/api/status")
                    if status.get("feishu_state") != "disabled":
                        raise RuntimeError("isolated smoke test unexpectedly enabled Feishu")
                    actual = self.json_request("http://" + address + "/version")
                    if actual != release["version"]:
                        raise RuntimeError("isolated smoke test served a different build")
                    return
                except OSError:
                    time.sleep(0.25)
            raise RuntimeError("isolated Linux candidate did not become healthy")
        finally:
            self.run(["docker", "stop", "--time", "30", name], capture=False)
            self.run(["docker", "rm", name], capture=False)

    def switch(self, selected):
        previous = self.active()
        for release in [selected] + ([previous] if previous else []):
            actual = self.run(["docker", "image", "inspect", "--format", "{{.Id}}", release["tag"]]).stdout.strip()
            if actual != release["image"]:
                raise RuntimeError("release tag changed or rollback image is unavailable; current worker retained")
        if previous and previous["image"] == selected["image"] and previous["commit"] == selected["commit"]:
            return self.ready(previous)
        paths = [self.active_path, self.previous_path, self.root / "manager.py", self.operations / "last-operation.json"]
        saved = {path: path.read_bytes() if path.exists() else None for path in paths}
        try:
            if previous:
                self.compose(previous, ["down"], capture=False)
            self.compose(selected, ["up", "--detach", "--pull", "never"], capture=False)
            result = self.ready(selected)
            if previous:
                write_json(self.previous_path, previous)
            write_json(self.active_path, selected)
            # Keep the executing manager when rolling back older application images.
            atomic(self.root / "manager.py", Path(__file__).read_bytes(), 0o700)
            write_json(self.operations / "last-operation.json", {
                "verified_at": datetime.now(timezone.utc).isoformat(), "result": result,
                "image": selected["image"], "rollback_commit": previous["commit"] if previous else None})
            return result
        except Exception as failure:
            self.compose(selected, ["down"], capture=False, check=False)
            if previous:
                self.compose(previous, ["up", "--detach", "--pull", "never"], capture=False)
                self.ready(previous)
            for path, contents in saved.items():
                if contents is None:
                    path.unlink(missing_ok=True)
                else:
                    atomic(path, contents, 0o700 if path.name == "manager.py" else 0o600)
            if previous:
                print("previous_remote_release_restored=true", flush=True)
            raise failure

    def activate(self):
        with self.lock():
            if not self.active() and not self.initial:
                raise RuntimeError("first deployment requires the explicit stopped-Mac migration workflow")
            (self.root / "data").mkdir(exist_ok=True, mode=0o700)
            return self.switch(self.release(self.commit))

    def rollback(self):
        with self.lock():
            if not self.previous_path.exists():
                raise RuntimeError("there is no previous verified remote release")
            return self.switch(json.loads(self.previous_path.read_text()))

    def stop(self):
        with self.lock():
            active = self.active() or (self.release(self.commit) if self.commit else None)
            if active:
                self.compose(active, ["down"], capture=False)
            if getattr(self, "initial", False) and not self.previous_path.exists() and self.active_path.exists():
                self.active_path.unlink()
            return {"stopped": True}

    def status(self):
        active = self.active()
        if not active:
            return {"deployed": False}
        result = self.ready(active, timeout=5)
        result.update({"image": active["image"], "commit": active["commit"],
                       "rollback_available": self.previous_path.exists(), "root": str(self.root)})
        return result


class Client:
    def __init__(self, host, remote_root):
        self.root = Path(__file__).resolve().parent.parent
        if not re.fullmatch(r"[A-Za-z0-9_.@-]+", host) or host.startswith("-"):
            raise RuntimeError("invalid SSH host alias")
        self.host, self.remote_root = host, remote_root
        if Path(remote_root).name != "twc-approval-go-bridge" or not Path(remote_root).is_absolute():
            raise RuntimeError("use an absolute dedicated twc-approval-go-bridge directory")

    def run(self, args, **kwargs):
        return subprocess.run(args, cwd=self.root, check=True, **kwargs)

    def ssh(self, command, data=None, capture=False):
        return self.run(["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", self.host, command],
                        input=data, capture_output=capture)

    def clean_commit(self):
        if self.run(["git", "status", "--porcelain"], capture_output=True, text=True).stdout:
            raise RuntimeError("commit and verify all changes before a remote deployment")
        return self.run(["git", "rev-parse", "HEAD"], capture_output=True, text=True).stdout.strip()

    def checks(self):
        for command in ([sys.executable, "-B", "-m", "unittest", "discover", "-s", "scripts", "-p", "test_*.py"],
                        ["go", "test", "./..."], ["go", "test", "-tags", "no_anthropic", "./..."], ["go", "vet", "./..."]):
            self.run(command)

    def upload(self, destination, files):
        command = "umask 077; mkdir -p " + shlex.quote(destination) + "; tar --no-same-owner -xzf - -C " + shlex.quote(destination)
        self.ssh(command, archive_files(files))

    def source(self, commit):
        data = self.run(["git", "archive", "--format=tar.gz", commit], capture_output=True).stdout
        destination = self.remote_root + "/releases/" + commit + "/source"
        self.ssh("umask 077; mkdir -p " + shlex.quote(destination) + "; tar --no-same-owner -xzf - -C " + shlex.quote(destination), data)

    def agent(self, command, commit=None, manager=False, capture=False, initial=False):
        script = self.remote_root + "/manager.py" if manager else self.remote_root + "/releases/" + commit + "/source/scripts/remote-service.py"
        args = ["python3", "-B", script, "--on-host", "--remote-root", self.remote_root, command]
        if commit:
            args += ["--commit", commit]
        if initial:
            args += ["--initial-migration"]
        return self.ssh(shlex.join(args), capture=capture)

    def configuration_files(self):
        config_path = Path(os.environ.get("CONFIG_FILE", self.root / "configs/config.toml")).resolve()
        if not config_path.is_relative_to(self.root):
            raise RuntimeError("migration configuration must belong to this checkout")
        raw = config_path.read_text()
        config = tomllib.loads(raw)
        files = {}
        for section, key, remote_directory in [("", "tables_file", "tables"), ("review", "rules_file", "rules")]:
            reference = config.get(key) if not section else config.get(section, {}).get(key)
            if not reference:
                continue
            path = Path(reference)
            if not path.is_absolute():
                path = self.root / path if (self.root / path).exists() else config_path.parent / path
            path = path.resolve()
            if not path.is_relative_to(self.root):
                raise RuntimeError("referenced configuration is outside this checkout")
            target = remote_directory + "/" + path.name
            files[target] = path.read_bytes()
            raw = replace_string(raw, section, key, "/configs/" + target)
        raw = replace_string(raw, "runtime", "http_addr", ":8080")
        raw = replace_string(raw, "runtime", "state_dir", "/data")
        files["config.toml"] = raw.encode()
        frpc = (self.root / "data/public-debug/frpc.toml").read_text()
        parsed = tomllib.loads(frpc)
        proxies = parsed.get("proxies", [])
        expected_domain = config["runtime"]["public_base_url"].split("//", 1)[-1].split("/", 1)[0]
        if len(proxies) != 1 or proxies[0].get("customDomains") != [expected_domain]:
            raise RuntimeError("Mac FRP configuration is not exclusively owned by this service")
        frpc = replace_string(frpc, "[proxies", "name", "twc-approval-wsl-web")
        frpc = replace_string(frpc, "[proxies", "localIP", "server")
        frpc, count = re.subn(r"(?m)^(localPort\s*=\s*)[0-9]+(\s*(?:#.*)?)$", r"\g<1>8080\2", frpc)
        if count != 1:
            raise RuntimeError("expected one Mac FRP target port")
        tomllib.loads(frpc)
        files["frpc.toml"] = frpc.encode()
        state = Path(config["runtime"]["state_dir"])
        if not state.is_absolute():
            state = self.root / state
        state = state.resolve()
        if not state.is_relative_to(self.root):
            raise RuntimeError("migration state directory must belong to this checkout")
        return files, state

    def download(self, remote_path, local_path):
        local_path = Path(local_path)
        with local_path.open("wb") as output:
            os.chmod(local_path, 0o600)
            self.run(["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", self.host,
                      "cat " + shlex.quote(remote_path)], stdout=output)

    def build(self):
        commit = self.clean_commit()
        self.source(commit)
        self.agent("build", commit)
        directory = self.root / "data/remote-releases" / commit
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        for name in ["artifact.json", "image.tar", "deployment.tar.gz"]:
            self.download(self.remote_root + "/artifacts/" + commit + "/" + name, directory / name)
        validate_artifact(directory)
        write_json(directory.parent / "latest.json", {"path": str(directory)})
        print("local_artifact=" + str(directory), flush=True)
        return directory

    def deploy(self, directory, initial=False):
        directory = Path(directory).resolve()
        manifest = validate_artifact(directory)
        commit = manifest["commit"]
        trusted_sha = file_sha(directory / "artifact.json")
        incoming = self.remote_root + "/operations/incoming/" + trusted_sha
        self.upload(incoming, {name: (directory / name).read_bytes() for name in
                              ["artifact.json", "image.tar", "deployment.tar.gz"]})
        # Verify the minimal executable bundle before expanding or executing it.
        bootstrap = ("import pathlib,json,hashlib,tarfile; p=pathlib.Path(" + repr(incoming) + "); "
                     "assert hashlib.sha256((p/'artifact.json').read_bytes()).hexdigest()==" + repr(trusted_sha) + "; "
                     "m=json.loads((p/'artifact.json').read_text()); "
                     "assert hashlib.sha256((p/'deployment.tar.gz').read_bytes()).hexdigest()==m['deployment_sha256']; "
                     "a=tarfile.open(p/'deployment.tar.gz'); items=a.getmembers(); "
                     "assert sorted(x.name for x in items)==['compose.yml','manager.py']; "
                     "assert all(x.isfile() and x.size<=1048576 for x in items); "
                     "[(p/x.name).write_bytes(a.extractfile(x).read()) for x in items]")
        self.ssh(shlex.join(["python3", "-c", bootstrap]))
        script = incoming + "/manager.py"
        base = ["python3", "-B", script, "--on-host", "--remote-root", self.remote_root, "--commit", commit]
        self.ssh(shlex.join(base + ["import", "--artifact", incoming, "--manifest-sha", trusted_sha]))
        self.ssh(shlex.join(base + ["activate"] + (["--initial-migration"] if initial else [])))

    def update(self, builder=None):
        # Validate before any upload so an uncommitted checkout never publishes.
        self.clean_commit()
        self.deploy((builder or self).build())

    def migrate(self):
        commit = self.clean_commit()
        self.ssh("test ! -f " + shlex.quote(self.remote_root + "/operations/active.json"))
        self.checks()
        files, state = self.configuration_files()
        self.source(commit)
        self.upload(self.remote_root + "/configs", files)
        # No business worker or public proxy is started during preparation.
        self.agent("prepare", commit)
        spec = importlib.util.spec_from_file_location("mac_local", self.root / "scripts/local-service.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        mac = module.LocalService(self.root)
        frp_label = "com.yingqing.twc-approval-frpc"
        frp_target = f"gui/{os.getuid()}/{frp_label}"
        frp_agent = Path.home() / "Library/LaunchAgents" / (frp_label + ".plist")
        frp = plistlib.loads(frp_agent.read_bytes())
        if frp.get("Label") != frp_label or str(self.root / "data/public-debug/frpc.toml") not in frp.get("ProgramArguments", []):
            raise RuntimeError("Mac FRP LaunchAgent is not owned by this checkout")
        private = self.root / "data/remote-migration"
        private.mkdir(exist_ok=True, mode=0o700)
        stopped = False
        try:
            mac.stop()
            stopped = True
            self.run(["launchctl", "bootout", frp_target])
            contents = {name: (state / name).read_bytes() for name in hashes(state)}
            write_json(private / "state-manifest.json", hashes(state))
            atomic(private / "mac-state.tar.gz", archive_files(contents))
            self.upload(self.remote_root + "/data", contents)
            verifier = "import json,pathlib,hashlib; p=pathlib.Path(" + repr(self.remote_root + "/data") + "); print(json.dumps({str(x.relative_to(p)):hashlib.sha256(x.read_bytes()).hexdigest() for x in p.rglob('*') if x.is_file()}))"
            received = self.ssh(shlex.join(["python3", "-c", verifier]), capture=True)
            if json.loads(received.stdout) != hashes(state):
                raise RuntimeError("transferred business state hashes do not match the stopped Mac state")
            self.agent("activate", commit, initial=True)
            # Persistently disable only these two dedicated Mac jobs after success.
            self.run(["launchctl", "disable", mac.target])
            self.run(["launchctl", "disable", frp_target])
            write_json(private / "migration.json", {"host": self.host, "root": self.remote_root,
                       "commit": commit, "state_files": len(contents), "mac_services_disabled": True})
        except Exception:
            if stopped:
                # Halt Linux before pulling its latest state back to the Mac.
                self.agent("stop", commit, initial=True)
                remote = self.ssh("tar -czf - -C " + shlex.quote(self.remote_root + "/data") + " .", capture=True)
                with tarfile.open(fileobj=io.BytesIO(remote.stdout), mode="r:gz") as archive:
                    archive.extractall(state, filter="data")
                self.run(["launchctl", "enable", mac.target])
                self.run(["launchctl", "enable", frp_target])
                mac.start()
                self.run(["launchctl", "bootstrap", f"gui/{os.getuid()}", str(frp_agent)])
                mac.wait_ready(mac.binary_version(mac.binary))
            raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["migrate", "build", "deploy", "update", "status", "rollback", "prepare", "import", "activate", "stop"])
    parser.add_argument("--host", default="wsl-dev", help="runtime SSH host")
    parser.add_argument("--remote-root", default=DEFAULT_ROOT)
    parser.add_argument("--build-host", default="wsl-dev")
    parser.add_argument("--build-root", default="/home/dev/apps/twc-builder/twc-approval-go-bridge")
    parser.add_argument("--artifact", help="finished local artifact directory (or host incoming directory)")
    parser.add_argument("--manifest-sha")
    parser.add_argument("--initial-deploy", action="store_true", help="explicit first worker start after external handoff")
    parser.add_argument("--on-host", action="store_true")
    parser.add_argument("--commit")
    parser.add_argument("--initial-migration", action="store_true")
    args = parser.parse_args()
    try:
        if args.on_host:
            if args.command not in {"build", "import", "prepare", "activate", "status", "rollback", "stop"}:
                raise RuntimeError("invalid host operation")
            host = Host(args.remote_root, args.commit, args.initial_migration)
            result = host.import_artifact(args.artifact, args.manifest_sha) if args.command == "import" else getattr(host, args.command)()
            if args.command == "build":
                result = {key: result[key] for key in ("commit", "platform", "tag", "archive_bytes", "archive_sha256", "platform_manifest_digest")}
            print(json.dumps(result, ensure_ascii=False, indent=2))
        else:
            client = Client(args.host, args.remote_root)
            if args.command in {"build", "update"}:
                builder = Client(args.build_host, args.build_root)
                if args.command == "build":
                    builder.build()
                else:
                    client.update(builder)
            elif args.command == "deploy":
                path = args.artifact or json.loads((client.root / "data/remote-releases/latest.json").read_text())["path"]
                client.deploy(path, args.initial_deploy)
            elif args.command == "migrate":
                client.migrate()
            elif args.command in {"status", "rollback"}:
                client.agent(args.command, manager=True)
            else:
                raise RuntimeError("prepare/import/activate/stop are host-only operations")
    except (OSError, ValueError, KeyError, RuntimeError, tarfile.TarError, subprocess.CalledProcessError) as error:
        print("remote service operation failed: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
