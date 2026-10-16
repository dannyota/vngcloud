#!/usr/bin/env python3
"""Offline evidence checks. No submitted code or real service runs here."""
import copy
from contextlib import contextmanager
import importlib.util
import json
import hashlib
import base64
import argparse
import os
import io
import sys
import tempfile
import subprocess
import selectors
import time
from pathlib import Path
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('evidence', Path(__file__).with_name('ci-evidence.py'))
sys.dont_write_bytecode = True
E = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(E)
H, B, P = 'a' * 40, 'b' * 40, 'c' * 40


def review():
    return dict(version=1, repository=E.REPOSITORY, pr=7, head_sha=H,
                reviewed_base_sha=B, policy_sha=P, manager='manager', reviews=[
                    dict(reviewer='reviewer', author='author', reviewed_sha=H,
                         report='private/report', outcome='accept', findings=[],
                         fix_confirmations=[])])


class ReviewTests(unittest.TestCase):
    def test_accept(self):
        E.review_record(review(), 7, H, B, P, 'manager')

    def test_bad_bindings_and_types(self):
        for key, value in [('version', True), ('repository', 'other/repo'),
                           ('pr', 8), ('head_sha', B), ('reviewed_base_sha', H),
                           ('policy_sha', H), ('manager', ''), ('reviews', [])]:
            with self.subTest(key=key), self.assertRaises(E.Rejected):
                E.review_record(dict(review(), **{key: value}), 7, H, B, P, 'manager')
        for change in [dict(reviewer='author'), dict(outcome='pending'),
                       dict(reviewed_sha=B), dict(report='')]:
            data = review()
            data['reviews'][0].update(change)
            with self.assertRaises(E.Rejected):
                E.review_record(data, 7, H, B, P, 'manager')

    def test_blocking_fix_on_final_head(self):
        data = review()
        data['reviews'][0]['findings'] = [dict(reference='finding', blocking=True)]
        with self.assertRaises(E.Rejected):
            E.review_record(data, 7, H, B, P, 'manager')
        data['reviews'][0]['fix_confirmations'] = [dict(finding='finding', head_sha=H,
                                                       reviewer='reviewer', report='private/fix',
                                                       outcome='accept')]
        E.review_record(data, 7, H, B, P, 'manager')
        data['reviews'][0]['fix_confirmations'][0]['head_sha'] = B
        with self.assertRaises(E.Rejected):
            E.review_record(data, 7, H, B, P, 'manager')


class IdentityTests(unittest.TestCase):
    def record(self):
        return dict(version=2, repository=E.REPOSITORY, workflow='.github/workflows/ci.yml',
                    job='go', matrix={}, tested_sha=H, run_id=12, run_attempt=1,
                    workflow_ref=E.REPOSITORY + '/.github/workflows/ci.yml@refs/pull/7/merge',
                    workflow_sha='d' * 40)

    def test_exact_typed_record(self):
        self.assertEqual(E.identity(json.dumps(self.record())), self.record())
        for key, value in [('version', True), ('version', 1), ('matrix', []),
                           ('run_id', '12'), ('run_attempt', 0), ('tested_sha', H.upper()),
                           ('extra', True)]:
            with self.subTest(key=key), self.assertRaises(E.Rejected):
                E.identity(json.dumps(dict(self.record(), **{key: value})))
        with self.assertRaises(E.Rejected):
            E.identity('{"version":1,"version":1}')
        with self.assertRaises(E.Rejected):
            E.identity(' ' * 2049)

    def test_annotations_use_reserved_title_only(self):
        expected = self.record()
        valid = dict(title=E.TITLE, annotation_level='notice', message=json.dumps(expected))
        E.annotations([valid, dict(title='runner notice')], expected)
        for records in ([], [valid, valid], [dict(valid, annotation_level='warning')],
                        [dict(valid, message=json.dumps(dict(expected, run_attempt=2))) ]):
            with self.assertRaises(E.Rejected):
                E.annotations(records, expected)


class Response:
    def __init__(self, status=200, body=b'{}', headers=None):
        self.status, self.body, self.headers = status, body, headers or {}
    def getheader(self, name, default=None):
        return self.headers.get(name, 'application/json' if name == 'Content-Type' else default)
    def read(self, count):
        chunk, self.body = self.body[:count], self.body[count:]
        return chunk
    read1 = read


class TransportTests(unittest.TestCase):
    def adapter(self, responses):
        class Connection:
            def __init__(self, *args, **kwargs):
                self.args, self.kwargs = args, kwargs
            def connect(self):
                pass
            def request(self, *args, **kwargs):
                requests.append((args, kwargs))
            def getresponse(self):
                return responses.pop(0)
            def close(self):
                pass
        requests = []
        patcher = patch.object(E.http.client, 'HTTPSConnection', Connection)
        patcher.start()
        self.addCleanup(patcher.stop)
        return E.Adapter('synthetic-runtime-token', 10), requests

    def test_every_redirect_refused_before_second_request(self):
        for status in (301, 302, 303, 307, 308):
            for host in ('api.github.com', 'foreign.invalid'):
                with self.subTest(status=status, host=host):
                    adapter, requests = self.adapter([Response(status, headers={
                        'Location': 'https://' + host + '/elsewhere'})])
                    with self.assertRaises(E.Rejected):
                        adapter.get('/actions/runs')
                    self.assertEqual(len(requests), 1)

    def test_strict_json_errors_and_token_redaction(self):
        for body in (b'{"id":1,"id":2}', b'NaN', b'{', b'\xff'):
            adapter, _ = self.adapter([Response(body=body)])
            with self.assertRaises(E.Rejected) as caught:
                adapter.get('/actions/runs')
            self.assertNotIn('synthetic-runtime-token', str(caught.exception))
        for token in ('', 'bad\ninput', 'bad input'):
            with self.assertRaises(E.Rejected):
                E.Adapter(token, 10)

    def test_fixed_endpoint(self):
        for path in ('https://foreign.invalid/', '//foreign.invalid/', '/../../other',
                     '/actions/runs?host=foreign.invalid'):
            adapter, requests = self.adapter([])
            with self.assertRaises(E.Rejected):
                adapter.get(path)
            self.assertEqual(requests, [])

    def test_pagination_and_bounds(self):
        first = dict(total_count=101, workflow_runs=[dict(id=n) for n in range(1,101)])
        second = dict(total_count=101, workflow_runs=[dict(id=101)])
        link = '<https://api.github.com/repos/' + E.REPOSITORY + '/actions/runs?per_page=100&page=2>; rel="next"'
        adapter, requests = self.adapter([Response(body=json.dumps(first).encode(), headers={'Link': link}),
                                          Response(body=json.dumps(second).encode())])
        self.assertEqual(adapter.pages('/actions/runs', 'workflow_runs'), [dict(id=n) for n in range(1,102)])
        self.assertEqual(len(requests), 2)
        for bad in (link.replace('api.github.com', 'foreign.invalid'),
                    link.replace('/actions/runs?', '/check-runs/1/annotations?'),
                    link.replace('page=2', 'page=2&page=2'),
                    link.replace('page=2', 'page=3'),
                    link.replace('https://', 'http://')):
            adapter, requests = self.adapter([Response(body=json.dumps(first).encode(), headers={'Link': bad})])
            with self.assertRaises(E.Rejected):
                adapter.pages('/actions/runs', 'workflow_runs')
            self.assertEqual(len(requests), 1)
        adapter, _ = self.adapter([Response(body=json.dumps(first).encode())])
        with self.assertRaises(E.Rejected):
            adapter.pages('/actions/runs', 'workflow_runs')




CONTENT = b'name: synthetic reviewed workflow\n'
BLOB = hashlib.sha1(b'blob ' + str(len(CONTENT)).encode() + b'\0' + CONTENT).hexdigest()
W = 'd' * 40


