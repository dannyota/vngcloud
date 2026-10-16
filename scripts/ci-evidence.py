#!/usr/bin/env python3
"""Read-only manager evidence gate. Evaluation and HTTPS reads stay separate."""
import argparse
import base64
import binascii
import queue
import threading
import socket
import datetime
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import selectors
import signal
import ssl
import subprocess
import sys
import time
import urllib.parse

REPOSITORY = 'dannyota/vngcloud'
PREFIX = '/repos/' + REPOSITORY
TITLE = 'vngcloud-source-identity'
SHA = r'[0-9a-f]{40}'
JOBS = {'ci': {'go': 'go', 'lint': 'lint', 'security': 'security', 'required': 'CI required'},
        'semgrep': {'semgrep': 'semgrep'}, 'tools': {'outdated': 'outdated'},
        'wiki': {'preview': 'preview'}}
REVIEW_PATH = 'dynamic/agents/copilot-pull-request-reviewer'
MAX_BYTES = 4 * 1024 * 1024
MAX_PAGES = 100
MAX_ATTEMPTS = 20
SOURCE_TRUST = 'Workflow-asserted source association backed by exact-byte review; no execution attestation'
PROJECT_REVIEW = 'Manager-recorded input; manager attests actual independence'


class Rejected(Exception):
    pass


class Pending(Rejected):
    pass


def require(condition, message):
    if not condition:
        raise Rejected(message)


def integer(value):
    return type(value) is int and value > 0


def string(value):
    return isinstance(value, str) and bool(value.strip()) and '\x00' not in value


def sha(value):
    return isinstance(value, str) and re.fullmatch(SHA, value) is not None


def exact(value, keys, label):
    require(type(value) is dict and set(value) == set(keys), label + ' fields differ')


def pairs(items):
    result = {}
    for key, value in items:
        require(key not in result, 'Duplicate JSON key')
        result[key] = value
    return result


def decode(raw):
    try:
        return json.loads(raw, object_pairs_hook=pairs,
                          parse_constant=lambda _: require(False, 'Invalid JSON constant'))
    except (ValueError, UnicodeError, RecursionError, TypeError):
        raise Rejected('Invalid JSON') from None


def identity(message):
    require(isinstance(message, str) and len(message.encode('utf-8')) <= 2048,
            'Identity message exceeds its bound')
    record = decode(message)
    exact(record, ('version', 'repository', 'workflow', 'job', 'matrix', 'tested_sha',
                   'run_id', 'run_attempt', 'workflow_ref', 'workflow_sha'), 'Identity')
    require(type(record['version']) is int and record['version'] == 2, 'Unknown identity version')
    require(record['repository'] == REPOSITORY and sha(record['tested_sha']), 'Wrong source identity')
    require(record['workflow'] in ['.github/workflows/' + name + '.yml' for name in JOBS],
            'Unknown identity workflow')
    require(string(record['job']) and type(record['matrix']) is dict and
            all(string(k) and isinstance(v, str) for k, v in record['matrix'].items()),
            'Invalid job or matrix identity')
    require(integer(record['run_id']) and integer(record['run_attempt']), 'Invalid run identity')
    require(sha(record['workflow_sha']) and string(record['workflow_ref']), 'Invalid execution source')
    prefix = REPOSITORY + '/' + record['workflow'] + '@'
    require(record['workflow_ref'].startswith(prefix) and valid_ref(record['workflow_ref'][len(prefix):]),
            'Invalid workflow execution ref')
    return record


def annotations(records, expected):
    require(type(records) is list and all(type(item) is dict for item in records),
            'Invalid annotation listing')
    reserved = [item for item in records if item.get('title') == TITLE]
    require(len(reserved) == 1, 'Missing or duplicate source identity')
    require(reserved[0].get('annotation_level') == 'notice', 'Wrong identity annotation level')
    require(identity(reserved[0].get('message')) == expected, 'Source identity association differs')


def review_record(record, pr, head, base, policy_sha, manager):
    exact(record, ('version', 'repository', 'pr', 'head_sha', 'reviewed_base_sha', 'policy_sha',
                   'manager', 'reviews'), 'Project review')
    require(type(record['version']) is int and record['version'] == 1, 'Unknown project review version')
    require(type(record['pr']) is int and record['pr'] == pr and
            record['repository'] == REPOSITORY and record['head_sha'] == head and
            record['reviewed_base_sha'] == base and record['policy_sha'] == policy_sha and
            string(manager) and record['manager'] == manager, 'Project review bindings differ')
    require(type(record['reviews']) is list and record['reviews'], 'Independent reviews are missing')
    seen = set()
    for review in record['reviews']:
        exact(review, ('reviewer', 'author', 'reviewed_sha', 'report', 'outcome', 'findings',
                       'fix_confirmations'), 'Independent review')
        require(string(review['reviewer']) and string(review['author']) and
                review['reviewer'] != review['author'] and string(review['report']) and
                sha(review['reviewed_sha']) and review['outcome'] == 'accept', 'Invalid independent review')
        require(review['report'] not in seen, 'Duplicate independent review')
        seen.add(review['report'])
        require(type(review['findings']) is list and type(review['fix_confirmations']) is list,
                'Invalid findings or confirmations')
        findings = {}
        for finding in review['findings']:
            exact(finding, ('reference', 'blocking'), 'Finding')
            require(string(finding['reference']) and type(finding['blocking']) is bool and
                    finding['reference'] not in findings, 'Invalid finding')
            findings[finding['reference']] = finding['blocking']
        confirmed = set()
        for fix in review['fix_confirmations']:
            exact(fix, ('finding', 'head_sha', 'reviewer', 'report', 'outcome'), 'Fix confirmation')
            require(fix['finding'] in findings and fix['finding'] not in confirmed and
                    fix['head_sha'] == head and fix['reviewer'] == review['reviewer'] and
                    string(fix['report']) and fix['outcome'] == 'accept', 'Invalid final-head confirmation')
            confirmed.add(fix['finding'])
        require(all(not blocking or ref in confirmed for ref, blocking in findings.items()),
                'Blocking findings lack final-head confirmation')
        require(review['reviewed_sha'] == head or (findings and all(ref in confirmed for ref in findings)),
                'Independent review is stale')


def valid_ref(ref):
    return (isinstance(ref, str) and re.fullmatch(r'refs/(?:heads|tags|pull)/[^\x00-\x20~^:?*\[\\]+', ref) is not None
            and '..' not in ref and '@{' not in ref and not ref.endswith(('/', '.', '.lock'))
            and '//' not in ref and all(not part.startswith('.') for part in ref.split('/')))


