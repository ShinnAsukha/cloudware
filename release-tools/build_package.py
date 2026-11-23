#!/usr/bin/env python3
"""GitHub Actions only: validate a release candidate and create an attested ZIP.
This does not touch the user's Cloudware installation and executes no candidate files.
"""
import argparse
import hashlib
import json
import pathlib
import re
import stat
import sys
import zipfile

TAG = re.compile(r"^cw-app-\d+\.\d+\.\d+$")
SHA = re.compile(r"^[a-f0-9]{64}$")
BAD_NAMES = {"con", "prn", "aux", "nul", *(f"com{x}" for x in range(1,10)), *(f"lpt{x}" for x in range(1,10))}


def path_ok(s):
    if not isinstance(s, str) or not s or len(s) > 240 or '\\' in s or not s.startswith(('apps/web/', 'apps/api/', 'AI/')):
        return False
    for part in s.split('/'):
        low = part.lower()
        if (not part or part in {'.','..'} or part.endswith(('.', ' ')) or any(x in part for x in ':*?"<>|\x00')
            or low in {'.git', 'node_modules', '.next', 'secrets'} or low.startswith('.env')
            or low.endswith(('.pem', '.key', '.pfx')) or low.split('.')[0] in BAD_NAMES):
            return False
    return True


def validate(candidate, tag):
    if not TAG.fullmatch(tag):
        raise ValueError('invalid release tag')
    mfile = candidate / 'manifest.json'
    if not mfile.is_file() or mfile.is_symlink() or mfile.stat().st_size > 256*1024:
        raise ValueError('missing/invalid manifest')
    data = json.loads(mfile.read_text(encoding='utf-8'))
    if data.get('schema') != 1 or data.get('tag') != tag or data.get('database_changes') is not False:
        raise ValueError('schema/tag/migrations not supported')
    files = data.get('files')
    if not isinstance(files, list) or not 1 <= len(files) <= 400:
        raise ValueError('invalid file count')
    services = data.get('services')
    if not isinstance(services, list) or len(services) != len(set(services)) or set(services) - {'web','api'}:
        raise ValueError('invalid services')
    names = set()
    total = 0
    for item in files:
        if not isinstance(item, dict):
            raise ValueError('invalid file entry')
        name = item.get('path')
        if not path_ok(name) or name.lower() in names:
            raise ValueError(f'bad/duplicate path {name}')
        names.add(name.lower())
        if not SHA.fullmatch(item.get('sha256', '')) or not (item.get('before_sha256') == 'absent' or SHA.fullmatch(item.get('before_sha256', ''))):
            raise ValueError('invalid SHA256')
        if name.startswith('apps/web/') and 'web' not in services or name.startswith('apps/api/') and 'api' not in services:
            raise ValueError('missing impacted service')
        source = candidate / 'payload' / pathlib.PurePosixPath(name)
        if not source.is_file() or source.is_symlink():
            raise ValueError(f'missing/nonregular file: {name}')
        size = source.stat().st_size
        if size > 64*1024*1024:
            raise ValueError('single file size exceeded')
        total += size
        if total > 256*1024*1024:
            raise ValueError('package uncompressed size exceeded')
        digest = hashlib.sha256(source.read_bytes()).hexdigest()
        if digest != item['sha256']:
            raise ValueError(f'SHA256 mismatch for {name}')
    present = {p.relative_to(candidate/'payload').as_posix().lower() for p in (candidate/'payload').rglob('*') if p.is_file()}
    if present != names:
        raise ValueError(f'undeclared or missing payload files: {present ^ names}')
    return data


def build(candidate, tag, output):
    data = validate(candidate, tag)
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED, compresslevel=8) as archive:
        archive.write(candidate/'manifest.json', 'manifest.json')
        for entry in sorted(data['files'], key=lambda x: x['path']):
            rel = entry['path']
            archive.write(candidate/'payload'/pathlib.PurePosixPath(rel), 'payload/' + rel)
    with zipfile.ZipFile(output, 'r') as archive:
        if archive.testzip() is not None:
            raise ValueError('ZIP CRC error')
    print(f'PASS: {tag}, {len(data["files"])} verified files, {output}')


def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument('--candidate', type=pathlib.Path, required=True)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args(argv)
    build(args.candidate.resolve(), args.tag, args.output.resolve())

if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, json.JSONDecodeError) as error:
        print(f'FAIL: {error}', file=sys.stderr)
        sys.exit(1)