def fixture():
    args = argparse.Namespace(pr=7, head=H, base=B, master=B, policy_sha=P, manager='manager',
                              mode='pre', signer='owner', fingerprint='SHA256:synthetic', tag=None)
    policy = dict(version=1, repository=E.REPOSITORY, policy_sha=P, actions_app=91,
                  publication='disabled', workflows={}, sources=[], review_workflows=[
                      dict(id=81, path='dynamic/agents/copilot-pull-request-reviewer', purpose='review-only')],
                  review_apps=[])
    snapshot = dict(pr=dict(number=7, head=dict(sha=H, repo=dict(full_name=E.REPOSITORY)),
                            base=dict(ref='master', repo=dict(full_name=E.REPOSITORY))),
                    master=dict(ref='refs/heads/master', object=dict(type='commit', sha=B)),
                    runs=[], checks=[], attempts={}, sources={})
    for offset, (name, job_names) in enumerate(E.JOBS.items(), 1):
        path = '.github/workflows/' + name + '.yml'
        policy['workflows'][name] = dict(id=offset, path=path, steps={
            key: ['Require successful jobs on the expected source'] if key == 'required' else
            ['Assert tested source identity', 'Source checks'] for key in job_names})
        binding = dict(repository=E.REPOSITORY, workflow_id=offset, workflow=path,
                       workflow_ref=E.REPOSITORY + '/' + path + '@refs/pull/7/merge', workflow_sha=W,
                       blob_sha=BLOB, content_sha256=hashlib.sha256(CONTENT).hexdigest(), report='private/source')
        policy['sources'].append(binding)
        snapshot['sources'][(W, path)] = dict(blob_sha=BLOB, content_sha256=binding['content_sha256'])
        run = dict(id=offset * 10, workflow_id=offset, path=path, head_sha=H,
                   repository=dict(full_name=E.REPOSITORY), head_repository=dict(full_name=E.REPOSITORY),
                   html_url=f'https://github.com/{E.REPOSITORY}/actions/runs/{offset * 10}',
                   head_branch='slice', event='pull_request', pull_requests=[dict(number=7, head=dict(sha=H))],
                   check_suite_id=offset * 100, run_attempt=1, status='completed', conclusion='success')
        snapshot['runs'].append(run)
        attempt = dict(metadata=copy.deepcopy(run), jobs=[], checks={})
        snapshot['attempts'][run['id']] = [attempt]
        for index, (key, job_name) in enumerate(job_names.items(), 1):
            jid, cid = offset * 1000 + index, offset * 10000 + index
            job = dict(id=jid, run_id=run['id'], run_attempt=1, head_sha=H, name=job_name,
                       check_run_url=f'https://api.github.com/repos/{E.REPOSITORY}/check-runs/{cid}',
                       status='completed', conclusion='success', steps=[dict(name=step, status='completed',
                          conclusion='success') for step in policy['workflows'][name]['steps'][key]])
            check = dict(id=cid, name=job_name, head_sha=H, app=dict(id=91),
                         check_suite=dict(id=run['check_suite_id']), status='completed', conclusion='success',
                         details_url=f'https://github.com/{E.REPOSITORY}/actions/runs/{run["id"]}/job/{jid}')
            notice = dict(version=2, repository=E.REPOSITORY, workflow=path, job=key, matrix={},
                          tested_sha=H, run_id=run['id'], run_attempt=1,
                          workflow_ref=binding['workflow_ref'], workflow_sha=W)
            records = [] if key == 'required' else [dict(title=E.TITLE, annotation_level='notice', message=json.dumps(notice))]
            check['output'] = dict(annotations_count=len(records))
            attempt['jobs'].append(job)
            attempt['checks'][cid] = dict(check=copy.deepcopy(check), annotations=records)
            snapshot['checks'].append(check)
    return args, policy, snapshot


class FakeGit:
    def checker(self, *args):
        pass
    def signatures(self, *args):
        pass
    def paths(self, *args):
        return ['.github/workflows/wiki.yml']
    def workflows(self, *args):
        pass
    def tag(self, *args):
        pass


class EvaluationTests(unittest.TestCase):
    def setUp(self):
        self.args, self.policy, self.data = fixture()
        self.git = FakeGit()
    def evaluate(self):
        E.policy_record(self.policy, P)
        E.review_record(review(), 7, H, B, P, 'manager')
        return E.evaluate(self.data, self.args, self.policy, self.git)
    def test_complete_and_conditional_green(self):
        self.assertEqual(len(self.evaluate()['runs']), 4)
        self.data['runs'] = self.data['runs'][:1]
        self.data['checks'] = self.data['checks'][:4]
        self.git.paths = lambda *args: ['README.md']
        self.assertEqual(len(self.evaluate()['exclusions']), 3)
    def test_all_unsuccessful_runs_and_jobs(self):
        for kind in ('run', 'attempt', 'job', 'step', 'check'):
            for conclusion in ('failure', 'cancelled', 'timed_out', 'neutral', 'skipped', 'action_required', None):
                with self.subTest(kind=kind, conclusion=conclusion):
                    self.setUp()
                    attempt = self.data['attempts'][10][0]
                    target = {'run': self.data['runs'][0], 'attempt': attempt['metadata'],
                              'job': attempt['jobs'][0], 'step': attempt['jobs'][0]['steps'][0],
                              'check': attempt['checks'][10001]['check']}[kind]
                    target['conclusion'] = conclusion
                    with self.assertRaises(E.Rejected): self.evaluate()
        for kind in ('runs', 'jobs', 'steps'):
            self.setUp()
            target = self.data['runs'][0] if kind == 'runs' else self.data['attempts'][10][0]['jobs'][0]
            if kind == 'steps': target = target['steps'][0]
            target['status'] = 'in_progress'
            with self.assertRaises(E.Rejected): self.evaluate()
    def test_missing_empty_and_partial(self):
        for mutation in (lambda d: d.update(runs=[]), lambda d: d['runs'].pop(),
                         lambda d: d['attempts'][10].clear(), lambda d: d['attempts'][10][0]['jobs'].pop(),
                         lambda d: d['attempts'][10][0]['jobs'][0]['steps'].pop(),
                         lambda d: d['attempts'][10][0]['checks'][10001]['annotations'].clear()):
            self.setUp(); mutation(self.data)
            with self.assertRaises(E.Rejected): self.evaluate()
    def test_associations_and_source_claims(self):
        for kind, key, value in [('run', 'head_sha', B), ('run', 'path', 'renamed.yml'),
                                 ('run', 'event', 'pull_request_target'), ('job', 'run_attempt', 2),
                                 ('job', 'check_run_url', 'https://foreign.invalid/check-runs/1'),
                                 ('check', 'head_sha', B), ('check', 'app', {'id': 9}),
                                 ('check', 'details_url', 'https://foreign.invalid/job'),
                                 ('check', 'check_suite', {'id': 999})]:
            self.setUp()
            target = {'run': self.data['runs'][0], 'job': self.data['attempts'][10][0]['jobs'][0],
                      'check': self.data['attempts'][10][0]['checks'][10001]['check']}[kind]
            target[key] = value
            with self.subTest(kind=kind, key=key), self.assertRaises(E.Rejected): self.evaluate()
        for key, value in [('version',1), ('workflow_sha',H), ('workflow_ref',E.REPOSITORY + '/.github/workflows/ci.yml@refs/heads/master'),
                           ('workflow','.github/workflows/wiki.yml'), ('repository','other/repo'),
                           ('matrix',{'os':'unexpected'}), ('tested_sha',B), ('run_id',99), ('job','lint')]:
            self.setUp()
            notice = self.data['attempts'][10][0]['checks'][10001]['annotations'][0]
            claim = json.loads(notice['message']); claim[key] = value; notice['message'] = json.dumps(claim)
            with self.subTest(key=key), self.assertRaises(E.Rejected): self.evaluate()
        self.setUp(); self.data['sources'].clear()
        with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp(); self.data['sources'][(W,'.github/workflows/ci.yml')]['content_sha256']='f'*64
        with self.assertRaises(E.Rejected): self.evaluate()
    def test_duplicate_or_forged_notices(self):
        for key in (10001,10004):
            self.setUp()
            source = self.data['attempts'][10][0]['checks'][10001]['annotations'][0]
            self.data['attempts'][10][0]['checks'][key]['annotations'].append(copy.deepcopy(source))
            with self.assertRaises(E.Rejected): self.evaluate()
    def add_attempt(self, run_id=10, status='success', workflow_sha=W):
        run = next(r for r in self.data['runs'] if r['id']==run_id)
        first = self.data['attempts'][run_id][0]
        run['run_attempt'] = 2
        second = copy.deepcopy(first); second['metadata']['run_attempt'] = 2
        first['metadata']['conclusion'] = status
        oldchecks = second['checks']; second['checks'] = {}
        for job in second['jobs']:
            job['id'] += 100; job['run_attempt'] = 2
            oldid = E.check_id(job['check_run_url']); cid = oldid+100
            job['check_run_url'] = job['check_run_url'].replace(str(oldid),str(cid))
            bundle = oldchecks[oldid]; check=bundle['check']; check['id']=cid
            check['details_url']=f'https://github.com/{E.REPOSITORY}/actions/runs/{run_id}/job/{job["id"]}'
            for notice in bundle['annotations']:
                claim=json.loads(notice['message']); claim.update(run_attempt=2,workflow_sha=workflow_sha)
                notice['message']=json.dumps(claim)
            second['checks'][cid]=bundle
            self.data['checks'].append(copy.deepcopy(check))
        self.data['attempts'][run_id].append(second)
    def test_rerun_retains_failures_and_requires_matching_source(self):
        self.add_attempt(); self.evaluate()
        self.setUp(); self.add_attempt(status='failure'); self.data['runs'][0]['reason']='infrastructure'
        with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp(); self.add_attempt(workflow_sha=H)
        binding=copy.deepcopy(self.policy['sources'][0]);binding['workflow_sha']=H
        self.policy['sources'].append(binding)
        self.data['sources'][(H,binding['workflow'])]=self.data['sources'][(W,binding['workflow'])]
        with self.assertRaises(E.Rejected): self.evaluate()
    def test_independent_failure_and_review_triggered_validation(self):
        original = copy.deepcopy(self.data['runs'][0]); original.update(id=99,event='workflow_dispatch',conclusion='failure')
        original['html_url']=f'https://github.com/{E.REPOSITORY}/actions/runs/99'
        self.data['runs'].append(original)
        with self.assertRaises(E.Rejected): self.evaluate()
    def test_optional_review_never_becomes_a_slot(self):
        for status, conclusion in [('queued',None),('completed','failure'),('completed','success')]:
            self.setUp()
            self.data['runs'].append(dict(id=90,workflow_id=81,path='dynamic/agents/copilot-pull-request-reviewer',
                head_sha=H,repository={'full_name':E.REPOSITORY},head_repository={'full_name':E.REPOSITORY},
                check_suite_id=901,status=status,conclusion=conclusion))
            self.evaluate()
        self.data['runs'] = self.data['runs'][-1:]
        self.data['checks'] = []
        with self.assertRaises(E.Rejected): self.evaluate()
    def test_independent_runs_can_retain_distinct_reviewed_sources(self):
        old = self.data['runs'][0]
        run = copy.deepcopy(old)
        run.update(id=90, check_suite_id=900)
        run['html_url'] = f'https://github.com/{E.REPOSITORY}/actions/runs/90'
        attempt = copy.deepcopy(self.data['attempts'][10][0])
        attempt['metadata'] = copy.deepcopy(run)
        checks = attempt['checks']; attempt['checks'] = {}
        for job in attempt['jobs']:
            old_cid = E.check_id(job['check_run_url']); cid = old_cid + 900
            job['id'] += 900; job['run_id'] = 90
            job['check_run_url'] = job['check_run_url'].replace(str(old_cid), str(cid))
            bundle = checks[old_cid]
            check = bundle['check']; check['id'] = cid; check['check_suite']['id'] = 900
            check['details_url'] = f'https://github.com/{E.REPOSITORY}/actions/runs/90/job/{job["id"]}'
            for annotation in bundle['annotations']:
                record = json.loads(annotation['message']); record.update(run_id=90, workflow_sha=H)
                annotation['message'] = json.dumps(record)
            attempt['checks'][cid] = bundle; self.data['checks'].append(copy.deepcopy(check))
        self.data['runs'].append(run); self.data['attempts'][90] = [attempt]
        binding = copy.deepcopy(self.policy['sources'][0]); binding['workflow_sha'] = H
        self.policy['sources'].append(binding)
        self.data['sources'][(H, binding['workflow'])] = self.data['sources'][(W, binding['workflow'])]
        result = self.evaluate()
        self.assertEqual({item['workflow_sha'] for item in result['runs']}, {H, W})
        # A second independent failure remains blocking even beside its green sibling.
        run['conclusion'] = 'failure'
        with self.assertRaises(E.Rejected): self.evaluate()

    def test_unknown_runs_and_orphan_checks_fail_closed(self):
        for unknown in ('dynamic/dependabot/update-graph','.github/workflows/evidence-probe.yml','CI'):
            self.setUp(); self.data['runs'].append(dict(id=98,workflow_id=98,path=unknown))
            with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp();self.data['checks'].append(dict(id=999,head_sha=H,app={'id':91},check_suite={'id':998}))
        with self.assertRaises(E.Rejected): self.evaluate()
    def test_moving_head_master_and_stale_ancestry(self):
        for container,key in [('head','sha'),('master','sha')]:
            self.setUp()
            target=self.data['pr']['head'] if container=='head' else self.data['master']['object']
            target[key]=P
            with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp();self.git.signatures=lambda *args: E.require(False,'Stale ancestry')
        with self.assertRaises(E.Rejected): self.evaluate()
    def test_manual_recovery_cannot_replace_pr_ci(self):
        self.data['runs'][0]['event']='workflow_dispatch'
        self.data['attempts'][10][0]['metadata']['event']='workflow_dispatch'
        with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp();self.data['runs'][1]['event']='workflow_dispatch'
        self.data['attempts'][20][0]['metadata']['event']='workflow_dispatch'
        binding=copy.deepcopy(self.policy['sources'][1]);binding['workflow_ref']=E.REPOSITORY+'/'+binding['workflow']+'@refs/heads/slice'
        self.policy['sources'].append(binding)
        for notice in self.data['attempts'][20][0]['checks'][20001]['annotations']:
            claim=json.loads(notice['message']);claim['workflow_ref']=binding['workflow_ref'];notice['message']=json.dumps(claim)
        self.evaluate()
    def add_push_runs(self):
        original = copy.deepcopy(self.data)
        for run in copy.deepcopy(original['runs']):
            old=run['id'];new=old+500;run.update(id=new,event='push',head_branch='master',check_suite_id=run['check_suite_id']+500,run_attempt=1)
            run['html_url']=f'https://github.com/{E.REPOSITORY}/actions/runs/{new}'
            attempt=copy.deepcopy(original['attempts'][old][0]);attempt['metadata']=copy.deepcopy(run)
            checks=attempt['checks'];attempt['checks']={}
            path=run['path'];binding=copy.deepcopy(next(b for b in self.policy['sources'] if b['workflow']==path))
            binding.update(workflow_sha=H,workflow_ref=E.REPOSITORY+'/'+path+'@refs/heads/master')
            self.policy['sources'].append(binding);self.data['sources'][(H,path)]=self.data['sources'][(W,path)]
            for job in attempt['jobs']:
                job['id']+=500;job['run_id']=new
                cid=E.check_id(job['check_run_url']);newcid=cid+500
                job['check_run_url']=job['check_run_url'].replace(str(cid),str(newcid))
                bundle=checks[cid];check=bundle['check'];check['id']=newcid;check['check_suite']['id']=run['check_suite_id']
                check['details_url']=f'https://github.com/{E.REPOSITORY}/actions/runs/{new}/job/{job["id"]}'
                for notice in bundle['annotations']:
                    claim=json.loads(notice['message']);claim.update(run_id=new,workflow_sha=H,workflow_ref=binding['workflow_ref'])
                    notice['message']=json.dumps(claim)
                attempt['checks'][newcid]=bundle;self.data['checks'].append(copy.deepcopy(check))
            self.data['runs'].append(run);self.data['attempts'][new]=[attempt]
        self.args.mode='post';self.args.master=H;self.data['master']['object']['sha']=H

    def retain_pre(self):
        retained = self.evaluate()
        retained['cutoff'] = '2026-01-01T00:00:00+00:00'
        self.args.retained = copy.deepcopy(retained)
        return retained

    def test_post_and_release_retain_original_pr_sources(self):
        self.retain_pre()
        self.add_push_runs()
        self.evaluate()
        self.args.mode='release';self.args.tag='v1.2.3';self.evaluate()
        del self.data['sources'][(W,'.github/workflows/ci.yml')]
        with self.assertRaises(E.Rejected): self.evaluate()


