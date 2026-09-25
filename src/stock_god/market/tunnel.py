"""Owned Windows process group and fail-closed Cloudflare proxy relay."""

import argparse
import ctypes
import json
import logging
import os
import re
import socket
import socketserver
import subprocess
import sys
import time
from ctypes import wintypes
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Lock, Thread

import httpx

from stock_god.config import AppConfig

PROXY = ("127.0.0.1", 7890)
EDGE_TARGET = "region1.v2.argotunnel.com:7844"
PROVISION_URL = "https://api.trycloudflare.com/tunnel"
MAX_PROVISION_BYTES = 1 << 20


def _shutdown(connection):
    try:
        connection.shutdown(socket.SHUT_RDWR)
    except OSError:
        pass


def forward(client, *, connect=socket.create_connection):
    """The only outbound TCP destination is the configured local proxy."""
    upstream = None
    reader = None
    worker = None
    try:
        upstream = connect(PROXY, timeout=10)
        upstream.settimeout(15)
        upstream.sendall(f"CONNECT {EDGE_TARGET} HTTP/1.1\r\nHost: {EDGE_TARGET}\r\n\r\n".encode("ascii"))
        reader = upstream.makefile("rb")
        line = reader.readline(8193)
        parts = line.split()
        if len(line) > 8192 or len(parts) < 2 or not parts[0].startswith(b"HTTP/") or parts[1] != b"200":
            raise OSError("proxy CONNECT failed")
        length = len(line)
        while True:
            line = reader.readline(8193)
            length += len(line)
            if not line or length > 65536 or len(line) > 8192:
                raise OSError("invalid proxy CONNECT response headers")
            if line in (b"\r\n", b"\n"):
                break
        upstream.settimeout(None)

        def upload():
            try:
                while chunk := client.recv(65536):
                    upstream.sendall(chunk)
            except OSError:
                pass
            finally:
                _shutdown(upstream)

        worker = Thread(target=upload, daemon=True)
        worker.start()
        while chunk := reader.read1(65536):
            client.sendall(chunk)
    finally:
        _shutdown(client)
        if upstream is not None:
            _shutdown(upstream)
        if worker is not None:
            worker.join(timeout=2)
        if reader is not None:
            reader.close()
        if upstream is not None:
            upstream.close()
        client.close()


class Provisioner:
    def __init__(self, client=None):
        self.client = client or httpx.Client(
            proxy="http://127.0.0.1:7890", trust_env=False, follow_redirects=False, timeout=60
        )
        self.owns_client = client is None
        self.lock = Lock()
        self.prepared = None

    def _fetch(self, body=b""):
        output = bytearray()
        try:
            with self.client.stream(
                "POST",
                PROVISION_URL,
                content=body,
                headers={"Content-Type": "application/json"},
                follow_redirects=False,
                timeout=60,
            ) as response:
                if response.status_code != 200:
                    raise RuntimeError("Tunnel preparation response invalid")
                for chunk in response.iter_bytes():
                    output.extend(chunk)
                    if len(output) > MAX_PROVISION_BYTES:
                        raise RuntimeError("Tunnel preparation response exceeds limit")
            json.loads(output)
        except (httpx.HTTPError, ValueError):
            raise RuntimeError("Tunnel preparation request failed") from None
        return bytes(output)

    def prepare(self):
        with self.lock:
            if self.prepared is None:
                self.prepared = self._fetch()

    def consume(self, body=b""):
        with self.lock:
            value = self.prepared
            self.prepared = None
        return value if value is not None else self._fetch(body)

    def close(self):
        self.prepared = None
        if self.owns_client:
            self.client.close()


def provisioning_handler(provisioner):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, format, *args):
            pass

        def do_GET(self):
            if self.path != "/healthz":
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"ready":true}')

        def do_POST(self):
            if self.path not in ("/prepare", "/tunnel"):
                self.send_error(404)
                return
            try:
                length = int(self.headers.get("Content-Length", "0"))
                if not 0 <= length <= MAX_PROVISION_BYTES:
                    raise ValueError
            except ValueError:
                self.send_error(400)
                return
            body = self.rfile.read(length)
            try:
                if self.path == "/prepare":
                    provisioner.prepare()
                    self.send_response(204)
                    self.end_headers()
                    return
                result = provisioner.consume(body)
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Cache-Control", "no-store")
                self.send_header("Content-Length", str(len(result)))
                self.end_headers()
                self.wfile.write(result)
            except RuntimeError:
                self.send_error(502, "Tunnel preparation failed")

    return Handler


