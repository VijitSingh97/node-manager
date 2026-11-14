"""Run the same public dashboard contracts against the Python reference or Go."""
import base64
import hashlib
import http.server
import json
import os
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ADDRESS = '1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa'
TIP = 'ab' * 32


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        value = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        method, params = value['method'], value['params']
        state = self.server.state
        wallet = urllib.parse.unquote(self.path.removeprefix('/wallet/')) if self.path.startswith('/wallet/') else ''
        with self.server.lock:
            state['calls'].append((wallet, method, params, self.headers.get('Authorization')))
            result = None
            allow_null = False
            if method == 'getblockchaininfo' and not state['down']:
                result = {'blocks': 123, 'headers': 123, 'verificationprogress': 1,
                          'initialblockdownload': state['ibd'], 'pruned': state['pruned'],
                          'prune_target_size': 2**30, 'size_on_disk': 10**9,
                          'bestblockhash': TIP}
            elif method == 'getnetworkinfo':
                result = {'subversion': state['version'], 'connections': 3}
            elif method == 'getpeerinfo':
                result = [{'inbound': True}, {'inbound': False}, {'inbound': False}]
            elif method == 'uptime':
                result = 90061
            elif method == 'getmempoolinfo':
                result = {'size': 42, 'bytes': 2**20}
            elif method == 'estimatesmartfee':
                result = {'feerate': 0.000004 if params == [1] else 0.00002}
            elif method == 'loadwallet' and params[0] in state['wallets']:
                state['wallets'][params[0]]['loaded'] = True
                result = {'name': params[0]}
            elif method == 'createwallet':
                state['wallets'][params[0]] = {'loaded': True, 'mode': 'ok', 'descriptors': []}
                result = {'name': params[0]}
            elif method == 'unloadwallet' and params[0] in state['wallets']:
                state['wallets'][params[0]]['loaded'] = False
                result = {}
            elif method == 'getdescriptorinfo':
                result = {'descriptor': params[0] + '#fixture', 'isrange': '/*' in params[0], 'hasprivatekeys': False}
            elif method == 'deriveaddresses':
                result = [ADDRESS]
            elif method == 'scantxoutset' and params == ['status']:
                allow_null = True
            elif method == 'scantxoutset' and params[0] == 'start':
                result = {'success': True, 'txouts': 200_000_000, 'height': 123,
                          'bestblock': TIP, 'total_amount': 1.23456789,
                          'unspents': [{'txid': 'cd' * 32, 'vout': 0,
                                        'scriptPubKey': '76a91462e907b15cbf27d5425399ebf6f0fb50ebb88f188ac',
                                        'desc': f'addr({ADDRESS})#fixture',
                                        'amount': 1.23456789, 'height': 100}]}
            elif method == 'getwalletinfo' and wallet in state['wallets']:
                info = state['wallets'][wallet]
                if info['loaded'] and info['mode'] != 'error':
                    result = {'scanning': {'progress': 0.5} if info['mode'] == 'scanning' else False,
                              'descriptors': True, 'private_keys_enabled': False}
            elif method == 'getbalances':
                result = {'mine': {'trusted': 1.23456789}}
            elif method == 'importdescriptors':
                state['wallets'][wallet]['descriptors'] = [
                    {'desc': item['desc'], 'internal': item['internal'], 'timestamp': item['timestamp'],
                     **({'range': [0, item['range']]} if 'range' in item else {})}
                    for item in params[0]
                ]
                result = [{'success': True} for _ in params[0]]
            elif method == 'listdescriptors' and wallet in state['wallets']:
                result = {'descriptors': [
                    {key: value for key, value in item.items() if key != 'internal'}
                    for item in state['wallets'][wallet].get('descriptors', [])
                ]}
        if method in state.get('failed_methods', []):
            result = None
            allow_null = False
        success = result is not None or allow_null
        body = json.dumps({'result': result, 'error': None if success else {'code': -18}, 'id': value.get('id')}).encode()
        self.send_response(200 if success else 500)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(body)


