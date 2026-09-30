import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import JSZip from 'jszip';

import { readProjectFootprintSourceArchive } from './native-footprint-source';
import { projectFootprintSourceInventory, readNativeFootprintSource } from './util';

const pageUuid = '1234567890abcdef';
const page = `{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","uuid":"${pageUuid}"}|\n`;
const footprint = readFileSync(join(__dirname, 'testdata/hongen-footprint-source.efoo'), 'utf8');

async function archive(files: Record<string, string>): Promise<Blob> {
	const zip = new JSZip();
	for (const [name, text] of Object.entries(files)) zip.file(name, text);
	return new Blob([await zip.generateAsync({ type: 'arraybuffer', compression: 'DEFLATE' })]);
}

test('project source archive: unique root epru provides bounded identity metadata for the current SCH_PAGE', async () => {
	const file = await archive({ '工程.epru': footprint + page, 'project2.json': '{"title":"工程"}', 'IMAGE/preview.webp': 'ignored' });
	const inventory = await readProjectFootprintSourceArchive(file, pageUuid);
	assert.equal(inventory.length, 1);
	assert.equal(inventory[0].documentSource.split('\n').filter(Boolean).length, 2, 'only DOCHEAD/META leave the archive reader');
	assert.equal(inventory[0].documentSource.includes('"type":"PAD"'), false);
	assert.deepEqual(readNativeFootprintSource(inventory, 'abe23dba1def1246').source, {
		instanceUuid: 'abe23dba1def1246', uuid: '20c29e37a9b84b4197418483096f9c05', libraryUuid: '0819f05c4eef4c71ace90d822a990e87', sourceKind: 'project-epro2',
	});
});

test('project source archive: ignores geometry source-looking strings in a different document', async () => {
	const source = footprint + page + '{"type":"ATTR"}||{"source":"bad|library"}|\n';
	const inventory = await readProjectFootprintSourceArchive(await archive({ 'one.epru': source }), pageUuid);
	assert.equal(readNativeFootprintSource(inventory, 'abe23dba1def1246').source?.uuid, '20c29e37a9b84b4197418483096f9c05');
});

test('project source archive: accepts the official EOF BLOB without a trailing separator', async () => {
	const source = footprint + page + '{"type":"BLOB"}||{"content":"data:image/png;base64,AA=="}';
	const inventory = await readProjectFootprintSourceArchive(await archive({ 'one.epru': source }), pageUuid);
	assert.equal(readNativeFootprintSource(inventory, 'abe23dba1def1246').source?.uuid, '20c29e37a9b84b4197418483096f9c05');
});

for (const [label, files] of [
	['no epru', { 'project2.json': '{}' }],
	['two root epru files', { 'one.epru': footprint + page, 'two.epru': footprint + page }],
	['nested-only epru', { 'nested/one.epru': footprint + page }],
	['missing current SCH_PAGE', { 'one.epru': footprint }],
	['duplicate current SCH_PAGE', { 'one.epru': footprint + page + page }],
	['malformed project source row', { 'one.epru': footprint + page + 'invalid' }],
] as Array<[string, Record<string, string>]>) {
	test(`project source archive: refuses ${label}`, async () => {
		await assert.rejects(readProjectFootprintSourceArchive(await archive(files), pageUuid));
	});
}

test('project source archive: bounds archive size before reading or decompressing', async () => {
	let reads = 0;
	const huge = { size: 64 * 1024 * 1024 + 1, arrayBuffer: async () => { reads++; return new ArrayBuffer(0); } } as Blob;
	await assert.rejects(readProjectFootprintSourceArchive(huge, pageUuid), /exceeds 64 MiB/);
	assert.equal(reads, 0);
});

test('project source archive: bounds ZIP entry and native row counts', async () => {
	const files: Record<string, string> = { 'one.epru': footprint + page };
	for (let i = 0; i < 2048; i++) files[`unused-${i}`] = '';
	await assert.rejects(readProjectFootprintSourceArchive(await archive(files), pageUuid), /2048 entries/);
	assert.throws(() => projectFootprintSourceInventory(page + '\n'.repeat(200000), pageUuid), /200000 rows/);
});

test('project source archive: rejects a valid-looking JSON source with a corrupt ZIP CRC', async () => {
	const zip = new JSZip(); zip.file('one.epru', footprint + page);
	const raw = await zip.generateAsync({ type: 'arraybuffer', compression: 'STORE' });
	const bytes = new Uint8Array(raw);
	const sourceUuidOffset = Buffer.from(raw).indexOf('20c29e37a9b84b4197418483096f9c05');
	assert.ok(sourceUuidOffset >= 0);
	bytes[sourceUuidOffset] = '3'.charCodeAt(0);
	await assert.rejects(readProjectFootprintSourceArchive(new Blob([raw]), pageUuid), /CRC32/);
});