def run_relay():
    provisioner = Provisioner()

    class RelayHandler(socketserver.BaseRequestHandler):
        def handle(self):
            try:
                forward(self.request)
            except OSError:
                logging.warning("Cloudflare relay connection failed through 127.0.0.1:7890")

    class RelayServer(socketserver.ThreadingTCPServer):
        allow_reuse_address = True
        daemon_threads = True

    http = ThreadingHTTPServer(("127.0.0.1", 17845), provisioning_handler(provisioner))
    http.daemon_threads = True
    try:
        with RelayServer(("127.0.0.1", 17844), RelayHandler) as relay:
            thread = Thread(target=http.serve_forever, daemon=True)
            thread.start()
            try:
                relay.serve_forever(poll_interval=0.2)
            except KeyboardInterrupt:
                pass
            finally:
                http.shutdown()
                thread.join(timeout=2)
    finally:
        http.server_close()
        provisioner.close()


class _BasicLimits(ctypes.Structure):
    _fields_ = [
        ("PerProcessUserTimeLimit", ctypes.c_int64),
        ("PerJobUserTimeLimit", ctypes.c_int64),
        ("LimitFlags", wintypes.DWORD),
        ("MinimumWorkingSetSize", ctypes.c_size_t),
        ("MaximumWorkingSetSize", ctypes.c_size_t),
        ("ActiveProcessLimit", wintypes.DWORD),
        ("Affinity", ctypes.c_size_t),
        ("PriorityClass", wintypes.DWORD),
        ("SchedulingClass", wintypes.DWORD),
    ]


class _IOCounters(ctypes.Structure):
    _fields_ = [
        (name, ctypes.c_uint64)
        for name in (
            "ReadOperationCount",
            "WriteOperationCount",
            "OtherOperationCount",
            "ReadTransferCount",
            "WriteTransferCount",
            "OtherTransferCount",
        )
    ]


class _ExtendedLimits(ctypes.Structure):
    _fields_ = [
        ("BasicLimitInformation", _BasicLimits),
        ("IoInfo", _IOCounters),
        ("ProcessMemoryLimit", ctypes.c_size_t),
        ("JobMemoryLimit", ctypes.c_size_t),
        ("PeakProcessMemoryUsed", ctypes.c_size_t),
        ("PeakJobMemoryUsed", ctypes.c_size_t),
    ]


def _kernel():
    if os.name != "nt":
        raise RuntimeError("此进程管理入口需要Windows")
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.CreateJobObjectW.argtypes = [ctypes.c_void_p, wintypes.LPCWSTR]
    kernel.CreateJobObjectW.restype = wintypes.HANDLE
    kernel.SetInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD]
    kernel.SetInformationJobObject.restype = wintypes.BOOL
    kernel.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
    kernel.AssignProcessToJobObject.restype = wintypes.BOOL
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.CloseHandle.restype = wintypes.BOOL
    kernel.CreateMutexW.argtypes = [ctypes.c_void_p, wintypes.BOOL, wintypes.LPCWSTR]
    kernel.CreateMutexW.restype = wintypes.HANDLE
    kernel.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
    kernel.WaitForSingleObject.restype = wintypes.DWORD
    kernel.ReleaseMutex.argtypes = [wintypes.HANDLE]
    kernel.ReleaseMutex.restype = wintypes.BOOL
    return kernel


