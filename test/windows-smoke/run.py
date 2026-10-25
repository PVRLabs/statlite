#!/usr/bin/env python3
"""Exercise the actual executable and Spring demo; no third-party Python packages."""

import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import socket
import sqlite3
import subprocess
import time
from urllib.error import URLError
from urllib.request import ProxyHandler, build_opener

HTTP = build_opener(ProxyHandler({}))


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def get(url, *, raw=False):
    with HTTP.open(url, timeout=3) as response:
        body = response.read()
    return body if raw else json.loads(body)


def wait_for(label, predicate, process, seconds=60):
    deadline = time.monotonic() + seconds
    last_error = None
    while time.monotonic() < deadline:
        require(process.poll() is None, f'Process exited before {label}: {process.returncode}')
        try:
            result = predicate()
            if result:
                return result
        except (URLError, OSError, ValueError) as error:
            last_error = error
        time.sleep(0.25)
    raise AssertionError(f'Timed out waiting for {label}; last error: {last_error}')


def windows_console():
    """CI may have redirected output and no console; the child needs our console."""
    kernel = ctypes.WinDLL('kernel32', use_last_error=True)
    kernel.GetConsoleProcessList.argtypes = [ctypes.POINTER(ctypes.c_ulong), ctypes.c_ulong]
    kernel.GetConsoleProcessList.restype = ctypes.c_ulong
    kernel.AllocConsole.argtypes = []
    kernel.AllocConsole.restype = ctypes.c_int
    processes = (ctypes.c_ulong * 1)()
    if not kernel.GetConsoleProcessList(processes, 1) and not kernel.AllocConsole():
        raise ctypes.WinError(ctypes.get_last_error())
    return kernel


def stop(process, port, report, name, kernel):
    require(process.poll() is None, f'{name} exited before shutdown was requested')
    started = time.monotonic()
    if kernel is not None:
        # CTRL_BREAK targets this process group only. Go maps it to os.Interrupt.
        # Call the API directly: never fall back to TerminateProcess on failure.
        kernel.GenerateConsoleCtrlEvent.argtypes = [ctypes.c_ulong, ctypes.c_ulong]
        kernel.GenerateConsoleCtrlEvent.restype = ctypes.c_int
        if not kernel.GenerateConsoleCtrlEvent(1, process.pid):
            raise ctypes.WinError(ctypes.get_last_error())
    else:
        process.send_signal(signal.SIGINT)
    code = process.wait(timeout=15)
    require(code == 0, f'{name} shutdown exit code: {code}')
    with socket.socket() as sock:
        sock.settimeout(1)
        require(sock.connect_ex(('127.0.0.1', port)) != 0, f'{name} listener is still open')
    report[name] = {'exit_code': code, 'seconds': round(time.monotonic() - started, 3)}


def database_snapshot(path):
    require(path.is_file(), 'SQLite database missing')
    # Open only after StatLite has stopped; do not let the test create a database.
    with sqlite3.connect(path.as_uri() + '?mode=ro', uri=True) as db:
        require(db.execute('PRAGMA integrity_check').fetchall() == [('ok',)], 'SQLite integrity failed')
        rows = {table: db.execute(f'SELECT * FROM {table} ORDER BY id').fetchall()
                for table in ('polls', 'metric_samples')}
    return rows