class SourceTests(unittest.TestCase):
    def setUp(self):
        self.objects = {'/git/commits/'+W: dict(sha=W,tree=dict(sha='1'*40)),
            '/git/trees/'+'1'*40: dict(sha='1'*40,truncated=False,tree=[dict(path='.github',mode='040000',type='tree',sha='2'*40)]),
            '/git/trees/'+'2'*40: dict(sha='2'*40,truncated=False,tree=[dict(path='workflows',mode='040000',type='tree',sha='3'*40)]),
            '/git/trees/'+'3'*40: dict(sha='3'*40,truncated=False,tree=[dict(path='ci.yml',mode='100644',type='blob',sha=BLOB)]),
            '/git/blobs/'+BLOB: dict(sha=BLOB,size=len(CONTENT),encoding='base64',content=base64.b64encode(CONTENT).decode())}
        self.adapter = type('Objects',(),{'object':lambda _,endpoint: copy.deepcopy(self.objects[endpoint])})()
    def test_immutable_source_green(self):
        self.assertEqual(E.source_bytes(self.adapter,W,'.github/workflows/ci.yml')['blob_sha'],BLOB)
    def test_truncated_trees_bad_objects_and_files(self):
        for object_path,key,value in [('/git/commits/'+W,'sha',H),('/git/trees/'+'1'*40,'truncated',True),
            ('/git/trees/'+'1'*40,'sha',H),('/git/blobs/'+BLOB,'sha',H),('/git/blobs/'+BLOB,'encoding','utf-8'),
            ('/git/blobs/'+BLOB,'content','!'),('/git/blobs/'+BLOB,'size',999),
            ('/git/blobs/'+BLOB,'content',base64.b64encode(b'changed').decode())]:
            self.setUp();self.objects[object_path][key]=value
            with self.subTest(key=key),self.assertRaises(E.Rejected): E.source_bytes(self.adapter,W,'.github/workflows/ci.yml')
        for mode,kind in [('120000','blob'),('160000','commit'),('040000','tree')]:
            self.setUp();self.objects['/git/trees/'+'3'*40]['tree'][0].update(mode=mode,type=kind)
            with self.assertRaises(E.Rejected): E.source_bytes(self.adapter,W,'.github/workflows/ci.yml')
        self.setUp();tree=self.objects['/git/trees/'+'1'*40]['tree'];tree.append(copy.deepcopy(tree[0]))
        with self.assertRaises(E.Rejected): E.source_bytes(self.adapter,W,'.github/workflows/ci.yml')
        self.setUp();self.objects['/git/trees/'+'1'*40]['tree']=[]
        with self.assertRaises(E.Rejected): E.source_bytes(self.adapter,W,'.github/workflows/ci.yml')



class GitTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root=Path(self.temp.name); (root/'manager').mkdir();(root/'public-trust').touch()
        self.git=E.Git(root/'manager',root/'public-trust')
        self.calls=[]
    def stub(self, unsigned=False, signer='owner', target=H, tag_type='tag', changed=False,
             tag_header=None):
        counts={}
        def run(*args,diagnostics=False):
            self.calls.append(args);counts[args[0]]=counts.get(args[0],0)+1
            if args[0]=='merge-base':return (B+'\n').encode()
            if args[0]=='rev-list':return (P+'\n'+H+'\n').encode()
            if args[0]=='verify-commit':
                E.require(not unsigned,'Unsigned commit');return b''
            if args[0]=='show':return ('G\n'+signer+'\nSHA256:synthetic\n').encode()
            if args[:2]==('cat-file','-t'):return (tag_type+'\n').encode()
            if args[:2]==('cat-file','-p'):
                return tag_header if tag_header is not None else (
                    'object '+H+'\ntype commit\ntag v1.2.3\ntagger synthetic\n\nmessage').encode()
            if args[0]=='rev-parse':
                result=target if args[-1].endswith('^{commit}') else 'e'*40
                if changed and counts['rev-parse']>2:result='f'*40
                return (result+'\n').encode()
            if args[0]=='verify-tag':
                E.require(not unsigned,'Unsigned tag')
                return b'',('Good "git" signature for '+signer+' with ED25519 key SHA256:synthetic\n').encode()
            raise AssertionError('Unexpected Git command')
        self.git.run=run
    def test_all_introduced_commits_and_head(self):
        self.stub();self.git.signatures(B,H,'owner','SHA256:synthetic')
        verified={call[1] for call in self.calls if call[0]=='verify-commit'}
        self.assertEqual(verified,{H,P})
        for kwargs in (dict(unsigned=True),dict(signer='unexpected')):
            self.stub(**kwargs)
            with self.assertRaises(E.Rejected):self.git.signatures(B,H,'owner','SHA256:synthetic')
    def test_release_tag_signer_type_target_and_change(self):
        self.stub();self.git.tag('v1.2.3',H,'owner','SHA256:synthetic')
        for kwargs in (dict(unsigned=True),dict(signer='unexpected'),dict(target=B),dict(tag_type='commit'),dict(changed=True)):
            self.stub(**kwargs)
            with self.assertRaises(E.Rejected):self.git.tag('v1.2.3',H,'owner','SHA256:synthetic')
    def test_child_environment_and_fixed_verifier(self):
        real_popen = subprocess.Popen
        def start(command, **kwargs):
            return real_popen(['/usr/bin/python3', '-I', '-c', 'pass'], **kwargs)
        token='synthetic-runtime-token'
        with patch.dict(os.environ,GH_TOKEN=token,GIT_CONFIG_COUNT='1',GIT_CONFIG_KEY_0='gpg.ssh.program',GIT_CONFIG_VALUE_0='submitted'),patch.object(E.subprocess,'Popen',side_effect=start) as run:
            self.git.run('verify-commit',H)
        kwargs=run.call_args.kwargs;command=run.call_args.args[0]
        self.assertTrue(token not in repr((command,kwargs)), 'Runtime token leaked to child')
        self.assertEqual(kwargs['env']['GIT_NO_REPLACE_OBJECTS'],'1')
        self.assertNotIn('GH_TOKEN',kwargs['env']);self.assertNotIn('GIT_CONFIG_COUNT',kwargs['env'])
        self.assertIn('gpg.ssh.program=/usr/bin/ssh-keygen',command)
        self.assertIn('--no-replace-objects',command)
    def test_expired_git_budget_starts_no_child(self):
        self.git.deadline = E.time.monotonic() - 1
        with patch.object(E.subprocess, 'Popen') as child:
            with self.assertRaises(E.Rejected): self.git.run('verify-commit', H)
            child.assert_not_called()

    def test_complete_rename_paths(self):
        self.git.run=lambda *args: b'old.go\0new.md\0'
        self.assertEqual(self.git.paths(B,H),['new.md','old.go'])
        self.git.run=lambda *args:b'partial'
        with self.assertRaises(E.Rejected):self.git.paths(B,H)


class CollectorTests(unittest.TestCase):
    def adapter(self, changed=False, partial=False, failure=False):
        args,policy,data=fixture()
        objects={}
        objects['/pulls/7']=data['pr'];objects['/git/ref/heads/master']=data['master']
        for run in data['runs']:
            rid=run['id'];attempt=data['attempts'][rid][0]
            objects['/actions/runs/'+str(rid)]=run
            objects['/actions/runs/'+str(rid)+'/attempts/1']=attempt['metadata']
            for cid,bundle in attempt['checks'].items():objects['/check-runs/'+str(cid)]=bundle['check']
        class Reader:
            def object(_,path):
                E.require(not failure,'API failure')
                result=copy.deepcopy(objects[path])
                if changed and path=='/actions/runs/10':result['run_attempt']=2
                return result
            def pages(_,path,key=None,query=None):
                E.require(not failure,'API failure')
                if path=='/actions/runs':return copy.deepcopy(data['runs'])
                if path=='/commits/'+H+'/check-runs':return copy.deepcopy(data['checks'])
                if path.endswith('/jobs'):return copy.deepcopy(data['attempts'][int(path.split('/')[3])][0]['jobs'])
                if path.endswith('/annotations'):
                    cid=int(path.split('/')[2]);rid=(cid//10000)*10
                    notices=copy.deepcopy(data['attempts'][rid][0]['checks'][cid]['annotations'])
                    return [] if partial else notices
                raise AssertionError('Unexpected API endpoint')
        return args,policy,data,Reader()
    def test_complete_collector(self):
        args,policy,data,adapter=self.adapter()
        with patch.object(E,'source_bytes',return_value={'blob_sha':BLOB,'content_sha256':hashlib.sha256(CONTENT).hexdigest()}):
            snapshot=E.collect(adapter,args,policy)
        self.assertEqual(snapshot,data)
    def test_changed_attempt_partial_annotations_and_api_error(self):
        for kwargs in (dict(changed=True),dict(partial=True),dict(failure=True)):
            args,policy,_,adapter=self.adapter(**kwargs)
            with patch.object(E,'source_bytes',return_value={}),self.assertRaises(E.Rejected):E.collect(adapter,args,policy)
    def test_reread_inventory_ignores_optional_only(self):
        _,policy,data,_=self.adapter();new=copy.deepcopy(data)
        new['runs'].append(dict(id=90,workflow_id=81,path=E.REVIEW_PATH))
        self.assertEqual(E.validation_inventory(data,policy),E.validation_inventory(new,policy))
        new['attempts'][10][0]['jobs'][0]['id']+=1
        self.assertNotEqual(E.validation_inventory(data,policy),E.validation_inventory(new,policy))


class CLITests(unittest.TestCase):
    def invoke(self, change=None, pending=False, token=True):
        args,policy,data=fixture()
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);candidate=root/'candidate';checkout=root/'manager';candidate.mkdir();checkout.mkdir()
            trust=root/'public-trust';trust.touch()
            p=root/'policy.json';p.write_text(json.dumps(policy));r=root/'review.json';r.write_text(json.dumps(review()))
            argv=['ci-evidence','--mode','pre','--pr','7','--head',H,'--base',B,'--master',B,'--policy-sha',P,
                  '--manager','manager','--signer','owner','--fingerprint','SHA256:synthetic',
                  '--checkout',str(checkout),'--candidate',str(candidate),'--trust',str(trust),
                  '--policy',str(p),'--review',str(r),'--wait','1']
            after=copy.deepcopy(data)
            if change:change(after)
            git=FakeGit();git.checkout=checkout;git.trust=trust
            adapter=type('Bounded',(),{})();adapter.deadline=E.time.monotonic()+1
            adapter.remaining=lambda: E.require(False,'Timed out') if pending else 1
            stream=io.StringIO();err=io.StringIO()
            env={'GH_TOKEN':'synthetic-runtime-token'} if token else {}
            with patch.object(sys,'argv',argv),patch.dict(os.environ,env,clear=True),patch.object(E,'Git',return_value=git),patch.object(E,'collect',side_effect=[data,after]),patch.object(E,'Adapter',side_effect=lambda supplied,wait: (E.require(supplied is not None,'Runtime token missing') or adapter)),patch.object(sys,'stdout',stream),patch.object(sys,'stderr',err):
                if pending:data['runs']=[];data['checks']=[]
                code=E.main()
            output=stream.getvalue()+err.getvalue()
            self.assertTrue('synthetic-runtime-token' not in output,'Runtime token leaked')
            return code,stream.getvalue()
    def test_actual_parser_and_output_offline(self):
        code,output=self.invoke();self.assertEqual(code,0)
        result=json.loads(output);self.assertIn('cutoff',result);self.assertEqual(len(result['runs']),4)
    def test_changed_head_set_and_bounded_timeout(self):
        for change in (lambda d:d['pr']['head'].update(sha=B),lambda d:d['runs'].pop(),
                       lambda d:d['attempts'][10][0]['jobs'][0].update(id=999)):
            code,output=self.invoke(change);self.assertEqual(code,1);self.assertEqual(output,'')
        self.assertEqual(self.invoke(pending=True)[0],1)
        self.assertEqual(self.invoke(token=False)[0],1)



