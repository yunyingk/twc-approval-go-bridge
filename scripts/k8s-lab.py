#!/usr/bin/env python3
"""Operate only the isolated WSL kind lab; never select a production context."""
import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import time
import urllib.request
import uuid

ROOT = Path('/home/dev/apps/twc-k8s-lab')
CLUSTER = 'twc-lab'
NAMESPACE = 'twc-lab'
NODE_IMAGE = 'docker.m.daocloud.io/kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d'
TOOLS = {
    'kind': ('https://github.com/kubernetes-sigs/kind/releases/download/v0.33.0/kind-linux-amd64', 'aee6151561422756b764a4ae28e7f44cda5af5a9eead3cc9985112b1de8d8e0d'),
    'kubectl': ('https://dl.k8s.io/release/v1.34.11/bin/linux/amd64/kubectl', '8efbb9435132a190920eb65a47a8c1ecf755ad85ab57a600c9bedbab460bb7a8'),
}


def load_remote(path):
    spec = importlib.util.spec_from_file_location('remote_service', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def render_overlay(text, tag):
    if not re.fullmatch(r'[a-zA-Z0-9./_-]+:[a-zA-Z0-9._-]+', tag):
        raise RuntimeError('invalid lab image reference')
    name, version = tag.rsplit(':', 1)
    return text + '\nimages:\n  - name: twc-approval-go-bridge\n    newName: ' + name + '\n    newTag: ' + version + '\n'


def verify_image_reference(reference, manifest, read_blob):
    """Follow containerd import indexes to the trusted platform manifest."""
    match = re.search(r'sha256:[a-f0-9]{64}$', reference)
    if not match:
        raise RuntimeError('invalid running image reference')
    digest = match.group(0)
    for _ in range(4):
        raw = read_blob(digest)
        if 'sha256:' + hashlib.sha256(raw).hexdigest() != digest:
            raise RuntimeError('runtime image descriptor checksum mismatch')
        value = json.loads(raw)
        if digest == manifest['platform_manifest_digest']:
            if value.get('config', {}).get('digest') != manifest['config_digest']:
                raise RuntimeError('runtime image configuration mismatch')
            return digest
        if digest == manifest['config_digest']:
            if value.get('rootfs', {}).get('diff_ids') != manifest['diff_ids']:
                raise RuntimeError('runtime image layers mismatch')
            return manifest['platform_manifest_digest']
        choices = [item['digest'] for item in value.get('manifests', [])
                   if item.get('platform', {}).get('os') == 'linux' and
                   item.get('platform', {}).get('architecture') == 'amd64']
        if len(choices) != 1:
            raise RuntimeError('runtime image index does not select one lab platform')
        digest = choices[0]
    raise RuntimeError('runtime image index nesting exceeded')


class Lab:
    def __init__(self):
        self.root = ROOT
        self.helper = load_remote(ROOT / 'source/scripts/remote-service.py')
        self.active = ROOT / 'active.json'
        self.previous = ROOT / 'previous.json'

    def run(self, args, capture=True, check=True, **kwargs):
        try:
            return subprocess.run(args, text=True, capture_output=capture, check=check, **kwargs)
        except subprocess.CalledProcessError as error:
            if capture and error.stderr:
                print(error.stderr[-3000:], file=sys.stderr)
            raise

    def k(self, args, **kwargs):
        return self.run([str(ROOT / 'bin/kubectl'), '--kubeconfig', str(ROOT / 'kubeconfig'),
                         '--context', 'kind-' + CLUSTER] + args, **kwargs)

    @contextmanager
    def lock(self):
        with (ROOT / 'operation.lock').open('a') as stream:
            os.chmod(stream.name, 0o600)
            try:
                fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RuntimeError('another lab operation is running') from None
            yield

    def node(self):
        result = self.run(['docker', 'inspect', CLUSTER + '-control-plane'], check=False)
        if result.returncode:
            return None
        node = json.loads(result.stdout)[0]
        if node['Config']['Labels'].get('io.x-k8s.kind.cluster') != CLUSTER:
            raise RuntimeError('refusing to manage an unrelated Docker container')
        return node

    def guard(self):
        if not self.node():
            raise RuntimeError('create the lab with make k8s-lab-up first')
        config = json.loads(self.k(['config', 'view', '--minify', '-o', 'json']).stdout)
        server = config['clusters'][0]['cluster']['server']
        if not re.fullmatch(r'https://127\.0\.0\.1:[0-9]+', server):
            raise RuntimeError('lab kubeconfig must use its local-only API endpoint')

    def up(self):
        with self.lock():
            for name, (_, digest) in TOOLS.items():
                if self.helper.file_sha(ROOT / 'bin' / name) != digest:
                    raise RuntimeError('tool checksum mismatch: ' + name)
                os.chmod(ROOT / 'bin' / name, 0o700)
            if not self.node():
                config = {'kind': 'Cluster', 'apiVersion': 'kind.x-k8s.io/v1alpha4',
                          'networking': {'apiServerAddress': '127.0.0.1'},
                          'nodes': [{'role': 'control-plane', 'extraPortMappings': [
                              {'containerPort': 30080, 'hostPort': 0, 'listenAddress': '127.0.0.1'}]}]}
                self.helper.write_json(ROOT / 'kind.json', config)
                self.run([str(ROOT / 'bin/kind'), 'create', 'cluster', '--name', CLUSTER,
                          '--config', str(ROOT / 'kind.json'), '--kubeconfig', str(ROOT / 'kubeconfig'),
                          '--image', NODE_IMAGE, '--wait', '180s'], capture=False)
            else:
                self.run(['docker', 'start', CLUSTER + '-control-plane'], capture=False)
            self.run(['docker', 'update', '--cpus', '2', '--memory', '4g', '--memory-swap', '4g', CLUSTER + '-control-plane'], capture=False)
            config = self.run([str(ROOT / 'bin/kind'), 'get', 'kubeconfig', '--name', CLUSTER]).stdout
            self.helper.atomic(ROOT / 'kubeconfig', config.encode(), 0o600)
            self.guard()
            self.k(['wait', '--for=condition=Ready', 'nodes', '--all', '--timeout=180s'], capture=False)
            return self.status()

    def stop(self):
        with self.lock():
            self.guard()
            self.run(['docker', 'stop', '--timeout', '30', CLUSTER + '-control-plane'], capture=False)
            return {'stopped': True, 'data_retained': True}

    def url(self):
        address = self.run(['docker', 'port', CLUSTER + '-control-plane', '30080/tcp']).stdout.strip()
        if not re.fullmatch(r'127\.0\.0\.1:[0-9]+', address):
            raise RuntimeError('lab HTTP port must bind only WSL localhost')
        return 'http://' + address

    def http(self, path):
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(self.url() + path, timeout=5) as response:
            return json.load(response)

    def wait(self, manifest):
        self.k(['-n', NAMESPACE, 'rollout', 'status', 'deployment/twc-approval-bridge', '--timeout=180s'], capture=False)
        for _ in range(20):
            try:
                version = self.http('/version')
                if version != manifest['version']:
                    raise RuntimeError('lab version differs from selected artifact')
                status = self.http('/api/status')
                if status.get('feishu_state') != 'disabled':
                    raise RuntimeError('lab must not enable Feishu business connections')
                if self.http('/healthz').get('status') != 'ok' or self.http('/readyz').get('status') != 'ready':
                    raise OSError('lab not ready')
                pods = json.loads(self.k(['-n', NAMESPACE, 'get', 'pods', '-l', 'app=twc-approval-bridge', '-o', 'json']).stdout)['items']
                pods = [p for p in pods if not p['metadata'].get('deletionTimestamp')]
                if len(pods) != 1:
                    raise RuntimeError('lab expects exactly one active application Pod')
                image_id = pods[0]['status']['containerStatuses'][0]['imageID']
                verify_image_reference(image_id, manifest, lambda digest: self.run([
                    'docker', 'exec', CLUSTER + '-control-plane', 'ctr', '-n', 'k8s.io',
                    'content', 'get', digest]).stdout.encode())
                return {'version': version, 'feishu_state': 'disabled', 'pod': pods[0]['metadata']['name'],
                        'image_id': image_id, 'wsl_local_url': self.url()}
            except OSError:
                time.sleep(1)
        raise RuntimeError('lab HTTP checks timed out')

    def deploy(self, directory):
        with self.lock():
            self.guard()
            directory = Path(directory).resolve()
            if not directory.is_relative_to(ROOT / 'artifacts'):
                raise RuntimeError('artifact must belong to the lab directory')
            manifest = self.helper.validate_artifact(directory)
            if manifest['platform'] != 'linux/amd64':
                raise RuntimeError('the WSL lab requires linux/amd64')
            self.run([str(ROOT / 'bin/kind'), 'load', 'image-archive', str(directory / 'image.tar'), '--name', CLUSTER], capture=False)
            overlay = ROOT / 'source/deploy/k8s/overlays/lab'
            # The pristine overlay is uploaded for each deployment.
            path = overlay / 'kustomization.yaml'
            path.write_text(render_overlay(path.read_text(), manifest['tag']))
            document = self.k(['kustomize', str(overlay)]).stdout
            release_dir = ROOT / 'releases' / manifest['commit']
            release_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
            self.helper.atomic(release_dir / 'manifest.yaml', document.encode())
            self.k(['apply', '-f', '-'], input=json.dumps({'apiVersion': 'v1', 'kind': 'Namespace',
                                                        'metadata': {'name': NAMESPACE}}), capture=False)
            secret = self.k(['-n', NAMESPACE, 'create', 'secret', 'generic', 'twc-approval-config',
                             '--from-file=config.toml=' + str(overlay / 'config.toml'),
                             '--from-file=tables.json=' + str(overlay / 'tables.json'),
                             '--dry-run=client', '-o', 'json']).stdout
            self.k(['apply', '-f', '-'], input=secret, capture=False)
            previous = json.loads(self.active.read_text()) if self.active.exists() else None
            try:
                self.k(['apply', '-f', str(release_dir / 'manifest.yaml')], capture=False)
                result = self.wait(manifest)
            except Exception:
                if previous:
                    self.k(['apply', '-f', str(ROOT / 'releases' / previous['commit'] / 'manifest.yaml')], capture=False)
                    self.wait(previous)
                raise
            if previous and previous['commit'] != manifest['commit']:
                self.helper.write_json(self.previous, previous)
            self.helper.write_json(self.active, manifest)
            return result

    def rollback(self):
        with self.lock():
            self.guard()
            if not self.previous.exists():
                raise RuntimeError('no previous lab release')
            selected = json.loads(self.previous.read_text())
            current = json.loads(self.active.read_text())
            try:
                self.k(['apply', '-f', str(ROOT / 'releases' / selected['commit'] / 'manifest.yaml')], capture=False)
                result = self.wait(selected)
            except Exception:
                self.k(['apply', '-f', str(ROOT / 'releases' / current['commit'] / 'manifest.yaml')], capture=False)
                self.wait(current)
                raise
            self.helper.write_json(self.active, selected)
            self.helper.write_json(self.previous, current)
            return result

    def status(self):
        node = self.node()
        if not node or not node['State']['Running']:
            return {'running': False}
        self.guard()
        result = {'running': True, 'cluster': CLUSTER, 'namespace': NAMESPACE,
                  'node_memory_limit_GiB': node['HostConfig']['Memory'] / 2**30,
                  'node_cpu_limit': node['HostConfig']['NanoCpus'] / 1e9}
        if self.active.exists():
            result.update(self.wait(json.loads(self.active.read_text())))
        return result

    def verify(self):
        with self.lock():
            self.guard()
            selected = json.loads(self.active.read_text())
            marker = uuid.uuid4().hex
            helper_image = 'docker.m.daocloud.io/library/busybox:1.37.0'
            self.run(['docker', 'pull', helper_image], capture=False)
            # Export one platform; Docker's multi-platform index may reference absent blobs.
            helper_archive = ROOT / 'helper-image.tar'
            with helper_archive.open('wb') as output:
                self.run(['docker', 'image', 'save', '--platform', 'linux/amd64', helper_image], stdout=output, capture=False)
            self.run([str(ROOT / 'bin/kind'), 'load', 'image-archive', str(helper_archive), '--name', CLUSTER], capture=False)
            def helper(command):
                name = 'pvc-check-' + uuid.uuid4().hex[:8]
                pod = {'apiVersion': 'v1', 'kind': 'Pod', 'metadata': {'name': name, 'namespace': NAMESPACE},
                       'spec': {'restartPolicy': 'Never', 'securityContext': {'runAsNonRoot': True,
                           'runAsUser': 65532, 'runAsGroup': 65532, 'fsGroup': 65532,
                           'seccompProfile': {'type': 'RuntimeDefault'}},
                           'containers': [{'name': 'check', 'image': helper_image, 'imagePullPolicy': 'IfNotPresent',
                               'command': ['sh', '-ec', command], 'securityContext': {'allowPrivilegeEscalation': False,
                                   'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}},
                               'resources': {'requests': {'cpu': '10m', 'memory': '16Mi'},
                                             'limits': {'cpu': '100m', 'memory': '32Mi'}},
                               'volumeMounts': [{'name': 'data', 'mountPath': '/data'}]}],
                           'volumes': [{'name': 'data', 'persistentVolumeClaim': {'claimName': 'twc-approval-data-pvc'}}]}}
                try:
                    self.k(['apply', '-f', '-'], input=json.dumps(pod), capture=False)
                    self.k(['-n', NAMESPACE, 'wait', '--for=jsonpath={.status.phase}=Succeeded', 'pod/' + name, '--timeout=120s'], capture=False)
                    return self.k(['-n', NAMESPACE, 'logs', name]).stdout.strip()
                finally:
                    self.k(['-n', NAMESPACE, 'delete', 'pod', name, '--ignore-not-found=true'], capture=False)
            helper('printf %s ' + marker + ' > /data/lab-persistence-marker')
            before = self.wait(selected)['pod']
            self.k(['-n', NAMESPACE, 'delete', 'pod', before], capture=False)
            after = self.wait(selected)['pod']
            if before == after or helper('cat /data/lab-persistence-marker') != marker:
                raise RuntimeError('PVC data did not survive application Pod replacement')
            actor = 'system:serviceaccount:' + NAMESPACE + ':viewer'
            checks = {}
            for resource, namespace, expected in [('pods', NAMESPACE, True), ('secrets', NAMESPACE, False), ('pods', 'default', False)]:
                result = self.k(['auth', 'can-i', 'get', resource, '-n', namespace, '--as', actor], check=False)
                allowed = result.stdout.strip() == 'yes'
                if allowed != expected:
                    raise RuntimeError('unexpected viewer permission')
                checks[namespace + '/' + resource] = allowed
            evidence = {'pvc_survived_pod_replacement': True, 'permissions': checks, 'status': self.status()}
            self.helper.write_json(ROOT / 'verification.json', evidence)
            return evidence


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['up', 'deploy', 'status', 'verify', 'rollback', 'stop'])
    parser.add_argument('--host', default='wsl-dev')
    parser.add_argument('--on-host', action='store_true')
    parser.add_argument('--artifact')
    args = parser.parse_args()
    try:
        if args.on_host:
            lab = Lab()
            result = lab.deploy(args.artifact) if args.action == 'deploy' else getattr(lab, args.action)()
            print(json.dumps(result, ensure_ascii=False, indent=2))
            return
        repo = Path(__file__).resolve().parent.parent
        remote = load_remote(repo / 'scripts/remote-service.py')
        client = remote.Client(args.host, remote.DEFAULT_ROOT)
        files = {'scripts/k8s-lab.py': Path(__file__).read_bytes(),
                 'scripts/remote-service.py': (repo / 'scripts/remote-service.py').read_bytes()}
        files.update({str(p.relative_to(repo)): p.read_bytes() for p in (repo / 'deploy/k8s').rglob('*') if p.is_file()})
        client.upload(str(ROOT / 'source'), files)
        if args.action == 'up':
            tools = {}
            cache = repo / 'data/k8s-lab/cache'
            cache.mkdir(parents=True, exist_ok=True)
            for name, (url, digest) in TOOLS.items():
                path = cache / name
                if not path.exists() or remote.file_sha(path) != digest:
                    with urllib.request.urlopen(url, timeout=60) as response:
                        remote.atomic(path, response.read())
                if remote.file_sha(path) != digest:
                    raise RuntimeError('download checksum mismatch: ' + name)
                tools[name] = path.read_bytes()
            client.upload(str(ROOT / 'bin'), tools)
        command = ['python3', '-B', str(ROOT / 'source/scripts/k8s-lab.py'), args.action, '--on-host']
        if args.action == 'deploy':
            latest = repo / 'data/k8s-lab/latest.json'
            if not latest.exists():
                latest = repo / 'data/remote-releases/latest.json'
            path = Path(args.artifact) if args.artifact else Path(json.loads(latest.read_text())['path'])
            manifest = remote.validate_artifact(path)
            destination = ROOT / 'artifacts' / manifest['commit']
            client.upload(str(destination), {name: (path / name).read_bytes() for name in ['image.tar', 'artifact.json', 'deployment.tar.gz']})
            command += ['--artifact', str(destination)]
        client.ssh(shlex.join(command))
        if args.action == 'deploy':
            remote.write_json(repo / 'data/k8s-lab/latest.json', {'path': str(path.resolve())})
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.CalledProcessError) as error:
        print('lab operation failed: ' + str(error), file=sys.stderr)
        raise SystemExit(1)


if __name__ == '__main__':
    main()