def run(args):
    binary = args.binary.resolve(strict=True)
    jar = args.spring_jar.resolve(strict=True)
    # Each run gets its own directory, including spaces to exercise Windows paths.
    work = args.results.resolve() / time.strftime('run-%Y%m%d-%H%M%S')
    work.mkdir(parents=True, exist_ok=False)
    runtime = work / 'runtime with spaces'
    runtime.mkdir()
    report = {'platform': platform.platform(), 'python': platform.python_version(),
              'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
              'passed': False, 'shutdown_signal': 'CTRL_BREAK_EVENT' if os.name == 'nt' else 'SIGINT'}
    processes, logs = [], []

    def start(name, command):
        log = (work / f'{name}.log').open('w', encoding='utf-8')
        logs.append(log)
        process = subprocess.Popen(command, cwd=runtime, stdin=subprocess.DEVNULL,
                                   stdout=log, stderr=subprocess.STDOUT,
                                   creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0)
        processes.append(process)
        return process

    try:
        kernel = windows_console() if os.name == 'nt' else None
        spring_port, port = free_port(), free_port()
        while spring_port == port:
            port = free_port()
        spring_base, base = f'http://127.0.0.1:{spring_port}', f'http://127.0.0.1:{port}'
        spring = start('spring', ['java', '-jar', str(jar), f'--server.port={spring_port}'])
        wait_for('Spring readiness', lambda: get(spring_base + '/actuator/health')['status'] == 'UP', spring)
        get(spring_base + '/api/hello', raw=True)
        config = runtime / 'statlite config.yaml'
        config.write_text(
            f'server:\n  listen: "127.0.0.1:{port}"\n'
            'storage:\n  sqlite_path: "./history.sqlite"\n'
            'polling:\n  interval: "1s"\n  timeout: "3s"\n'
            'targets:\n  - name: "self"\n    type: "statlite-metrics"\n'
            f'    url: "{base}/statlite/metrics"\n'
            '  - name: "spring"\n    type: "spring"\n'
            f'    url: "{spring_base}/actuator"\n', encoding='utf-8')
        command = [str(binary), '--config', str(config)]
        process = start('initial', command)

        def ready():
            health = get(base + '/healthz')
            return health['status'] == 'ok' and health['storage']['status'] == 'ok'

        wait_for('StatLite readiness', ready, process)
        require(b'StatLite' in get(base + '/', raw=True), 'Dashboard HTML missing')
        for target in ('self', 'spring'):
            wait_for(target + ' collection', lambda: get(base + '/api/v1/status?target=' + target)['collection_status'] == 'ok', process)
        for _ in range(5):
            get(spring_base + '/api/hello', raw=True)
        wait_for('Spring heap metrics', lambda: any(
            p['runtime_memory_bytes'] is not None and p['runtime_memory_bytes'] > 0
            for p in get(base + '/api/v1/metrics?target=spring')['points']), process)
        stop(process, port, report, 'initial_shutdown', kernel)
        db = runtime / 'history.sqlite'
        before = database_snapshot(db)
        require(len(before['polls']) >= 2 and before['metric_samples'], 'Missing durable polls or samples')

        # Read final data after shutdown, then reopen without polling. New samples
        # cannot mask lost history or races between capturing a baseline and stopping.
        process = start('replay', command + ['--no-poll'])
        wait_for('replay readiness', ready, process)
        for target in ('self', 'spring'):
            report[target + '_status'] = get(base + '/api/v1/status?target=' + target)
            metrics = get(base + '/api/v1/metrics?target=' + target)
            report[target + '_metrics'] = metrics
            require(metrics['points'], f'{target} history unavailable through API after restart')
            require(report[target + '_status']['last_successful_poll_at'], f'{target} latest success missing')
            require(any(p['runtime_memory_bytes'] is not None for p in metrics['points']), f'{target} runtime memory missing')
            if target == 'self' and os.name == 'nt':
                require(all(p[key] is None for p in metrics['points']
                            for key in ('host_cpu_usage', 'host_memory_usage', 'host_disk_usage')),
                        'Windows host resources must remain absent')
            if target == 'spring':
                require(report[target + '_status']['application_health'] == 'UP', 'Spring health missing')
        stop(process, port, report, 'replay_shutdown', kernel)
        after = database_snapshot(db)
        require(before == after, 'Polls or samples changed during polling-disabled restart')
        report['history_preserved'] = {'polls': len(before['polls']), 'samples': len(before['metric_samples'])}

        process = start('resumed', command)
        wait_for('resumed readiness', ready, process)
        for target in ('self', 'spring'):
            previous = report[target + '_status']['last_successful_poll_at']
            wait_for(target + ' resumed collection', lambda: (
                (s := get(base + '/api/v1/status?target=' + target))['collection_status'] == 'ok'
                and s['last_successful_poll_at'] > previous), process)
        stop(process, port, report, 'resumed_shutdown', kernel)
        resumed = database_snapshot(db)
        require(len(resumed['polls']) > len(before['polls']), 'No appended polls after normal restart')
        require(resumed['polls'][:len(before['polls'])] == before['polls'], 'Earlier polls changed')
        require(resumed['metric_samples'][:len(before['metric_samples'])] == before['metric_samples'], 'Earlier samples changed')
        report['resumed_poll_count'] = len(resumed['polls'])
        report['passed'] = True
        print('PASS: dashboard, SQLite integrity/history, Spring collection, three graceful stops and resumed polling')
    except Exception as error:
        report['error'] = repr(error)
        raise
    finally:
        # Force termination is cleanup only, never evidence of a graceful stop.
        for process in processes:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=10)
        for log in logs:
            log.close()
        (work / 'result.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
        print(f'Evidence: {work}', flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--spring-jar', type=Path, required=True)
    parser.add_argument('--results', type=Path, default=Path('dist/windows-smoke'))
    run(parser.parse_args())