def policy_record(policy, expected_sha):
    exact(policy, ('version', 'repository', 'policy_sha', 'actions_app', 'workflows',
                   'sources', 'publication', 'review_workflows', 'review_apps'), 'Policy')
    require(type(policy['version']) is int and policy['version'] == 1 and
            policy['repository'] == REPOSITORY and policy['policy_sha'] == expected_sha and
            integer(policy['actions_app']) and policy['publication'] == 'disabled', 'Policy binding differs')
    require(type(policy['workflows']) is dict and set(policy['workflows']) == set(JOBS),
            'Reviewed validation workflow set differs')
    ids = set()
    for name, config in policy['workflows'].items():
        exact(config, ('id', 'path', 'steps'), 'Workflow policy')
        require(integer(config['id']) and config['id'] not in ids and
                config['path'] == '.github/workflows/' + name + '.yml', 'Workflow identity differs')
        ids.add(config['id'])
        require(type(config['steps']) is dict and set(config['steps']) == set(JOBS[name]),
                'Reviewed job steps differ')
        for key, steps in config['steps'].items():
            require(type(steps) is list and steps and all(string(step) for step in steps) and
                    len(steps) == len(set(steps)), 'Required step inventory is missing')
            if key != 'required':
                require('Assert tested source identity' in steps and len(steps) >= 2,
                        'Identity or source checks are missing from policy')
    require(type(policy['sources']) is list and policy['sources'], 'Source review bindings are missing')
    tuples = set()
    for source in policy['sources']:
        exact(source, ('repository', 'workflow_id', 'workflow', 'workflow_ref', 'workflow_sha',
                       'blob_sha', 'content_sha256', 'report'), 'Source review')
        require(source['repository'] == REPOSITORY and sha(source['workflow_sha']) and sha(source['blob_sha'])
                and isinstance(source['content_sha256'], str) and
                re.fullmatch(r'[0-9a-f]{64}', source['content_sha256']) and string(source['report']),
                'Invalid immutable source review')
        require(integer(source['workflow_id']), 'Invalid source workflow identity')
        matches = [w for w in policy['workflows'].values() if w['id'] == source['workflow_id'] and
                   w['path'] == source['workflow']]
        prefix = REPOSITORY + '/' + source['workflow'] + '@'
        require(len(matches) == 1 and integer(source['workflow_id']) and
                isinstance(source['workflow_ref'], str) and source['workflow_ref'].startswith(prefix) and
                valid_ref(source['workflow_ref'][len(prefix):]), 'Source policy workflow differs')
        key = (source['workflow_id'], source['workflow_ref'], source['workflow_sha'])
        require(key not in tuples, 'Duplicate or contradictory source review')
        tuples.add(key)
    require(type(policy['review_workflows']) is list and type(policy['review_apps']) is list,
            'Invalid review purpose policy')
    for item in policy['review_workflows']:
        exact(item, ('id', 'path', 'purpose'), 'Review workflow')
        require(integer(item['id']) and item['id'] not in ids and item['path'] == REVIEW_PATH and
                item['purpose'] == 'review-only', 'Unknown review workflow purpose')
        ids.add(item['id'])
    require(all(integer(app) and app != policy['actions_app'] for app in policy['review_apps']) and
            len(policy['review_apps']) == len(set(policy['review_apps'])), 'Invalid review app identity')