class WindowsJob:
    def __init__(self):
        self.kernel = _kernel()
        self.handle = self.kernel.CreateJobObjectW(None, None)
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        limits = _ExtendedLimits()
        limits.BasicLimitInformation.LimitFlags = 0x2000
        if not self.kernel.SetInformationJobObject(
            self.handle, 9, ctypes.byref(limits), ctypes.sizeof(limits)
        ):
            error = ctypes.WinError(ctypes.get_last_error())
            self.close()
            raise error

    def assign(self, process):
        handle = getattr(process, "_handle", None)
        if handle is None or not self.kernel.AssignProcessToJobObject(self.handle, int(handle)):
            raise ctypes.WinError(ctypes.get_last_error())

    def close(self):
        if self.handle:
            self.kernel.CloseHandle(self.handle)
            self.handle = None


class WindowsMutex:
    def __init__(self, name="Local\\StockGodMinuteAPI"):
        self.kernel = _kernel()
        self.handle = self.kernel.CreateMutexW(None, False, name)
        self.acquired = False
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        status = self.kernel.WaitForSingleObject(self.handle, 0)
        if status not in (0, 0x80):
            self.close()
            raise RuntimeError("分钟API启动器已在运行")
        self.acquired = True

    def close(self):
        if self.handle:
            if self.acquired:
                self.kernel.ReleaseMutex(self.handle)
            self.kernel.CloseHandle(self.handle)
            self.handle = None
            self.acquired = False


class ProcessGroup:
    def __init__(self, run_dir, *, job=None, popen=subprocess.Popen):
        self.job = job or WindowsJob()
        self.processes = []
        self.run_dir = Path(run_dir)
        self.popen = popen

    def start(self, name, args, *, cwd, env):
        with (
            (self.run_dir / (name + ".stdout.log")).open("wb") as stdout,
            (self.run_dir / (name + ".stderr.log")).open("wb") as stderr,
        ):
            process = self.popen(
                args,
                cwd=str(cwd),
                env=env,
                stdout=stdout,
                stderr=stderr,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
            )
        self.processes.append(process)
        try:
            self.job.assign(process)
        except BaseException:
            process.kill()
            process.wait(timeout=5)
            raise
        return process

    def close(self):
        self.job.close()
        for process in reversed(self.processes):
            if process.poll() is None:
                process.kill()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                pass


def cloudflared_command(executable):
    return [
        str(executable),
        "tunnel",
        "--no-autoupdate",
        "--protocol",
        "http2",
        "--edge",
        "127.0.0.1:17844",
        "--quick-service",
        "http://127.0.0.1:17845",
        "--http-host-header",
        "127.0.0.1:18080",
        "--url",
        "http://127.0.0.1:18080",
    ]


def child_environment(root):
    env = os.environ.copy()
    env["STOCK_GOD_ROOT"] = str(root)
    for name in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
        env[name] = "http://127.0.0.1:7890"
    for name in ("NO_PROXY", "no_proxy"):
        env[name] = "127.0.0.1,localhost,::1"
    return env


def _assert_ports_available():
    for port in (18080, 17844, 17845):
        with socket.socket() as connection:
            try:
                connection.bind(("127.0.0.1", port))
            except OSError:
                raise RuntimeError(f"{port}已被占用，请先停止对应的旧服务") from None


def _check_processes(processes, stop_file):
    if stop_file.exists():
        raise InterruptedError("已请求停止分钟API和隧道")
    if any(process.poll() is not None for process in processes):
        raise RuntimeError("分钟API、隧道或代理转发已退出，请查看运行日志")


