"""Open release identity checks; all payloads and outputs are temporary fixtures."""
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile


spec = importlib.util.spec_from_file_location('pack_open', Path(__file__).resolve().parents[1] / 'pack-open.py')
pack = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pack)


class OpenPackageTests(unittest.TestCase):
    version = '1.8.1-open.9.6'
    connector_version = version + '-kb.30'
    revision = 'a' * 40

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.sources = {'version': self.version, 'connectorVersion': self.connector_version, 'connectorUuid': 'b' * 32}
        self.write_json('sources.open.json', self.sources)
        self.write_json('.codex-plugin/plugin.json', {'version': self.version})
        self.manifest = {'name': 'easyeda-agent-open-connector', 'version': self.connector_version,
                         'uuid': self.sources['connectorUuid'], 'entry': './dist/index',
                         'images': {'logo': './images/logo.png'}, 'engines': {'eda': '~3.2.0'}}
        self.write_json('extension/extension.json', self.manifest)
        for directory, version in [('mcp', self.version), ('extension', self.connector_version), ('knowledge', self.version)]:
            data = {'name': directory, 'version': version, 'engines': {'node': '>=22.13.0' if directory == 'knowledge' else '>=20.17.0'}}
            if directory == 'mcp':
                data['dependencies'] = {'@modelcontextprotocol/sdk': '1.30.0'}
            self.write_json(directory + '/package.json', data)
            packages = {'': data}
            if directory == 'mcp':
                packages['node_modules/@modelcontextprotocol/sdk'] = {'version': '1.30.0'}
            self.write_json(directory + '/package-lock.json', {'name': directory, 'version': version, 'packages': packages})
        self.write_json('mcp/node_modules/@modelcontextprotocol/sdk/package.json', {'version': '1.30.0'})
        for name in ['.mcp.json', 'LICENSE', 'NOTICE', 'LICENSE-APACHE-2.0', 'LICENSE-SDK', 'README.md', 'README-LOCAL.md', 'README-OPEN.md', 'VALIDATION.md', 'mcp/README.md',
                     'knowledge/LICENSE-APACHE-2.0', 'knowledge/src/context-contract.mjs', 'knowledge/ui/index.html',
                     'skills/open/SKILL.md', 'api-reference/index.json', 'mcp/src/server.mjs', 'extension/images/logo.png',
                     'bin/easyeda.exe', 'bin/easyeda-pcb-review.exe', *('tools/' + name for name in pack.PCB_TOOLS)]:
            self.write(name, b'fixture\n')
        self.write('extension/dist/index.js', f'var CONNECTOR_VERSION = "{self.connector_version}";'.encode())
        self.write('knowledge/src/service.mjs', f"const version = '{self.version}+knowledge.11';\n".encode())
        self.archive = self.root / f'extension/build/dist/{self.manifest["name"]}_v{self.connector_version}.eext'
        self.make_connector()
        self.git_files = ['sources.open.json', 'knowledge/src/context-contract.mjs', 'mcp/src/server.mjs']
        self.binary_version = f'easyeda-agent v{self.version}'
        self.binary_revision = self.revision
        self.dirty = False

    def write(self, name, data):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def write_json(self, name, data):
        self.write(name, json.dumps(data).encode())

    def make_connector(self, manifest=None, bundle=None):
        self.archive.parent.mkdir(parents=True, exist_ok=True)
        with zipfile.ZipFile(self.archive, 'w') as archive:
            archive.writestr('extension.json', json.dumps(manifest or self.manifest))
            archive.writestr('dist/index.js', bundle if bundle is not None else (self.root / 'extension/dist/index.js').read_bytes())
            archive.writestr('images/logo.png', (self.root / 'extension/images/logo.png').read_bytes())

    def output(self, args, **kwargs):
        if args[:3] == ['git', 'rev-parse', 'HEAD']:
            return self.revision + '\n'
        if args[:3] == ['git', 'status', '--porcelain']:
            return b' M fixture' if self.dirty else b''
        if args[:3] == ['git', 'ls-files', '-z']:
            return ('\0'.join(self.git_files) + '\0').encode()
        if args[1:] == ['version']:
            return self.binary_version
        if args[1:3] == ['version', '-m']:
            return (f'{args[-1]}: go1.26.3\n\tpath\t{pack.GO_PACKAGE}\n'
                    f'\tbuild\tGOOS=windows\n\tbuild\tGOARCH=amd64\n\tbuild\tvcs=git\n'
                    f'\tbuild\tvcs.revision={self.binary_revision}\n\tbuild\tvcs.modified=false\n')
        raise AssertionError(args)

    def package(self):
        with patch.object(subprocess, 'check_output', side_effect=self.output):
            return pack.package_open(self.root)

    def test_workflow_source_is_installable_and_excludes_untracked_state(self):
        name = 'workflow/skills/evidence-to-delivery/SKILL.md'
        self.write(name, b'workflow source\n')
        self.git_files.append(name)
        license_name = 'third-party/go/example/LICENSE'
        self.write(license_name, b'original dependency notice\n')
        self.git_files.append(license_name)
        self.write('workflow/.workflow/state.json', b'private runtime state\n')
        result = self.package()
        with zipfile.ZipFile(result['archive']) as archive:
            prefix = archive.namelist()[0].split('/')[0] + '/'
            self.assertEqual(archive.read(prefix + name), b'workflow source\n')
            self.assertEqual(archive.read(prefix + 'source/' + name), b'workflow source\n')
            self.assertEqual(archive.read(prefix + license_name), b'original dependency notice\n')
            self.assertNotIn(prefix + 'workflow/.workflow/state.json', archive.namelist())

    def test_matching_components_stream_sources_and_hash_real_payload(self):
        result = self.package()
        self.assertFalse((self.root / 'source').exists())
        self.assertEqual(list((self.root / 'dist').iterdir()), [Path(result['archive'])])
        with zipfile.ZipFile(result['archive']) as archive:
            prefix = archive.namelist()[0].split('/')[0] + '/'
            build = json.loads(archive.read(prefix + 'BUILD.json'))
            hashes = json.loads(archive.read(prefix + 'SHA256SUMS.json'))
            self.assertEqual(build['sourceBaseCommit'], self.revision)
            self.assertEqual(build['components']['connector']['version'], self.connector_version)
            self.assertEqual(set(build['components']['go']), {'easyeda.exe', 'easyeda-pcb-review.exe'})
            self.assertFalse(build['components']['knowledge']['serviceDependenciesBundled'])
            self.assertFalse(build['components']['knowledge']['runtimeValidated'])
            self.assertEqual(build['components']['knowledge']['serviceVersion'], self.version + '+knowledge.11')
            self.assertEqual(build['components']['go']['easyeda.exe']['goToolchain'], 'go1.26.3')
            self.assertIn(prefix + 'knowledge/src/context-contract.mjs', archive.namelist())
            self.assertEqual(len(hashes) + 1, len(archive.namelist()))
            for name, digest in hashes.items():
                self.assertEqual(hashlib.sha256(archive.read(prefix + name)).hexdigest(), digest, name)
        with self.assertRaisesRegex(ValueError, 'Refusing to replace'):
            self.package()

    def test_exact_cli_version_rejects_suffix_that_old_substring_gate_accepted(self):
        self.binary_version += '-stale'
        with self.assertRaisesRegex(ValueError, 'exact CLI version mismatch'):
            self.package()

    def test_same_version_old_binary_is_rejected_by_source_commit(self):
        self.binary_revision = 'c' * 40
        with self.assertRaisesRegex(ValueError, 'build provenance'):
            self.package()

    def check_binary_with_real_utf8_output(self):
        path = self.root / '工作-硬件工作流整合' / 'easyeda.exe'
        path.parent.mkdir()
        path.write_bytes(b'fixture binary')
        actual_check_output = subprocess.check_output
        def emit_utf8(args, **kwargs):
            data = self.output(args, **kwargs).encode('utf-8')
            # Exercise real child-process bytes; emulate a CP936 Windows default.
            kwargs.setdefault('encoding', 'gbk')
            return actual_check_output([sys.executable, '-c',
                                        'import sys; sys.stdout.buffer.write(bytes.fromhex(sys.argv[1]))',
                                        data.hex()], **kwargs)
        with self.assertRaises(UnicodeDecodeError):
            emit_utf8(['go', 'version', '-m', str(path)], text=True)
        with patch.object(subprocess, 'check_output', side_effect=emit_utf8):
            return pack.check_go_binary(path, self.version, self.revision)

    def test_chinese_path_utf8_build_info_is_read_on_a_gbk_host(self):
        result = self.check_binary_with_real_utf8_output()
        self.assertEqual(result['goToolchain'], 'go1.26.3')
        self.assertIn('\tbuild\tvcs.revision=' + self.revision, result['buildInfo'])

    def test_utf8_decoding_does_not_accept_an_old_binary(self):
        self.binary_revision = 'c' * 40
        with self.assertRaisesRegex(ValueError, 'build provenance'):
            self.check_binary_with_real_utf8_output()

    def test_second_binary_is_checked_independently(self):
        original = self.output
        def stale_pcb(args, **kwargs):
            result = original(args, **kwargs)
            if args[1:3] == ['version', '-m'] and args[-1].endswith('easyeda-pcb-review.exe'):
                return result.replace(self.revision, 'd' * 40)
            return result
        with patch.object(subprocess, 'check_output', side_effect=stale_pcb):
            with self.assertRaisesRegex(ValueError, 'easyeda-pcb-review.exe: Go build provenance'):
                pack.package_open(self.root)

    def test_source_and_lock_identity_mismatches_are_rejected(self):
        for directory in ['extension', 'mcp', 'knowledge']:
            with self.subTest(directory=directory):
                path = self.root / directory / 'package-lock.json'
                original = path.read_bytes()
                lock = json.loads(original)
                lock['packages']['']['version'] = 'old'
                path.write_text(json.dumps(lock))
                with self.assertRaisesRegex(ValueError, 'package/lock identity'):
                    self.package()
                path.write_bytes(original)

    def test_installed_mcp_sdk_must_match_exact_lock(self):
        self.write_json('mcp/node_modules/@modelcontextprotocol/sdk/package.json', {'version': '1.29.0'})
        with self.assertRaisesRegex(ValueError, 'Installed MCP SDK'):
            self.package()

    def test_eext_wrong_identity_or_bundle_is_rejected(self):
        for key, value in [('uuid', 'c' * 32), ('version', '1.8.1-open.9.5')]:
            with self.subTest(key=key):
                self.make_connector(dict(self.manifest, **{key: value}))
                with self.assertRaisesRegex(ValueError, 'Packaged Connector manifest differs'):
                    self.package()
        self.make_connector(bundle=b'old code with same manifest')
        with self.assertRaisesRegex(ValueError, 'Packaged Connector bundle differs'):
            self.package()

    def test_compiled_connector_must_report_true_source_version(self):
        self.write('extension/dist/index.js', b'var CONNECTOR_VERSION = "1.8.1-open.9.5";')
        with self.assertRaisesRegex(ValueError, 'Compiled Connector version differs'):
            self.package()

    def test_dirty_source_is_not_a_releasable_commit(self):
        self.dirty = True
        with self.assertRaisesRegex(ValueError, 'source tree is dirty'):
            self.package()
        self.assertFalse((self.root / 'dist').exists())


if __name__ == '__main__':
    unittest.main()
