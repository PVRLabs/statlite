#!/usr/bin/env python3
"""Focused Linux tests; fake only Telegram transport, with no network access."""
import json
import os
from pathlib import Path
import subprocess
import shutil
import tempfile
import time
import unittest

HELPER = Path(__file__).with_name("notify-telegram.sh")


class Notifications(unittest.TestCase):
    def test_transitions_and_delivery(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            curl = root / "curl"
            curl.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys, time
args = sys.argv[1:]
assert os.environ["TELEGRAM_BOT_TOKEN"] not in " ".join(args)
assert os.environ["TELEGRAM_CHAT_ID"] not in " ".join(args)
assert "sendMessage" in sys.stdin.read()
payload = pathlib.Path(args[args.index("--data-binary") + 1][1:]).read_text()
message = json.loads(payload)
assert message["chat_id"] == os.environ["TELEGRAM_CHAT_ID"]
with open(os.environ["SENDS"], "a") as log:
    log.write(message["text"] + "\\n")
pathlib.Path(os.environ["STARTED"]).touch()
time.sleep(float(os.environ.get("DELAY", "0")))
if os.environ.get("FAIL") == "transport":
    sys.exit(22)
pathlib.Path(args[args.index("--output") + 1]).write_text(
    json.dumps({"ok": os.environ.get("FAIL") != "api"}))
''')
            curl.chmod(0o755)
            state = root / "health.state"
            log = root / "sends"
            env = dict(os.environ, PATH=f"{root}:{os.environ['PATH']}",
                       TELEGRAM_BOT_TOKEN="123:secret", TELEGRAM_CHAT_ID="-12345",
                       STATE_FILE=str(state), SENDS=str(log), STARTED=str(root / "started"))

            def run(code, expected=0, **extra):
                result = subprocess.run(["sh", str(HELPER), "sh", "-c", f"exit {code}"],
                                        env=dict(env, **extra), capture_output=True, text=True)
                self.assertEqual(result.returncode, expected, result.stderr)
                self.assertNotIn("123:secret", result.stdout + result.stderr)

            def messages():
                return log.read_text().splitlines() if log.exists() else []

            run(2, 2)
            self.assertFalse(state.exists())
            run(0)
            self.assertEqual(state.read_text(), "healthy\n")
            self.assertEqual(messages(), [])
            run(1)
            run(1)
            run(2, 2)
            run(127, 2)
            self.assertEqual(state.read_text(), "unhealthy\n")
            self.assertEqual(len(messages()), 1)
            for failure in ("transport", "api"):
                run(0, 2, FAIL=failure)
                self.assertEqual(state.read_text(), "unhealthy\n")
            run(0)
            run(0)
            self.assertEqual(messages()[-1], "RECOVERY: application health")
            self.assertEqual(len(messages()), 4)
            state.unlink()
            run(1)
            self.assertEqual(len(messages()), 5)  # first unhealthy alerts
            other = root / "other.state"
            run(0, STATE_FILE=str(other))
            self.assertEqual(state.read_text(), "unhealthy\n")
            self.assertEqual(other.read_text(), "healthy\n")
            # First process holds the real flock while fake transport is slow.
            (root / "started").unlink()
            process = subprocess.Popen(["sh", str(HELPER), "sh", "-c", "exit 0"],
                                       env=dict(env, DELAY="1"))
            try:
                deadline = time.monotonic() + 5
                while not (root / "started").exists() and time.monotonic() < deadline:
                    time.sleep(0.01)
                self.assertTrue((root / "started").exists())
                run(0)
                self.assertEqual(process.wait(timeout=5), 0)
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
            self.assertEqual(len(messages()), 6)
            self.assertEqual(state.stat().st_mode & 0o777, 0o600)
            self.assertEqual(list(root.glob("*.tmp.*")), [])
            self.assertEqual(list(root.glob("*.response.*")), [])

            # Operational failures must never look like an unhealthy check (1).
            for operation in ("mktemp", "response-mktemp", "jq", "mv", "cat"):
                with self.subTest(operation=operation):
                    command = "mktemp" if operation == "response-mktemp" else operation
                    real_command = shutil.which(command)
                    stub = root / command
                    if operation == "response-mktemp":
                        stub.write_text(f'''#!/bin/sh
case "$1" in *.response.*) exit 1 ;; esac
exec "{real_command}" "$@"
''')
                    else:
                        stub.write_text("#!/bin/sh\nexit 1\n")
                    stub.chmod(0o755)
                    try:
                        run(1, 2)
                        self.assertEqual(state.read_text(), "healthy\n")
                        self.assertEqual(list(root.glob("*.tmp.*")), [])
                        self.assertEqual(list(root.glob("*.response.*")), [])
                    finally:
                        stub.unlink()
            run(1)  # A later successful invocation still retries the transition.
            self.assertEqual(state.read_text(), "unhealthy\n")
            run(0, 2, STATE_FILE=str(root / "missing" / "health.state"))


if __name__ == "__main__":
    unittest.main()
