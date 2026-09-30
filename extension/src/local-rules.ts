import { exactJSON } from './preserve-instance';
import { wirePointOnSegment, type WireSegment } from './schematic-wire-topology';

export type Facts = Record<string, any>;
export interface RuleResult { id: string; status: 'pass' | 'fail' | 'unknown'; severity: 'error' | 'warn'; coverage: 'checked' | 'unavailable' | 'not_applicable'; targets: string[]; evidence: unknown }
const rect = (b: any) => b && [b.minX,b.minY,b.maxX,b.maxY].every(Number.isFinite) && b.maxX>b.minX && b.maxY>b.minY;
const overlap = (a:any,b:any) => a.minX<b.maxX && a.maxX>b.minX && a.minY<b.maxY && a.maxY>b.minY;
const segment = (w:any):WireSegment => [w.x0,w.y0,w.x1,w.y1];
const close = (a:number,b:number) => Math.abs(a-b)<1e-6;

/** Fixed facts only. Layout style and visual readability remain model decisions. */
export function localRules(before: Facts, after: Facts, mode: 'layout'|'design', expectedPins?: Record<string,string|null>, options: {displayOnly?:boolean; wireIds?:string[]} = {}): RuleResult[] {
	const results:RuleResult[]=[];
	const add=(id:string,status:RuleResult['status'],severity:RuleResult['severity'],coverage:RuleResult['coverage'],targets:string[],evidence:unknown)=>results.push({id,status,severity,coverage,targets,evidence});
	const old=new Map<string,Facts>((before.components??[]).filter((c:Facts)=>c.componentType==='part').map((c:Facts)=>[c.primitiveId,c]));
	const now=new Map<string,Facts>((after.components??[]).filter((c:Facts)=>c.componentType==='part').map((c:Facts)=>[c.primitiveId,c]));
	const identityErrors:string[]=[],identityUnknown:string[]=[];
	if(mode==='layout') {
		const fields=['uniqueId','designator','component','footprint','supplierId'];
		for(const [id,c] of old) {
			const n=now.get(id);
			if(!n) {identityErrors.push(id);continue}
			if(fields.some(k=>c[k]===undefined||n[k]===undefined)||!c.uniqueId||!n.uniqueId)identityUnknown.push(id);
			if(fields.some(k=>c[k]!==undefined&&n[k]!==undefined&&exactJSON(c[k])!==exactJSON(n[k])))identityErrors.push(id);
		}
		for(const id of now.keys())if(!old.has(id))identityErrors.push(id);
		add('identity',identityErrors.length?'fail':identityUnknown.length||!now.size?'unknown':'pass','error',identityUnknown.length||!now.size?'unavailable':'checked',identityErrors.length?identityErrors:identityUnknown,'Stable part identity comparison within the observed scope; missing fields are unknown.');
	} else add('identity','unknown','error','not_applicable',[],'Design edits may intentionally change identities; compare against the approved design.');
	const changed:string[]=[],unknown:string[]=[];
	for(const [id,c] of now) {
		const previous=old.get(id); const previousPins=new Map<string,Facts>((previous?.pins??[]).map((p:Facts)=>[String(p.pinNumber),p]));
		if(c.pinsAvailable!==true || !Array.isArray(c.pins)){unknown.push(id);continue}
		if(mode==='layout' && previous?.pinsAvailable!==true){unknown.push(id);continue}
		if(mode==='layout' && previousPins.size!==c.pins.length)changed.push(`${id}:pin-count`);
		for(const p of c.pins) {
			const key=`${id}.${p.pinNumber}`,refKey=`${c.designator}.${p.pinNumber}`;
			const oldPin=previousPins.get(String(p.pinNumber));
			const expected=mode==='layout'?oldPin?.net:expectedPins && Object.prototype.hasOwnProperty.call(expectedPins,key)?expectedPins[key]:expectedPins?.[refKey];
			if(mode==='layout') {
				if(typeof p.noConnected==='boolean'&&typeof oldPin?.noConnected==='boolean'&&p.noConnected!==oldPin.noConnected)changed.push(`${key}:NC`);
				if(p.noConnected===true&&oldPin?.noConnected===true)continue; // Explicit native NC evidence, never inferred from null net.
			}
			else if(expected===null) {if(p.noConnected!==true)unknown.push(`${key}:NC`);continue;}
			if(c.netlistAvailable!==true || c.netAmbiguous===true || (mode==='layout'&&(previous?.netlistAvailable!==true||previous?.netAmbiguous===true))){unknown.push(key);continue}
			if(typeof p.net!=='string' || typeof expected!=='string'){unknown.push(key);continue}
			if(p.net!==expected)changed.push(key);
		}
	}
	add('pin_nets',changed.length?'fail':unknown.length?'unknown':now.size?'pass':'unknown','error',unknown.length||!now.size?'unavailable':'checked',changed.length?changed:unknown,
		{mode,source:'document-netlist',scope:'observed active-page parts',missingIsNotNC:true});
	const relevantWires=new Set([...relatedWireIds(before.components??[],before.wires??[]),...relatedWireIds(after.components??[],after.wires??[]),...(options.wireIds??[])]);
	const wires:Facts[]=(after.wires??[]).filter((w:Facts)=>relevantWires.has(String(w.primitiveId)));
	if(after.wiresAvailable!==true) {add('valid_wires','unknown','error','unavailable',[],'Native wire read unavailable.');add('pin_exit','unknown','warn','unavailable',[],'Wire geometry unavailable.')} else {
		const zero=wires.filter(w=>close(w.x0,w.x1)&&close(w.y0,w.y1)).map(w=>String(w.primitiveId));
		const invalid=wires.filter(w=>![w.x0,w.y0,w.x1,w.y1].every(Number.isFinite)).map(w=>String(w.primitiveId));
		add('valid_wires',zero.length?'fail':invalid.length?'unknown':'pass','error',invalid.length?'unavailable':'checked',zero.length?zero:invalid,'Zero-length check covers affected wires and wires touching observed pins.');
		const exits:string[]=[],unmeasured:string[]=[];
		for(const [id,c] of now) {
			if(c.pinsAvailable!==true||!Array.isArray(c.pins))unmeasured.push(`${id}:pins-unavailable`);
			for(const p of c.pins??[]) {
			if(p.noConnected===true)continue;
			if(![p.x,p.y,p.rotation].every(Number.isFinite)){unmeasured.push(`${id}.${p.pinNumber}`);continue}
			const touching=wires.filter(w=>(close(p.x,w.x0)&&close(p.y,w.y0))||(close(p.x,w.x1)&&close(p.y,w.y1)));
			if(!touching.length)continue; // Native labels/flags may connect without an explicit stub.
			const angle=p.rotation*Math.PI/180,dx=Math.cos(angle),dy=Math.sin(angle);
			for(const w of touching){const sx=close(p.x,w.x0)&&close(p.y,w.y0)?w.x1:w.x0,sy=close(p.x,w.x0)&&close(p.y,w.y0)?w.y1:w.y0;
				if((sx-p.x)*dx+(sy-p.y)*dy<=1e-6 || Math.abs((sx-p.x)*dy-(sy-p.y)*dx)>1e-6)exits.push(`${id}.${p.pinNumber}:${w.primitiveId}`)}
		}}
		add('pin_exit',exits.length?'fail':unmeasured.length?'unknown':'pass','warn',unmeasured.length?'unavailable':'checked',exits.length?exits:unmeasured,'Measured pin direction; native direct connections exempt.');
	}
	const textIssues:string[]=[],textUnknown:string[]=[];
	for(const t of after.texts??[]) {
		if(t.visible===false || t.rendered===false)continue;
		if(t.visible!==true || !rect(t.bbox)){textUnknown.push(String(t.primitiveId));continue}
		for(const c of (after.components??[]).filter((c:Facts)=>c.componentType==='part')) {
			if(!rect(c.bbox)){textUnknown.push(String(c.primitiveId));continue}
			if(overlap(t.bbox,c.bbox))textIssues.push(`${t.primitiveId}:${c.primitiveId}`);
		}
	}
	add('text_overlap',textIssues.length?'fail':textUnknown.length||after.textsAvailable!==true?'unknown':'pass','warn',textUnknown.length||after.textsAvailable!==true?'unavailable':'checked',textIssues.length?textIssues:textUnknown,
		'Native BBox intersections include own-component text. Envelopes may include attributes: candidates are WARN and require the local image, not automatic layout rejection.');
	add('visual_review','unknown','warn','unavailable',[],'Inspect the completed scope image for symbol borders, text and readability.');
	if (options.displayOnly) for (const rule of results) {
		if (['pin_nets','pin_exit','valid_wires'].includes(rule.id)) Object.assign(rule,{status:'unknown',coverage:'not_applicable',targets:[],evidence:'Official attribute display-only edit; electrical connectivity is not changed.'});
	}
	if(before.readScope?.concurrentChange===true||after.readScope?.concurrentChange===true){
		for(const rule of results)if(rule.coverage==='checked')Object.assign(rule,{status:'unknown',coverage:'unavailable',evidence:'Native change occurred while these facts were being collected; refresh the affected scope.'});
		add('observation','unknown','error','unavailable',[],'Readback changed during collection; it is not one confirmed state.');
	}
	return results;
}