class Adapter:
    """No environment configuration or redirect-capable HTTP client is used."""
    def __init__(self, token, wait):
        require(isinstance(token, str) and re.fullmatch(r'[\x21-\x7e]{1,4096}', token) is not None,
                'Explicit runtime GH_TOKEN is missing or invalid')
        require(type(wait) in (int, float) and 0 < wait <= 600, 'Invalid total wait bound')
        self.token = token
        self.deadline = time.monotonic() + wait
        self.bytes = 0
        self.requests = 0

    def remaining(self):
        remaining = self.deadline - time.monotonic()
        require(remaining > 0, 'Evidence wait timed out')
        return remaining

    def get(self, endpoint, query=None):
        require(isinstance(endpoint, str) and re.fullmatch(
            r'/(?:actions/runs(?:/[1-9][0-9]*(?:/attempts/[1-9][0-9]*(?:/jobs)?)?)?'
            r'|commits/[0-9a-f]{40}/check-runs|check-runs/[1-9][0-9]*(?:/annotations)?'
            r'|pulls/[1-9][0-9]*|git/ref/heads/master|git/(?:commits|trees|blobs)/[0-9a-f]{40})', endpoint), 'Unsupported evidence endpoint')
        query = query or {}
        require(set(query) <= {'head_sha', 'filter', 'per_page', 'page'} and
                all(isinstance(value, str) for value in query.values()), 'Unsupported query')
        require('head_sha' not in query or sha(query['head_sha']), 'Invalid head query')
        require('filter' not in query or query['filter'] == 'all', 'Invalid check filter')
        require('per_page' not in query or query['per_page'] == '100', 'Invalid page size')
        require('page' not in query or re.fullmatch(r'[1-9][0-9]*', query['page']), 'Invalid page number')
        path = PREFIX + endpoint + ('?' + urllib.parse.urlencode(query) if query else '')
        for retry in range(3):
            self.requests += 1
            require(self.requests <= 5000, 'Request bound exceeded')
            duration = min(10, self.remaining())
            # Construct a verified context directly; inherited key-log and proxy settings are unused.
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            paths = ssl.get_default_verify_paths()
            context.load_verify_locations(
                cafile=paths.openssl_cafile if Path(paths.openssl_cafile).is_file() else None,
                capath=paths.openssl_capath if Path(paths.openssl_capath).is_dir() else None)
            connection = http.client.HTTPSConnection('api.github.com', 443, timeout=duration, context=context)
            result = queue.Queue(maxsize=1)
            cancelled = threading.Event()

            def read():
                try:
                    connection.connect()
                    if cancelled.is_set():
                        return
                    connection.request('GET', path, headers={
                        'Authorization': 'Bearer ' + self.token, 'Accept': 'application/vnd.github+json',
                        'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'vngcloud-ci-evidence'})
                    response = connection.getresponse()
                    require(response.status == 200, 'Evidence API did not return HTTP 200')
                    require(response.getheader('Content-Type', '').split(';')[0] == 'application/json',
                            'Evidence API did not return JSON')
                    length = response.getheader('Content-Length')
                    require(length is None or (length.isdecimal() and int(length) <= MAX_BYTES),
                            'Invalid response length')
                    chunks, size = [], 0
                    while True:
                        chunk = response.read1(min(65536, MAX_BYTES + 1 - size))
                        if not chunk:
                            break
                        size += len(chunk)
                        require(size <= MAX_BYTES and not cancelled.is_set(), 'Response byte or time bound exceeded')
                        chunks.append(chunk)
                    raw = b''.join(chunks)
                    require(length is None or int(length) == len(raw), 'Truncated response')
                    result.put((raw, response.getheader('Link', '')))
                except Rejected as error:
                    result.put(error)
                except (OSError, http.client.HTTPException):
                    result.put(OSError())
                except Exception:
                    result.put(Rejected('Malformed transport response'))
                finally:
                    connection.close()

            thread = threading.Thread(target=read, daemon=True)
            thread.start()
            try:
                value = result.get(timeout=duration)
            except queue.Empty:
                cancelled.set()
                sock = getattr(connection, 'sock', None)
                if sock is not None:
                    try:
                        sock.shutdown(socket.SHUT_RDWR)
                    except OSError:
                        pass
                connection.close()
                raise Rejected('Evidence request timed out') from None
            if isinstance(value, Rejected):
                raise value
            if isinstance(value, OSError):
                if retry == 2:
                    raise Rejected('Evidence transport failed after bounded read retries') from None
                time.sleep(min(0.1 * (retry + 1), self.remaining()))
                continue
            raw, link = value
            self.bytes += len(raw)
            require(self.bytes <= 64 * MAX_BYTES, 'Total response byte bound exceeded')
            self.remaining()
            return decode(raw), link
        raise Rejected('Evidence transport failed')

    def next_page(self, link, endpoint, query, page):
        if not link:
            return False
        links = {}
        for part in link.split(','):
            match = re.fullmatch(r'\s*<([^<>]+)>;\s*rel="(next|last|first|prev)"\s*', part)
            require(match is not None and match[2] not in links, 'Invalid pagination header')
            parsed = urllib.parse.urlsplit(match[1])
            require(parsed.scheme == 'https' and parsed.netloc in ('api.github.com', 'api.github.com:443') and
                    parsed.path == PREFIX + endpoint and not parsed.fragment and not parsed.username,
                    'Foreign pagination endpoint')
            items = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True, strict_parsing=True)
            require(len(items) == len(dict(items)), 'Duplicate pagination query')
            values = dict(items)
            require(set(values) == set(query) | {'page'} and
                    all(values[key] == value for key, value in query.items()) and
                    re.fullmatch(r'[1-9][0-9]*', values['page']) is not None,
                    'Pagination query differs')
            links[match[2]] = int(values['page'])
        if 'next' in links:
            require(links['next'] == page + 1, 'Pagination skipped a page')
            return True
        require('last' not in links or links['last'] <= page, 'Missing next page')
        return False

    def pages(self, endpoint, key=None, query=None):
        query = dict(query or {}, per_page='100')
        records, count = [], None
        for page in range(1, MAX_PAGES + 1):
            value, link = self.get(endpoint, dict(query, page=str(page)))
            if key is None:
                batch = value
            else:
                require(type(value) is dict and type(value.get('total_count')) is int and
                        0 <= value['total_count'] <= 10000, 'Listing count is invalid')
                require(count is None or count == value['total_count'], 'Listing count changed')
                count = value['total_count']
                batch = value.get(key)
            require(type(batch) is list and len(batch) <= 100 and
                    all(type(record) is dict for record in batch), 'Invalid evidence page')
            records.extend(batch)
            if not self.next_page(link, endpoint, query, page):
                require(count is None or len(records) == count, 'Incomplete evidence listing')
                return records
            require(len(batch) == 100, 'Incomplete intermediate page')
        raise Rejected('Evidence page bound exceeded')

    def object(self, endpoint):
        value, link = self.get(endpoint)
        require(type(value) is dict and not link, 'Invalid object response')
        return value


def source_bytes(adapter, revision, path):
    require(sha(revision) and path in {'.github/workflows/' + name + '.yml' for name in JOBS},
            'Invalid immutable source request')
    commit = adapter.object('/git/commits/' + revision)
    require(commit.get('sha') == revision and sha(commit.get('tree', {}).get('sha')), 'Source commit differs')
    tree_id = commit['tree']['sha']
    components = path.split('/')
    entry = None
    for index, component in enumerate(components):
        tree = adapter.object('/git/trees/' + tree_id)
        require(tree.get('sha') == tree_id and tree.get('truncated') is False and
                type(tree.get('tree')) is list, 'Source tree is incomplete')
        names = set()
        for child in tree['tree']:
            require(type(child) is dict and string(child.get('path')) and '/' not in child['path'] and
                    child['path'] not in ('.', '..') and child['path'] not in names and sha(child.get('sha')),
                    'Invalid or duplicate tree component')
            names.add(child['path'])
        matches = [child for child in tree['tree'] if child['path'] == component]
        require(len(matches) == 1, 'Source path is missing')
        entry = matches[0]
        if index < len(components) - 1:
            require(entry.get('type') == 'tree' and entry.get('mode') == '040000', 'Source directory is not a tree')
            tree_id = entry['sha']
        else:
            require(entry.get('type') == 'blob' and entry.get('mode') in ('100644', '100755'),
                    'Source is not a regular file')
    blob = adapter.object('/git/blobs/' + entry['sha'])
    require(blob.get('sha') == entry['sha'] and blob.get('encoding') == 'base64' and
            type(blob.get('size')) is int and 0 < blob['size'] <= MAX_BYTES and
            isinstance(blob.get('content'), str) and len(blob['content']) <= MAX_BYTES * 2,
            'Invalid source blob')
    try:
        content = base64.b64decode(blob['content'].replace('\n', ''), validate=True)
    except (ValueError, binascii.Error):
        raise Rejected('Invalid source base64') from None
    require(len(content) == blob['size'], 'Source blob size differs')
    object_id = hashlib.sha1(b'blob ' + str(len(content)).encode() + b'\0' + content).hexdigest()
    require(object_id == entry['sha'], 'Source blob object ID differs')
    return {'blob_sha': object_id, 'content_sha256': hashlib.sha256(content).hexdigest()}


def check_id(url):
    require(isinstance(url, str), 'Missing job check URL')
    match = re.fullmatch(r'https://api\.github\.com/repos/' + REPOSITORY + r'/check-runs/([1-9][0-9]*)', url)
    require(match is not None, 'Foreign job check URL')
    return int(match[1])


def unique(records, key, label):
    require(type(records) is list and all(type(item) is dict and integer(item.get(key)) for item in records),
            'Invalid ' + label + ' inventory')
    result = {item[key]: item for item in records}
    require(len(result) == len(records), 'Duplicate ' + label + ' inventory')
    return result


def success(record, label):
    if record.get('status') in ('queued', 'in_progress', 'waiting', 'pending', 'requested'):
        raise Pending(label + ' is pending')
    require(record.get('status') == 'completed' and record.get('conclusion') == 'success',
            label + ' did not succeed')


def required_paths(paths):
    require(type(paths) is list and paths and all(string(path) and not path.startswith('/') for path in paths),
            'Complete change paths are missing')
    expected = {'ci'}
    if any(not (path.endswith('.md') or path == 'LICENSE') for path in paths):
        expected.add('semgrep')
    tool_files = {'.tool-versions', 'go.mod', 'go.sum', 'scripts/tools-outdated.sh',
                  'scripts/test-tools-outdated.py', 'scripts/check-workflow-pins.py',
                  'scripts/test-ci-workflows.py'}
    wiki_files = {'scripts/wiki-sync.sh', 'scripts/test-wiki-publication.py', '.github/workflows/wiki.yml',
                  'scripts/ci-evidence.py', 'scripts/test-ci-evidence.py'}
    if any(path in tool_files or path.startswith('.github/workflows/') for path in paths):
        expected.add('tools')
    if any(path in wiki_files or path.startswith('docs/wiki/') for path in paths):
        expected.add('wiki')
    return expected


def classify(run, policy):
    for field in ('id', 'workflow_id', 'check_suite_id', 'run_attempt'):
        if field in run or field in ('id', 'workflow_id'):
            require(integer(run.get(field)), 'Invalid numeric run identity')
    for name, config in policy['workflows'].items():
        if run.get('workflow_id') == config['id']:
            require(run.get('path') == config['path'], 'Validation workflow path differs')
            return name
    for config in policy['review_workflows']:
        if run.get('workflow_id') == config['id'] and run.get('path') == config['path']:
            return None
    raise Rejected('Unknown workflow evidence requires reviewed policy')


def pr_associations(run):
    associations = run.get('pull_requests')
    require(type(associations) is list and all(type(item) is dict and integer(item.get('number'))
            for item in associations), 'Invalid numeric PR association')
    return associations


def collect(adapter, args, policy):
    pr = adapter.object('/pulls/' + str(args.pr))
    master = adapter.object('/git/ref/heads/master')
    runs = adapter.pages('/actions/runs', 'workflow_runs', {'head_sha': args.head})
    require(len(runs) < 1000, 'Workflow run search limit reached')
    checks = adapter.pages('/commits/' + args.head + '/check-runs', 'check_runs', {'filter': 'all'})
    bundles, sources = {}, {}
    for run_id, run in unique(runs, 'id', 'run').items():
        name = classify(run, policy)
        if name is None:
            continue
        if run.get('event') == 'pull_request':
            pr_associations(run)
        attempt = run.get('run_attempt')
        require(integer(attempt) and attempt <= MAX_ATTEMPTS, 'Run attempt bound exceeded')
        attempts = []
        for number in range(1, attempt + 1):
            endpoint = '/actions/runs/' + str(run_id) + '/attempts/' + str(number)
            metadata = adapter.object(endpoint)
            if 'pull_requests' in metadata:
                pr_associations(metadata)
            jobs = adapter.pages(endpoint + '/jobs', 'jobs')
            job_checks = {}
            for job in jobs:
                cid = check_id(job.get('check_run_url'))
                check = adapter.object('/check-runs/' + str(cid))
                notices = adapter.pages('/check-runs/' + str(cid) + '/annotations')
                count = check.get('output', {}).get('annotations_count')
                require(type(count) is int and count >= 0 and count == len(notices),
                        'Incomplete annotation listing')
                require(cid not in job_checks, 'Duplicate job check association')
                job_checks[cid] = {'check': check, 'annotations': notices}
                for notice in notices:
                    if notice.get('title') == TITLE:
                        claim = identity(notice.get('message'))
                        source_key = (claim['workflow_sha'], claim['workflow'])
                        if source_key not in sources:
                            sources[source_key] = source_bytes(adapter, *source_key)
            attempts.append({'metadata': metadata, 'jobs': jobs, 'checks': job_checks})
        reread = adapter.object('/actions/runs/' + str(run_id))
        classify(reread, policy)
        if reread.get('event') == 'pull_request':
            pr_associations(reread)
        require(reread == run, 'Run changed during attempt collection')
        bundles[run_id] = attempts
    return {'pr': pr, 'master': master, 'runs': runs, 'checks': checks, 'attempts': bundles, 'sources': sources}


def state(snapshot, args):
    pr = snapshot['pr']
    require(integer(pr.get('number')) and pr.get('number') == args.pr and pr.get('head', {}).get('sha') == args.head and
            pr.get('head', {}).get('repo', {}).get('full_name') == REPOSITORY and
            pr.get('base', {}).get('repo', {}).get('full_name') == REPOSITORY and
            pr.get('base', {}).get('ref') == 'master', 'PR source, head, or target differs')
    require(snapshot['master'].get('ref') == 'refs/heads/master' and
            snapshot['master'].get('object', {}).get('type') == 'commit' and
            snapshot['master']['object'].get('sha') == args.master, 'Current master changed')
    require(args.master == (args.base if args.mode == 'pre' else args.head), 'Mode master input differs')


def evaluate(snapshot, args, policy, git):
    policy_record(policy, args.policy_sha)
    retained = getattr(args, 'retained', None)
    require((args.mode == 'pre') == (retained is None), 'Retained pre-landing input differs from mode')
    if retained is not None:
        retained_pre_record(retained, args, policy)
    state(snapshot, args)
    git.signatures(args.base, args.head, args.signer, args.fingerprint)
    paths = git.paths(args.base, args.head)
    expected = required_paths(paths)
    git.workflows(args.head, policy)
    require(all(not path.startswith('.github/workflows/') or
                path in {item['path'] for item in policy['workflows'].values()} for path in paths),
            'Changed workflow lacks reviewed policy')
    runs = unique(snapshot['runs'], 'id', 'run')
    checks = unique(snapshot['checks'], 'id', 'check')
    require(type(snapshot['attempts']) is dict and all(integer(key) for key in snapshot['attempts']),
            'Invalid attempt inventory keys')
    if retained is not None:
        require({run['id'] for run in retained['runs']} <= set(runs), 'Original validation runs are missing')
    required_event = 'pull_request' if args.mode == 'pre' else 'push'
    covered, associated, optional_suites, accepted, exclusions = set(), set(), set(), [], []
    latest_checks, fetched_checks = set(), {}
    for run_id, run in runs.items():
        name = classify(run, policy)
        if name is None:
            if integer(run.get('check_suite_id')):
                optional_suites.add(run['check_suite_id'])
            continue
        require(integer(run.get('workflow_id')) and integer(run.get('check_suite_id')) and
                not run.get('referenced_workflows') and run.get('head_sha') == args.head and
                run.get('repository', {}).get('full_name') == REPOSITORY and
                run.get('head_repository', {}).get('full_name') == REPOSITORY, 'Run source differs')
        config = policy['workflows'][name]
        require(run.get('event') in ('pull_request', 'push', 'workflow_dispatch', 'schedule'),
                'Unexpected validation event')
        require(run['event'] != 'schedule' or name == 'tools', 'Unexpected scheduled workflow')
        require(run['event'] not in ('push', 'schedule') or run.get('head_branch') == 'master',
                'Validation is not a master run')
        require(run.get('html_url') == f'https://github.com/{REPOSITORY}/actions/runs/{run_id}',
                'Foreign run URL')
        if run['event'] == 'pull_request':
            associations = pr_associations(run)
            require(any(
                item.get('number') == args.pr and item.get('head', {}).get('sha') == args.head
                for item in associations), 'PR run association differs')
        source_tuple = None
        success(run, 'Validation run')
        number = run.get('run_attempt')
        attempts = snapshot['attempts'].get(run_id)
        attempt_inventory = []
        require(integer(number) and number <= MAX_ATTEMPTS and type(attempts) is list and len(attempts) == number,
                'Attempt evidence is incomplete')
        for index, evidence in enumerate(attempts, 1):
            metadata = evidence['metadata']
            if 'pull_requests' in metadata:
                pr_associations(metadata)
            require(not metadata.get('referenced_workflows'), 'Unexpected reusable workflow execution')
            require(all(integer(metadata.get(field)) for field in
                        ('id', 'workflow_id', 'check_suite_id', 'run_attempt')), 'Invalid numeric attempt identity')
            for field in ('id', 'workflow_id', 'path', 'head_sha', 'event', 'check_suite_id'):
                require(metadata.get(field) == run.get(field), 'Attempt metadata differs')
            require(metadata.get('run_attempt') == index, 'Attempt number differs')
            success(metadata, 'Validation attempt')
            jobs = unique(evidence['jobs'], 'id', 'job')
            require(type(evidence['checks']) is dict and all(integer(key) for key in evidence['checks']),
                    'Invalid job check inventory keys')
            job_inventory = []
            require(len(jobs) == len(JOBS[name]) and
                    {job.get('name') for job in jobs.values()} == set(JOBS[name].values()), 'Required job set differs')
            for key, job_name in JOBS[name].items():
                job = next(job for job in jobs.values() if job['name'] == job_name)
                require(integer(job.get('run_id')) and integer(job.get('run_attempt')) and
                        job.get('run_id') == run_id and job.get('run_attempt') == index and
                        job.get('head_sha') == args.head, 'Job attempt association differs')
                success(job, 'Required job')
                cid = check_id(job.get('check_run_url'))
                bundle = evidence['checks'].get(cid)
                require(type(bundle) is dict, 'Job check evidence is missing')
                check = bundle['check']
                count = check.get('output', {}).get('annotations_count')
                require(type(bundle['annotations']) is list and type(count) is int and count >= 0 and
                        count == len(bundle['annotations']), 'Incomplete annotation listing')
                require(integer(check.get('id')) and integer(check.get('app', {}).get('id')) and
                        integer(check.get('check_suite', {}).get('id')) and check.get('id') == cid and check.get('name') == job_name and
                        check.get('head_sha') == args.head and
                        check.get('app', {}).get('id') == policy['actions_app'] and
                        check.get('check_suite', {}).get('id') == run.get('check_suite_id') and
                        check.get('details_url') == f'https://github.com/{REPOSITORY}/actions/runs/{run_id}/job/{job["id"]}',
                        'Job/check/app association differs')
                success(check, 'Required check')
                require(cid not in associated, 'Check reused by another job or attempt')
                associated.add(cid)
                fetched_checks[cid] = check
                if index == number:
                    latest_checks.add(cid)
                steps = job.get('steps')
                require(type(steps) is list and all(type(step) is dict for step in steps), 'Job steps are missing')
                for required in config['steps'][key]:
                    matches = [step for step in steps if step.get('name') == required]
                    require(len(matches) == 1, 'Required source step is missing or duplicated')
                    success(matches[0], 'Required source step')
                for step in steps:
                    if step.get('conclusion') != 'skipped':
                        success(step, 'Job step')
                source_binding, source_annotation = None, None
                if key != 'required':
                    reserved = [item for item in bundle['annotations'] if item.get('title') == TITLE]
                    require(len(reserved) == 1, 'Missing or duplicate source identity')
                    claim = identity(reserved[0].get('message'))
                    annotations(bundle['annotations'], dict(version=2, repository=REPOSITORY,
                        workflow=config['path'], job=key, matrix={}, tested_sha=args.head,
                        run_id=run_id, run_attempt=index, workflow_ref=claim['workflow_ref'],
                        workflow_sha=claim['workflow_sha']))
                    asserted = (claim['workflow_ref'], claim['workflow_sha'])
                    require(source_tuple is None or source_tuple == asserted, 'Conflicting job or attempt sources')
                    source_tuple = asserted
                    expected_ref = f'refs/pull/{args.pr}/merge' if run['event'] == 'pull_request' else 'refs/heads/master'
                    if run['event'] == 'workflow_dispatch':
                        refs = {'refs/heads/' + run.get('head_branch', ''), 'refs/tags/' + run.get('head_branch', '')}
                    else:
                        refs = {expected_ref}
                    prefix = REPOSITORY + '/' + config['path'] + '@'
                    require(claim['workflow_ref'] in {prefix + ref for ref in refs}, 'Wrong event workflow ref')
                    source = snapshot['sources'].get((claim['workflow_sha'], config['path']))
                    require(type(source) is dict, 'Historical execution source is unavailable')
                    bindings = [binding for binding in policy['sources'] if
                        binding['workflow_id'] == run['workflow_id'] and binding['workflow'] == config['path'] and
                        binding['workflow_ref'] == claim['workflow_ref'] and binding['workflow_sha'] == claim['workflow_sha']]
                    require(len(bindings) == 1 and bindings[0]['blob_sha'] == source.get('blob_sha') and
                            bindings[0]['content_sha256'] == source.get('content_sha256'), 'Unknown or mismatched reviewed source')
                    source_binding = bindings[0]
                    source_annotation = claim
                else:
                    require(not any(item.get('title') == TITLE for item in bundle['annotations']),
                            'Aggregate fabricated source identity')
                job_inventory.append(dict(job=key, job_id=job['id'], check_id=cid,
                    app_id=policy['actions_app'], matrix={}, conclusion='success',
                    required_steps=config['steps'][key], annotation=source_annotation, source_binding=source_binding))
            attempt_inventory.append(dict(attempt=index, conclusion='success', jobs=job_inventory))
        if run['event'] == required_event:
            covered.add(name)
        elif run['event'] == 'workflow_dispatch' and name != 'ci':
            covered.add(name)
        accepted.append({'id': run_id, 'attempts': number, 'workflow': config['path'],
                         'event': run['event'], 'url': run['html_url'], 'conclusion': 'success',
                         'workflow_ref': source_tuple[0], 'workflow_sha': source_tuple[1],
                         'attempt_evidence': attempt_inventory,
                         'sources': [dict(attempt=n, workflow_ref=source_tuple[0], workflow_sha=source_tuple[1])
                                     for n in range(1, number + 1)]})
    require(latest_checks <= set(checks), 'Current required checks are absent from head inventory')
    for cid, check in checks.items():
        require(integer(check.get('app', {}).get('id')) and
                ('check_suite' not in check or integer(check.get('check_suite', {}).get('id'))),
                'Invalid numeric check identity')
        require(check.get('head_sha') == args.head, 'Check source differs')
        if cid in associated:
            fetched = fetched_checks[cid]
            require(all(check.get(field) == fetched.get(field) for field in
                        ('id', 'name', 'head_sha', 'details_url', 'status', 'conclusion')) and
                    check.get('app', {}).get('id') == fetched.get('app', {}).get('id') and
                    check.get('check_suite', {}).get('id') == fetched.get('check_suite', {}).get('id'),
                    'Listed check differs from job check')
            continue
        if check.get('app', {}).get('id') in policy['review_apps']:
            continue
        if (check.get('app', {}).get('id') == policy['actions_app'] and
                check.get('check_suite', {}).get('id') in optional_suites):
            continue
        raise Rejected('Unassociated check requires reviewed purpose policy')
    if not expected <= covered:
        raise Pending('Expected event validation is missing')
    for name in sorted(set(JOBS) - expected):
        exclusions.append({'workflow': policy['workflows'][name]['path'], 'paths': paths,
                           'reason': 'Event paths exclude this workflow; observed runs remain required'})
    require(accepted, 'CI evidence is empty')
    if retained is not None:
        compare_retained(retained, accepted, exclusions, expected)
    if args.mode == 'release':
        git.tag(args.tag, args.head, args.signer, args.fingerprint)
    return {'repository': REPOSITORY, 'pr': args.pr, 'head_sha': args.head,
            'reviewed_base_sha': args.base, 'master_sha': args.master, 'policy_sha': args.policy_sha,
            'mode': args.mode, 'runs': sorted(accepted, key=lambda item: item['id']),
            'exclusions': exclusions, 'publication': 'disabled',
            'source_trust': SOURCE_TRUST, 'project_review': PROJECT_REVIEW}


def retained_pre_record(record, args, policy):
    """Validate saved pre output; its provenance and completeness remain manager attestations."""
    exact(record, ('repository', 'pr', 'head_sha', 'reviewed_base_sha', 'master_sha', 'policy_sha',
                   'mode', 'runs', 'exclusions', 'publication', 'source_trust', 'project_review', 'cutoff'),
          'Retained pre-landing evidence')
    require(integer(record['pr']) and record['pr'] == args.pr and record['repository'] == REPOSITORY and
            record['head_sha'] == args.head and record['reviewed_base_sha'] == args.base and
            record['master_sha'] == args.base and record['policy_sha'] == args.policy_sha and
            record['mode'] == 'pre' and record['publication'] == 'disabled' and
            record['source_trust'] == SOURCE_TRUST and record['project_review'] == PROJECT_REVIEW,
            'Retained pre-landing bindings differ')
    cutoff = record['cutoff']
    require(isinstance(cutoff, str) and re.fullmatch(
        r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,6})?\+00:00', cutoff),
        'Invalid retained evidence cutoff')
    try:
        datetime.datetime.fromisoformat(cutoff)
    except ValueError:
        raise Rejected('Invalid retained evidence cutoff') from None
    runs = unique(record['runs'], 'id', 'retained run')
    require(0 < len(runs) < 1000, 'Retained run bound exceeded')
    check_ids = set()
    for run in runs.values():
        exact(run, ('id', 'attempts', 'workflow', 'event', 'url', 'conclusion', 'workflow_ref',
                    'workflow_sha', 'attempt_evidence', 'sources'), 'Retained run')
        require(integer(run['attempts']) and run['attempts'] <= MAX_ATTEMPTS and
                run['event'] in ('pull_request', 'push', 'workflow_dispatch', 'schedule') and
                run['conclusion'] == 'success' and sha(run['workflow_sha']) and string(run['workflow_ref']) and
                run['url'] == f'https://github.com/{REPOSITORY}/actions/runs/{run["id"]}', 'Invalid retained run')
        matches = [name for name, config in policy['workflows'].items() if config['path'] == run['workflow']]
        require(len(matches) == 1, 'Unknown retained validation workflow')
        name = matches[0]
        config = policy['workflows'][name]
        require(type(run['attempt_evidence']) is list and len(run['attempt_evidence']) == run['attempts'] and
                type(run['sources']) is list and len(run['sources']) == run['attempts'],
                'Retained attempt evidence is incomplete')
        for index, (attempt, source) in enumerate(zip(run['attempt_evidence'], run['sources']), 1):
            exact(attempt, ('attempt', 'conclusion', 'jobs'), 'Retained attempt')
            exact(source, ('attempt', 'workflow_ref', 'workflow_sha'), 'Retained source')
            require(integer(attempt['attempt']) and attempt['attempt'] == index and
                    integer(source['attempt']) and source['attempt'] == index and
                    attempt['conclusion'] == 'success' and source['workflow_ref'] == run['workflow_ref'] and
                    source['workflow_sha'] == run['workflow_sha'], 'Retained attempt source differs')
            jobs = attempt['jobs']
            require(type(jobs) is list and len(jobs) == len(JOBS[name]) and
                    all(type(job) is dict and string(job.get('job')) for job in jobs) and
                    {job['job'] for job in jobs} == set(JOBS[name]), 'Retained job inventory differs')
            for job in jobs:
                exact(job, ('job', 'job_id', 'check_id', 'app_id', 'matrix', 'conclusion',
                            'required_steps', 'annotation', 'source_binding'), 'Retained job')
                require(all(integer(job[field]) for field in ('job_id', 'check_id', 'app_id')) and
                        job['check_id'] not in check_ids and job['app_id'] == policy['actions_app'] and
                        type(job['matrix']) is dict and job['matrix'] == {} and job['conclusion'] == 'success' and
                        job['required_steps'] == config['steps'][job['job']], 'Invalid retained job evidence')
                check_ids.add(job['check_id'])
                if job['job'] == 'required':
                    require(job['annotation'] is None and job['source_binding'] is None,
                            'Retained aggregate fabricated source identity')
                    continue
                claim = identity(json.dumps(job['annotation'], ensure_ascii=False, separators=(',', ':')))
                require(claim == dict(version=2, repository=REPOSITORY, workflow=config['path'],
                    job=job['job'], matrix={}, tested_sha=args.head, run_id=run['id'], run_attempt=index,
                    workflow_ref=run['workflow_ref'], workflow_sha=run['workflow_sha']),
                    'Retained annotation association differs')
                binding = job['source_binding']
                exact(binding, ('repository', 'workflow_id', 'workflow', 'workflow_ref', 'workflow_sha',
                                'blob_sha', 'content_sha256', 'report'), 'Retained source binding')
                require(integer(binding['workflow_id']) and binding['workflow_id'] == config['id'] and
                        binding['workflow'] == config['path'] and binding['workflow_ref'] == run['workflow_ref'] and
                        binding['workflow_sha'] == run['workflow_sha'] and binding in policy['sources'],
                        'Original reviewed source binding is missing or changed')
    require(type(record['exclusions']) is list and len(record['exclusions']) <= len(JOBS),
            'Invalid retained exclusions')
    excluded = set()
    for exclusion in record['exclusions']:
        exact(exclusion, ('workflow', 'paths', 'reason'), 'Retained exclusion')
        require(string(exclusion['workflow']) and exclusion['workflow'] not in excluded and
                exclusion['workflow'] in {config['path'] for config in policy['workflows'].values()} and
                exclusion['reason'] == 'Event paths exclude this workflow; observed runs remain required',
                'Invalid retained exclusion')
        required_paths(exclusion['paths'])
        excluded.add(exclusion['workflow'])


