#!/usr/bin/env python3
"""Use a signed-out disposable VM profile to test the real portal autostart path.

Unlike lifecycle-fixture.py this temporarily uses the default app profile because
portal autostart must run without a test-only environment override. Never run on
a personal installation: authenticated or nonempty profiles are refused.
"""
import json
import os
from pathlib import Path
import shutil
import sys

root = Path.home() / '.var/app/com.modeluplink.app/config/modeluplink'
backup = root.with_name('modeluplink-before-startup-test')
marker = root / '.startup-test-fixture'
action = sys.argv[1]
os.umask(0o077)
if action == 'prepare':
    if backup.exists() or marker.exists():
        raise SystemExit('An unfinished startup fixture exists; refusing to replace it')
    cfg_path = root / 'config.json'
    if cfg_path.exists():
        cfg = json.loads(cfg_path.read_text())
        if cfg.get('account_token') or cfg.get('endpoints'):
            raise SystemExit('Refusing to replace an authenticated or nonempty app profile')
    if root.exists():
        root.rename(backup)
    else:
        backup.mkdir(parents=True)
    (root / 'endpoints').mkdir(parents=True)
    endpoint = {'id': 'ep_flatpak_startup', 'slug': 'flatpak-startup',
                'engine': 'openai_compatible', 'runtime_ownership': 'external',
                'control_url': 'http://127.0.0.1:9',
                'relay_url': 'ws://127.0.0.1:9/v1/relay/connect',
                'url': 'https://flatpak-startup.invalid',
                'agent_token': 'local-test-placeholder',
                'upstream_url': 'http://127.0.0.1:11434/v1',
                'shared_models': ['gemma3:1b'], 'stopped': False}
    (root / 'config.json').write_text(json.dumps({
        'startup_disabled': True, 'control_url': 'http://127.0.0.1:9',
        'account_token': 'local-test-placeholder',
        'endpoints': {'flatpak-startup': endpoint}}))
    (root / 'endpoints/flatpak-startup.json').write_text(json.dumps(endpoint))
    marker.touch()
    print('Disposable default profile prepared; all control/relay traffic uses loopback port 9.')
elif action in ('state', 'restore'):
    if not marker.exists() or not backup.exists():
        raise SystemExit('Fixture marker and backup are required')
    cfg = json.loads((root / 'config.json').read_text())
    endpoint = json.loads((root / 'endpoints/flatpak-startup.json').read_text())
    result = {'startup_disabled': cfg.get('startup_disabled', False),
              'config_stopped': cfg['endpoints']['flatpak-startup'].get('stopped', False),
              'endpoint_stopped': endpoint.get('stopped', False)}
    if action == 'restore':
        if not all(result.values()):
            raise SystemExit('Disable autostart and Stop before restoring the profile')
        shutil.rmtree(root)
        backup.rename(root)
        result['restored'] = True
    print(json.dumps(result))
else:
    raise SystemExit('Use prepare, state, or restore')
