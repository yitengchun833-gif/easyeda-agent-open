"""Package already-built Open binaries and locked Node dependencies; no installs."""
from pathlib import Path
import hashlib
import json
import shutil
import subprocess
import zipfile

root = Path(__file__).resolve().parents[1]
version = json.loads((root / '.codex-plugin/plugin.json').read_text(encoding='utf-8'))['version']
connector_version = json.loads((root / 'extension/extension.json').read_text(encoding='utf-8'))['version']
required_connector = json.loads((root / 'sources.open.json').read_text(encoding='utf-8')).get('connectorVersion', version)
dest = root / f'dist/easyeda-agent-open-{version}'
if dest.exists():
    raise SystemExit(f'Refusing to replace an existing package: {dest}')
cli = root / 'bin/easyeda.exe'
connector = root / f'extension/build/dist/easyeda-agent-open-connector_v{connector_version}.eext'
for required in [cli, connector, root / 'mcp/node_modules/@modelcontextprotocol/sdk/package.json']:
    if not required.is_file():
        raise SystemExit(f'Missing build input: {required}')
actual_version = subprocess.check_output([str(cli), 'version'], cwd=root, text=True).strip()
if f'v{version}' not in actual_version or connector_version != required_connector:
    raise SystemExit(f'Build versions do not match: {actual_version}, connector={connector_version}, package={version}')
dest.mkdir(parents=True)
for name in ['.codex-plugin', 'skills', 'api-reference', 'mcp/src', 'mcp/node_modules']:
    shutil.copytree(root / name, dest / name)
for name in ['.mcp.json', 'sources.open.json', 'LICENSE', 'NOTICE', 'LICENSE-APACHE-2.0', 'LICENSE-SDK', 'README-OPEN.md', 'VALIDATION.md', 'mcp/package.json', 'mcp/package-lock.json']:
    shutil.copy2(root / name, dest / name)
(dest / 'bin').mkdir()
shutil.copy2(cli, dest / 'bin/easyeda.exe')
(dest / 'connector').mkdir()
shutil.copy2(connector, dest / 'connector' / connector.name)
revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
dirty = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=root))
# Source files and locked manifests travel with the runnable package; runtime,
# dependencies, historical build outputs and credentials are not source inputs.
listed = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard'], cwd=root).decode('utf-8').split('\0')
source_hashes = {}
for name in sorted(set(listed)):
    if not name or name.startswith(('dist/', '.runtime/', '.git/')):
        continue
    path = root / name
    if path.is_file():
        output = dest / 'source' / name
        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, output)
        source_hashes[name] = hashlib.sha256(path.read_bytes()).hexdigest()
source_digest = hashlib.sha256(json.dumps(source_hashes, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
(dest / 'BUILD.json').write_text(json.dumps({'version': version, 'connectorVersion': connector_version, 'sourceBaseCommit': revision, 'sourceDirty': dirty, 'sourceTreeSha256': source_digest, 'sourceFiles': len(source_hashes), 'platform': 'windows-x64'}, indent=2)+'\n', encoding='utf-8')
hashes = {p.relative_to(dest).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(dest.rglob('*')) if p.is_file()}
(dest / 'SHA256SUMS.json').write_text(json.dumps(hashes, indent=2)+'\n', encoding='utf-8')
archive = root / f'dist/easyeda-agent-open-{version}-windows-x64.zip'
with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as package:
    for p in sorted(dest.rglob('*')):
        if p.is_file():
            package.write(p, p.relative_to(dest.parent).as_posix())
with zipfile.ZipFile(archive) as package:
    if package.testzip() is not None:
        raise RuntimeError('ZIP integrity failure')
print(json.dumps({'directory': str(dest), 'archive': str(archive), 'sha256': hashlib.sha256(archive.read_bytes()).hexdigest(), 'files': len(hashes)+1}, ensure_ascii=False))