def compare_retained(retained, accepted, exclusions, expected):
    current = {run['id']: run for run in accepted}
    covered = set()
    for original in retained['runs']:
        require(original['id'] in current, 'Original validation run is missing')
        run = dict(current[original['id']])
        number = original['attempts']
        require(run['attempts'] >= number, 'Original validation attempts are missing')
        # New attempts remain mandatory; compare every original attempt without replacing the run.
        run.update(attempts=number, attempt_evidence=run['attempt_evidence'][:number], sources=run['sources'][:number])
        require(run == original, 'Original validation evidence changed')
        name = original['workflow'].rsplit('/', 1)[1][:-4]
        if original['event'] == 'pull_request' or (original['event'] == 'workflow_dispatch' and name != 'ci'):
            covered.add(name)
    require(expected <= covered and retained['exclusions'] == exclusions,
            'Retained pre-landing coverage or exclusions differ')


def validation_inventory(snapshot, policy):
    runs = sorted([run for run in snapshot['runs'] if classify(run, policy) is not None], key=lambda run: run['id'])
    run_ids = {run['id'] for run in runs}
    attempts = {key: value for key, value in snapshot['attempts'].items() if key in run_ids}
    associated = {cid for evidence in attempts.values() for attempt in evidence for cid in attempt['checks']}
    checks = sorted([check for check in snapshot['checks'] if check['id'] in associated], key=lambda check: check['id'])
    return runs, attempts, checks, snapshot['sources']


