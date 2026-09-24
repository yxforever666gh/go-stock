"""Loopback HTTP boundary shared by API, static assets and WebSocket."""

import ipaddress
from urllib.parse import urlsplit

from starlette.responses import JSONResponse


def loopback(value: str) -> bool:
    if value.lower() == "localhost":
        return True
    try:
        return ipaddress.ip_address(value).is_loopback
    except ValueError:
        return False


def authority(value: str, scheme: str):
    try:
        parsed = urlsplit(scheme + "://" + value)
        if parsed.username or parsed.password or parsed.path or parsed.query or parsed.fragment:
            return None
        host = parsed.hostname or ""
        if not loopback(host):
            return None
        return host.lower(), parsed.port or (443 if scheme == "https" else 80)
    except ValueError:
        return None


def allowed(scope) -> bool:
    headers = {key.decode("latin1").lower(): value.decode("latin1") for key, value in scope["headers"]}
    if any(
        headers.get(key, "").strip()
        for key in ("forwarded", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto")
    ):
        return False
    client = scope.get("client")
    if not client or not loopback(client[0]):
        return False
    scheme = "https" if scope.get("scheme") in {"https", "wss"} else "http"
    host = authority(headers.get("host", ""), scheme)
    if host is None:
        return False
    origin = headers.get("origin", "").strip()
    if not origin:
        return True
    try:
        parsed = urlsplit(origin)
        if parsed.scheme not in {"http", "https"} or parsed.path or parsed.query or parsed.fragment:
            return False
        return authority(parsed.netloc, parsed.scheme) == host
    except ValueError:
        return False


class LocalBoundary:
    def __init__(self, app, max_body=4 << 20):
        self.app, self.max_body = app, max_body

    async def __call__(self, scope, receive, send):
        if scope["type"] not in {"http", "websocket"}:
            return await self.app(scope, receive, send)
        if not allowed(scope):
            if scope["type"] == "websocket":
                return await send({"type": "websocket.close", "code": 1008})
            return await JSONResponse({"error": "local requests only"}, 403)(scope, receive, send)
        if scope["type"] == "websocket":
            return await self.app(scope, receive, send)
        # Buffer at most the old API limit before dispatch. This also bounds chunked bodies.
        chunks, size = [], 0
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            size += len(message.get("body", b""))
            if size > self.max_body:
                return await JSONResponse({"error": "request body exceeds 4 MiB"}, 413)(scope, receive, send)
            chunks.append(message.get("body", b""))
            if not message.get("more_body"):
                break
        consumed = False

        async def buffered():
            nonlocal consumed
            if consumed:
                return await receive()
            consumed = True
            return {"type": "http.request", "body": b"".join(chunks), "more_body": False}

        await self.app(scope, buffered, send)