class BoundaryTests(TransportTests):
    def test_types_missing_fields_and_policy_booleans(self):
        claim=IdentityTests().record()
        for key in claim:
            bad=dict(claim);del bad[key]
            with self.subTest(key=key),self.assertRaises(E.Rejected):E.identity(json.dumps(bad))
        _,policy,_=fixture()
        for mutation in (lambda p:p.update(publication='enabled'),lambda p:p.update(sources=[]),
                         lambda p:p['sources'][0].update(report=''),lambda p:p['sources'][0].update(workflow_sha='mutable'),
                         lambda p:p['sources'][0].update(content_sha256='0'),
                         lambda p:p['review_workflows'][0].update(path='dynamic/dependabot/update-graph'),
                         lambda p:p.update(actions_app=True),lambda p:p.update(reviewed=True)):
            bad=copy.deepcopy(policy);mutation(bad)
            with self.assertRaises(E.Rejected):E.policy_record(bad,P)
    def test_foreign_source_refs_and_duplicate_json(self):
        for ref in ('other/repo/.github/workflows/ci.yml@refs/pull/7/merge',
                    E.REPOSITORY+'/.github/workflows/other.yml@refs/pull/7/merge',
                    E.REPOSITORY+'/.github/workflows/ci.yml@refs/heads/../master'):
            bad=IdentityTests().record();bad['workflow_ref']=ref
            with self.assertRaises(E.Rejected):E.identity(json.dumps(bad))
    def test_http_errors_lengths_and_response_bound(self):
        for status in (400,401,403,404,429,500,503):
            adapter,requests=self.adapter([Response(status)])
            with self.assertRaises(E.Rejected):adapter.get('/actions/runs')
            self.assertEqual(len(requests),1)
        for headers in ({'Content-Type':'text/plain'},{'Content-Length':'100'},
                        {'Content-Length':str(E.MAX_BYTES+1)}):
            adapter,_=self.adapter([Response(body=b'{}',headers=headers)])
            with self.assertRaises(E.Rejected):adapter.get('/actions/runs')
        adapter,_=self.adapter([Response(body=b' '*(E.MAX_BYTES+1))])
        with self.assertRaises(E.Rejected):adapter.get('/actions/runs')
        adapter,_=self.adapter([Response()]);adapter.deadline=E.time.monotonic()-1
        with self.assertRaises(E.Rejected):adapter.get('/actions/runs')
        adapter,_=self.adapter([Response()]);adapter.requests=5000
        with self.assertRaises(E.Rejected):adapter.get('/actions/runs')
    def test_partial_next_page_fails(self):
        body=json.dumps({'total_count':2,'workflow_runs':[{'id':1}]}).encode()
        link='<https://api.github.com/repos/'+E.REPOSITORY+'/actions/runs?per_page=100&page=2>; rel="next"'
        adapter,requests=self.adapter([Response(body=body,headers={'Link':link})])
        with self.assertRaises(E.Rejected):adapter.pages('/actions/runs','workflow_runs')
        self.assertEqual(len(requests),1)
    def test_finite_retries_and_tls_ignores_environment(self):
        connections=[]
        class Offline:
            def __init__(self,*args,**kwargs):connections.append((args,kwargs))
            def connect(self):raise OSError('Synthetic transport failure')
            def close(self):pass
        with patch.object(E.http.client,'HTTPSConnection',Offline),patch.object(E.time,'sleep'),patch.dict(os.environ,HTTPS_PROXY='https://foreign.invalid',SSLKEYLOGFILE='/must-not-write',SSL_CERT_FILE='/must-not-read'):
            with self.assertRaises(E.Rejected):E.Adapter('synthetic-runtime-token',10).get('/actions/runs')
        self.assertEqual(len(connections),3)
        for args,kwargs in connections:
            self.assertEqual(args,('api.github.com',443))
            self.assertTrue(kwargs['context'].check_hostname)
            self.assertEqual(kwargs['context'].verify_mode,E.ssl.CERT_REQUIRED)
            self.assertIsNone(kwargs['context'].keylog_filename)
    def test_listed_forgery_and_reusable_source(self):
        args,policy,data=fixture();data['checks'][0]['conclusion']='failure'
        with self.assertRaises(E.Rejected):E.evaluate(data,args,policy,FakeGit())
        args,policy,data=fixture();data['runs'][0]['referenced_workflows']=[{'path':'other.yml'}]
        with self.assertRaises(E.Rejected):E.evaluate(data,args,policy,FakeGit())


class RetentionTests(unittest.TestCase):
    setUp = EvaluationTests.setUp
    evaluate = EvaluationTests.evaluate
    add_attempt = EvaluationTests.add_attempt
    add_push_runs = EvaluationTests.add_push_runs
    retain_pre = EvaluationTests.retain_pre
    def post(self):
        self.retain_pre()
        self.add_push_runs()

    def remove_run(self, run_id):
        self.data['runs'] = [run for run in self.data['runs'] if run['id'] != run_id]
        attempts = self.data['attempts'].pop(run_id)
        ids = {cid for attempt in attempts for cid in attempt['checks']}
        self.data['checks'] = [check for check in self.data['checks'] if check['id'] not in ids]

    def test_current_pushes_cannot_replace_retained_pr_inventory(self):
        for mode in ('post', 'release'):
            for keep_policy in (True, False):
                with self.subTest(mode=mode, keep_policy=keep_policy):
                    self.setUp(); self.post()
                    self.args.mode = mode; self.args.tag = 'v1.2.3' if mode == 'release' else None
                    for rid in (10, 20, 30, 40):
                        self.remove_run(rid)
                    self.data['sources'] = {key: value for key, value in self.data['sources'].items() if key[0] == H}
                    if not keep_policy:
                        self.policy['sources'] = [item for item in self.policy['sources'] if item['workflow_sha'] == H]
                    with self.assertRaises(E.Rejected): self.evaluate()

    def test_retained_input_is_required_and_cannot_be_synthesized_from_pushes(self):
        for mode in ('post', 'release'):
            self.setUp(); self.add_push_runs()
            self.args.mode = mode; self.args.tag = 'v1.2.3' if mode == 'release' else None
            with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp(); self.post()
        with self.assertRaises(E.Rejected):
            self.args.retained = dict(self.evaluate(), cutoff='2026-01-01T00:00:00+00:00')
            self.evaluate()

    def test_every_retained_run_attempt_job_check_and_source_remains_required(self):
        mutations = [lambda s: s['attempts'][10].pop(),
                     lambda s: s['attempts'][10][0]['jobs'].pop(),
                     lambda s: s['attempts'][10][0]['checks'].pop(10001),
                     lambda s: s['sources'].pop((W, '.github/workflows/ci.yml')),
                     lambda s: s['attempts'][10][0]['checks'][10001]['annotations'].clear()]
        for mutate in mutations:
            self.setUp(); self.post(); mutate(self.data)
            with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp(); self.post()
        self.policy['sources'] = [item for item in self.policy['sources'] if item['workflow_sha'] == H]
        with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp(); self.post()
        self.policy['sources'][0]['report'] = 'private/replacement'
        with self.assertRaises(E.Rejected): self.evaluate()

    def test_retained_full_rerun_is_separate_from_independent_runs(self):
        self.add_attempt(); self.post()
        self.assertEqual(self.evaluate()['runs'][0]['attempts'], 2)
        self.remove_run(10)
        with self.assertRaises(E.Rejected): self.evaluate()
        self.setUp(); self.post(); self.add_attempt()
        self.assertEqual(self.evaluate()['runs'][0]['attempts'], 2)
        self.data['attempts'][10][1]['metadata']['conclusion'] = 'failure'
        with self.assertRaises(E.Rejected): self.evaluate()

    def test_retained_record_rejects_missing_extra_stale_and_numeric_fields(self):
        mutations = [lambda r: r.update(mode='post'), lambda r: r.update(pr=7.0),
                     lambda r: r.update(head_sha=B), lambda r: r.update(reviewed_base_sha=H),
                     lambda r: r.update(policy_sha=H), lambda r: r.update(master_sha=H),
                     lambda r: r.update(repository='other/repo'), lambda r: r.update(extra=True),
                     lambda r: r.pop('cutoff'), lambda r: r.update(cutoff='unknown'),
                     lambda r: r.update(runs=[]), lambda r: r['runs'].append(copy.deepcopy(r['runs'][0])),
                     lambda r: r['runs'][0].update(id=10.0), lambda r: r['runs'][0].update(attempts=True),
                     lambda r: r['runs'][0]['attempt_evidence'][0].update(attempt=True),
                     lambda r: r['runs'][0]['sources'][0].update(attempt=1.0),
                     lambda r: r['runs'][0]['attempt_evidence'][0]['jobs'][0].update(job_id=1001.0),
                     lambda r: r['runs'][0]['attempt_evidence'][0]['jobs'][0].update(check_id=10001.0),
                     lambda r: r['runs'][0]['attempt_evidence'][0]['jobs'][0].update(app_id=91.0),
                     lambda r: r['runs'][0]['attempt_evidence'][0]['jobs'][0]['source_binding'].update(workflow_id=True),
                     lambda r: r['runs'][0]['attempt_evidence'][0]['jobs'][0].update(extra=True)]
        for index, mutate in enumerate(mutations):
            with self.subTest(index=index):
                self.setUp(); self.post(); mutate(self.args.retained)
                with self.assertRaises(E.Rejected): self.evaluate()

    def test_original_immutable_commit_and_blob_remain_required(self):
        self.post()
        source = SourceTests(); source.setUp()
        objects = source.objects
        current = b'name: current synthetic workflow\n'
        current_blob = hashlib.sha1(b'blob '+str(len(current)).encode()+b'\0'+current).hexdigest()
        objects['/git/trees/'+'3'*40]['tree'] = [dict(path=name+'.yml', mode='100644', type='blob', sha=BLOB) for name in E.JOBS]
        for tree_id, next_id, path in (('4'*40, '5'*40, '.github'), ('5'*40, '6'*40, 'workflows')):
            objects['/git/trees/'+tree_id] = dict(sha=tree_id, truncated=False,
                tree=[dict(path=path, mode='040000', type='tree', sha=next_id)])
        objects['/git/commits/'+H] = dict(sha=H, tree=dict(sha='4'*40))
        objects['/git/trees/'+'6'*40] = dict(sha='6'*40, truncated=False,
            tree=[dict(path=name+'.yml', mode='100644', type='blob', sha=current_blob) for name in E.JOBS])
        objects['/git/blobs/'+current_blob] = dict(sha=current_blob, size=len(current),
            encoding='base64', content=base64.b64encode(current).decode())
        for binding in self.policy['sources']:
            if binding['workflow_sha'] == H:
                binding.update(blob_sha=current_blob, content_sha256=hashlib.sha256(current).hexdigest())
        objects['/pulls/7'] = self.data['pr']; objects['/git/ref/heads/master'] = self.data['master']
        pages = {'/actions/runs': self.data['runs'], '/commits/'+H+'/check-runs': self.data['checks']}
        for run in self.data['runs']:
            rid = run['id']; attempt = self.data['attempts'][rid][0]
            objects['/actions/runs/'+str(rid)] = run
            objects['/actions/runs/'+str(rid)+'/attempts/1'] = attempt['metadata']
            pages['/actions/runs/'+str(rid)+'/attempts/1/jobs'] = attempt['jobs']
            for cid, bundle in attempt['checks'].items():
                objects['/check-runs/'+str(cid)] = bundle['check']
                pages['/check-runs/'+str(cid)+'/annotations'] = bundle['annotations']
        requested = []
        class Reader:
            def object(_, path):
                requested.append(path)
                E.require(path in objects, 'Synthetic immutable object is unavailable')
                return copy.deepcopy(objects[path])
            def pages(_, path, key=None, query=None): return copy.deepcopy(pages[path])
        for mode in ('post', 'release'):
            self.args.mode = mode; self.args.tag = 'v1.2.3' if mode == 'release' else None
            collected = E.collect(Reader(), self.args, self.policy)
            E.evaluate(collected, self.args, self.policy, self.git)
            self.assertIn('/git/commits/'+W, requested)
            self.assertIn('/git/blobs/'+BLOB, requested)
            for path in ('/git/commits/'+W, '/git/blobs/'+BLOB):
                original = objects.pop(path)
                with self.subTest(mode=mode, path=path), self.assertRaises(E.Rejected):
                    E.collect(Reader(), self.args, self.policy)
                objects[path] = original