class DashboardContracts(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.state_dir = Path(self.temp.name)
        self.fixture = None
        self.fresh_fixture()
        self.go_dashboard = 'DASHBOARD_COMMAND' in os.environ
        self.log = (self.state_dir / 'server.log').open('w+')
        self.start()

    def fresh_fixture(self):
        # A new port per dashboard instance: an in-flight RPC from the previous
        # instance can otherwise be recorded after the new instance started.
        state = {'calls': [], 'wallets': {}, 'down': False, 'ibd': False, 'pruned': False, 'version': '/Satoshi:31.1.0/'}
        if self.fixture is not None:
            with self.fixture.lock:
                state = dict(self.fixture.state, calls=[])
            self.fixture.shutdown()
            self.fixture.server_close()
            self.thread.join()
        self.fixture = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        self.fixture.lock = threading.Lock()
        self.fixture.state = state
        self.thread = threading.Thread(target=self.fixture.serve_forever, daemon=True)
        self.thread.start()

    def start(self, password='paritypass', expect_failure=False):
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            self.port = listener.getsockname()[1]
        self.url = f'http://127.0.0.1:{self.port}'
        env = dict(os.environ, RPC_URL=f'http://127.0.0.1:{self.fixture.server_port}',
                   RPC_USER='parityuser', RPC_PASSWORD='parityrpc', DASHBOARD_PASSWORD=password,
                   PORT=str(self.port), DATA_DIR=str(self.state_dir),
                   STATE_DIR=str(self.state_dir), WATCH_STORE=str(self.state_dir/'wallets.json'),
                   BALANCE_HISTORY=str(self.state_dir/'balance_history.json'), EVENTS_LOG=str(self.state_dir/'events.log'),
                   WATCH_WALLETS_B64='', TELEGRAM_BOT_TOKEN='', TELEGRAM_CHAT_ID='', HEALTHCHECKS_URL='', STACK_VERSION='1.31.1')
        self.password = password
        command = json.loads(os.environ.get('DASHBOARD_COMMAND', json.dumps([sys.executable, str(ROOT/'parity/python_server.py')])))
        self.process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=self.log, stderr=self.log)
        for _ in range(100):
            if self.process.poll() is not None:
                self.log.seek(0)
                output = self.log.read()
                if expect_failure:
                    return output
                self.fail(f'dashboard exited: {output}')
            try:
                if self.call('/api/fees')[0] == 200:
                    if expect_failure:
                        self.stop()
                        self.fail('dashboard started without the required password')
                    return
            except OSError:
                pass
            time.sleep(0.05)
        self.fail('dashboard did not become ready')

    def stop(self):
        self.process.terminate()
        try:
            self.process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait()
            self.fail('dashboard failed graceful shutdown')
        self.fresh_fixture()

    def tearDown(self):
        self.stop()
        self.fixture.shutdown()
        self.fixture.server_close()
        self.thread.join()
        self.log.close()
        self.temp.cleanup()

    def call(self, path, method='GET', data=None, csrf=True, auth=True, binary=False, follow_redirects=True):
        headers = {}
        if auth and self.password:
            headers['Authorization'] = 'Basic ' + base64.b64encode(f'admin:{self.password}'.encode()).decode()
        if csrf:
            headers['X-Requested-With'] = 'fetch'
        payload = None if data is None else json.dumps(data).encode()
        if payload is not None:
            headers['Content-Type'] = 'application/json'
        req = urllib.request.Request(self.url+path, data=payload, headers=headers, method=method)
        try:
            opener = urllib.request.build_opener() if follow_redirects else urllib.request.build_opener(NoRedirect)
            response = opener.open(req, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = response.read()
            return response.status, body if binary else body.decode(), response.headers

    def body(self, path='/api/watch'):
        status, body, _ = self.call(path)
        self.assertEqual(status, 200)
        return json.loads(body)

    def bitcoin_panel(self, body):
        marker = 'id="bitcoin-panel"'
        if marker not in body:
            return body
        start = body.index(marker)
        end = body.find('id="monero-panel"', start)
        if end < 0:
            return body[start:]
        return body[start:end]

    def add(self, name='Cold', key=ADDRESS, birthday='2021-03-15'):
        status, body, _ = self.call('/api/watch', 'POST', {'name': name, 'key': key, 'birthday': birthday})
        self.assertEqual((status, json.loads(body)), (200, {'ok': True}))
        for _ in range(100):
            with self.fixture.lock:
                ready = any(method == 'importdescriptors' for _, method, _, _ in self.fixture.state['calls'])
            if ready and any(row['name'] == name and row['state'] in ('ok', 'scanning')
                             for row in self.body()['wallets']):
                return
            time.sleep(0.01)
        self.fail('descriptor import did not complete')

    def wallet_name(self, label='Cold', key=ADDRESS):
        if self.go_dashboard:
            return 'watch_' + hashlib.sha256(key.strip().encode()).hexdigest()
        return 'watch_' + label

    def test_auth_covers_routes_and_static(self):
        if self.go_dashboard:
            status, _, headers = self.call('/', auth=False, follow_redirects=False)
            self.assertEqual(status, 302)
            self.assertEqual(headers['Location'], '/login')
            self.assertNotIn('WWW-Authenticate', headers)
            for path in ['/metrics', '/api/settings', '/api/watch', '/api/fees']:
                status, _, headers = self.call(path, auth=False)
                self.assertEqual(status, 401, path)
                self.assertNotIn('WWW-Authenticate', headers)
            for path in ['/static/dashboard.css', '/static/theme.js', '/static/favicon.svg']:
                status, _, headers = self.call(path, auth=False)
                self.assertEqual(status, 200, path)
                self.assertNotIn('WWW-Authenticate', headers)
            return
        for path in ['/', '/metrics', '/api/settings', '/api/watch', '/api/fees',
                     '/static/dashboard.css', '/static/theme.js', '/static/favicon.svg']:
            status, _, headers = self.call(path, auth=False)
            self.assertEqual(status, 401, path)
            self.assertIn('Basic', headers['WWW-Authenticate'])
        icon_path = '/static/favicon.png?v=tower-1'
        status, icon, headers = self.call(icon_path, auth=False, binary=True)
        self.assertEqual(status, 200)
        self.assertIn('image/png', headers['Content-Type'])
        self.assertTrue(icon.startswith(b'\x89PNG\r\n\x1a\n'))
        status, body, headers = self.call(icon_path, method='HEAD', auth=False, binary=True)
        self.assertEqual(status, 200)
        self.assertIn('image/png', headers['Content-Type'])
        self.assertEqual(body, b'')
        self.assertEqual(self.call(icon_path, method='POST', auth=False)[0], 401)

    def test_optional_password_compatibility(self):
        self.stop()
        if self.go_dashboard:
            output = self.start(password='', expect_failure=True)
            self.assertIn('dashboard sign-in requires a password', output)
            return
        self.start(password='')
        self.assertEqual(self.call('/', auth=False)[0], 200)
        self.assertFalse(self.body()['has_password'])

    def test_synced_ui_and_rpc_auth(self):
        status, body, _ = self.call('/')
        self.assertEqual(status, 200)
        bitcoin = self.bitcoin_panel(body)
        for value in ['Bitcoin Node Status', '/Satoshi:31.1.0/', '1d 1h 1m', '123 / 123', 'Sync Progress', 'Mempool:', '42 tx', '0.4', 'v1.31.1', 'id="watch"', 'data-blocks="123"']:
            self.assertIn(value, bitcoin)
        for value in ['/static/watch.js']:
            self.assertIn(value, body)
        expected = 'Basic '+base64.b64encode(b'parityuser:parityrpc').decode()
        self.assertTrue(all(call[3] == expected for call in self.fixture.state['calls']))

    def test_loading_and_down_metrics(self):
        self.fixture.state['down'] = True
        self.assertIn('Bitcoin Node Initializing', self.call('/')[1])
        self.assertIn('bitcoin_node_up 0', self.call('/metrics')[1])

    def test_ibd_avoids_fee_and_mempool_reads(self):
        self.stop()
        with self.fixture.lock:
            self.fixture.state['ibd'] = True
        self.start()
        body = self.bitcoin_panel(self.call('/')[1])
        self.assertNotIn('Mempool:', body)
        methods = [row[1] for row in self.fixture.state['calls']]
        self.assertNotIn('getmempoolinfo', methods)
        self.assertNotIn('estimatesmartfee', methods)

    def test_metrics(self):
        status, body, headers = self.call('/metrics')
        self.assertEqual(status, 200)
        self.assertIn('text/plain', headers['Content-Type'])
        for value in ['bitcoin_node_up 1', 'bitcoin_blocks 123', 'bitcoin_headers 123', 'bitcoin_connections 3', 'bitcoin_peers{direction="in"} 1', 'bitcoin_peers{direction="out"} 2', 'bitcoin_fee_sat_vb{blocks="1"} 0.4', 'bitcoin_mempool_txs 42']:
            self.assertIn(value, body)

    def test_pruned_watch_is_public_only(self):
        self.fixture.state['pruned'] = True
        self.assertIn('Pruned', self.call('/')[1])
        status, body, _ = self.call('/api/watch', 'POST', {'name':'Cold','key':ADDRESS})
        if not self.go_dashboard:
            self.assertEqual(status, 400)
            self.assertIn('full node', body)
            self.assertEqual(self.body()['wallets'], [])
            return
        self.assertEqual((status, json.loads(body)), (200, {'ok': True}))
        for _ in range(300):
            view = self.body()
            if view['wallets'] and view['wallets'][0]['state'] == 'ok':
                break
            time.sleep(0.01)
        row = view['wallets'][0]
        self.assertTrue(view['pruned'])
        self.assertEqual(row['state'], 'ok')
        self.assertEqual(row['btc'], '1.23456789')
        self.assertEqual(row['balance_kind'], 'confirmed')
        self.assertEqual(row['scan_height'], 123)
        self.assertTrue(row['scan_time'])
        for _ in range(5):
            self.assertEqual(self.body()['wallets'][0]['btc'], '1.23456789')
        methods = [call[1] for call in self.fixture.state['calls']]
        self.assertNotIn('createwallet', methods)
        self.assertNotIn('importdescriptors', methods)
        self.assertEqual(sum(method == 'scantxoutset' and params[0] == 'start'
                             for _, method, params, _ in self.fixture.state['calls']), 1)

    def test_wallet_crud_and_restart(self):
        self.add()
        view = self.body()
        self.assertEqual(view['wallets'][0], {'name':'Cold','key':ADDRESS,'state':'ok','btc':'1.23456789','history':[]})
        self.assertEqual(view['total'], '1.23456789')
        self.assertFalse(view['show_total'])
        stored = json.loads((self.state_dir/'wallets.json').read_text())
        self.assertEqual(stored, [{'name':'Cold','key':ADDRESS,'birthday':'2021-03-15'}])
        core_name = self.wallet_name()
        create = next(row[2] for row in self.fixture.state['calls'] if row[1]=='createwallet')
        self.assertEqual(create, [core_name,True,True,'',False,True,True])
        imported = next(row[2][0][0] for row in self.fixture.state['calls'] if row[1]=='importdescriptors')
        self.assertEqual(imported, {'desc':f'addr({ADDRESS})#fixture','timestamp':1615766400,'active':False,'internal':False})
        self.stop()
        self.start()
        for _ in range(300):
            restarted = self.body()['wallets'][0]
            if restarted['state'] != 'pending':
                break
            time.sleep(0.01)
        self.assertEqual((restarted['name'], restarted['state'], restarted['btc']),
                         ('Cold', 'ok', '1.23456789'))
        self.assertEqual(self.call('/api/watch/Cold', 'DELETE')[0], 200)
        self.assertEqual(self.body()['wallets'], [])
        self.assertEqual(self.call('/api/watch/Cold', 'DELETE')[0], 404)
        self.assertIn(('', 'unloadwallet', [core_name,False]), [row[:3] for row in self.fixture.state['calls']])

    def test_wallet_validation_and_csrf(self):
        for data in [{}, {'name':'Cold','key':'bad'}, {'name':'Cold','key':ADDRESS,'birthday':'tomorrow'}]:
            self.assertEqual(self.call('/api/watch', 'POST', data)[0], 400)
        self.assertEqual(self.call('/api/watch', 'POST', {'name':'Cold','key':ADDRESS}, csrf=False)[0], 403)
        self.assertEqual(self.call('/api/watch/Cold', 'DELETE', csrf=False)[0], 403)
        self.add()
        self.assertEqual(self.call('/api/watch', 'POST', {'name':'cold','key':ADDRESS})[0], 400)
        self.assertEqual(len(self.body()['wallets']), 1)

    def test_wallet_scanning_and_stale_history(self):
        self.add()
        core_name = self.wallet_name()
        with self.fixture.lock:
            self.fixture.state['wallets'][core_name]['mode']='scanning'
        self.assertEqual(self.body()['wallets'][0]['state'], 'scanning')
        self.stop()
        kid=hashlib.sha256(ADDRESS.encode()).hexdigest()[:16]
        (self.state_dir/'balance_history.json').write_text(json.dumps({kid:[[100,0.5],[3700,0.75]]}))
        with self.fixture.lock:
            self.fixture.state['wallets'][core_name]['mode']='error'
        self.start()
        for _ in range(30):
            row=self.body()['wallets'][0]
            if row['state'] != 'pending':
                break
            time.sleep(0.1)
        self.assertEqual((row['state'],row['btc'],row['history']), ('stale','0.75',[0.5,0.75]))

    def test_static_and_fee_contract(self):
        status, body, headers=self.call('/static/dashboard.css')
        self.assertEqual(status,200)
        self.assertIn('text/css',headers['Content-Type'])
        self.assertIn('max-age=0',headers.get('Cache-Control',''))
        self.assertIn('--',body)
        status, icon, headers=self.call('/static/favicon.png?v=tower-1', binary=True)
        self.assertEqual(status,200)
        self.assertIn('image/png',headers['Content-Type'])
        self.assertTrue(icon.startswith(b'\x89PNG\r\n\x1a\n'))
        self.assertIsInstance(self.body('/api/fees')['fee'],list)
        self.assertEqual(self.call('/metrics','POST')[0],405)

    def test_partial_rpc_failure(self):
        self.fixture.state['failed_methods']=['getnetworkinfo','uptime','getmempoolinfo','estimatesmartfee']
        status, body, _=self.call('/')
        self.assertEqual(status,200)
        bitcoin = self.bitcoin_panel(body)
        self.assertIn('Unknown',bitcoin)
        self.assertIn('N/A',bitcoin)
        self.assertNotIn('Mempool:',bitcoin)

    def test_node_version_escaped(self):
        self.fixture.state['version']='<script>alert(1)</script>'
        body=self.call('/')[1]
        self.assertNotIn('<script>alert(1)</script>',body)
        self.assertIn('&lt;script&gt;',body)


if __name__ == '__main__':
    unittest.main()
