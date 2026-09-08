#!/usr/bin/env python3
"""Own one isolated stack for real browser Memory or Redis Session publication."""
import argparse
import copy
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time
from urllib.request import urlopen

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent / 'worker-v1-joint'))
from memory_joint_fixture import MemoryHarness, MemoryModelFixture, TOOLS
import channel_lab_fixture as gateway_fixture
from faults import wait_success

class WebPublicationMixin:
    def api(self, method, path, body=None, status=200, idem=None):
        if method == 'POST' and path == '/v1/auth/login':
            self.login_user = body['username']
        if method == 'POST' and path == '/v1/me/change-password' and getattr(self, 'login_user', '') == 'joint-owner':
            self.owner_password = body['new_password']
        if method == 'POST' and path.endswith('/channel-bindings'):
            body = copy.deepcopy(body)
            body['target']['revision_number'] = self.revision_number
        return super().api(method, path, body, status, idem)

class WebHarness(WebPublicationMixin, MemoryHarness):
    pass

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--artifacts', type=Path, required=True)
    parser.add_argument('--coordination', type=Path, required=True)
    parser.add_argument('--timeout', type=int, default=1800)
    parser.add_argument('--backend', choices=('postgresql', 'redis'), default='postgresql')
    parser.add_argument('--scenario', choices=('memory', 'session'), default='memory')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    harness = WebHarness
    model_fixture = MemoryModelFixture
    if args.scenario == 'session':
        if args.backend != 'redis':
            parser.error('session scenario requires --backend redis')
        from redis_session_joint_fixture import RedisSessionHarness, SummaryModelFixture, wait_redis_success
        class RedisSessionWebHarness(WebPublicationMixin, RedisSessionHarness):
            pass
        harness = RedisSessionWebHarness
        model_fixture = SummaryModelFixture
    if args.backend == 'redis' and args.scenario == 'memory':
        from redis_memory_joint_fixture import RedisMemoryHarness
        class RedisWebHarness(WebPublicationMixin, RedisMemoryHarness):
            pass
        harness = RedisWebHarness
    h = harness(root, args.artifacts)
    backend_id = 'joint-session-redis' if args.scenario == 'session' else ('joint-memory-redis' if args.backend == 'redis' else 'joint-memory-pg')
    web = None
    web_log = None
    evidence = {'result': 'PENDING', 'gui': 'PENDING', 'external_model': 'DETERMINISTIC_HTTP_FIXTURE', 'shared_services_changed': False}
    target = h.artifacts / ('session-web.json' if args.scenario == 'session' else 'memory-web.json')
    def save():
        target.write_text(h.redact(json.dumps(evidence, indent=2, ensure_ascii=False)) + '\n')
    try:
        h.provision()
        h.model.close()
        h.model = model_fixture(h)
        h.urls['model'] = h.model.url
        h.control_start(gateway_fixture.prepare(h))
        h.seed()
        h.start_worker()
        h.verify_dependencies()
        with socket.socket() as s:
            s.bind(('127.0.0.1', 0))
            port = s.getsockname()[1]
        web_url = 'http://127.0.0.1:' + str(port)
        web_log = open(h.artifacts / 'web.log', 'w')
        # A dedicated process group is owned here, not the shared Docker Web.
        web = subprocess.Popen(['node', str(root / 'web/node_modules/next/dist/bin/next'), 'dev', '--hostname', '127.0.0.1', '--port', str(port)], cwd=root / 'web', env=dict(os.environ, CONTROL_API_BASE=h.urls['control'], NEXT_TELEMETRY_DISABLED='1'), stdout=web_log, stderr=subprocess.STDOUT, start_new_session=True)
        deadline = time.monotonic() + 120
        while True:
            if web.poll() is not None:
                raise RuntimeError('isolated Web exited before readiness')
            try:
                with urlopen(web_url + '/login', timeout=2) as response:
                    if response.status == 200:
                        break
            except Exception:
                if time.monotonic() > deadline:
                    raise RuntimeError('isolated Web readiness timeout') from None
                time.sleep(.5)
        access = Path(h.work) / 'gui-access.json'
        access.write_text(json.dumps({'web_url': web_url, 'username': 'joint-owner', 'password': h.owner_password, args.scenario + '_password': h.session_password if args.scenario == 'session' else h.memory_password, 'tenant_id': h.tenant_id, 'agent_id': h.agent_id, 'profile_id': h.profile_id, 'deployment_id': h.deployment_id, 'backend_id': backend_id, 'backend_revision': 1, 'artifacts': str(h.artifacts)}))
        access.chmod(0o600)
        args.coordination.mkdir(parents=True, exist_ok=True)
        (args.coordination / 'ready.json').write_text(json.dumps({'url': web_url, 'private_access_file': str(access), 'artifacts': str(h.artifacts)}))
        print('MEMORY_WEB_READY=' + str(args.coordination / 'ready.json'), flush=True)
        done = args.coordination / 'gui-published.json'
        deadline = time.monotonic() + args.timeout
        while not done.exists():
            if time.monotonic() > deadline:
                raise RuntimeError('browser publication marker timeout')
            if web.poll() is not None:
                raise RuntimeError('isolated Web exited during browser test')
            time.sleep(1)
        marker = json.loads(done.read_text())
        assert marker['result'] == 'PASS'
        h.deployment_id, h.revision_number = marker['deployment_id'], marker['revision_number']
        publication = h.api('GET', '/v1/tenants/' + h.tenant_id + '/deployments/' + h.deployment_id + '/revisions/' + str(h.revision_number))
        view = publication['manifest_view']
        node = view['agent_plan']['nodes'][view['agent_plan']['root']]
        if args.scenario == 'memory':
            assert sorted(node['memory']['tools']) == sorted(TOOLS)
            selected_storage = node['memory']['resource']
        else:
            assert view['runtime']['summary']['enabled'] and node['add_session_summary']
            selected_storage = view['storage_roles']['session']
        assert view['resources']['storage'][selected_storage]['backend']['backend_id'] == backend_id
        h.revision_id = publication['id']
        h.manifest_id, h.manifest_digest = publication['manifest_id'], publication['manifest_digest']
        evidence.update(gui='PASS', publication=publication, browser=marker)
        h.gateway = gateway_fixture.start(h)
        if args.scenario == 'memory':
            run_id = h.send_text('memory-six')
            delivery = h.wait_delivery(run_id)
            result = wait_success(h, run_id)
            assert delivery['final_text'] == 'memory final: memory-six'
            actual_request = json.loads(h.sql('SELECT request_json::text FROM worker.execution_runs WHERE run_id=' + h.quote(run_id))[0][0])
            assert actual_request['Route']['ManifestRef'] == h.manifest_id and actual_request['Route']['ManifestDigest'] == h.manifest_digest and actual_request['Route']['DeploymentRevisionID'] == h.revision_id
            evidence['worker_request_route'] = actual_request['Route']
            state = h.memory_state()
            assert len(state) == 1 and 'persistent orchid memory' in json.dumps(state)
            assert h.sql('SELECT memory_status FROM worker.execution_completions WHERE run_id=' + h.quote(run_id)) == [['APPLIED']]
            evidence.update(result='PASS', worker_used_gui_manifest=True, run_id=run_id, delivery=delivery, memory=state, completion=result['completion'])
        else:
            from faults import run, head
            rounds = []
            prior_summary = None
            prior_candidate = None
            for text in ('gui redis history seed', 'gui redis summary generate', 'gui redis summary consume'):
                offset = len(h.model.snapshot())
                run_id = h.send_text(text)
                delivery = h.wait_delivery(run_id)
                result = wait_redis_success(h, run_id)
                actual_request = json.loads(h.sql('SELECT request_json::text FROM worker.execution_runs WHERE run_id=' + h.quote(run_id))[0][0])
                route = actual_request['Route']
                assert (route['ManifestRef'], route['ManifestDigest'], route['DeploymentRevisionID']) == (h.manifest_id, h.manifest_digest, h.revision_id)
                candidate = result['candidate']
                accepted = head(h, run(h, run_id))
                assert (accepted['accepted_ref'], accepted['accepted_digest']) == (candidate['candidate_ref'], candidate['content_digest'])
                assert candidate['parent_ref'] == (prior_candidate['candidate_ref'] if prior_candidate else '')
                calls = h.model.snapshot()[offset:]
                primary = [call for call in calls if call['model'] == 'joint-fixture']
                assert len(primary) == 1
                if prior_summary:
                    assert prior_summary in json.dumps(primary[0]['messages'])
                summaries = candidate['content']['snapshot']['session'].get('summaries', {})
                if summaries:
                    prior_summary = next(iter(summaries.values()))['summary']
                    assert prior_summary == h.model.summary_outputs()[-1]
                assert delivery['final_text'] == 'joint answer: ' + text
                rounds.append({'run_id': run_id, 'route': route, 'candidate': candidate, 'head': accepted, 'delivery': delivery, 'calls': calls})
                prior_candidate = candidate
            assert prior_summary and len(h.model.summary_outputs()) >= 2
            assert len({item['candidate']['content']['identity']['session_id'] for item in rounds}) == 1
            evidence.update(result='PASS', worker_used_gui_manifest=True, rounds=rounds, session=h.session_state(), same_redis_summary=True, next_run_consumed_summary=True)
        save()
    except BaseException as exc:
        evidence.update(result='FAIL', error=h.redact(str(exc)))
        save()
        raise RuntimeError(evidence['error']) from None
    finally:
        if web is not None and web.poll() is None:
            os.killpg(web.pid, signal.SIGTERM)
            try:
                web.wait(timeout=20)
            except subprocess.TimeoutExpired:
                os.killpg(web.pid, signal.SIGKILL)
                web.wait(timeout=5)
        if web_log:
            web_log.close()
        evidence['web_exit'] = web.returncode if web is not None else None
        try:
            h.close()
            evidence['cleanup'] = 'PASS'
        except BaseException as exc:
            evidence.update(result='FAIL', cleanup_error=h.redact(str(exc)))
            raise
        finally:
            save()
            leaked = [str(path) for path in h.artifacts.rglob('*') if path.is_file() and any(secret.encode() in path.read_bytes() for secret in h.secrets if secret)]
            evidence['credential_leak_scan'] = {'result': 'FAIL' if leaked else 'PASS', 'files': leaked}
            if leaked:
                evidence['result'] = 'FAIL'
            save()
            if leaked:
                raise RuntimeError('credential leak in browser artifacts')
    print('WORKER_' + args.scenario.upper() + '_WEB=PASS', flush=True)

if __name__ == '__main__':
    main()