/** Compare only this scope's observed identities, geometry, text and touching wires. */
export function localObservationKey(facts:Facts):string {
	const pick=(o:Facts,keys:string[])=>Object.fromEntries(keys.map(k=>[k,o[k]]));
	const sorted=(items:Facts[])=>items.sort((a,b)=>String(a.primitiveId).localeCompare(String(b.primitiveId)));
	const components=sorted((facts.components??[]).map((c:Facts)=>({...pick(c,['primitiveId','componentType','uniqueId','designator','component','footprint','supplierId','x','y','rotation','mirror','bbox','pinsAvailable','netlistAvailable']),pins:sorted((c.pins??[]).map((p:Facts)=>pick(p,['primitiveId','pinNumber','x','y','rotation','net','noConnected'])))})));
	const texts=sorted((facts.texts??[]).filter((t:Facts)=>t.visible!==false&&t.rendered!==false).map((t:Facts)=>pick(t,['primitiveId','parentId','key','value','x','y','visible','keyVisible','valueVisible','bbox'])));
	const wireIds=new Set(relatedWireIds(facts.components??[],facts.wires??[]));
	const wires=sorted((facts.wires??[]).filter((w:Facts)=>wireIds.has(w.primitiveId)).map((w:Facts)=>pick(w,['primitiveId','x0','y0','x1','y1','net'])));
	return exactJSON({components,texts,wires});
}

