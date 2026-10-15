#!/usr/bin/env python3
"""Publish a verified finished artifact using preconfigured registry credentials."""
import argparse
import importlib.util
import json
from pathlib import Path
import re
import subprocess


def publish(directory, repository):
    directory = Path(directory).resolve()
    spec = importlib.util.spec_from_file_location('remote_service', Path(__file__).with_name('remote-service.py'))
    remote = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(remote)
    manifest = remote.validate_artifact(directory)
    if not re.fullmatch(r'[a-zA-Z0-9.-]+(?::[0-9]+)?/[a-zA-Z0-9_./-]+', repository):
        raise RuntimeError('provide a registry repository without a tag')
    subprocess.run(['docker', 'load', '--input', str(directory / 'image.tar')], check=True)
    inspected = json.loads(subprocess.check_output(['docker', 'image', 'inspect', manifest['tag']]))[0]
    if inspected['Id'] not in {manifest['platform_manifest_digest'], manifest['config_digest']}:
        raise RuntimeError('loaded image differs from the verified artifact')
    tag = repository + ':' + manifest['commit'] + '-' + manifest['builder_image'].split(':')[1][:12]
    subprocess.run(['docker', 'tag', manifest['tag'], tag], check=True)
    subprocess.run(['docker', 'push', tag], check=True)
    digests = json.loads(subprocess.check_output(['docker', 'image', 'inspect', '--format', '{{json .RepoDigests}}', tag]))
    reference = next(value for value in digests if value.startswith(repository + '@sha256:'))
    result = {'image': reference, 'tag': tag, 'commit': manifest['commit'],
              'platform_manifest_digest': manifest['platform_manifest_digest']}
    remote.write_json(directory / 'registry.json', result)
    print(json.dumps(result))
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('artifact')
    parser.add_argument('repository')
    args = parser.parse_args()
    publish(args.artifact, args.repository)
