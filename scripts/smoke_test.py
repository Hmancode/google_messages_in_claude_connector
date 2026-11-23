"""Smoke-test `gm-reader serve` over stdio with no pairing.

Checks: MCP handshake, tool list (read-only only), pairing_status, a tool
call when unpaired, no listening sockets, and a clean exit on stdin close.
"""
import json
import os
import subprocess
import sys
import tempfile
import time

exe = sys.argv[1]
extra = sys.argv[2:]
home = tempfile.mkdtemp()
env = dict(os.environ, GM_READER_HOME=home)
p = subprocess.Popen([exe, "serve", *extra], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                     stderr=subprocess.PIPE, env=env, text=True, bufsize=1)

def send(obj):
    p.stdin.write(json.dumps(obj) + "\n")
    p.stdin.flush()

def recv(want_id, timeout=10):
    deadline = time.time() + timeout
    while time.time() < deadline:
        line = p.stdout.readline()
        if not line:
            break
        msg = json.loads(line)
        if msg.get("id") == want_id:
            return msg
    raise SystemExit(f"no response for id {want_id}")

send({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
    "protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "smoke", "version": "0"}}})
init = recv(1)
print("server:", init["result"]["serverInfo"])
send({"jsonrpc": "2.0", "method": "notifications/initialized"})
send({"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
tools = recv(2)["result"]["tools"]
names = sorted(t["name"] for t in tools)
print("tools:", names)
assert names == ["list_conversations", "pairing_status", "read_messages", "search_contacts", "search_messages"], names
assert all(t.get("annotations", {}).get("readOnlyHint") for t in tools), "all tools must be readOnlyHint"

send({"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "pairing_status", "arguments": {}}})
status = recv(3)["result"]["structuredContent"]
print("pairing_status:", json.dumps(status)[:300])
assert status["paired"] is False and status["warnings"]

send({"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "list_conversations", "arguments": {}}})
res = recv(4)["result"]
print("list_conversations unpaired -> isError:", res.get("isError"), "|", res["content"][0]["text"][:120])
assert res.get("isError")

# No listening or connected sockets of any kind.
def open_sockets(pid):
    if sys.platform == "win32":
        out = subprocess.run(["netstat", "-ano"], capture_output=True, text=True).stdout
        return [l.strip() for l in out.splitlines() if l.split() and l.split()[-1] == str(pid)]
    fds = f"/proc/{pid}/fd"
    return [l for l in os.listdir(fds) if "socket" in os.readlink(os.path.join(fds, l))]

time.sleep(1)
sockets = open_sockets(p.pid)
print("open sockets:", len(sockets), sockets[:5])
assert not sockets, "serve must not open any sockets while unpaired"

p.stdin.close()
try:
    p.wait(timeout=5)
    print("exited after stdin closed, code", p.returncode)
except subprocess.TimeoutExpired:
    p.kill()
    raise SystemExit("did not exit after stdin closed")
stdout_rest = p.stdout.read()
assert stdout_rest == "", "unexpected stdout output: " + stdout_rest[:200]
print("stderr:", repr(p.stderr.read()[:300]))
files = sorted(os.listdir(home))
print("files in data dir:", files)
if "--no-cache" in extra:
    assert not any(f.startswith("cache.db") for f in files), "--no-cache must not write a cache file"
print("log:\n" + open(os.path.join(home, "gm-reader.log"), encoding="utf-8").read())
print("SMOKE OK")
