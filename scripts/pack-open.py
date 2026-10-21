"""Package freshly built Open inputs and locked MCP dependencies; no installs."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import zipfile


PCB_TOOLS = ['pcb_constraint_apply.mjs', 'pcb_constraint_compiler.mjs', 'pcb_professional_checks.mjs', 'pcb_copper_attachment.mjs']
GO_PACKAGE = 'github.com/zhoushoujianwork/easyeda-agent/cmd/easyeda'


def read_json(path):
    return json.loads(path.read_text(encoding='utf-8'))


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def check_package(root, directory, expected):
    manifest = read_json(root / directory / 'package.json')
    lock = read_json(root / directory / 'package-lock.json')
    if (manifest.get('version') != expected or lock.get('version') != expected
            or lock.get('packages', {}).get('', {}).get('version') != expected
            or lock.get('name') != manifest.get('name')
            or lock.get('packages', {}).get('', {}).get('name') != manifest.get('name')):
        raise ValueError(f'{directory}: package/lock identity differs from {expected}')
    return manifest


def check_connector(root, sources):
    manifest = read_json(root / 'extension/extension.json')
    version = sources['connectorVersion']
    check_package(root, 'extension', version)
    if manifest.get('version') != version or manifest.get('uuid') != sources['connectorUuid']:
        raise ValueError('Connector source version/UUID differs from sources.open.json')
    entry = manifest['entry'].removeprefix('./') + '.js'
    bundle = (root / 'extension' / entry).read_bytes()
    compiled = re.search(rb'\bCONNECTOR_VERSION\s*=[^;\r\n]*"(\d+\.\d+\.\d+[^"\r\n]*)"', bundle)
    if not compiled or compiled[1].decode() != version:
        raise ValueError('Compiled Connector version differs; compile before packaging')
    connector = root / f'extension/build/dist/{manifest["name"]}_v{version}.eext'
    with zipfile.ZipFile(connector) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)) or archive.testzip() is not None:
            raise ValueError('Connector archive has duplicate entries or corrupt data')
        if json.loads(archive.read('extension.json')) != manifest:
            raise ValueError('Packaged Connector manifest differs from source version/UUID/configuration')
        if archive.read(entry) != bundle:
            raise ValueError('Packaged Connector bundle differs from current compiled bundle')
        logo = manifest['images']['logo'].removeprefix('./')
        if not archive.read(logo) or archive.read(logo) != (root / 'extension' / logo).read_bytes():
            raise ValueError('Packaged Connector logo differs from source')
    return connector, {'version': version, 'uuid': manifest['uuid'], 'edaEngine': manifest['engines']['eda'],
                       'bundleSha256': hashlib.sha256(bundle).hexdigest(), 'sha256': sha256(connector)}


def check_go_binary(path, version, revision, go='go'):
    actual = subprocess.check_output([str(path), 'version'], cwd=path.parent, text=True, encoding='utf-8').strip()
    if actual != f'easyeda-agent v{version}':
        raise ValueError(f'{path.name}: exact CLI version mismatch: {actual}')
    metadata = subprocess.check_output([go, 'version', '-m', str(path)], text=True, encoding='utf-8')
    fields = dict(re.findall(r'^\s*build\s+([^=\s]+)=(.*)$', metadata, re.MULTILINE))
    package = re.search(r'^\s*path\s+(\S+)\s*$', metadata, re.MULTILINE)
    if (not package or package[1] != GO_PACKAGE or fields.get('vcs.revision') != revision
            or fields.get('vcs.modified') != 'false' or fields.get('vcs') != 'git'
            or fields.get('GOOS') != 'windows' or fields.get('GOARCH') != 'amd64'):
        raise ValueError(f'{path.name}: Go build provenance does not match clean source commit {revision}')
    toolchain = re.search(r': (go\S+)', metadata.splitlines()[0])
    return {'version': actual, 'sha256': sha256(path), 'goToolchain': toolchain[1] if toolchain else None,
            'buildInfo': metadata.splitlines()[1:]}


def package_open(root, go='go'):
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True, encoding='utf-8').strip()
    if subprocess.check_output(['git', 'status', '--porcelain'], cwd=root):
        raise ValueError('Commit reviewed sources before packaging; source tree is dirty')
    sources = read_json(root / 'sources.open.json')
    version = read_json(root / '.codex-plugin/plugin.json')['version']
    if sources.get('version') != version:
        raise ValueError('Plugin version differs from sources.open.json')
    mcp = check_package(root, 'mcp', version)
    sdk = read_json(root / 'mcp/node_modules/@modelcontextprotocol/sdk/package.json')
    sdk_locked = read_json(root / 'mcp/package-lock.json')['packages']['node_modules/@modelcontextprotocol/sdk']['version']
    if sdk.get('version') != sdk_locked or mcp['dependencies']['@modelcontextprotocol/sdk'] != sdk_locked:
        raise ValueError('Installed MCP SDK differs from exact dependency lock')
    connector, connector_identity = check_connector(root, sources)
    cli = root / 'bin/easyeda.exe'
    pcb_cli = root / 'bin/easyeda-pcb-review.exe'
    components = {'connector': connector_identity, 'mcp': {'version': mcp['version'], 'node': mcp['engines']['node'], 'sdk': sdk_locked},
                  'go': {path.name: check_go_binary(path, version, revision, go) for path in [cli, pcb_cli]}}
    knowledge = check_package(root, 'knowledge', version)
    service_version = re.search(r"^const version = '([^']+)';$", (root / 'knowledge/src/service.mjs').read_text(encoding='utf-8'), re.MULTILINE)
    if not service_version:
        raise ValueError('Knowledge service source version cannot be identified')
    components['knowledge'] = {'packageVersion': knowledge['version'], 'serviceVersion': service_version[1], 'node': knowledge['engines']['node'],
                               'sourceAndClientBundled': True, 'serviceDependenciesBundled': False,
                               'serviceOptional': True, 'runtimeValidated': False}
    files = {}
    for name in ['.codex-plugin', 'skills', 'api-reference', 'mcp/src', 'mcp/node_modules', 'knowledge/src', 'knowledge/ui']:
        if not (root / name).is_dir():
            raise ValueError(f'Missing build input directory: {root / name}')
        for path in sorted((root / name).rglob('*')):
            if path.is_file():
                files[path.relative_to(root).as_posix()] = path
    for name in ['.mcp.json', 'sources.open.json', 'LICENSE', 'NOTICE', 'LICENSE-APACHE-2.0', 'LICENSE-SDK', 'README-OPEN.md', 'VALIDATION.md',
                 'mcp/package.json', 'mcp/package-lock.json', 'knowledge/package.json', 'knowledge/package-lock.json', 'knowledge/LICENSE-APACHE-2.0',
                 'bin/easyeda.exe', 'bin/easyeda-pcb-review.exe', *('tools/' + name for name in PCB_TOOLS)]:
        path = root / name
        if not path.is_file():
            raise ValueError(f'Missing build input: {path}')
        files[name] = path
    files['connector/' + connector.name] = connector
    listed = subprocess.check_output(['git', 'ls-files', '-z'], cwd=root).decode('utf-8').split('\0')
    source_hashes = {}
    for name in sorted(filter(None, listed)):
        path = root / name
        if not path.is_file():
            raise ValueError(f'Missing tracked source: {path}')
        source_hashes[name] = sha256(path)
        files['source/' + name] = path
    source_digest = hashlib.sha256(json.dumps(source_hashes, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    build = {'version': version, 'connectorVersion': connector_identity['version'], 'sourceBaseCommit': revision, 'sourceDirty': False,
             'sourceTreeSha256': source_digest, 'sourceFiles': len(source_hashes), 'components': components,
             'pcbCompilerInputs': {name: sha256(root / 'tools' / name) for name in PCB_TOOLS}, 'platform': 'windows-x64'}
    prefix = f'easyeda-agent-open-{version}-{revision[:12]}'
    archive = root / f'dist/{prefix}-windows-x64.zip'
    if archive.exists():
        raise ValueError(f'Refusing to replace an existing package: {archive}')
    archive.parent.mkdir(parents=True, exist_ok=True)
    hashes = {name: sha256(path) for name, path in sorted(files.items())}
    build_bytes = (json.dumps(build, indent=2) + '\n').encode()
    hashes['BUILD.json'] = hashlib.sha256(build_bytes).hexdigest()
    # Stream source into the ZIP; do not leave another extracted source tree.
    with zipfile.ZipFile(archive, 'x', zipfile.ZIP_DEFLATED, compresslevel=6) as package:
        for name, path in sorted(files.items()):
            package.write(path, prefix + '/' + name)
        package.writestr(prefix + '/BUILD.json', build_bytes)
        package.writestr(prefix + '/SHA256SUMS.json', json.dumps(hashes, indent=2) + '\n')
    with zipfile.ZipFile(archive) as package:
        if package.testzip() is not None:
            raise RuntimeError('ZIP integrity failure')
    return {'archive': str(archive), 'sha256': sha256(archive), 'files': len(hashes) + 1, 'build': build}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default='go', help='Go executable used to inspect binary VCS build information')
    args = parser.parse_args()
    try:
        print(json.dumps(package_open(Path(__file__).resolve().parents[1], args.go), ensure_ascii=True))
    except (ValueError, OSError, KeyError, zipfile.BadZipFile, subprocess.CalledProcessError) as error:
        raise SystemExit(str(error))
