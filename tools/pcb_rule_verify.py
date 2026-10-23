"""Dedicated P1/PCB1 rule experiment runner, using the installed native Connector path."""
import argparse
import json
import shutil
import subprocess
import hashlib
from datetime import datetime
from pathlib import Path

TARGET = {'projectUuid':'6bde69a919dc419f98f8b06ec7b318d9','documentUuid':'d260cc249c21359b'}
CASES = ['via-size','safe-spacing','net-to-net-safe-spacing','differential-pair','equal-length-group','region-rule']

def run_case(a):
    root=Path(__file__).resolve().parents[1]
    stamp=datetime.now().strftime('%Y%m%d-%H%M%S-%f')
    out=root/'evidence'/f'pcb-rule-{a.case}-{stamp}.json'
    script=root/'tools'/'pcb_rule_case.js'
    temp=out.with_suffix('.exec.js')
    evidence={'target':TARGET,'window':a.window,'case':a.case,'status':'unknown',
        'scriptSha256':hashlib.sha256(script.read_bytes()).hexdigest(),
        'runnerSha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        'statusMeaning':'supported requires positive/negative native DRC, explicit binding and restoration; partial is an incomplete chain; unknown lacks schema/evidence; unsupported requires affirmative API evidence'}
    compiler_source=''
    if a.compiler_v0:
        loaded=subprocess.run(['node','--input-type=module','--eval',"import {compilePcbConstraints,executePcbConstraints} from './tools/pcb_constraint_compiler.mjs'; console.log('const compilePcbConstraints = '+compilePcbConstraints.toString()+'; const executePcbConstraints = '+executePcbConstraints.toString()+';');"],capture_output=True,text=True,encoding='utf-8',check=True,cwd=root)
        compiler_source=loaded.stdout
        evidence['compilerSha256']=hashlib.sha256((root/'tools/pcb_constraint_compiler.mjs').read_bytes()).hexdigest()
    def save():
        out.write_text(json.dumps(evidence,ensure_ascii=False,indent=2),encoding='utf-8')
    def execute(data):
        temp.write_text(compiler_source+'const CASE_INPUT = '+json.dumps(data,ensure_ascii=False)+';\n'+script.read_text(encoding='utf-8'),encoding='utf-8')
        r=subprocess.run([shutil.which('easyeda') or 'easyeda.exe','--ports',f'{a.port}-{a.port}','debug','exec','--window',a.window,'--timeout','60','--file',str(temp)],capture_output=True,text=True,encoding='utf-8',timeout=70)
        try: receipt=json.loads(r.stdout)
        except ValueError: receipt={'ok':False,'stdout':r.stdout,'stderr':r.stderr,'exitCode':r.returncode}
        evidence.setdefault('receipts',[]).append(receipt)
        save()
        if r.returncode or receipt.get('ok') is not True:
            raise RuntimeError('Native request failed; evidence saved; do not replay writes')
        value=receipt['result']['value']
        return json.loads(value) if isinstance(value,str) else value
    try:
        before=execute({'mode':'read','target':TARGET})
        evidence['before']=before
        save()  # Durable before evidence precedes the first write.
        result=execute({'mode':'run','case':a.case,'target':TARGET,'before':before['snapshot'],'compilerV0':a.compiler_v0,
            'prefix':'AI_RULE_'+a.case.replace('-','_')+'_'+stamp.replace('-','_'), 'x':5000,'y':5000})
        evidence['result']=result
        evidence['status']=result['status']
        save()
        print(json.dumps({'evidence':str(out),'status':result['status'],'error':result.get('error'),'restored':result['checks'].get('restored'),'restoration':result.get('restoration'),'targetViolationCount':len(result.get('targetViolations',[])),'regionCreated':result.get('regionCreated')},ensure_ascii=False))
        if not result['checks'].get('restored'): raise RuntimeError('Restoration differs; stop before the next case')
    except Exception as e:
        evidence['runnerError']=str(e); save(); raise
    finally:
        temp.unlink(missing_ok=True)

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--case',choices=['all','track-width',*CASES],default='all')
    p.add_argument('--compiler-v0',action='store_true')
    p.add_argument('--window',required=True)
    p.add_argument('--port',type=int,default=60932)
    a=p.parse_args()
    selected=(['track-width','via-size','safe-spacing'] if a.compiler_v0 else CASES) if a.case=='all' else [a.case]
    if a.compiler_v0 and any(c not in ['track-width','via-size','safe-spacing'] for c in selected):
        p.error('Compiler v0 regression supports Track, Via Size, Safe Spacing only')
    for case in selected:
        a.case=case
        run_case(a)  # Never start the next case after failed restoration or transport uncertainty.

if __name__=='__main__': main()