class NumericTests(unittest.TestCase):
    def test_all_consumed_numeric_identities_are_exact_integers(self):
        locations = [lambda p, s: (s['pr'], 'number'),
                     lambda p, s: (s['runs'][0], 'id'),
                     lambda p, s: (s['runs'][0], 'workflow_id'),
                     lambda p, s: (s['runs'][0], 'check_suite_id'),
                     lambda p, s: (s['runs'][0], 'run_attempt'),
                     lambda p, s: (s['runs'][0]['pull_requests'][0], 'number'),
                     lambda p, s: (s['attempts'][10][0]['metadata'], 'id'),
                     lambda p, s: (s['attempts'][10][0]['metadata'], 'workflow_id'),
                     lambda p, s: (s['attempts'][10][0]['metadata'], 'check_suite_id'),
                     lambda p, s: (s['attempts'][10][0]['metadata'], 'run_attempt'),
                     lambda p, s: (s['attempts'][10][0]['metadata']['pull_requests'][0], 'number'),
                     lambda p, s: (s['attempts'][10][0]['jobs'][0], 'id'),
                     lambda p, s: (s['attempts'][10][0]['jobs'][0], 'run_id'),
                     lambda p, s: (s['attempts'][10][0]['jobs'][0], 'run_attempt'),
                     lambda p, s: (s['attempts'][10][0]['checks'][10001]['check'], 'id'),
                     lambda p, s: (s['attempts'][10][0]['checks'][10001]['check']['app'], 'id'),
                     lambda p, s: (s['attempts'][10][0]['checks'][10001]['check']['check_suite'], 'id'),
                     lambda p, s: (s['checks'][0], 'id'),
                     lambda p, s: (s['checks'][0]['app'], 'id'),
                     lambda p, s: (s['checks'][0]['check_suite'], 'id')]
        for index, location in enumerate(locations):
            for kind in ('bool', 'float'):
                with self.subTest(index=index, kind=kind):
                    args, policy, data = fixture(); target, key = location(policy, data)
                    target[key] = True if kind == 'bool' else float(target[key])
                    with self.assertRaises(E.Rejected): E.evaluate(data, args, policy, FakeGit())
        for container, old in (('attempts', 10), ('checks', 10001)):
            args, policy, data = fixture()
            mapping = data['attempts'] if container == 'attempts' else data['attempts'][10][0]['checks']
            mapping[float(old)] = mapping.pop(old)
            with self.subTest(container=container), self.assertRaises(E.Rejected):
                E.evaluate(data, args, policy, FakeGit())

    def test_optional_numeric_identities_reject_boolean_and_float_aliases(self):
        for field in ('workflow_id', 'check_suite_id'):
            for value in (True, 81.0 if field == 'workflow_id' else 901.0):
                args, policy, data = fixture()
                optional = dict(id=90, workflow_id=81, path=E.REVIEW_PATH, check_suite_id=901)
                optional[field] = value; data['runs'].append(optional)
                with self.subTest(field=field, value=value), self.assertRaises(E.Rejected):
                    E.evaluate(data, args, policy, FakeGit())
        for app in (True, 99.0):
            args, policy, data = fixture(); policy['review_apps'] = [1, 99]
            data['checks'].append(dict(id=999, head_sha=H, app=dict(id=app)))
            with self.assertRaises(E.Rejected): E.evaluate(data, args, policy, FakeGit())
        args, policy, data = fixture()
        data['runs'].append(dict(id=90, workflow_id=81, path=E.REVIEW_PATH, check_suite_id=901))
        data['checks'].append(dict(id=999, head_sha=H, app=dict(id=91), check_suite=dict(id=901.0)))
        with self.assertRaises(E.Rejected): E.evaluate(data, args, policy, FakeGit())

    def test_policy_and_annotation_numeric_identities(self):
        for mutate in (lambda p: p.update(actions_app=91.0),
                       lambda p: p['workflows']['ci'].update(id=True),
                       lambda p: p['sources'][0].update(workflow_id=1.0),
                       lambda p: p['review_workflows'][0].update(id=81.0),
                       lambda p: p.update(review_apps=[99.0])):
            _, policy, _ = fixture(); mutate(policy)
            with self.assertRaises(E.Rejected): E.policy_record(policy, P)
        for key in ('version', 'run_id', 'run_attempt'):
            for kind in ('bool', 'float'):
                claim = IdentityTests().record()
                claim[key] = True if kind == 'bool' else float(claim[key])
                with self.assertRaises(E.Rejected): E.identity(json.dumps(claim))

    def test_collector_annotation_count_is_an_exact_integer(self):
        for value in (True, 1.0):
            args, policy, _, adapter = CollectorTests().adapter()
            original = adapter.object
            def object_read(path):
                result = original(path)
                if path.startswith('/check-runs/'):
                    result['output']['annotations_count'] = value
                return result
            adapter.object = object_read
            with patch.object(E, 'source_bytes', return_value={}), self.assertRaises(E.Rejected):
                E.collect(adapter, args, policy)

    def test_collector_reread_numeric_alias_rejects(self):
        for field in ('run_attempt', 'pr'):
            args, policy, _, adapter = CollectorTests().adapter()
            original = adapter.object
            def object_read(path):
                result = original(path)
                if path == '/actions/runs/10':
                    if field == 'pr': result['pull_requests'][0]['number'] = 7.0
                    else: result['run_attempt'] = 1.0
                return result
            adapter.object = object_read
            with self.subTest(field=field), patch.object(E, 'source_bytes', return_value={}), self.assertRaises(E.Rejected):
                E.collect(adapter, args, policy)