class Git:
    def __init__(self, checkout, trust):
        self.checkout = Path(checkout).resolve(strict=True)
        self.trust = Path(trust).resolve(strict=True)
        require(self.checkout.is_dir() and self.trust.is_file() and
                not self.trust.is_relative_to(self.checkout), 'Public trust must be outside candidate checkout')
        self.deadline = time.monotonic() + 600
        self.env = {'PATH': '/usr/bin:/bin', 'LANG': 'C', 'LC_ALL': 'C',
                    'GIT_NO_REPLACE_OBJECTS': '1', 'GIT_CONFIG_NOSYSTEM': '1',
                    'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_TERMINAL_PROMPT': '0'}

    def run(self, *arguments, diagnostics=False):
        require(time.monotonic() < self.deadline, 'Trusted Git verification timed out')
        command = ['/usr/bin/git', '--no-replace-objects', '-c', 'core.hooksPath=/dev/null',
                   '-c', 'gpg.format=ssh', '-c', 'gpg.ssh.program=/usr/bin/ssh-keygen',
                   '-c', 'gpg.ssh.allowedSignersFile=' + str(self.trust),
                   '-c', 'gpg.minTrustLevel=fully', *arguments]
        deadline = min(self.deadline, time.monotonic() + 20)
        child = None
        outputs = (bytearray(), bytearray())
        try:
            child = subprocess.Popen(command, cwd=self.checkout, env=self.env, stdin=subprocess.DEVNULL,
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE, bufsize=0, start_new_session=True)
            with selectors.DefaultSelector() as selector:
                for stream, output in zip((child.stdout, child.stderr), outputs):
                    os.set_blocking(stream.fileno(), False)
                    selector.register(stream, selectors.EVENT_READ, output)
                while selector.get_map():
                    remaining = deadline - time.monotonic()
                    require(remaining > 0, 'Trusted Git verification timed out')
                    for key, _ in selector.select(remaining):
                        output = key.data
                        chunk = os.read(key.fd, min(65536, MAX_BYTES + 1 - len(output)))
                        require(time.monotonic() < deadline, 'Trusted Git verification timed out')
                        if not chunk:
                            selector.unregister(key.fileobj)
                            continue
                        require(len(output) + len(chunk) <= MAX_BYTES, 'Trusted Git output bound exceeded')
                        output.extend(chunk)
            remaining = deadline - time.monotonic()
            require(remaining > 0, 'Trusted Git verification timed out')
            require(child.wait(timeout=remaining) == 0, 'Trusted Git verification failed')
            require(time.monotonic() < deadline, 'Trusted Git verification timed out')
            stdout, stderr = (bytes(output) for output in outputs)
            return (stdout, stderr) if diagnostics else stdout
        except Rejected:
            raise
        except (Exception, KeyboardInterrupt):
            raise Rejected('Trusted Git verification could not finish') from None
        finally:
            if child is not None:
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                finally:
                    child.wait()
                    for stream in (child.stdout, child.stderr):
                        stream.close()

    def checker(self, revision, script):
        expected = self.run('show', revision + ':scripts/ci-evidence.py')
        require(script.stat().st_size <= MAX_BYTES and expected == script.read_bytes(),
                'Checker differs from reviewed policy revision')

    def signatures(self, base, head, signer, fingerprint):
        self.run('merge-base', '--is-ancestor', base, head)
        require(self.run('merge-base', base, head).decode().strip() == base, 'Stale ancestry')
        commits = self.run('rev-list', base + '..' + head).decode().splitlines()
        require(len(commits) <= 1000, 'Commit verification bound exceeded')
        require(all(sha(commit) for commit in commits), 'Invalid commit inventory')
        for commit in sorted(set(commits + [head])):
            self.run('verify-commit', commit)
            verified = self.run('show', '-s', '--format=%G?%n%GS%n%GF', commit).decode().splitlines()
            require(verified == ['G', signer, fingerprint], 'Commit signer differs from owner')

    def paths(self, base, head):
        raw = self.run('diff', '--no-ext-diff', '--no-textconv', '--name-only', '--no-renames', '-z', base, head, '--')
        try:
            paths = raw.decode('utf-8').split('\x00')
        except UnicodeError:
            raise Rejected('Change paths are not UTF-8') from None
        require(paths[-1] == '', 'Change paths are incomplete')
        return sorted(set(paths[:-1]))

    def workflows(self, head, policy):
        files = self.run('ls-tree', '-r', '--name-only', head, '--', '.github/workflows/').decode().splitlines()
        require(set(files) == {item['path'] for item in policy['workflows'].values()},
                'New or renamed workflow requires reviewed policy')

    def tag(self, tag, head, signer, fingerprint):
        require(string(tag) and re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', tag), 'Invalid release tag')
        ref = 'refs/tags/' + tag
        obj = self.run('rev-parse', '--verify', ref + '^{tag}').decode().strip()
        require(sha(obj) and self.run('cat-file', '-t', obj).decode().strip() == 'tag', 'Release tag is not annotated')
        raw = self.run('cat-file', '-p', obj)
        headers, separator, _ = raw.partition(b'\n\n')
        lines = headers.split(b'\n')
        require(separator and len(lines) == 4 and re.fullmatch(b'object [0-9a-f]{40}', lines[0]) and
                lines[1] in (b'type commit', b'type tag') and lines[2] == b'tag ' + tag.encode('ascii') and
                lines[3].startswith(b'tagger ') and lines[3][7:].strip() and
                b'\r' not in headers and b'\x00' not in headers, 'Signed release tag headers differ')
        require(self.run('rev-parse', '--verify', obj + '^{commit}').decode().strip() == head,
                'Release tag target differs')
        _, diagnostics = self.run('verify-tag', '--raw', obj, diagnostics=True)
        lines = diagnostics.decode().splitlines()
        pattern = r'Good "git" signature for ' + re.escape(signer) + r' with (?:ED25519|RSA|ECDSA) key ' + re.escape(fingerprint)
        require(len(lines) == 1 and re.fullmatch(pattern, lines[0]) is not None, 'Tag signer differs from owner')
        require(self.run('rev-parse', '--verify', ref + '^{tag}').decode().strip() == obj, 'Release tag changed')


def private_json(path, checkout):
    path = Path(path).resolve(strict=True)
    require(path.is_file() and not path.is_relative_to(checkout), 'Manager input must be outside candidate checkout')
    require(path.stat().st_size <= MAX_BYTES, 'Manager input byte bound exceeded')
    with path.open('rb') as stream:
        raw = stream.read(MAX_BYTES + 1)
    require(len(raw) <= MAX_BYTES, 'Manager input byte bound exceeded')
    return decode(raw)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('head', 'base', 'master', 'policy-sha', 'manager', 'signer', 'fingerprint', 'checkout', 'candidate', 'trust', 'policy', 'review'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--pr', type=int, required=True)
    parser.add_argument('--mode', choices=('pre', 'post', 'release'), required=True)
    parser.add_argument('--tag')
    parser.add_argument('--retained-pre')
    parser.add_argument('--wait', type=int, default=120)
    args = parser.parse_args()
    try:
        require(integer(args.pr) and all(sha(value) for value in (args.head, args.base, args.master, args.policy_sha))
                and string(args.manager) and string(args.signer) and
                re.fullmatch(r'SHA256:[A-Za-z0-9+/]+', args.fingerprint), 'Invalid expected evidence inputs')
        require((args.mode == 'release') == (args.tag is not None), 'Tag input differs from mode')
        require((args.mode == 'pre') == (args.retained_pre is None), 'Retained pre-landing input differs from mode')
        candidate = Path(args.candidate).resolve(strict=True)
        require(candidate.is_dir(), 'Candidate checkout is missing')
        git = Git(args.checkout, args.trust)
        require(not git.checkout.is_relative_to(candidate) and not git.trust.is_relative_to(candidate) and
                not Path(__file__).resolve().is_relative_to(candidate),
                'Manager checkout and public trust must be outside candidate')
        policy = private_json(args.policy, candidate)
        policy_record(policy, args.policy_sha)
        record = private_json(args.review, candidate)
        review_record(record, args.pr, args.head, args.base, args.policy_sha, args.manager)
        args.retained = None if args.retained_pre is None else private_json(args.retained_pre, candidate)
        if args.retained is not None:
            retained_pre_record(args.retained, args, policy)
        # Only the explicitly supplied runtime value enters the adapter. No fallback exists.
        adapter = Adapter(os.environ.pop('GH_TOKEN', None), args.wait)
        git.deadline = adapter.deadline
        git.checker(args.policy_sha, Path(__file__))
        while True:
            try:
                first = collect(adapter, args, policy)
                accepted = evaluate(first, args, policy, git)
                second = collect(adapter, args, policy)
                final = evaluate(second, args, policy, git)
                require(accepted == final and validation_inventory(first, policy) == validation_inventory(second, policy),
                        'Evidence changed before acceptance')
                current_policy = private_json(args.policy, candidate)
                policy_record(current_policy, args.policy_sha)
                current_review = private_json(args.review, candidate)
                review_record(current_review, args.pr, args.head, args.base, args.policy_sha, args.manager)
                current_retained = None if args.retained_pre is None else private_json(args.retained_pre, candidate)
                if current_retained is not None:
                    retained_pre_record(current_retained, args, current_policy)
                require(current_policy == policy and current_review == record and current_retained == args.retained,
                        'Manager input changed')
                adapter.remaining()
                accepted['cutoff'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
                print(json.dumps(accepted, separators=(',', ':'), sort_keys=True))
                return 0
            except Pending:
                time.sleep(min(2, adapter.remaining()))
    except Rejected as error:
        print('ci-evidence: rejected: ' + str(error), file=sys.stderr)
        return 1
    except (OSError, ValueError, KeyError, TypeError, UnicodeError, RecursionError, AttributeError, IndexError):
        # API strings, subprocess diagnostics, paths, and secret-bearing exceptions stay private.
        print('ci-evidence: rejected; evidence is incomplete, changed, or outside reviewed policy', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