/** Only official display fields qualify; a caller flag alone cannot skip electrical checks. */
export function displayOnlyAction(action:string,payload:Facts):boolean {
	if(action==='debug.batch')return Array.isArray(payload.steps)&&payload.steps.length>0&&payload.steps.every((s:Facts)=>s&&displayOnlyAction(s.action,s.payload??{}));
	if(action!=='schematic.attribute.modify'||!payload.props||Array.isArray(payload.props)||typeof payload.props!=='object')return false;
	const fields=['x','y','rotation','color','fontName','fontSize','bold','italic','underLine','alignMode','fillColor','keyVisible','valueVisible'];
	const keys=Object.keys(payload.props);
	return keys.length>0&&keys.every(k=>fields.includes(k));
}

export function includesNativeScript(action:string,payload:Facts):boolean {
	return action==='debug.exec_js'||(action==='debug.batch'&&Array.isArray(payload.steps)&&payload.steps.some((s:Facts)=>s&&includesNativeScript(s.action,s.payload??{})));
}

/** Resolve nearby wire parents for text measurement without measuring every label. */
export function relatedWireIds(components:Facts[],wires:Facts[]):string[] {
	return [...new Set(wires.filter(w=>components.some(c=>(c.pins??[]).some((p:Facts)=>Number.isFinite(p.x)&&Number.isFinite(p.y)&&wirePointOnSegment({x:p.x,y:p.y},segment(w))))).map(w=>String(w.primitiveId)))];
}
