"""Bounded regression of actual action contracts; uses existing save fixtures."""
import copy
import unittest
import subprocess
import sys
import json
import workflow
import execution_contract as contract
import test_save_guard as save_fixture


class ExecutionTests(unittest.TestCase):
    def setUp(self):
        self.fixture = save_fixture.SaveGuardTests(); self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        f = self.fixture
        self.root = f.root
        f.req['execution_rules']['edit_scope']['mutable_ids'] = {'components': ['c']}
        self.action = {'action': 'schematic.component.modify', 'payload': {'primitiveId': 'c', 'patch': {'x': 25, 'y':20}},
                       'postcondition': [{'collection': 'components', 'match': {'primitiveId': 'c'}, 'equals': {'x': 25, 'y':20}}]}
        f.plan.update(batch_id='B1', runtime={'mcpBuild': 'fixture'})
        f.plan['actions'].append(self.action)
        f.fixture.confirm_rules()
        f.put('requirements.json', f.req); f.put('plan.json', f.plan); f.put('before.json', f.snap)
        self.key = contract.action_identity(f.plan, self.action, workflow.digest)
        self.state = {'generation': 0}
        self.snapshot_index = 0
        self.data = {'operation': 'prepare', 'requirements': 'requirements.json', 'plan': 'plan.json', 'baseline': 'baseline.json',
                     'action_id': self.key, 'project': 'p', 'doc': 'd', **self.action, 'snapshot': f.snap, 'snapshot_file': 'before.json'}

    def run_contract(self, **kwargs):
        if 'snapshot' in kwargs:
            self.snapshot_index += 1
            name = f'after-{self.snapshot_index}.json'
            self.fixture.put(name, kwargs['snapshot'])
            kwargs['snapshot_file'] = name
        return contract.operate(workflow, self.root, self.state, {**self.data, **kwargs})

    def test_rewire_is_intermediate_until_original_pin_net_is_restored(self):
        f = self.fixture
        f.snap['result']['pagePrimitives']['wires'] = [dict(primitiveId='w', Net='SIG', Line=[10,20,20,20])]
        f.snap['result']['wires'] = [dict(primitiveId='w', net='SIG', x0=10, y0=20, x1=20, y1=20)]
        f.snap['result']['pagePrimitives']['wires'].append(dict(primitiveId='w-extra', Net='SIG', Line=[20,20,30,20]))
        f.snap['result']['wires'].append(dict(primitiveId='w-extra', net='SIG', x0=20, y0=20, x1=30, y1=20))
        f.put('baseline.json', f.snap)
        scope = f.req['execution_rules']['edit_scope']
        scope.update(mode='layout', acceptance='page', baseline_sha256=workflow.file_hash(self.root/'baseline.json'),
                     mutable_ids={'wires':['w','w-extra']}, rewire={'authorization':{'source':'S','locator':'fixture whole-page request'}})
        f.req['depiction'] = [dict(id='signal', kind='text', text='SIG',
                                   evidence={'source':'S','locator':'fixture schematic'})]
        delete = dict(action='schematic.primitives.delete', payload={'primitiveIds':['w','w-extra']},
                      postcondition=[dict(collection='wires', match={'primitiveId':i}, absent=True) for i in ('w','w-extra')])
        create = dict(action='schematic.wire.create', payload={'points':[10,20,20,20], 'net':'SIG'},
                      postcondition=[dict(collection='wires', match={'primitiveId':'w2'}, equals={'Net':'SIG'})])
        f.plan.update(actions=[delete, create], rewire=True)
        f.fixture.confirm_rules()
        f.put('requirements.json',f.req); f.put('plan.json',f.plan); f.put('before.json',f.snap)
        first, second = [contract.action_identity(f.plan,a,workflow.digest) for a in (delete,create)]
        self.run_contract(action_id=first, action=delete['action'], payload=delete['payload'])
        broken = copy.deepcopy(f.snap);broken['seq']=2;broken['mutationSeq']=1
        broken['result']['pagePrimitives']['wires']=[];broken['result']['wires']=[]
        broken['result']['components'][0]['pins'][0]['net']=None
        step=self.run_contract(operation='record',action_id=first,action=delete['action'],payload=delete['payload'],snapshot=broken)
        self.assertEqual(step['effective_state'],'APPLIED_INTERMEDIATE')
        self.assertNotEqual(self.state['execution']['actions'][first]['effective_state'],'APPLIED_VERIFIED')
        self.run_contract(operation='prepare',action_id=second,action=create['action'],payload=create['payload'],snapshot=broken)
        restored=copy.deepcopy(f.snap);restored['seq']=3;restored['mutationSeq']=2
        restored['result']['pagePrimitives']['wires']=[dict(primitiveId='w2', Net='SIG', Line=[10,20,20,20])]
        restored['result']['wires']=[dict(primitiveId='w2', net='SIG', x0=10, y0=20, x1=20, y1=20)]
        wrong=copy.deepcopy(restored)
        wrong['result']['components'][0]['pins'][0]['net']='WRONG'
        rejected=self.run_contract(operation='record',action_id=second,action=create['action'],payload=create['payload'],snapshot=wrong)
        self.assertEqual(rejected['effective_state'],'PARTIAL')
        self.assertEqual(self.state['execution']['actions'][first]['effective_state'],'APPLIED_INTERMEDIATE')
        restored['seq']=4
        final=self.run_contract(operation='recover',action_id=second,action=create['action'],payload=create['payload'],snapshot=restored)
        self.assertEqual(final['effective_state'],'APPLIED_VERIFIED')
        self.assertEqual(self.state['execution']['actions'][first]['effective_state'],'APPLIED_VERIFIED')

    def test_confirmed_design_rewire_can_change_the_final_pin_net(self):
        f = self.fixture
        f.snap['result']['pagePrimitives']['wires'] = [dict(primitiveId='w', Net='SIG', Line=[10,20,20,20])]
        f.snap['result']['wires'] = [dict(primitiveId='w', net='SIG', x0=10, y0=20, x1=20, y1=20)]
        f.put('baseline.json', f.snap)
        scope = f.req['execution_rules']['edit_scope']
        scope.update(mode='design', acceptance='page', baseline_sha256=workflow.file_hash(self.root/'baseline.json'),
                     mutable_ids={'wires':['w']},
                     authorization={'source':'S','locator':'fixture circuit change'},
                     rewire={'authorization':{'source':'S','locator':'fixture whole-page rewire'}})
        f.req['depiction'] = [dict(id='new-net', kind='text', text='NEW', evidence={'source':'S','locator':'fixture'})]
        delete = dict(action='schematic.primitives.delete', payload={'primitiveIds':['w']},
                      postcondition=[dict(collection='wires', match={'primitiveId':'w'}, absent=True)])
        create = dict(action='schematic.wire.create', payload={'points':[10,20,20,20], 'net':'NEW'},
                      postcondition=[dict(collection='wires', match={'primitiveId':'w2'}, equals={'Net':'NEW'})])
        f.plan.update(actions=[delete, create], rewire=True)
        f.fixture.confirm_rules()
        f.put('requirements.json',f.req); f.put('plan.json',f.plan); f.put('before.json',f.snap)
        first, second = [contract.action_identity(f.plan,a,workflow.digest) for a in (delete,create)]
        self.run_contract(action_id=first,action=delete['action'],payload=delete['payload'])
        broken=copy.deepcopy(f.snap);broken['seq']=2;broken['mutationSeq']=1
        broken['result']['pagePrimitives']['wires']=[];broken['result']['wires']=[]
        broken['result']['components'][0]['pins'][0]['net']=None
        self.assertEqual(self.run_contract(operation='record',action_id=first,action=delete['action'],payload=delete['payload'],snapshot=broken)['effective_state'],'APPLIED_INTERMEDIATE')
        self.run_contract(operation='prepare',action_id=second,action=create['action'],payload=create['payload'],snapshot=broken)
        changed=copy.deepcopy(f.snap);changed['seq']=3;changed['mutationSeq']=2
        changed['result']['components'][0]['pins'][0]['net']='NEW'
        changed['result']['pagePrimitives']['wires']=[dict(primitiveId='w2',Net='NEW',Line=[10,20,20,20])]
        changed['result']['wires']=[dict(primitiveId='w2',net='NEW',x0=10,y0=20,x1=20,y1=20)]
        self.assertEqual(self.run_contract(operation='record',action_id=second,action=create['action'],payload=create['payload'],snapshot=changed)['effective_state'],'APPLIED_VERIFIED')

    def test_fresh_process_persists_unknown_before_dispatch(self):
        state = dict(schema=1,generation=0,goal='fixture',sources={},plan={},pending={},changes=[],retired=[],reviews={},results=[],context={})
        workflow.write(self.root/'.workflow/state.json',state)
        workflow.render(self.root,state)
        result = subprocess.run([sys.executable,'-X','utf8','-B',workflow.__file__,'--root',str(self.root),'execution'],
            input=json.dumps(self.data),text=True,encoding='utf-8',capture_output=True)
        self.assertEqual(result.returncode,0,result.stderr)
        state=workflow.read(self.root/'.workflow/state.json')
        self.assertEqual(state['execution']['actions'][self.key]['effective_state'],'UNKNOWN')
        self.assertEqual(state['generation'],1)

    def test_node_group_real_journal(self):
        from pathlib import Path
        f=self.fixture
        second=copy.deepcopy(self.action)
        second['payload']['patch']['x']=30
        second['postcondition'][0]['equals']['x']=30
        f.req['execution_rules']['edit_scope']['mode']='layout'
        f.plan['actions'].append(second)
        f.plan['runtime']=dict(daemonVersion='daemon',connectorVersion='connector',mcpBuild='fixture',capabilities=['execution.bound.v1'])
        f.fixture.confirm_rules()
        f.put('requirements.json',f.req);f.put('plan.json',f.plan)
        state=dict(schema=1,generation=0,goal='fixture',sources={},plan={},pending={},changes=[],retired=[],reviews={},results=[],context={})
        workflow.write(self.root/'.workflow/state.json',state);workflow.render(self.root,state)
        keys=[contract.action_identity(f.plan,a,workflow.digest) for a in [self.action,second]]
        entry=Path(workflow.__file__).resolve().parents[3]/'components/easyeda-mcp/src/execution.mjs'
        script="""
import {readFileSync} from 'node:fs';
const d=JSON.parse(readFileSync(0,'utf8'));
const {guardedSchematicAction}=await import(d.entry);
const p=d.packet;let reads=0,writes=0,healthCalls=0;
const run=async args=>{
 if(args[0]==='daemon'){healthCalls++;return {ok:true,result:{found:{raw:{version:'daemon',windows:[{windowId:'w',connectorVersion:'connector',capabilities:['schematic.review.v2'],context:{projectUuid:'p',documentUuid:'d'}}]}}}};}
 const name=args[args.indexOf('call')+1];
 if(name==='schematic.components.list'){reads++;p.seq++;return {ok:true,result:structuredClone(p)};}
 const patch=JSON.parse(args[args.indexOf('--payload')+1]).patch;
 for(const rows of [p.result.components,p.result.pagePrimitives.components])Object.assign(rows.find(r=>r.primitiveId==='c'),patch);
 writes++;p.mutationSeq++;p.seq++;return {ok:true,result:{}};
};
const result=await guardedSchematicAction({name:'schematic.component.modify',mutates:true},d.input,{mcp:{build:'fixture',capabilities:['execution.bound.v1']},run});
console.log(JSON.stringify({result:result.structuredContent,isError:result.isError,reads,writes,healthCalls}));
"""
        data=dict(entry=entry.as_uri(),packet=f.snap,input=dict(project='p',doc='d',payload=self.action['payload'],execution=dict(root=str(self.root),requirements=str(self.root/'requirements.json'),plan=str(self.root/'plan.json'),baseline=str(self.root/'baseline.json'),action_ids=keys)))
        result=subprocess.run(['node','--input-type=module','-e',script],input=json.dumps(data),text=True,encoding='utf-8',capture_output=True)
        self.assertEqual(result.returncode,0,result.stderr)
        out=json.loads(result.stdout);self.assertFalse(out['isError'],out)
        self.assertEqual((out['reads'],out['writes'],out['healthCalls']),(3,2,1))
        entries=workflow.read(self.root/'.workflow/state.json')['execution']['actions']
        for key in keys:self.assertEqual(entries[key]['effective_state'],'APPLIED_VERIFIED')
        self.assertTrue(Path(entries[keys[0]]['readback_file']).samefile(entries[keys[1]]['before']))

    def test_save_requires_all_planned_action_receipts(self):
        f = self.fixture
        for name in ('plan.json', 'requirements.json'):
            f.report['inputs'][str(f.root/name)] = workflow.file_hash(f.root/name)
        f.commit_report()
        with self.assertRaisesRegex(Exception, 'planned action lacks'):
            f.check()

    def after(self, change=True):
        packet = copy.deepcopy(self.fixture.snap)
        packet.update(seq=3, mutationSeq=2)
        if change:
            packet['result']['components'][0]['x'] = 25
            packet['result']['pagePrimitives']['components'][0]['x'] = 25
        return packet

    def test_transport_error_landed_verified_and_reordered_plan_cannot_replay(self):
        self.assertTrue(self.run_contract()['dispatch'])
        result = self.run_contract(operation='record', snapshot=self.after(), transport_status='error')
        self.assertEqual(result['effective_state'], 'APPLIED_VERIFIED')
        self.assertEqual(result['transport_status'], 'error')
        f = self.fixture; f.plan['actions'].reverse(); f.put('plan.json', f.plan)
        self.assertEqual(contract.action_identity(f.plan, self.action, workflow.digest), self.key)
        with self.assertRaisesRegex(ValueError, 'cannot replay'): self.run_contract()

    def test_unknown_blocks_retry_fresh_unchanged_recovery_allows_remaining(self):
        self.run_contract()
        with self.assertRaisesRegex(ValueError, 'recovery required'): self.run_contract()
        result = self.run_contract(operation='recover', snapshot=self.after(False))
        self.assertEqual(result['effective_state'], 'NOT_APPLIED')
        self.assertTrue(self.run_contract()['dispatch'])

    def test_reconnect_abandoned_wrong_target_and_scope_drift_never_verify(self):
        self.run_contract()
        for field, value in [('runtimeInstance', 'other'), ('seqAbandoned', 1), ('seq', 0)]:
            packet = self.after(); packet[field] = value
            with self.assertRaises(ValueError): self.run_contract(operation='recover', snapshot=packet)
        packet = self.after(); packet['context']['documentUuid'] = 'other'
        with self.assertRaises(ValueError): self.run_contract(operation='recover', snapshot=packet)
        packet = self.after(); packet['result']['pagePrimitives']['components'][0]['name'] = 'unauthorized'
        self.assertFalse(self.run_contract(operation='record', snapshot=packet)['verified'])

    def test_changed_action_old_id_and_unbound_postcondition_refused(self):
        f = self.fixture
        self.action['payload']['patch']['x'] = 26
        f.put('plan.json', f.plan)
        with self.assertRaisesRegex(ValueError, 'not in current plan'): self.run_contract()
        self.action['payload']['patch']['x'] = 25
        self.action['postcondition'][0]['match']['primitiveId'] = 'other'
        f.put('plan.json', f.plan)
        key = contract.action_identity(f.plan, self.action, workflow.digest)
        with self.assertRaisesRegex(ValueError, 'does not cover'): self.run_contract(action_id=key)

    def test_missing_visible_measurement_is_not_an_empty_success(self):
        from test_save_guard import layout
        snapshot = self.fixture.snap['result']
        snapshot['pagePrimitives']['attributes'].append({'primitiveId': 'visible', 'KeyVisible': False, 'ValueVisible': True})
        result = layout.audit(snapshot, snapshot['textMeasurements'])
        self.assertFalse(result['passed'])
        self.assertIn('text_measurement_missing', [v['kind'] for v in result['unknown']])

    def test_save_rejects_unknown_generation_and_runtime_change(self):
        f = self.fixture
        workflow.write(self.root/'.workflow/state.json', {'generation': 0, 'execution': {'actions': {'a': {'effective_state': 'UNKNOWN'}}}})
        with self.assertRaisesRegex(ValueError, 'unresolved mutation'): f.check()
        workflow.write(self.root/'.workflow/state.json', {'generation': 1})
        with self.assertRaisesRegex(ValueError, 'generation changed'): f.check()


if __name__ == '__main__': unittest.main()