class TagNameTests(unittest.TestCase):
    setUp = GitTests.setUp
    stub = GitTests.stub
    def test_signed_internal_name_must_match_selected_ref(self):
        self.stub(tag_header=('object '+H+'\ntype commit\ntag v9.9.9\ntagger synthetic\n\nmessage').encode())
        with self.assertRaises(E.Rejected): self.git.tag('v1.2.3', H, 'owner', 'SHA256:synthetic')

    def test_internal_tag_headers_are_strict(self):
        for header in ('object '+H+'\ntype commit\ntagger synthetic',
                       'object '+H+'\ntype commit\ntag v1.2.3\ntag v1.2.3\ntagger synthetic',
                       'object '+H+'\ntype unknown\ntag v1.2.3\ntagger synthetic',
                       'object invalid\ntype commit\ntag v1.2.3\ntagger synthetic',
                       'object '+H+'\ntype commit\ntag v1.2.3 \ntagger synthetic'):
            self.stub(tag_header=(header+'\n\nmessage').encode())
            with self.subTest(header=header), self.assertRaises(E.Rejected):
                self.git.tag('v1.2.3', H, 'owner', 'SHA256:synthetic')
        self.stub(tag_header=b'\xff')
        with self.assertRaises(E.Rejected): self.git.tag('v1.2.3', H, 'owner', 'SHA256:synthetic')

    def test_annotated_target_keeps_peeled_commit_check(self):
        self.stub(tag_header=('object '+P+'\ntype tag\ntag v1.2.3\ntagger synthetic\n\nmessage').encode())
        self.git.tag('v1.2.3', H, 'owner', 'SHA256:synthetic')


