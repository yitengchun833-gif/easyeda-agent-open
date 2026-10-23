import path from 'node:path';
import fs from 'fs-extra';
import ignore from 'ignore';
import JSZip from 'jszip';

import * as extensionConfig from '../extension.json';

/**
 * 将多行字符串拆分成字符串数组
 *
 * @param str - 多行字符串
 * @returns 字符串数组
 */
function multiLineStrToArray(str: string): Array<string> {
	return str.split(/[\r\n]+/);
}

/**
 * 检查 UUID 是否合法
 *
 * @param uuid - UUID
 * @returns 是否合法
 */
function testUuid(uuid?: string): uuid is string {
	const regExp = /^[a-z0-9]{32}$/;
	if (uuid && uuid !== '00000000000000000000000000000000') {
		return regExp.test(uuid.trim());
	}
	else {
		return false;
	}
}

/**
 * 获取正确的 UUID
 *
 * @param uuid - UUID
 * @returns UUID
 */
function fixUuid(uuid?: string): string {
	uuid = uuid?.trim() || undefined;
	if (testUuid(uuid)) {
		return uuid.trim();
	}
	else {
		return crypto.randomUUID().replaceAll('-', '');
	}
}

/**
 * 主逻辑方法
 */
function main() {
	const logoPath = path.join(__dirname, '../', extensionConfig.images.logo);
	if (!fs.existsSync(logoPath) || !fs.statSync(logoPath).isFile() || fs.statSync(logoPath).size === 0) {
		throw new Error(`Connector logo is missing or empty: ${logoPath}`);
	}
	const bundle = fs.readFileSync(path.join(__dirname, '../', `${extensionConfig.entry}.js`), 'utf-8');
	const compiledVersion = bundle.match(/\bCONNECTOR_VERSION\s*=[^;\r\n]*"(\d+\.\d+\.\d+[^"\r\n]*)"/)?.[1];
	if (compiledVersion !== extensionConfig.version) {
		throw new Error(`Compiled Connector version ${compiledVersion ?? 'unknown'} differs from ${extensionConfig.version}; compile before packaging`);
	}
	if (!testUuid(extensionConfig.uuid)) {
		const newExtensionConfig = { ...extensionConfig };
		// @ts-expect-error - Removing default property from extension config
		delete newExtensionConfig.default;
		newExtensionConfig.uuid = fixUuid(extensionConfig.uuid);
		fs.writeJsonSync(path.join(__dirname, '../extension.json'), newExtensionConfig, { spaces: '\t', EOL: '\n', encoding: 'utf-8' });
	}
	const filepathListWithoutFilter = fs.readdirSync(path.join(__dirname, '../'), { encoding: 'utf-8', recursive: true });
	const edaignoreListWithoutResolve = multiLineStrToArray(fs.readFileSync(path.join(__dirname, '../.edaignore'), { encoding: 'utf-8' }));
	const edaignoreList: Array<string> = [];
	for (const edaignoreLine of edaignoreListWithoutResolve) {
		if (edaignoreLine.endsWith('/') || edaignoreLine.endsWith('\\')) {
			edaignoreList.push(edaignoreLine.slice(0, edaignoreLine.length - 1));
		}
		else {
			edaignoreList.push(edaignoreLine);
		}
	}
	const edaignore = ignore().add(edaignoreList);
	const filepathListWithoutResolve = edaignore.filter(filepathListWithoutFilter);
	const fileList: Array<string> = [];
	const folderList: Array<string> = []; // 无用数据
	for (const filepath of filepathListWithoutResolve) {
		if (fs.lstatSync(filepath).isFile()) {
			fileList.push(filepath.replace(/\\/g, '/'));
		}
		else {
			folderList.push(filepath.replace(/\\/g, '/'));
		}
	}

	const zip = new JSZip();
	for (const file of fileList) {
		zip.file(file, fs.createReadStream(path.join(__dirname, '../', file)));
	}

	fs.ensureDirSync(path.join(__dirname, 'dist'));
	zip.generateNodeStream({ type: 'nodebuffer', streamFiles: true, compression: 'DEFLATE', compressionOptions: { level: 9 } }).pipe(
		fs.createWriteStream(path.join(__dirname, 'dist', `${extensionConfig.name}_v${extensionConfig.version}.eext`)),
	);
}

main();
