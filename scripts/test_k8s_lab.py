"""Check isolation and offline configuration without creating a real cluster."""
import importlib.util
import json
import hashlib
from pathlib import Path
import tomllib
import unittest
from unittest.mock import Mock

spec = importlib.util.spec_from_file_location('k8s_lab', Path(__file__).with_name('k8s-lab.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class K8sLabTests(unittest.TestCase):
    def test_api_guard_rejects_nonlocal_or_production_endpoint(self):
        host = object.__new__(lab.Lab)
        host.node = Mock(return_value={'owned': True})
        host.k = Mock(return_value=Mock(stdout=json.dumps({'clusters': [{'cluster': {'server': 'https://production.example.com'}}]})))
        with self.assertRaisesRegex(RuntimeError, 'local-only'):
            host.guard()

    def test_all_kubectl_operations_use_explicit_private_context(self):
        host = object.__new__(lab.Lab)
        host.run = Mock()
        host.k(['get', 'pods'])
        command = host.run.call_args.args[0]
        self.assertIn('--kubeconfig', command)
        self.assertEqual(command[command.index('--context') + 1], 'kind-twc-lab')

    def test_unrelated_container_cannot_be_started_stopped_or_changed(self):
        host = object.__new__(lab.Lab)
        host.run = Mock(return_value=Mock(returncode=0, stdout=json.dumps([
            {'Config': {'Labels': {'io.x-k8s.kind.cluster': 'someone-else'}}}])))
        with self.assertRaisesRegex(RuntimeError, 'unrelated'):
            host.node()

    def test_generated_containerd_index_resolves_to_original_platform_manifest(self):
        config = {'rootfs': {'diff_ids': ['sha256:layer']}}
        encode = lambda value: json.dumps(value).encode()
        digest = lambda value: 'sha256:' + hashlib.sha256(value).hexdigest()
        config_raw = encode(config)
        platform_raw = encode({'config': {'digest': digest(config_raw)}})
        index_raw = encode({'manifests': [{'digest': digest(platform_raw), 'platform': {'os': 'linux', 'architecture': 'amd64'}}]})
        blobs = {digest(value): value for value in [config_raw, platform_raw, index_raw]}
        manifest = {'platform_manifest_digest': digest(platform_raw), 'config_digest': digest(config_raw), 'diff_ids': config['rootfs']['diff_ids']}
        reference = 'docker.io/library/import-date@' + digest(index_raw)
        self.assertEqual(lab.verify_image_reference(reference, manifest, blobs.__getitem__), digest(platform_raw))
        with self.assertRaisesRegex(RuntimeError, 'checksum'):
            lab.verify_image_reference(reference, manifest, lambda key: b'corrupted descriptor')

    def test_lab_config_has_no_real_business_connections(self):
        path = Path(__file__).resolve().parent.parent / 'deploy/k8s/overlays/lab/config.toml'
        config = tomllib.loads(path.read_text())
        self.assertEqual(config['recognition']['provider'], 'disabled')
        self.assertEqual(config['review']['trigger_mode'], 'manual')
        self.assertFalse(config.get('feishu', {}).get('app_secret'))
        self.assertFalse(config['transactions']['auto_notify'])
        self.assertFalse(config['notification']['enabled'])
        tables = json.loads(path.with_name('tables.json').read_text())
        self.assertTrue(all(t['base_token'] == 'offline' for t in tables['tables'].values()))

    def test_image_references_are_data_and_cannot_inject_yaml_or_commands(self):
        for image in ['bad\nresources: []', 'name:tag; rm -rf /', '$(secret):tag']:
            with self.assertRaisesRegex(RuntimeError, 'invalid'):
                lab.render_overlay('resources: []\n', image)
        rendered = lab.render_overlay('resources: []\n', 'twc-approval-go-bridge:artifact-test')
        self.assertIn('newTag: artifact-test', rendered)


if __name__ == '__main__':
    unittest.main()