def _wait_http(client, url, processes, stop_file, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        _check_processes(processes, stop_file)
        try:
            response = client.get(url, timeout=2)
            if response.status_code == 200 and response.json().get("ready"):
                return
        except (httpx.HTTPError, ValueError):
            pass
        time.sleep(0.2)
    raise RuntimeError("本机服务启动检查失败：" + url)


def supervise(root, data_root, index_dir, cloudflared, private_config=None):
    root = Path(root).resolve()
    run_dir = root / "runtime/minute-api"
    run_dir.mkdir(parents=True, exist_ok=True)
    stop_file = run_dir / "stop.request"
    if not Path(cloudflared).is_file():
        raise FileNotFoundError("cloudflared不存在：" + str(cloudflared))
    if not Path(data_root).is_dir():
        raise FileNotFoundError("data-root不存在")
    mutex = WindowsMutex()
    group = None
    try:
        _assert_ports_available()
        for name in ("stop.request", "public-url.txt", "mcp-url.txt"):
            (run_dir / name).unlink(missing_ok=True)
        env = child_environment(root)
        command = [
            sys.executable,
            "-m",
            "stock_god.market.minute_app",
            "--data-root",
            str(Path(data_root).resolve()),
            "--index-dir",
            str(Path(index_dir).resolve()),
        ]
        if private_config:
            command += ["--diemeng-config", str(private_config)]
        group = ProcessGroup(run_dir)
        index = group.start("index", command + ["--prepare-data"], cwd=root, env=env)
        while index.poll() is None:
            if stop_file.exists():
                raise InterruptedError("索引准备已请求停止")
            time.sleep(0.2)
        if index.returncode:
            raise RuntimeError("索引准备失败；已完成文件保留，详见index.stderr.log")
        api = group.start("api", command, cwd=root, env=env)
        with httpx.Client(trust_env=False, follow_redirects=False) as local:
            _wait_http(local, "http://127.0.0.1:18080/readyz", [api], stop_file, timeout=180)
            relay = group.start(
                "relay", [sys.executable, "-m", "stock_god.market.tunnel", "--relay"], cwd=root, env=env
            )
            _wait_http(local, "http://127.0.0.1:17845/healthz", [api, relay], stop_file)
            prepared = local.post("http://127.0.0.1:17845/prepare", timeout=65)
            prepared.raise_for_status()
        tunnel = group.start("tunnel", cloudflared_command(cloudflared), cwd=root, env=env)
        processes = [api, relay, tunnel]
        deadline = time.monotonic() + 60
        url = None
        while time.monotonic() < deadline:
            _check_processes(processes, stop_file)
            log = (run_dir / "tunnel.stderr.log").read_text(encoding="utf-8", errors="replace")
            matches = re.findall(r"https://[a-z0-9-]+\.trycloudflare\.com", log, re.I)
            if matches and "registered tunnel connection" in log.lower():
                url = matches[-1]
                break
            time.sleep(0.5)
        if not url:
            raise RuntimeError("隧道未能连接，请查看tunnel.stderr.log")
        (run_dir / "public-url.txt").write_text(url + "\n", encoding="utf-8")
        (run_dir / "mcp-url.txt").write_text(url + "/mcp\n", encoding="utf-8")
        print("公网地址：" + url, flush=True)
        print("ChatGPT MCP地址：" + url + "/mcp", flush=True)
        print("MCP共54个工具；隧道重启后请更新连接地址。", flush=True)
        while True:
            _check_processes(processes, stop_file)
            time.sleep(0.5)
    except (InterruptedError, KeyboardInterrupt):
        return 0
    finally:
        if group is not None:
            group.close()
        for name in ("stop.request", "public-url.txt", "mcp-url.txt"):
            (run_dir / name).unlink(missing_ok=True)
        mutex.close()


def main(argv=None):
    parser = argparse.ArgumentParser(description="Stock God分钟API和Cloudflare隧道")
    parser.add_argument("--relay", action="store_true")
    parser.add_argument("--stop", action="store_true")
    parser.add_argument("--root", type=Path)
    parser.add_argument("--data-root", type=Path)
    parser.add_argument("--index-dir", type=Path)
    parser.add_argument("--diemeng-config", type=Path)
    parser.add_argument(
        "--cloudflared",
        type=Path,
        default=Path(r"H:\Program Files (x86)\Cloudflare\cloudflared-windows-amd64.exe"),
    )
    args = parser.parse_args(argv)
    if args.relay:
        run_relay()
        return 0
    config = AppConfig.from_env(args.root)
    if args.stop:
        run_dir = config.root / "runtime/minute-api"
        run_dir.mkdir(parents=True, exist_ok=True)
        (run_dir / "stop.request").write_text("stop\n", encoding="utf-8")
        print("已请求停止分钟API和隧道。")
        return 0
    return supervise(
        config.root,
        args.data_root or config.market_data_root,
        args.index_dir or config.market_index_dir,
        args.cloudflared,
        args.diemeng_config,
    )


if __name__ == "__main__":
    raise SystemExit(main())
