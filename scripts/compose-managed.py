#!/usr/bin/env python3
"""One explicit local Compose project; never discovers or mutates other stacks."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.request
import time
import tempfile
import shutil
import urllib.error

ROOT = Path(__file__).resolve().parents[1]
GENERATOR = ROOT / 'scripts/compose-managed-config.py'
DEFAULT_STATE = Path.home() / '.local/share/trpc-agent-service/managed-local'
FILES = [ROOT / 'deploy/compose' / name for name in (
    'compose.yaml', 'compose.local.yaml', 'compose.worker-v1.yaml', 'compose.data-backends.yaml')]


def run(command, *, capture=False, env=None):
    if len(command)>2 and list(command[:2])==['docker','compose']:
        index=command.index('--env-file')
        state=Path(command[index+1]).parent
        env={**os.environ, **read_env(state)}
    p = subprocess.run([str(x) for x in command], cwd=ROOT, text=True,
                       stdout=subprocess.PIPE if capture else None,
                       stderr=subprocess.PIPE if capture else None, env=env)
    if p.returncode:
        # Captured tools may expand private config: do not echo their output.
        raise RuntimeError(f'{Path(str(command[0])).name} failed exit={p.returncode}')
    return p.stdout if capture else ''


def read_env(state):
    result = {}
    for line in (state/'compose.env').read_text().splitlines():
        if not line or line.startswith('#'): continue
        key, value = line.split('=', 1)
        # Generated values are unquoted and contain no newlines or interpolation.
        result[key] = value
    return result


def compose(state, values):
    project = values.get('COMPOSE_PROJECT_NAME', 'trpc-agent-managed-local')
    if project in ('trpc-agent-latest', 'channel-lab-dev'):
        raise RuntimeError('select an independent managed project')
    cmd = ['docker', 'compose', '--project-name', project, '--env-file', state/'compose.env']
    for path in FILES: cmd.extend(['-f', path])
    return cmd


def pin(state, values):
    names = ['CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS', 'CONTROL_PLATFORM_BACKEND_CATALOG_SHA256',
             'CONTROL_PLATFORM_BACKEND_TARGETS_SHA256']
    environment = os.environ.copy()
    cmd = ['docker', 'run', '--rm']
    for name in names:
        environment[name] = values[name]
        cmd.extend(['-e', name])
    cmd.extend([values['CONTROL_API_IMAGE'], '--print-deployment-contract-digest'])
    digest = run(cmd, capture=True, env=environment).strip()
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', digest):
        raise RuntimeError('Control returned invalid contract digest')
    run([sys.executable, GENERATOR, 'pin', '--state-dir', state, '--digest', digest])


def ready(values):
    endpoints = {
        'control': f"http://127.0.0.1:{values.get('CONTROL_API_HTTP_PORT','28080')}/healthz",
        'gateway': f"http://127.0.0.1:{values.get('GATEWAY_ADMIN_PORT','28091')}/readyz",
        'worker': f"http://127.0.0.1:{values.get('WORKER_ADMIN_PORT','28083')}/readyz",
        'web': f"http://127.0.0.1:{values.get('WEB_HTTP_PORT','23000')}/",
    }
    pending = dict(endpoints); deadline = time.monotonic()+180
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    while pending and time.monotonic() < deadline:
        for name, url in list(pending.items()):
            try:
                with opener.open(url, timeout=3) as response:
                    if response.status in (200,204): del pending[name]
            except urllib.error.HTTPError as error: error.close()
            except OSError: pass
        if pending: time.sleep(2)
    if pending: raise RuntimeError('HTTP readiness failed: '+','.join(pending))
    print('APPLICATION_HTTP_READY=PASS control gateway worker web')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=['up','status','stop','grant','restart-backends','config'])
    p.add_argument('--state-dir', type=Path, default=DEFAULT_STATE)
    p.add_argument('--project', default='trpc-agent-managed-local')
    p.add_argument('--allowed-host', action='append')
    p.add_argument('--tenant-id', action='append')
    p.add_argument('--skip-build', action='store_true', help='use already-built images explicitly')
    args = p.parse_args(); state = args.state_dir.expanduser().resolve()
    if args.action == 'up' and not (state/'compose.env').exists():
        cmd = [sys.executable, GENERATOR, 'init', '--state-dir', state, '--project', args.project]
        for host in args.allowed_host or []: cmd += ['--allowed-host', host]
        run(cmd)
    if not (state/'compose.env').exists(): raise RuntimeError('run up to initialize this state first')
    values=read_env(state); dc=compose(state, values)
    if args.action=='up':
        # Build independent release images; -f overlays are always the complete stack.
        if not args.skip_build:
            # Reuse the host Go module cache; no credentials or repo files enter
            # the runtime image context, only the cross-built executable.
            arch = run(['docker','info','--format','{{.Architecture}}'],capture=True).strip()
            arch = {'aarch64':'arm64','x86_64':'amd64'}.get(arch,arch)
            if arch not in ('arm64','amd64'): raise RuntimeError('unsupported Docker architecture')
            environment=os.environ.copy()
            environment.update(CGO_ENABLED='0',GOOS='linux',GOARCH=arch)
            for service, package in [('CONTROL_API','control-api'),('CHANNEL_GATEWAY','channel-gateway'),('AGENT_WORKER','agent-worker')]:
                with tempfile.TemporaryDirectory(prefix='managed-image-') as build_dir:
                    run(['go','build','-trimpath','-ldflags=-s -w','-o',Path(build_dir)/'binary',
                         './services/'+package+'/cmd/'+package],env=environment)
                    shutil.copyfile(ROOT/'deploy/compose/Dockerfile.runtime-managed',Path(build_dir)/'Dockerfile')
                    run(['docker','build','-t',values[service+'_IMAGE'],build_dir])
            run(['docker','build','-t',values.get('WEB_IMAGE','trpc-agent-service/web:managed-local'),
                 '--build-arg','RUNTIME_API_BASE=http://channel-gateway:8090',
                 '-f','deploy/compose/Dockerfile.web-managed','.'])
            run(['docker','build','-t',values.get('BACKEND_TOOLS_IMAGE','trpc-agent-service/backend-tools:managed-local'),
                 '-f','deploy/compose/Dockerfile.backend-tools','deploy/compose'])
        pin(state,values); values=read_env(state); dc=compose(state,values)
        acl=[sys.executable,ROOT/'deploy/compose/data-backends/backendctl.py','redis-acl']
        for role in ['admin','memory','session']:
            acl += ['--'+role+'-password-file',state/'secrets'/('redis_'+role)]
        acl += ['--output',state/'redis/users.acl']; run(acl)
        run(dc+['config','--quiet'])
        run(dc+['up','-d','--no-build','--wait','--wait-timeout','240'])
        ready(values)
        print('MANAGED_STACK=READY project='+values.get('COMPOSE_PROJECT_NAME',args.project))
        print('PRIVATE_CONFIG='+str(state))
        print('EXTERNAL_MODEL=NOT_PROVISIONED_BY_STARTUP IM_BINDING=NOT_PROVISIONED_BY_STARTUP')
    elif args.action=='grant':
        if not args.tenant_id: raise RuntimeError('grant requires explicit --tenant-id')
        cmd=[sys.executable,GENERATOR,'grant','--state-dir',state]
        for tenant in args.tenant_id: cmd += ['--tenant-id',tenant]
        # This entry is for an unused local stack; don't switch active deployments.
        run(cmd); values=read_env(state); pin(state,values)
        run(dc+['up','-d','--no-deps','--force-recreate','control-api','agent-worker'])
        ready(read_env(state)); print('CATALOG_GRANT_PIN=PASS')
    elif args.action=='status':
        run(dc+['ps']); ready(values)
    elif args.action=='config': run(dc+['config','--quiet']); print('COMPOSE_CONFIG=PASS')
    elif args.action=='stop': run(dc+['stop']); print('VOLUMES_PRESERVED=true')
    elif args.action=='restart-backends':
        run(dc+['restart','postgres','redis','qdrant','minio'])
        run(dc+['exec','-T','backend-tools','sh','/tooling/health-all.sh'])
        print('BACKEND_RESTART=PASS volumes_preserved=true')


if __name__=='__main__':
    try: main()
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print('MANAGED_STACK=FAIL '+str(error), file=sys.stderr); sys.exit(1)
