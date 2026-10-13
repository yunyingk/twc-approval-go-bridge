"""Exercise failure and rollback boundaries without touching a real LaunchAgent."""

from contextlib import redirect_stdout
import importlib.util
import io
import json
import os
from pathlib import Path
import plistlib
import tempfile
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location("local_service", Path(__file__).with_name("local-service.py"))
local = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local)


class LocalServiceTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        (self.root / "configs").mkdir()
        (self.root / "configs/config.toml").write_text(
            '[runtime]\nhttp_addr="127.0.0.1:18088"\nshutdown_timeout="10s"\n')
        self.agent = self.root / "agent.plist"
        self.agent.write_bytes(plistlib.dumps({
            "Label": local.LABEL,
            "WorkingDirectory": str(self.root),
            "ProgramArguments": ["python3", str(self.root / "deploy/run-local-debug.py")],
        }))
        with patch.dict(os.environ, {}, clear=True):
            self.service = local.LocalService(self.root, self.agent)

    def test_duration_and_shutdown_grace(self):
        self.assertEqual(local.duration_seconds("1m500ms"), 60.5)
        self.assertEqual(local.duration_seconds("0"), 0)
        self.assertEqual(self.service.grace, 20)
        with self.assertRaises(ValueError):
            local.duration_seconds("10s ignored")

    def test_foreign_launchagent_is_rejected(self):
        value = plistlib.loads(self.agent.read_bytes())
        value["WorkingDirectory"] = str(self.root / "some-other-checkout")
        self.agent.write_bytes(plistlib.dumps(value))
        with self.assertRaisesRegex(RuntimeError, "does not belong"):
            local.LocalService(self.root, self.agent)

    def test_failed_atomic_replace_keeps_previous_file(self):
        target = self.root / "current"
        target.write_bytes(b"old")
        with patch.object(local.os, "replace", side_effect=OSError("replace failed")):
            with self.assertRaises(OSError):
                local.atomic_write(target, b"new")
        self.assertEqual(target.read_bytes(), b"old")
        self.assertEqual(list(self.root.glob(".local-service-*")), [])

    def test_active_release_cannot_escape_checkout(self):
        local.write_json(self.service.active_file, {
            "app_path": "/other/app", "binary_path": "/other/server", "control_path": "/other/control"})
        with self.assertRaisesRegex(RuntimeError, "outside"):
            self.service.active()

    def test_snapshot_restore_preserves_business_state(self):
        state = self.root / "data/public-debug/state/checkpoint.json"
        state.parent.mkdir(parents=True)
        state.write_bytes(b'{"processed":"existing-attachment"}')
        self.service.binary.parent.mkdir()
        self.service.binary.write_bytes(b"old binary")
        version = {"version": "0.3.3", "git_commit": "saved"}
        self.service.binary_version = Mock(return_value=version)
        with self.service.lock():
            previous = self.service.snapshot()
            self.service.binary.write_bytes(b"new binary")
            self.service.restore(previous)
        self.assertEqual(self.service.binary.read_bytes(), b"old binary")
        self.assertEqual(state.read_bytes(), b'{"processed":"existing-attachment"}')
        self.assertEqual(self.agent.read_bytes(), (Path(previous["directory"]) / "agent.plist").read_bytes())
        self.assertEqual((Path(previous["directory"]) / "snapshot.json").stat().st_mode & 0o777, 0o600)

    def test_failed_prebuild_does_not_stop_service(self):
        self.service.checks = Mock(side_effect=RuntimeError("tests failed"))
        self.service.assert_owned_listener = Mock(return_value=[123])
        self.service.stop = Mock()
        self.service.build = Mock()
        with self.assertRaisesRegex(RuntimeError, "tests failed"):
            self.service.update()
        self.service.stop.assert_not_called()
        self.service.build.assert_not_called()

    def test_failed_switch_restores_and_verifies_previous_version(self):
        previous = {"directory": str(self.root), "version": {"git_commit": "previous"}}
        self.service.assert_owned_listener = Mock()
        self.service.stop = Mock()
        self.service.start = Mock()
        self.service.restore = Mock()
        self.service.wait_ready = Mock(side_effect=[RuntimeError("candidate failed"), {"pid": 321}])
        with redirect_stdout(io.StringIO()), self.assertRaisesRegex(RuntimeError, "candidate failed"):
            self.service.switch(previous, Mock(), {"git_commit": "candidate"})
        self.service.restore.assert_called_once_with(previous)
        self.assertEqual(self.service.stop.call_count, 2)
        self.assertEqual(self.service.start.call_count, 2)
        self.assertEqual(self.service.wait_ready.call_args.args, (previous["version"],))

    def test_unrelated_port_owner_is_not_stopped(self):
        self.service.assert_owned_listener = Mock(side_effect=RuntimeError("unrelated process"))
        self.service.stop = Mock()
        self.service.restore = Mock()
        with self.assertRaisesRegex(RuntimeError, "unrelated process"):
            self.service.switch({}, Mock(), {})
        self.service.stop.assert_not_called()
        self.service.restore.assert_not_called()

    def test_foreign_rollback_reference_is_rejected(self):
        local.write_json(self.service.previous_file, {"directory": "/some-other-backup"})
        self.service.snapshot = Mock()
        self.service.stop = Mock()
        with self.assertRaisesRegex(RuntimeError, "invalid rollback"):
            self.service.rollback()
        self.service.snapshot.assert_not_called()
        self.service.stop.assert_not_called()

    def test_successful_rollback_saves_reverse_recovery_target(self):
        directory = self.service.private / "backups/saved-before"
        previous = {"directory": str(directory), "version": {"git_commit": "before"}, "active": None}
        local.write_json(directory / "snapshot.json", previous)
        local.write_json(self.service.previous_file, {"directory": str(directory)})
        current = {"directory": str(self.service.private / "backups/saved-current")}
        self.service.snapshot = Mock(return_value=current)
        self.service.switch = Mock(return_value={"pid": 321})
        self.assertEqual(self.service.rollback(), {"pid": 321})
        self.assertEqual(json.loads(self.service.previous_file.read_text()), {"directory": current["directory"]})

    def test_readiness_rejects_wrong_build_immediately(self):
        self.service.request = Mock(return_value={"git_commit": "other", "build_date": "same"})
        with self.assertRaisesRegex(RuntimeError, "metadata"):
            self.service.wait_ready({"git_commit": "selected", "build_date": "same"})

    def test_readiness_requires_icon_and_unique_backend(self):
        version = {"version": "0.3.3", "git_commit": "selected", "build_date": "same"}
        responses = {"/version": version, "/healthz": {"status": "ok"},
                     "/readyz": {"status": "ready"}, "/api/status": {"feishu_state": "disabled"}}
        self.service.request = Mock(side_effect=lambda endpoint: responses[endpoint])
        self.service.assert_owned_listener = Mock(return_value=[123])
        self.service.active = Mock(return_value={"app_path": str(self.root / "app")})
        app = {"pid": 123, "icon_present": False, "bundle_path": str(self.root / "app")}
        self.service.apps = Mock(return_value=[app])
        with patch.object(local.time, "monotonic", side_effect=[0, 0, 61]), patch.object(local.time, "sleep"):
            with self.assertRaisesRegex(RuntimeError, "icon checks"):
                self.service.wait_ready(version)
        app["icon_present"] = True
        self.assertEqual(self.service.wait_ready(version)["pid"], 123)


if __name__ == "__main__":
    unittest.main()
