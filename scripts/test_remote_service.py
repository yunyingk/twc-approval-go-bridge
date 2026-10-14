"""Verify release and migration failure boundaries without SSH or Docker effects."""

from contextlib import redirect_stdout
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock


spec = importlib.util.spec_from_file_location("remote_service", Path(__file__).with_name("remote-service.py"))
remote = importlib.util.module_from_spec(spec)
spec.loader.exec_module(remote)


class RemoteServiceTests(unittest.TestCase):
    def test_toml_rewrite_preserves_credentials_comments_and_unrelated_values(self):
        original = ('tables_file = "configs/tables/test.json" # source\n'
                    '[feishu]\napp_secret = "unit-secret"\n'
                    '[runtime]\nhttp_addr = "127.0.0.1:18088" # local\nstate_dir = "data/state"\n')
        rewritten = remote.replace_string(original, "", "tables_file", "/configs/tables/test.json")
        rewritten = remote.replace_string(rewritten, "runtime", "http_addr", ":8080")
        self.assertIn('app_secret = "unit-secret"', rewritten)
        self.assertIn('http_addr = ":8080" # local', rewritten)
        self.assertIn('state_dir = "data/state"', rewritten)
        self.assertIn('"/configs/tables/test.json" # source', rewritten)

    def test_proxy_rewrite_only_changes_dedicated_proxy(self):
        text = '[auth]\ntoken="unit-secret"\n[[proxies]]\nname="old"\nlocalIP="127.0.0.1"\n'
        rewritten = remote.replace_string(text, "[proxies", "localIP", "server")
        self.assertIn('token="unit-secret"', rewritten)
        self.assertIn('localIP="server"', rewritten)

    def test_missing_or_duplicate_toml_key_is_rejected(self):
        for text in ['[runtime]\nstate_dir="data"\n', '[runtime]\nhttp_addr="x"\nhttp_addr="y"\n']:
            with self.assertRaises(RuntimeError):
                remote.replace_string(text, "runtime", "http_addr", ":8080")

    def test_transfer_rejects_escaping_paths(self):
        for name in ["../credentials", "/credentials"]:
            with self.assertRaisesRegex(RuntimeError, "unsafe"):
                remote.archive_files({name: b"value"})

    def test_state_manifest_rejects_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "state.json").write_bytes(b'{"delivered":true}')
            before = remote.hashes(root)
            self.assertEqual(list(before), ["state.json"])
            (root / "external").symlink_to("/etc/passwd")
            with self.assertRaisesRegex(RuntimeError, "symlinks"):
                remote.hashes(root)

    def host_with_fake_release(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        host = object.__new__(remote.Host)
        host.root = Path(temporary.name)
        host.operations = host.root / "operations"
        host.active_path = host.operations / "active.json"
        host.previous_path = host.operations / "previous.json"
        before = {"commit": "a" * 40, "image": "sha256:old"}
        after = {"commit": "b" * 40, "image": "sha256:new"}
        remote.write_json(host.active_path, before)
        host.compose = Mock()
        return host, before, after

    def test_failed_candidate_restores_previous_without_changing_active_reference(self):
        host, before, after = self.host_with_fake_release()
        host.ready = Mock(side_effect=[RuntimeError("candidate unhealthy"), {"verified": True}])
        with redirect_stdout(io.StringIO()), self.assertRaisesRegex(RuntimeError, "candidate unhealthy"):
            host.switch(after)
        self.assertEqual(json.loads(host.active_path.read_text()), before)
        self.assertFalse(host.previous_path.exists())
        self.assertEqual(host.ready.call_args.args, (before,))
        self.assertEqual(host.compose.call_args.args[:2], (before, ["up", "--detach", "--pull", "never"]))

    def test_compose_uses_release_tag_but_verifies_immutable_image_id(self):
        host, before, after = self.host_with_fake_release()
        after.update({"tag": "twc-approval-go-bridge:0.3.3-bbbbbbbbbbbb"})
        host.commit = after["commit"]
        host.operations.mkdir(exist_ok=True)
        source = host.root / "releases" / after["commit"] / "source/deploy/docker-compose.wsl.yml"
        source.parent.mkdir(parents=True)
        source.write_text("name: test\nservices: {}\n")
        host.run = Mock(return_value=__import__("subprocess").CompletedProcess([], 0, "", ""))
        result = remote.Host.compose(host, after, ["config", "--quiet"])
        self.assertEqual(result.returncode, 0)
        self.assertEqual(host.run.call_args.kwargs["env"]["TWC_IMAGE"], after["tag"])

    def test_shared_ingress_survives_old_release_and_omits_dedicated_profile(self):
        host, before, after = self.host_with_fake_release()
        before["tag"] = "twc-approval-go-bridge:old"
        remote.write_json(host.operations / "ingress.json", {"mode": "shared-frpc"})
        host.run = Mock()
        remote.Host.compose(host, before, ["up", "--detach"])
        command = host.run.call_args.args[0]
        self.assertNotIn("--profile", command)
        self.assertIn(str(host.operations / "shared-ingress.compose.yml"), command)

    def test_release_manifest_requires_immutable_id_and_human_tag(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        host = object.__new__(remote.Host)
        host.root = Path(temporary.name)
        path = host.root / "releases" / ("a" * 40) / "release.json"
        remote.write_json(path, {"commit": "a" * 40, "image": "sha256:abc",
                                "tag": "twc-approval-go-bridge:0.3.3-aaaaaaaaaaaa"})
        self.assertEqual(host.release("a" * 40)["image"], "sha256:abc")
        remote.write_json(path, {"commit": "a" * 40, "image": "sha256:abc", "tag": "sha256:abc"})
        with self.assertRaisesRegex(RuntimeError, "invalid release"):
            host.release("a" * 40)

    def test_verified_switch_preserves_business_data_and_records_immutable_images(self):
        host, before, after = self.host_with_fake_release()
        state = host.root / "data/checkpoint.json"
        state.parent.mkdir()
        state.write_bytes(b'{"processed":"existing"}')
        source = host.root / "releases" / after["commit"] / "source/scripts/remote-service.py"
        source.parent.mkdir(parents=True)
        source.write_bytes(b"# manager")
        host.ready = Mock(return_value={"verified": True})
        self.assertEqual(host.switch(after), {"verified": True})
        self.assertEqual(json.loads(host.active_path.read_text()), after)
        self.assertEqual(json.loads(host.previous_path.read_text()), before)
        self.assertEqual(state.read_bytes(), b'{"processed":"existing"}')
        self.assertEqual((host.root / "manager.py").read_bytes(), Path(remote.__file__).read_bytes())

    def test_candidate_abort_works_before_first_manager_is_installed(self):
        host, before, after = self.host_with_fake_release()
        host.active_path.unlink()
        host.commit = after["commit"]
        host.release = Mock(return_value=after)
        host.operations.mkdir(exist_ok=True)
        self.assertEqual(host.stop(), {"stopped": True})
        host.compose.assert_called_once_with(after, ["down"], capture=False)

    def test_normal_update_cannot_start_first_worker_before_mac_handoff(self):
        host, before, after = self.host_with_fake_release()
        host.active_path.unlink()
        host.initial = False
        host.commit = after["commit"]
        host.release = Mock(return_value=after)
        with self.assertRaisesRegex(RuntimeError, "stopped-Mac"):
            host.activate()
        host.compose.assert_not_called()

    def test_uncommitted_source_never_reaches_remote_host(self):
        client = remote.Client("wsl-dev", remote.DEFAULT_ROOT)
        client.clean_commit = Mock(side_effect=RuntimeError("uncommitted changes"))
        client.source = Mock()
        client.agent = Mock()
        with self.assertRaisesRegex(RuntimeError, "uncommitted"):
            client.update()
        client.source.assert_not_called()
        client.agent.assert_not_called()

    def test_ssh_host_options_and_nonproject_roots_are_rejected(self):
        for host, root in [("-oProxyCommand=bad", remote.DEFAULT_ROOT), ("wsl-dev", "/home/dev/apps")]:
            with self.assertRaises(RuntimeError):
                remote.Client(host, root)


if __name__ == "__main__":
    unittest.main()
