#!/usr/bin/env python3
"""Build a committed image on a Linux builder or GitLab Shell Runner, without SSH."""
import argparse
import importlib.util
import json
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', default='data/ci-artifacts')
    parser.add_argument('--source-archive', help='trusted git-archive input when there is no Git checkout')
    parser.add_argument('--commit')
    parser.add_argument('--registry-image', help='optional authenticated destination repository, without tag')
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    spec = importlib.util.spec_from_file_location('remote_service', repo / 'scripts/remote-service.py')
    remote = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(remote)
    if args.source_archive:
        if not args.commit:
            raise RuntimeError('--source-archive requires the full trusted --commit')
        archive_path = Path(args.source_archive).resolve()
        commit = args.commit
    else:
        commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()
        if args.commit and args.commit != commit:
            raise RuntimeError('CI commit differs from checkout')
        if subprocess.check_output(['git', 'status', '--porcelain'], cwd=repo):
            raise RuntimeError('build requires a clean committed checkout')
        archive_path = None
    if not re.fullmatch(r'[a-f0-9]{40}', commit):
        raise RuntimeError('full committed revision required')
    output = Path(args.output).resolve()
    work = output.parent / 'image-build-work/twc-approval-go-bridge'
    source = work / 'releases' / commit / 'source'
    source.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tempfile.TemporaryFile() as stream:
        if archive_path:
            stream.write(archive_path.read_bytes())
        else:
            subprocess.run(['git', 'archive', '--format=tar.gz', commit], cwd=repo, stdout=stream, check=True)
        stream.seek(0)
        with tarfile.open(fileobj=stream) as archive:
            for item in archive:
                path = Path(item.name)
                if path.is_absolute() or '..' in path.parts or not (item.isfile() or item.isdir()):
                    raise RuntimeError('unsafe source archive')
                destination = source / path
                if item.isdir():
                    destination.mkdir(parents=True, exist_ok=True)
                else:
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    remote.atomic(destination, archive.extractfile(item).read(), item.mode & 0o777)
    manifest = remote.Host(str(work), commit).build()
    target = output / commit
    target.mkdir(parents=True, exist_ok=True, mode=0o700)
    for name in ['artifact.json', 'image.tar', 'deployment.tar.gz']:
        shutil.copyfile(work / 'artifacts' / commit / name, target / name)
    remote.validate_artifact(target)
    if args.registry_image:
        publisher_spec = importlib.util.spec_from_file_location('publish_image', repo / 'scripts/publish-image.py')
        publisher = importlib.util.module_from_spec(publisher_spec)
        publisher_spec.loader.exec_module(publisher)
        publisher.publish(target, args.registry_image)
    print(json.dumps({'artifact': str(target), 'commit': commit, 'platform': manifest['platform']}))


if __name__ == '__main__':
    main()
