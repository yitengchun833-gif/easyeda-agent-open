"""Save gate regressions; temporary fixtures only, never contacts EDA."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import unittest

import workflow

SCRIPTS = Path(__file__).resolve().parents[2] / 'easyeda-agent/scripts'
sys.path.insert(0, str(SCRIPTS))
import schematic_layout as layout
import schematic_preflight as preflight
import test_schematic_preflight as fixtures


class SaveGuardTests(unittest.TestCase):
    def setUp(self):
        fixture = fixtures.PreflightTests()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        self.root, self.req, self.plan = fixture.root, fixture.req, fixture.plan
        self.fixture = fixture
        part = dict(primitiveId='c', componentType='part', designator='J1', name='Connector', x=20, y=20,
                    bbox=dict(minX=10,minY=10,maxX=30,maxY=30), pinsAvailable=True,
                    pins=[dict(primitiveId='pin',pinNumber='1',pinName='IN',net='SIG',x=10,y=20)])
        sheet = dict(primitiveId='sheet', componentType='sheet', bbox=dict(minX=0,minY=0,maxX=200,maxY=200))
        page = {k: [] for k in ('components','wires','buses','arcs','circles','rectangles','polygons','texts','attributes','objects')}
        page['components'] = [{k:v for k,v in part.items() if k not in ('pins','bbox','pinsAvailable')}, sheet]
        self.snap = dict(ok=True,context=dict(projectUuid='p',documentUuid='d',documentType='schematic'),
                         runtimeInstance='runtime-1',mutationSeq=0,seq=1,seqAbandoned=0,
                         result=dict(components=[part,sheet],wires=[],wiresAvailable=True,pinNetsAvailable=True,
                         pagePrimitives=page,saveGuardVersion=1,pageStateSha256='a'*64,textMeasurements=dict(texts=[],errors=[])))
        self.put('baseline.json', self.snap)
        self.req['execution_rules']['edit_scope'] = dict(baseline_sha256=workflow.file_hash(self.root/'baseline.json'),mutable_ids={})
        fixture.confirm_rules()
        self.put('requirements.json', self.req)
        self.put('plan.json', self.plan)
        gate = fixture.run_gate()
        self.assertTrue(gate['passed'],gate['findings'])
        gate['inputs'] = {str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json')}
        self.put('preflight.json',gate)
        self.put('snapshot.json',self.snap)
        self.report = dict(passed=True,phase='before_save',rule_binding=self.plan['rule_binding'],
            execution_root=str(self.root),execution_generation=0,
            requirements='requirements.json',plan='plan.json',preflight='preflight.json',baseline='baseline.json',snapshot='snapshot.json',
            checks=[dict(id=key,status='pass',note='fixture actual observation',evidence=['snapshot.json']) for key in gate['execution_rules']['effective_ids']],
            inputs={str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json','preflight.json','baseline.json','snapshot.json')})
        self.report['inputs'].update(gate['evidence_files'])
        workflow.write(self.root/'.workflow/state.json',dict(schema=1,generation=0,execution=dict(actions={})))
        self.commit_report()

    def put(self,name,value):
        (self.root/name).write_text(json.dumps(value),encoding='utf-8')

    def commit_report(self):
        self.put('review.json',self.report)

    def check(self, live=None):
        return workflow.verify_save(self.root, 'review.json', live or self.snap)

    def review(self):
        self.put('snapshot.json',self.snap)
        self.report['inputs'][str(self.root/'snapshot.json')]=workflow.file_hash(self.root/'snapshot.json')
        self.commit_report()

    def test_valid_and_fresh_process_without_business_state(self):
        self.assertTrue(self.check()['passed'])
        out=subprocess.run([sys.executable,'-X','utf8','-B',str(Path(workflow.__file__)), '--root',str(self.root),
            'verify-save','--report',str(self.root/'review.json')],input=json.dumps(self.snap),text=True,encoding='utf-8',capture_output=True)
        self.assertEqual(out.returncode,0,out.stderr)
        self.assertEqual(json.loads(out.stdout)['phase'],'before_save')
        self.assertEqual(workflow.read(self.root/'.workflow/state.json')['generation'],0)

    def test_missing_rule_unknown_rule_and_stale_evidence_block(self):
        for change in ('missing','unknown','stale'):
            with self.subTest(change=change):
                saved=copy.deepcopy(self.report)
                if change=='missing':self.report['checks'].pop()
                elif change=='unknown':self.report['checks'][0]['status']='unknown'
                else:self.report['inputs'][str(self.root/'snapshot.json')]='old'
                self.commit_report()
                with self.assertRaises(ValueError):self.check()
                self.report=saved
        self.commit_report()

    def test_changed_live_state_and_old_connector_block(self):
        for field,value in [('pageStateSha256','b'*64),('saveGuardVersion',None)]:
            live=copy.deepcopy(self.snap);live['result'][field]=value
            with self.assertRaises(ValueError):self.check(live)

    def test_outside_scope_even_with_fresh_review_blocks(self):
        self.snap['result']['pagePrimitives']['components'][0]['x']=25
        self.review()
        with self.assertRaisesRegex(ValueError,'outside-scope'):self.check()

    def test_unknown_text_never_becomes_hidden_or_waived(self):
        self.snap['result']['textMeasurements']['texts']=[dict(primitiveId='unknown',parentId='c',key='Device',text='meta',
            rendered=False,visibilityKnown=False,bbox=dict(minX=0,minY=0,maxX=0,maxY=0))]
        self.review()
        with self.assertRaisesRegex(ValueError,'coverage remains unknown'):self.check()

    def test_visual_review_covers_text_gaps_but_not_electrical_changes(self):
        self.snap['result']['pagePrimitives']['attributes'] = [dict(primitiveId='attr', ParentPrimitiveId='c',
            Key='Name', Value='J1', KeyVisible=False, ValueVisible=True, X=20, Y=40)]
        self.put('baseline.json', self.snap)
        self.req['execution_rules']['edit_scope']['baseline_sha256'] = workflow.file_hash(self.root/'baseline.json')
        self.fixture.confirm_rules()
        self.put('requirements.json', self.req); self.put('plan.json', self.plan)
        gate = self.fixture.run_gate()
        self.assertTrue(gate['passed'], gate['findings'])
        gate['inputs'] = {str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json')}
        self.put('preflight.json', gate)
        for name in ('requirements.json','plan.json','baseline.json','preflight.json'):
            self.report['inputs'][str(self.root/name)] = workflow.file_hash(self.root/name)
        self.report['rule_binding'] = self.plan['rule_binding']
        image = self.root/'render.svg'
        image.write_text('<svg xmlns="http://www.w3.org/2000/svg"><text>J1</text></svg>', encoding='utf-8')
        self.report['inputs'][str(image)] = workflow.file_hash(image)
        self.report['visual_review'] = dict(status='pass', snapshot_digest=workflow.digest(self.snap), scope='page',
            note='Fixture visual inspection: label is readable, wires and functional path are clear.', evidence=['render.svg'],
            checks=dict(text_legibility=True, wire_clarity=True, functional_readability=True))
        self.review()
        self.assertEqual(self.check()['textVerification'], 'visual')
        valid = copy.deepcopy(self.report['visual_review'])
        for field, value in [('snapshot_digest','old'),('scope','local'),('evidence',[]),
                             ('checks',dict(text_legibility=False,wire_clarity=True,functional_readability=True))]:
            self.report['visual_review'] = {**valid, field:value}; self.commit_report()
            with self.assertRaises(ValueError): self.check()
        self.report['visual_review'] = valid; self.commit_report()
        measured = dict(unknown=[dict(kind='pin_net_unknown',id='attr')], findings=[])
        with self.assertRaisesRegex(ValueError, 'non-text'):
            workflow.check_layout_review(self.root/'review.json',self.report,measured,self.snap)
        image.write_text('<svg/>', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'changed'): self.check()
        self.report['inputs'][str(image)] = workflow.file_hash(image)
        self.snap['result']['components'][0]['pins'][0]['net'] = 'OTHER'
        self.report['visual_review']['snapshot_digest'] = workflow.digest(self.snap)
        self.review()
        with self.assertRaisesRegex(ValueError, 'pin connectivity changed'): self.check()

    def test_before_save_report_cannot_satisfy_final_acceptance(self):
        rows=[dict(id=key,status='pass',note='fixture',report=str(self.root/name),evidence=[str(self.root/name)])
              for key,name in [('execution_rules','preflight.json'),('rule_application','review.json')]]
        with self.assertRaisesRegex(ValueError,'phase'):
            workflow.check_evidence(self.root,dict(required_checks=['execution_rules','rule_application']),dict(status='verified',checks=rows))

    def test_identity_cannot_be_waived_by_movement_scope(self):
        old=self.snap['result'];new=copy.deepcopy(old)
        new['pagePrimitives']['components'][0]['name']='different part'
        result=layout.compare_scope(old,new,{'components':['c']})
        self.assertFalse(result['passed'])
        self.assertEqual(result['differences'][0]['reason'],'part_identity_changed')

    def test_scope_and_visibility_fail_closed(self):
        text=dict(primitiveId='a',parentId='c',key='Name',value='R1',text='R1',bbox=dict(minX=0,minY=0,maxX=0,maxY=0))
        self.assertEqual(layout.text_visibility(text,{}),'unknown')
        self.assertEqual(layout.text_visibility(dict(text,keyVisible=False,valueVisible=False),{}),'hidden')
        self.assertEqual(layout.text_visibility(dict(text,valueVisible=True),{}),'visible')
        native={'pagePrimitives':{'attributes':[dict(primitiveId='a',ParentPrimitiveId='c',Key='Name',Value='R1',KeyVisible=False,ValueVisible=True)]}}
        self.assertEqual(layout.text_visibility(text,native),'visible')
        self.assertEqual(layout.text_visibility(dict(text,valueVisible=False),native),'unknown')
        with self.assertRaisesRegex(ValueError,'inventory missing'):layout.compare_scope({},self.snap['result'],{})

    def test_created_wire_requires_planned_receipt_and_exact_readback(self):
        self.snap['result']['pagePrimitives']['wires']=[dict(primitiveId='new',Line=[10,100,90,100],Net='SIG')]
        self.snap['result']['wires']=[dict(primitiveId='new',x0=10,y0=100,x1=90,y1=100,net='SIG')]
        self.review()
        with self.assertRaisesRegex(ValueError,'outside-scope'):self.check()
        receipt=dict(ok=True,context=self.snap['context'],result=dict(primitiveId='new'))
        self.put('receipt.json',receipt)
        self.report['inputs'][str(self.root/'receipt.json')]=workflow.file_hash(self.root/'receipt.json')
        self.report['created_objects']=[dict(kind='wires',id='new',plan_index=0,receipt='receipt.json')]
        self.commit_report()
        self.assertTrue(self.check()['passed'])
        self.snap['result']['wires'][0]['x1']=95
        self.review()
        with self.assertRaisesRegex(ValueError,'planned geometry'):self.check()

    def test_candidate_review_cannot_waive_unknown_or_hard_failure(self):
        box=dict(minX=15,minY=15,maxX=25,maxY=25)
        self.snap['result']['pagePrimitives']['texts']=[dict(primitiveId='text',Content='note')]
        self.snap['result']['textMeasurements']['texts']=[dict(primitiveId='text',key='free_text',parentId=None,text='note',bbox=box)]
        self.put('baseline.json',self.snap)
        self.req['execution_rules']['edit_scope']['baseline_sha256']=workflow.file_hash(self.root/'baseline.json')
        self.fixture.confirm_rules();self.put('requirements.json',self.req);self.put('plan.json',self.plan)
        gate=self.fixture.run_gate();gate['inputs']={str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json')}
        self.put('preflight.json',gate);self.report['rule_binding']=self.plan['rule_binding']
        self.report['inputs'].update(gate['evidence_files'])
        self.report['inputs'].update({str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json','preflight.json','baseline.json')})
        self.review()
        with self.assertRaisesRegex(ValueError,'finding review'):self.check()
        finding=layout.audit(self.snap['result'],self.snap['result']['textMeasurements'])['findings'][0]
        self.report['finding_reviews']=[dict(index=0,finding=finding,status='pass',note='fixture visual review',evidence=['snapshot.json'])]
        self.commit_report()
        self.assertTrue(self.check()['passed'])
        self.snap['result']['textMeasurements']['texts'][0]['bbox']=dict(minX=-20,minY=15,maxX=-10,maxY=25)
        self.review()
        self.report['finding_reviews'][0]['finding']=layout.audit(self.snap['result'],self.snap['result']['textMeasurements'])['findings'][0]
        self.commit_report()
        with self.assertRaisesRegex(ValueError,'unresolved layout'):self.check()

    def test_final_acceptance_requires_ordered_bound_persistence(self):
        guard=self.check()
        def packet(result,seconds):
            return dict(ok=True,context=self.snap['context'],result=result,createdAt=f'2026-09-28T05:00:{seconds:02d}Z')
        proofs={'save_report':'review.json','save_receipt':'saved.json','close_receipt':'closed.json',
                'open_receipt':'opened.json','reopened_snapshot':'reopened.json'}
        self.put('saved.json',packet(dict(saved=True,workflowSave=guard),1))
        self.put('closed.json',packet(dict(closed=True,uuid='d'),2))
        self.put('opened.json',packet(dict(ready=True),3))
        reopened=packet(copy.deepcopy(self.snap['result']),4)
        self.put('reopened.json',reopened)
        final=copy.deepcopy(self.report)
        final.update(phase='after_reopen',persistence=proofs)
        def publish():
            final['inputs'].update({str(self.root/n):workflow.file_hash(self.root/n) for n in proofs.values()})
            self.put('final.json',final)
        publish()
        self.assertTrue(workflow.verify_reopen(self.root,'final.json')['passed'])
        reopened['result']['pagePrimitives']['components'][0]['x']=26
        self.put('reopened.json',reopened);publish()
        with self.assertRaisesRegex(ValueError,'content changed'):workflow.verify_reopen(self.root,'final.json')
        reopened['result']=copy.deepcopy(self.snap['result']);reopened['createdAt']='2026-09-28T04:59:59Z'
        self.put('reopened.json',reopened);publish()
        with self.assertRaisesRegex(ValueError,'out of order'):workflow.verify_reopen(self.root,'final.json')

    def design_case(self, new_component=False, net='SIG', rewire=False):
        device='1'*32
        self.snap['result']['components'][0]['device']={'uuid':device,'libraryUuid':'library'}
        self.snap['result']['components'][0]['pins'][0]['net']=net
        target=dict(schemaVersion='1.4',projectId='p',documentId='d',
            components=[dict(id='cmp-J1',ref='J1',device=dict(deviceUuid=device,libraryUuid='library',name='Connector'),
                             pins=[dict(number='1',name='IN')])],
            nets=[dict(id='signal',name=net)],connections=[dict(componentId='cmp-J1',pinNumber='1',netId='signal',kind='netlist')])
        self.put('target.json',target)
        self.req['sources'].append(dict(id='TARGET',path='target.json',sha256=workflow.file_hash(self.root/'target.json')))
        self.req['parts'][0]['required']['pin_count']=1
        self.req['parts'][0]['selected']['pin_count']=1
        scope=self.req['execution_rules']['edit_scope']
        scope.update(mode='design',target_source='TARGET',authorization=dict(source='S',locator='fixture explicit new design authorization'))
        if rewire:
            scope.pop('target_source')
            self.req['sources']=[source for source in self.req['sources'] if source['id']!='TARGET']
            scope.update(acceptance='page',rewire=dict(authorization=dict(source='S',locator='fixture change existing connections')))
            self.plan['rewire']=True
        if new_component:
            baseline=copy.deepcopy(self.snap)
            baseline['result']['components']=[baseline['result']['components'][1]]
            baseline['result']['pagePrimitives']['components']=[baseline['result']['pagePrimitives']['components'][1]]
            self.put('baseline.json',baseline)
            scope['baseline_sha256']=workflow.file_hash(self.root/'baseline.json')
            self.plan['actions'].append(dict(action='schematic.component.place',payload=dict(deviceUuid=device,designator='J1',x=20,y=20)))
            self.put('component-receipt.json',dict(ok=True,context=self.snap['context'],result=dict(primitiveId='c')))
            self.report['created_objects']=[dict(kind='components',id='c',plan_index=len(self.plan['actions'])-1,receipt='component-receipt.json')]
            self.report['inputs'][str(self.root/'component-receipt.json')]=workflow.file_hash(self.root/'component-receipt.json')
        self.fixture.confirm_rules()
        self.put('requirements.json',self.req);self.put('plan.json',self.plan)
        gate=self.fixture.run_gate()
        self.assertTrue(gate['passed'],gate['findings'])
        gate['inputs']={str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json')}
        self.put('preflight.json',gate)
        self.report['rule_binding']=self.plan['rule_binding']
        self.report['inputs'].update(gate['evidence_files'])
        self.report['inputs'].update({str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json','preflight.json','baseline.json')})
        self.review()
        return target

    def test_new_design_uses_confirmed_source_not_empty_baseline(self):
        self.design_case(new_component=True)
        self.assertTrue(self.check()['passed'])
        self.snap['result']['components'][0]['pins'][0]['net']='WRONG'
        self.review()
        with self.assertRaisesRegex(ValueError,'source design mismatch'):self.check()

    def test_confirmed_design_can_change_pin_net_from_baseline(self):
        self.design_case(net='NEW')
        self.assertTrue(self.check()['passed'])

    def test_authorized_rewire_does_not_require_canonical_target(self):
        self.design_case(net='NEW', rewire=True)
        result=self.check()
        self.assertTrue(result['passed'])
        self.assertEqual(result['electricalChanges'][0]['pin'],'J1.1')

    def test_design_target_change_requires_new_confirmation(self):
        target=self.design_case()
        self.assertTrue(self.check()['passed'])
        target['nets'][0]['name']='OTHER';self.put('target.json',target)
        with self.assertRaises(ValueError):self.check()

    def test_design_identity_unknown_pin_and_nc_conflict_fail(self):
        target=self.design_case()
        actual=copy.deepcopy(self.snap['result'])
        actual['components'][0]['device']['uuid']='2'*32
        self.assertFalse(layout.compare_target(target,actual)['passed'])
        actual=copy.deepcopy(self.snap['result']);actual['components'][0]['pins'][0]['net']=None
        with self.assertRaises(ValueError):layout.compare_target(target,actual)
        target['components'][0]['pins'][0]['noConnected']=True
        with self.assertRaises(ValueError):layout.compare_target(target,self.snap['result'])

    def test_circuit_change_preserves_unrelated_instance_properties(self):
        old=self.snap['result'];new=copy.deepcopy(old)
        old['pagePrimitives']['components'][0]['addIntoBom']=True
        new['pagePrimitives']['components'][0]['addIntoBom']=False
        self.assertFalse(layout.compare_scope(old,new,{'components':['c']},preserve_identity=False)['passed'])



    def test_grouped_evidence_covers_each_rule_once(self):
        first, second, *rest = self.report['checks']
        group = dict(first, id='shared-fact', rule_ids=[first['id'], second['id']])
        self.report['checks'] = [group, *rest];self.commit_report()
        self.assertTrue(self.check()['passed'])
        self.report['checks'].append(second);self.commit_report()
        with self.assertRaisesRegex(ValueError, 'duplicate'):self.check()
        self.report['checks'].pop();group['rule_ids'].append('NONEXISTENT');self.commit_report()
        with self.assertRaisesRegex(ValueError, 'unknown rule'):self.check()

    def local_case(self):
        self.snap['result']['pagePrimitives']['attributes'] = [dict(primitiveId='legacy', ParentPrimitiveId='other', Key='Meta', Value='unknown')]
        self.snap['result']['textMeasurements']['texts'] = [dict(primitiveId='legacy',parentId='other',key='Meta',text='unknown',value='unknown')]
        self.put('baseline.json',self.snap)
        scope=self.req['execution_rules']['edit_scope']
        scope.update(acceptance='local',baseline_sha256=workflow.file_hash(self.root/'baseline.json'))
        self.fixture.confirm_rules();self.put('requirements.json',self.req);self.put('plan.json',self.plan)
        gate=self.fixture.run_gate();self.assertTrue(gate['passed'],gate['findings'])
        gate['inputs']={str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json')}
        self.put('preflight.json',gate)
        self.report['rule_binding']=self.plan['rule_binding'];self.report['inputs'].update(gate['evidence_files'])
        self.report['inputs'].update({str(self.root/n):workflow.file_hash(self.root/n) for n in ('requirements.json','plan.json','preflight.json','baseline.json')})
        self.review()

    def test_local_carries_unchanged_outside_unknown_but_not_full_page(self):
        self.local_case();proof=self.check()
        self.assertEqual(proof['preexistingLayout'],{'findings':0,'unknown':1})
        self.assertEqual(proof['acceptanceScope'],'local')
        result=layout.audit(self.snap['result'],self.snap['result']['textMeasurements'])
        self.assertFalse(result['passed'])
        self.snap['result']['textMeasurements']['texts'].append(dict(primitiveId='new',key='Meta',parentId='other',text='unknown'))
        self.review()
        with self.assertRaisesRegex(ValueError,'coverage remains unknown'):self.check()

    def test_local_affected_unknown_and_new_geometry_failure_cannot_be_carried(self):
        self.local_case()
        before=self.snap['result'];after=copy.deepcopy(before)
        self.assertTrue(layout.audit_local(before,after,{})['passed'])
        self.assertFalse(layout.audit_local(before,after,{'attributes':['legacy']})['passed'])
        after['textMeasurements']['texts'].append(dict(primitiveId='visible',key='free_text',text='new',bbox=dict(minX=-20,minY=15,maxX=-10,maxY=25)))
        self.assertFalse(layout.audit_local(before,after,{})['passed'])
        after=copy.deepcopy(before);after['wiresAvailable']=False
        self.assertFalse(layout.audit_local(after,after,{})['passed'])

    def test_local_unknown_survives_native_attribute_id_regeneration(self):
        self.local_case();before=self.snap['result'];after=copy.deepcopy(before)
        after['pagePrimitives']['attributes'][0]['primitiveId']='regenerated'
        after['textMeasurements']['texts'][0]['primitiveId']='regenerated'
        self.assertTrue(layout.audit_local(before,after,{})['passed'])
        self.assertFalse(layout.audit_local(before,after,{'attributes':['legacy']})['passed'])


    def test_local_persistence_still_requires_bound_order_and_unchanged_content(self):
        self.local_case()
        self.test_final_acceptance_requires_ordered_bound_persistence()

if __name__=='__main__':unittest.main()
