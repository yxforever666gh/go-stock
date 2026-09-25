import ctypes
import io
import json
import os
import subprocess
import sys

import httpx
import pytest

from stock_god.market.tunnel import (
    EDGE_TARGET,
    PROXY,
    ProcessGroup,
    Provisioner,
    WindowsJob,
    WindowsMutex,
    _ExtendedLimits,
    child_environment,
    cloudflared_command,
    forward,
)


def test_provisioning_is_one_use_and_errors_are_not_retried():
    calls = []

    def handle(request):
        calls.append(request)
        assert str(request.url) == "https://api.trycloudflare.com/tunnel"
        return httpx.Response(200, json={"token": "fixture-private-token", "counter": len(calls)})

    p = Provisioner(httpx.Client(transport=httpx.MockTransport(handle)))
    p.prepare()
    p.prepare()
    assert len(calls) == 1
    assert json.loads(p.consume())["counter"] == 1
    assert json.loads(p.consume())["counter"] == 2
    assert p.prepared is None
    failed = []

    def error(request):
        failed.append(request)
        return httpx.Response(302, headers={"Location": "https://other.test"})

    p = Provisioner(httpx.Client(transport=httpx.MockTransport(error)))
    with pytest.raises(RuntimeError):
        p.prepare()
    assert len(failed) == 1 and p.prepared is None


def test_provisioning_rejects_oversized_or_invalid_responses():
    for body in (b"not-json", b'"' + b"x" * (1 << 20) + b'"'):
        p = Provisioner(
            httpx.Client(transport=httpx.MockTransport(lambda request, body=body: httpx.Response(200, content=body)))
        )
        with pytest.raises(RuntimeError):
            p.prepare()
        assert p.prepared is None


class FakeSocket:
    def __init__(self, payload=b""):
        self.payload = payload
        self.sent = []
        self.closed = False
        self.timeout = None

    def settimeout(self, timeout):
        self.timeout = timeout

    def sendall(self, data):
        self.sent.append(data)

    def makefile(self, mode):
        return io.BytesIO(self.payload)

    def recv(self, size):
        return b""

    def shutdown(self, how):
        pass

    def close(self):
        self.closed = True


def test_relay_connects_only_to_proxy_and_preserves_buffered_tls_bytes():
    upstream = FakeSocket(b"HTTP/1.1 200 Connection established\r\nX-Test: one\r\n\r\nTLS-DATA")
    downstream = FakeSocket()
    destinations = []

    def connect(address, timeout):
        destinations.append((address, timeout))
        return upstream

    forward(downstream, connect=connect)
    assert destinations == [(PROXY, 10)]
    assert upstream.sent == [f"CONNECT {EDGE_TARGET} HTTP/1.1\r\nHost: {EDGE_TARGET}\r\n\r\n".encode()]
    assert downstream.sent == [b"TLS-DATA"]
    assert downstream.closed and upstream.closed


def test_proxy_failure_never_falls_back_to_direct_connection():
    addresses = []
    downstream = FakeSocket()

    def connect(address, timeout):
        addresses.append(address)
        raise OSError("proxy down")

    with pytest.raises(OSError):
        forward(downstream, connect=connect)
    assert addresses == [("127.0.0.1", 7890)] and downstream.closed


def test_cloudflared_uses_local_edges_and_controlled_proxy_environment(tmp_path):
    command = cloudflared_command(tmp_path / "cloudflared.exe")
    assert command[command.index("--protocol") + 1] == "http2"
    assert command[command.index("--edge") + 1] == "127.0.0.1:17844"
    assert command[command.index("--quick-service") + 1] == "http://127.0.0.1:17845"
    assert command[command.index("--http-host-header") + 1] == "127.0.0.1:18080"
    assert "--no-autoupdate" in command
    env = child_environment(tmp_path)
    assert env["HTTP_PROXY"] == env["HTTPS_PROXY"] == env["ALL_PROXY"] == "http://127.0.0.1:7890"
    assert env["NO_PROXY"] == "127.0.0.1,localhost,::1"


def test_process_group_starts_hidden_and_kills_only_owned_children(tmp_path):
    calls = []

    class Process:
        def __init__(self):
            self.killed = False

        def kill(self):
            self.killed = True

        def poll(self):
            return 0 if self.killed else None

        def wait(self, timeout):
            return 0

    class Job:
        def __init__(self):
            self.assigned = []
            self.closed = False

        def assign(self, process):
            self.assigned.append(process)

        def close(self):
            self.closed = True

    job = Job()

    def popen(args, **kwargs):
        calls.append((args, kwargs))
        return Process()

    group = ProcessGroup(tmp_path, job=job, popen=popen)
    child = group.start("fixture", ["python", "-c", "pass"], cwd=tmp_path, env={})
    assert calls[0][1]["creationflags"] == getattr(subprocess, "CREATE_NO_WINDOW", 0)
    assert job.assigned == [child]
    group.close()
    assert job.closed and child.killed