class GitPipeTests(unittest.TestCase):
    setUp = GitTests.setUp
    def producer(self, code):
        real_popen = subprocess.Popen
        self.children = []
        def start(command, **kwargs):
            child = real_popen(['/usr/bin/python3', '-I', '-c', code], **kwargs)
            self.children.append(child)
            return child
        return patch.object(E.subprocess, 'Popen', side_effect=start)

    def assert_reaped(self):
        self.assertEqual(len(self.children), 1)
        self.assertIsNotNone(self.children[0].poll())
        self.assertTrue(self.children[0].stdout.closed)
        self.assertTrue(self.children[0].stderr.closed)

    @contextmanager
    def completed_producer(self, sizes):
        # Files model fully buffered child pipes without depending on kernel pipe capacity.
        root = Path(self.temp.name)
        for name, size in zip(('stdout', 'stderr'), sizes):
            (root/name).write_bytes(b'x'*size)
        real_popen, real_read = subprocess.Popen, os.read
        self.children = []
        self.read_counts = {'stdout': 0, 'stderr': 0}
        names = {}
        def start(command, **kwargs):
            child = real_popen(['/usr/bin/python3', '-I', '-c', 'pass'], cwd=kwargs['cwd'], env=kwargs['env'],
                               stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            child.wait(timeout=5)
            child.stdout = (root/'stdout').open('rb', buffering=0)
            child.stderr = (root/'stderr').open('rb', buffering=0)
            self.children.append(child)
            names.update({child.stdout.fileno(): 'stdout', child.stderr.fileno(): 'stderr'})
            return child
        def read(fd, count):
            raw = real_read(fd, count)
            if fd in names:
                self.read_counts[names[fd]] += len(raw)
            return raw
        with patch.object(E.subprocess, 'Popen', side_effect=start), \
                patch.object(selectors, 'DefaultSelector', selectors.SelectSelector), \
                patch.object(E.os, 'read', side_effect=read), patch.object(E.os, 'killpg'):
            yield

    def test_separate_pipe_limits_bound_reads_from_completed_producer(self):
        for sizes in ((32768, 8192), (8192, 32768), (32768, 32768)):
            with self.subTest(sizes=sizes), patch.object(E, 'MAX_BYTES', 8192), self.completed_producer(sizes):
                with self.assertRaises(E.Rejected): self.git.run('synthetic')
                self.assertEqual(self.children[0].returncode, 0)
                for name in ('stdout', 'stderr'):
                    self.assertLessEqual(self.read_counts[name], 8193)
                self.assertIn(8193, self.read_counts.values())
                self.assert_reaped()

    def test_exact_independent_pipe_boundaries_pass(self):
        with patch.object(E, 'MAX_BYTES', 8192), self.completed_producer((8192, 8192)):
            self.assertEqual(self.git.run('synthetic', diagnostics=True), (b'x'*8192, b'x'*8192))
            self.assertEqual(self.read_counts, {'stdout': 8192, 'stderr': 8192})
            self.assert_reaped()

    def test_timeout_and_early_output_close_reap_child(self):
        for code in ('import time; time.sleep(2)',
                     'import os,time; os.close(1); os.close(2); time.sleep(2)'):
            self.git.deadline = time.monotonic() + 0.1
            with self.subTest(code=code), self.producer(code):
                with self.assertRaises(E.Rejected): self.git.run('synthetic')
                self.assert_reaped()

    def test_one_byte_over_either_pipe_rejects(self):
        for sizes in ((8193, 8192), (8192, 8193)):
            with self.subTest(sizes=sizes), patch.object(E, 'MAX_BYTES', 8192), self.completed_producer(sizes):
                with self.assertRaises(E.Rejected): self.git.run('synthetic')
                for name, size in zip(('stdout', 'stderr'), sizes):
                    if size == 8193: self.assertEqual(self.read_counts[name], 8193)
                    else: self.assertLessEqual(self.read_counts[name], 8192)
                self.assert_reaped()

    def test_read_error_and_cancellation_reap_child(self):
        real_read = os.read
        for error in (OSError('synthetic-runtime-token'), KeyboardInterrupt()):
            def read(fd, count):
                if self.children and fd == self.children[0].stdout.fileno():
                    raise error
                return real_read(fd, count)
            with self.subTest(error=type(error).__name__), self.producer(
                    'import os,time; os.write(1,b"ready"); time.sleep(2)'), patch.object(E.os, 'read', side_effect=read):
                with self.assertRaises(E.Rejected) as caught: self.git.run('synthetic')
                self.assertNotIn('synthetic-runtime-token', str(caught.exception))
                self.assert_reaped()

    def test_nonzero_exit_and_startup_error_use_fixed_diagnostics(self):
        with self.producer('import os; os.write(2,b"synthetic-runtime-token"); raise SystemExit(1)'):
            with self.assertRaises(E.Rejected) as caught: self.git.run('synthetic')
            self.assertNotIn('synthetic-runtime-token', str(caught.exception))
            self.assert_reaped()
        with patch.object(E.subprocess, 'Popen', side_effect=OSError('synthetic-runtime-token')):
            with self.assertRaises(E.Rejected) as caught: self.git.run('synthetic')
            self.assertNotIn('synthetic-runtime-token', str(caught.exception))


class RetentionCLITests(unittest.TestCase):
    def invoke(self, mode='post', supplied=True, mutate=None, location='private', changed=False):
        test = EvaluationTests(); test.setUp()
        saved = test.retain_pre()
        if mutate: mutate(saved)
        test.add_push_runs()
        if mode == 'pre':
            test.setUp()
        args, policy, data = test.args, test.policy, test.data
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            candidate, manager, trust = root/'candidate', root/'manager', root/'trust'
            candidate.mkdir(); manager.mkdir(); trust.touch()
            p, r = root/'policy.json', root/'review.json'
            p.write_text(json.dumps(policy)); r.write_text(json.dumps(review()))
            retained = (candidate if location == 'candidate' else root)/'accepted-pre.json'
            if location == 'oversized':
                retained.write_bytes(b' ' * (E.MAX_BYTES+1))
            elif location == 'duplicate':
                retained.write_text('{"pr":7,"pr":7}')
            else:
                retained.write_text(json.dumps(saved))
            if location == 'symlink':
                actual = candidate/'actual.json'; actual.write_text(json.dumps(saved))
                retained.unlink(); retained.symlink_to(actual)
            argv = ['ci-evidence', '--mode', mode, '--pr', '7', '--head', H, '--base', B,
                    '--master', args.master, '--policy-sha', P, '--manager', 'manager',
                    '--signer', 'owner', '--fingerprint', 'SHA256:synthetic', '--checkout', str(manager),
                    '--candidate', str(candidate), '--trust', str(trust), '--policy', str(p),
                    '--review', str(r), '--wait', '1']
            if supplied: argv += ['--retained-pre', str(retained)]
            if mode == 'release': argv += ['--tag', 'v1.2.3']
            git = FakeGit(); git.checkout = manager; git.trust = trust
            adapter = argparse.Namespace(deadline=time.monotonic()+1, remaining=lambda: 1)
            calls = []
            def collect(*unused):
                calls.append(True)
                if changed and len(calls) == 2:
                    retained.write_text(json.dumps(dict(saved, cutoff='2026-01-01T00:00:01+00:00')))
                return copy.deepcopy(data)
            out, err = io.StringIO(), io.StringIO()
            with patch.object(sys, 'argv', argv), patch.dict(os.environ, {'GH_TOKEN': 'synthetic-runtime-token'}, clear=True), \
                    patch.object(E, 'Git', return_value=git), patch.object(E, 'Adapter', return_value=adapter) as factory, \
                    patch.object(E, 'collect', side_effect=collect), patch.object(sys, 'stdout', out), patch.object(sys, 'stderr', err):
                code = E.main()
            self.assertNotIn('synthetic-runtime-token', out.getvalue()+err.getvalue())
            return code, out.getvalue(), factory.call_count

    def test_saved_pre_output_accepts_post_and_release(self):
        for mode in ('post', 'release'):
            code, out, count = self.invoke(mode)
            self.assertEqual(code, 0); self.assertEqual(count, 1)
            self.assertEqual(len(json.loads(out)['runs']), 8)

    def test_missing_input_rejects_before_adapter(self):
        for mode in ('post', 'release'):
            code, out, count = self.invoke(mode, supplied=False)
            self.assertEqual((code, out, count), (1, '', 0))

    def test_pre_mode_rejects_retained_input(self):
        self.assertEqual(self.invoke('pre'), (1, '', 0))

    def test_private_input_bounds_paths_and_schema_reject_before_adapter(self):
        for location in ('candidate', 'symlink', 'oversized', 'duplicate'):
            with self.subTest(location=location):
                self.assertEqual(self.invoke(location=location), (1, '', 0))
        self.assertEqual(self.invoke(mutate=lambda r: r.update(pr=7.0)), (1, '', 0))

    def test_changed_retained_input_rejects(self):
        self.assertEqual(self.invoke(changed=True), (1, '', 1))


def manual_ci_fixture(branch):
    args, policy, data = fixture()
    run = copy.deepcopy(data['runs'][0])
    run.update(id=810, check_suite_id=8100, event='workflow_dispatch', head_branch=branch,
               html_url=f'https://github.com/{E.REPOSITORY}/actions/runs/810')
    binding = dict(policy['sources'][0], workflow_ref=E.REPOSITORY+'/.github/workflows/ci.yml@refs/heads/'+branch)
    policy['sources'].append(binding)
    attempt = copy.deepcopy(data['attempts'][10][0]); attempt['metadata'] = copy.deepcopy(run)
    original = attempt['checks']; attempt['checks'] = {}
    for job in attempt['jobs']:
        old = E.check_id(job['check_run_url']); cid = old+81000
        job.update(id=job['id']+81000, run_id=810,
                   check_run_url=f'https://api.github.com/repos/{E.REPOSITORY}/check-runs/{cid}')
        bundle = original[old]; check = bundle['check']
        check.update(id=cid, check_suite=dict(id=8100),
                     details_url=f'https://github.com/{E.REPOSITORY}/actions/runs/810/job/{job["id"]}')
        for notice in bundle['annotations']:
            claim = json.loads(notice['message'])
            claim.update(run_id=810, workflow_ref=binding['workflow_ref'])
            notice['message'] = json.dumps(claim, ensure_ascii=False, separators=(',', ':'))
        attempt['checks'][cid] = bundle; data['checks'].append(copy.deepcopy(check))
    data['runs'].append(run); data['attempts'][810] = [attempt]
    return args, policy, data


class RetainedAnnotationTests(unittest.TestCase):
    def notices(self, data):
        return [notice for bundle in data['attempts'][810][0]['checks'].values() for notice in bundle['annotations']]

    def ascii_branch(self):
        _, _, data = manual_ci_fixture('a')
        size = max(len(notice['message'].encode('utf-8')) for notice in self.notices(data))
        length = 2048-size+1
        branch = ('a'*99+'/')*(length//100)+'a'*(length%100)
        return branch[:-1]+'a' if branch.endswith('/') else branch

    def invoke(self, args, policy, data, saved=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            candidate, manager, trust = root/'candidate', root/'manager', root/'trust'
            candidate.mkdir(); manager.mkdir(); trust.touch()
            p, r, retained = root/'policy.json', root/'review.json', root/'accepted-pre.json'
            p.write_text(json.dumps(policy)); r.write_text(json.dumps(review()))
            if saved is not None: retained.write_text(saved)
            argv = ['ci-evidence', '--mode', args.mode, '--pr', '7', '--head', H, '--base', B,
                    '--master', args.master, '--policy-sha', P, '--manager', 'manager',
                    '--signer', 'owner', '--fingerprint', 'SHA256:synthetic', '--checkout', str(manager),
                    '--candidate', str(candidate), '--trust', str(trust), '--policy', str(p),
                    '--review', str(r), '--wait', '1']
            if saved is not None: argv += ['--retained-pre', str(retained)]
            if args.mode == 'release': argv += ['--tag', 'v1.2.3']
            git = FakeGit(); git.checkout = manager; git.trust = trust
            adapter = argparse.Namespace(deadline=time.monotonic()+1, remaining=lambda: 1)
            out, err = io.StringIO(), io.StringIO()
            with patch.object(sys, 'argv', argv), patch.dict(os.environ, {'GH_TOKEN': 'synthetic-runtime-token'}, clear=True), \
                    patch.object(E, 'Git', return_value=git), patch.object(E, 'Adapter', return_value=adapter) as factory, \
                    patch.object(E, 'collect', side_effect=[copy.deepcopy(data), copy.deepcopy(data)]), \
                    patch.object(sys, 'stdout', out), patch.object(sys, 'stderr', err):
                code = E.main()
            self.assertNotIn('synthetic-runtime-token', out.getvalue()+err.getvalue())
            return code, out.getvalue(), err.getvalue(), factory.call_count

    def later(self, branch, mode):
        test = EvaluationTests(); test.setUp(); test.add_push_runs()
        _, original_policy, original = manual_ci_fixture(branch)
        test.policy['sources'].append(original_policy['sources'][-1])
        test.data['runs'].append(original['runs'][-1])
        test.data['attempts'][810] = original['attempts'][810]
        test.data['checks'].extend(check for check in original['checks'] if check['id'] >= 81000)
        test.args.mode = mode; test.args.tag = 'v1.2.3' if mode == 'release' else None
        return test.args, test.policy, test.data

    def saved_pre(self, branch):
        args, policy, data = manual_ci_fixture(branch)
        code, saved, err, count = self.invoke(args, policy, data)
        self.assertEqual((code, err, count), (0, '', 1))
        self.assertEqual(len(json.loads(saved)['runs']), 5)
        return saved

    def test_genuine_pre_output_round_trips_compact_utf8_and_ascii(self):
        branches = ('/'.join(['\u754c'*40]*10), self.ascii_branch())
        for branch in branches:
            _, _, data = manual_ci_fixture(branch)
            raw_sizes = [len(notice['message'].encode('utf-8')) for notice in self.notices(data)]
            default_sizes = [len(json.dumps(json.loads(notice['message'])).encode('utf-8')) for notice in self.notices(data)]
            self.assertLessEqual(max(raw_sizes), 2048)
            self.assertGreater(min(default_sizes), 2048)
            if branch.isascii(): self.assertEqual(max(raw_sizes), 2048)
            else: self.assertEqual(raw_sizes, [1531, 1533, 1537])
            saved = self.saved_pre(branch)
            for mode in ('post', 'release'):
                with self.subTest(ascii=branch.isascii(), mode=mode):
                    args, policy, later = self.later(branch, mode)
                    code, output, err, count = self.invoke(args, policy, later, saved)
                    self.assertEqual((code, err, count), (0, '', 1))
                    self.assertEqual(len(json.loads(output)['runs']), 9)
                    original = json.loads(saved)['runs'][-1]['attempt_evidence'][0]['jobs'][0]['annotation']
                    accepted = json.loads(output)['runs'][-1]['attempt_evidence'][0]['jobs'][0]['annotation']
                    self.assertEqual(original, accepted)

    def test_raw_notice_bound_schema_source_and_retention_stay_required(self):
        branch = '/'.join(['\u754c'*40]*10)
        saved = self.saved_pre(branch)
        mutations = [lambda d: self.notices(d)[0].update(message=self.notices(d)[0]['message']+' '*2048),
                     lambda d: self.notices(d)[0].update(message='{'),
                     lambda d: self.notices(d)[0].update(message='{"version":2,"version":2}'),
                     lambda d: self.notices(d)[0].update(message=json.dumps(dict(
                         json.loads(self.notices(d)[0]['message']), run_attempt=True), ensure_ascii=False)),
                     lambda d: self.notices(d)[0].update(message=json.dumps(dict(
                         json.loads(self.notices(d)[0]['message']), tested_sha=B), ensure_ascii=False)),
                     lambda d: self.notices(d)[0].update(message=json.dumps(dict(
                         json.loads(self.notices(d)[0]['message']), workflow_sha=P), ensure_ascii=False)),
                     lambda d: d.update(runs=[run for run in d['runs'] if run['id'] != 810])]
        for mode in ('post', 'release'):
            for index, mutate in enumerate(mutations):
                with self.subTest(mode=mode, index=index):
                    args, policy, later = self.later(branch, mode); mutate(later)
                    code, output, _, count = self.invoke(args, policy, later, saved)
                    self.assertEqual((code, output, count), (1, '', 1))
        args, policy, data = manual_ci_fixture(self.ascii_branch())
        self.notices(data)[-1]['message'] += ' '
        self.assertEqual(len(self.notices(data)[-1]['message'].encode('utf-8')), 2049)
        code, output, _, count = self.invoke(args, policy, data)
        self.assertEqual((code, output, count), (1, '', 1))

    def test_retained_annotation_values_remain_bounded_and_typed(self):
        branch = self.ascii_branch(); saved = self.saved_pre(branch)
        for key, value in (('workflow_ref', E.REPOSITORY+'/.github/workflows/ci.yml@refs/heads/'+'a'*2048),
                           ('run_attempt', True), ('version', 1), ('tested_sha', B)):
            record = json.loads(saved)
            record['runs'][-1]['attempt_evidence'][0]['jobs'][0]['annotation'][key] = value
            for mode in ('post', 'release'):
                args, policy, later = self.later(branch, mode)
                code, output, _, count = self.invoke(args, policy, later, json.dumps(record))
                self.assertEqual((code, output, count), (1, '', 0))


if __name__ == '__main__':
    unittest.main()
