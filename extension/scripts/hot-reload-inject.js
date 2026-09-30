// Reviewed development-only update of an ALREADY installed connector (§5 of
// docs/dev-environment.md). Save all open documents first. Supply independently
// recorded old/new SHA-256 hashes; never enable permissions or create a database.
// Invoke through the normal debug exec path; this does not edit design objects.
async function hotReloadConnector({ TEAM, UUID, VERSION, EXPECTED_VERSION, EXPECTED_SHA256, BUNDLE_SHA256, PORT = 8790, RELOAD = true }) {
	if (![TEAM, UUID, VERSION, EXPECTED_VERSION].every(v => typeof v === 'string' && v.length)
		|| ![EXPECTED_SHA256, BUNDLE_SHA256].every(v => /^[a-f0-9]{64}$/.test(v))) {
		throw new Error('Explicit team, UUID, versions and old/new SHA-256 hashes are required');
	}
	const sha = async bytes => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), v => v.toString(16).padStart(2, '0')).join('');
	const message = await new Promise((resolve, reject) => {
		const ws = new WebSocket(`ws://127.0.0.1:${PORT}`);
		const timer = setTimeout(() => finish(new Error('WS timeout')), 15000);
		function finish(err, value) { clearTimeout(timer); ws.close(); err ? reject(err) : resolve(value); }
		ws.onopen = () => ws.send(JSON.stringify({ action: 'getFile' }));
		ws.onmessage = ev => {
			try { const m = JSON.parse(ev.data); if (m.action === 'getFile_Response') finish(m.error ? new Error(m.error) : null, m); }
			catch (err) { finish(err); }
		};
		ws.onerror = () => finish(new Error('Local hot-reload WS error'));
	});
	const bin = Uint8Array.from(atob(message.content), c => c.charCodeAt(0));
	if (message.version !== VERSION || message.size !== bin.length || await sha(bin) !== BUNDLE_SHA256) throw new Error('Served bundle version/size/hash mismatch');
	const database = `User_${TEAM}_v6`;
	if ((await indexedDB.databases()).filter(d => d.name === database).length !== 1) throw new Error('Expected exactly one existing target database');
	const db = await new Promise((resolve, reject) => {
		const q = indexedDB.open(database);
		q.onupgradeneeded = () => { q.transaction.abort(); reject(new Error('Refusing to create/upgrade database')); };
		q.onsuccess = () => resolve(q.result);
		q.onerror = () => reject(q.error);
	});
	const fileKey = `${UUID}|dist/index.js`;
	const stores = ['extensionsIndex', 'extensionsObjectStorage'];
	const request = q => new Promise((resolve, reject) => { q.onsuccess = () => resolve(q.result); q.onerror = () => reject(q.error); });
	try {
		// Keep this single readwrite transaction alive while hashing the old File.
		// Both reads and both writes share its lock: drift cannot slip between a
		// read-only preflight and the update, and either both writes commit or neither.
		await new Promise((resolve, reject) => {
			const tx = db.transaction(stores, 'readwrite');
			const index = tx.objectStore(stores[0]), files = tx.objectStore(stores[1]);
			let pending = true, failure, commit;
			tx.oncomplete = () => resolve();
			tx.onabort = () => reject(failure || tx.error || new Error('Update aborted'));
			tx.onerror = () => { failure ||= tx.error; };
			function keepAlive() {
				if (pending) index.get(UUID).onsuccess = () => {
					if (commit) { const write = commit; commit = null; write(); }
					else keepAlive();
				};
			}
			keepAlive();
			(async () => {
				const [idx, rec] = await Promise.all([request(index.get(UUID)), request(files.get(fileKey))]);
				if (!idx || !rec || idx.config?.uuid !== UUID || idx.config?.version !== EXPECTED_VERSION) throw new Error('Installed connector identity/version mismatch');
				if (idx.isEnable !== true || idx.isAllowExternalInteractions !== true) throw new Error('Existing connector permissions are not enabled; refusing to change permissions');
				if (!rec.source || await sha(await rec.source.arrayBuffer()) !== EXPECTED_SHA256) throw new Error('Installed bundle hash mismatch');
				const oldSize = rec.source.size;
				rec.source = new File([bin], 'index.js', { type: 'text/javascript' });
				idx.config.version = VERSION;
				if (typeof idx.fileSize === 'number') idx.fileSize += bin.length - oldSize;
				// Schedule puts in an IDB callback where this transaction is active.
				commit = () => {
					try {
						const put = (store, value, key) => store.keyPath == null ? store.put(value, key) : store.put(value);
						put(files, rec, fileKey); put(index, idx, UUID); pending = false;
					}
					catch (err) { failure = err; pending = false; tx.abort(); }
				};
			})().catch(err => { failure = err; pending = false; tx.abort(); });
		});
		const tx = db.transaction(stores, 'readonly');
		const [idx, rec] = await Promise.all([request(tx.objectStore(stores[0]).get(UUID)), request(tx.objectStore(stores[1]).get(fileKey))]);
		if (idx.config.version !== VERSION || !idx.isEnable || !idx.isAllowExternalInteractions || await sha(await rec.source.arrayBuffer()) !== BUNDLE_SHA256) throw new Error('Stored update readback failed; do not reload');
		// Return evidence before the connector disconnects on the scheduled reload.
		if (RELOAD) setTimeout(() => window.top.location.reload(), 1500);
		return { ok: true, database, uuid: UUID, bytes: bin.length, oldVersion: EXPECTED_VERSION, newVersion: VERSION, sha256: BUNDLE_SHA256, permissionsPreserved: true, reloadScheduled: RELOAD };
	}
	finally { db.close(); }
}