@pytest.mark.skipif(os.name != "nt", reason="Windows Job Object lifecycle")
def test_real_windows_job_close_terminates_its_own_hidden_fixture_child():
    assert ctypes.sizeof(_ExtendedLimits) == 144
    job = WindowsJob()
    child = subprocess.Popen(
        [sys.executable, "-c", "import time; time.sleep(20)"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        creationflags=subprocess.CREATE_NO_WINDOW,
    )
    try:
        job.assign(child)
        assert child.poll() is None
        job.close()
        child.wait(timeout=5)
        assert child.poll() is not None
    finally:
        job.close()
        if child.poll() is None:
            child.kill()
            child.wait(timeout=5)


@pytest.mark.skipif(os.name != "nt", reason="Windows Mutex")
def test_mutex_excludes_another_thread_without_affecting_other_processes():
    from concurrent.futures import ThreadPoolExecutor

    name = "Local\\StockGodMinuteTest-" + str(os.getpid())
    first = WindowsMutex(name)
    try:
        with ThreadPoolExecutor(max_workers=1) as pool:
            future = pool.submit(WindowsMutex, name)
            with pytest.raises(RuntimeError):
                future.result()
    finally:
        first.close()


@pytest.mark.parametrize(
    ("api_state", "tunnel_fails"),
    [("slow", False), ("slow", True), ("timeout", False), ("exit", False), ("stop", False)],
)
def test_supervisor_publishes_only_registered_tunnel_and_cleans_owned_group(
    tmp_path, monkeypatch, api_state, tunnel_fails
):
    import stock_god.market.tunnel as module

    data = tmp_path / "data"
    data.mkdir()
    binary = tmp_path / "cloudflared.exe"
    binary.write_bytes(b"fixture")
    run = tmp_path / "runtime/minute-api"
    events = []
    elapsed = 0

    def sleep(seconds):
        nonlocal elapsed
        elapsed += seconds
        if api_state == "stop" and elapsed >= 5:
            (run / "stop.request").write_text("stop", encoding="utf-8")

    class Mutex:
        def close(self):
            events.append("mutex closed")

    class Process:
        def __init__(self, name):
            self.name = name
            self.returncode = 0 if name == "index" else None

        def poll(self):
            if self.name == "api" and api_state == "exit" and elapsed >= 5:
                return 1
            return 1 if self.name == "tunnel" and tunnel_fails else self.returncode

    class Group:
        def __init__(self, path):
            self.path = path

        def start(self, name, args, **kwargs):
            events.append(name)
            if name == "tunnel":
                (self.path / "tunnel.stderr.log").write_text(
                    "https://fixture.trycloudflare.com\nRegistered tunnel connection\n", encoding="utf-8"
                )
            return Process(name)

        def close(self):
            events.append("group closed")

    class LocalClient:
        def __init__(self, **kwargs):
            pass

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def get(self, url, **kwargs):
            if url == "http://127.0.0.1:18080/readyz":
                if elapsed < 50 or api_state == "timeout":
                    raise httpx.ConnectError("API still inventorying files")
            else:
                assert url == "http://127.0.0.1:17845/healthz"
            return httpx.Response(200, json={"ready": True})

        def post(self, url, **kwargs):
            assert url == "http://127.0.0.1:17845/prepare"
            return httpx.Response(204, request=httpx.Request("POST", url))

    original = module._check_processes

    def check(processes, stop):
        if (run / "mcp-url.txt").exists():
            assert (run / "mcp-url.txt").read_text(
                encoding="utf-8"
            ).strip() == "https://fixture.trycloudflare.com/mcp"
            events.append("published")
            stop.write_text("stop", encoding="utf-8")
        original(processes, stop)

    monkeypatch.setattr(module, "WindowsMutex", Mutex)
    monkeypatch.setattr(module, "ProcessGroup", Group)
    monkeypatch.setattr(module, "_assert_ports_available", lambda: None)
    monkeypatch.setattr(module.time, "monotonic", lambda: elapsed)
    monkeypatch.setattr(module.time, "sleep", sleep)
    monkeypatch.setattr(module, "_check_processes", check)
    monkeypatch.setattr(module.httpx, "Client", LocalClient)
    if tunnel_fails or api_state in ("timeout", "exit"):
        with pytest.raises(RuntimeError):
            module.supervise(tmp_path, data, tmp_path / "index", binary)
        assert "published" not in events
    else:
        assert module.supervise(tmp_path, data, tmp_path / "index", binary) == 0
        assert ("published" in events) == (api_state == "slow")
    if api_state == "slow":
        assert 50 <= elapsed < 51
        assert events[:4] == ["index", "api", "relay", "tunnel"]
    else:
        expected = 180 if api_state == "timeout" else 5
        assert expected <= elapsed < expected + 0.21
        assert events[:2] == ["index", "api"]
        assert "relay" not in events
    assert events[-2:] == ["group closed", "mutex closed"]
    assert not (run / "public-url.txt").exists() and not (run / "mcp-url.txt").exists()


def test_relay_readiness_keeps_twenty_second_default(tmp_path, monkeypatch):
    import stock_god.market.tunnel as module

    elapsed = 0

    def sleep(seconds):
        nonlocal elapsed
        elapsed += seconds

    monkeypatch.setattr(module.time, "monotonic", lambda: elapsed)
    monkeypatch.setattr(module.time, "sleep", sleep)
    transport = httpx.MockTransport(lambda request: httpx.Response(503))
    with httpx.Client(transport=transport) as client:
        with pytest.raises(RuntimeError, match="本机服务启动检查失败"):
            module._wait_http(client, "http://127.0.0.1:17845/healthz", [], tmp_path / "stop.request")
    assert 20 <= elapsed < 20.21
