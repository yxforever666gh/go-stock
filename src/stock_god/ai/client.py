"""Streaming provider calls with task-local configuration and audited fallback."""

import asyncio
import copy
import json
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any

import httpx


class ProviderError(Exception):
    def __init__(
        self, category: str, message: str, *, status: int = 0, retryable: bool = False, last_event: str = ""
    ):
        super().__init__(message)
        self.category, self.status, self.retryable, self.last_event = category, status, retryable, last_event


@dataclass(frozen=True)
class Completion:
    content: str
    response_id: str
    model: str
    provider_name: str
    attempts: list[dict[str, Any]]


def _now() -> str:
    return datetime.now(UTC).isoformat()


def _protocol(config: dict) -> str:
    protocol = str(config.get("apiProtocol", "")).strip().lower()
    return protocol if protocol in {"openai_responses", "anthropic_messages"} else "chat_completions"


def _redact(value: str, config: dict) -> str:
    for key in ("apiKey", "baseUrl", "httpProxy"):
        secret = str(config.get(key, "")).strip()
        if secret:
            value = value.replace(secret, "[REDACTED]")
    return " ".join(value.split())[:2048] or "模型服务返回了空错误"


def _error_message(payload: Any) -> str:
    if isinstance(payload, str):
        return payload
    if not isinstance(payload, dict):
        return "model provider reported a failed stream"
    for key in ("error", "response", "incomplete_details"):
        child = payload.get(key)
        if isinstance(child, dict):
            message = _error_message(child)
            if message != "model provider reported a failed stream":
                return message
    return str(
        payload.get("message")
        or payload.get("reason")
        or payload.get("code")
        or "model provider reported a failed stream"
    )


def _body(config: dict, messages: list[dict], previous_id: str) -> tuple[str, dict, dict]:
    protocol = _protocol(config)
    headers = {"Content-Type": "application/json"}
    model = config.get("modelName", "")
    if protocol == "chat_completions":
        headers["Authorization"] = "Bearer " + str(config.get("apiKey", ""))
        return "/chat/completions", {"model": model, "stream": True, "messages": messages}, headers
    system: list[str] = []
    dialog: list[dict] = []
    for item in messages:
        role, content = item.get("role"), str(item.get("content", ""))
        if role == "system":
            system.append(content)
        elif content.strip():
            role = "assistant" if role == "assistant" else "user"
            if dialog and dialog[-1]["role"] == role:
                dialog[-1]["content"] = (dialog[-1]["content"].strip() + "\n\n" + content.strip()).strip()
            else:
                dialog.append({"role": role, "content": content})
    if protocol == "openai_responses":
        headers["Authorization"] = "Bearer " + str(config.get("apiKey", ""))
        body: dict[str, Any] = {"model": model, "stream": True, "input": dialog}
        if system:
            body["instructions"] = "\n\n".join(system)
        if previous_id.strip():
            body["previous_response_id"] = previous_id.strip()
        return "/responses", body, headers
    headers.update({"x-api-key": str(config.get("apiKey", "")), "anthropic-version": "2023-06-01"})
    body = {"model": model, "max_tokens": config.get("maxTokens", 0), "stream": True, "messages": dialog}
    if system:
        body["system"] = "\n\n".join(system)
    return "/messages", body, headers


async def _frames(response: httpx.Response, idle: float):
    lines = response.aiter_lines().__aiter__()
    event, data = "", []
    deadline = asyncio.get_running_loop().time() + idle
    while True:
        try:
            async with asyncio.timeout_at(deadline):
                line = await anext(lines)
        except StopAsyncIteration:
            if data:
                yield event, "\n".join(data)
            return
        except TimeoutError as exc:
            raise ProviderError(
                "idle_timeout", "model stream had no activity before timeout", retryable=True
            ) from exc
        if line.startswith(":"):
            yield "heartbeat", ""
            deadline = asyncio.get_running_loop().time() + idle
        elif not line:
            if data:
                yield event, "\n".join(data)
                deadline = asyncio.get_running_loop().time() + idle
            event, data = "", []
        elif line.startswith("event:"):
            event = line[6:].strip()
        elif line.startswith("data:"):
            data.append(line[5:].lstrip(" "))


class AIClient:
    def __init__(
        self,
        configs: list[dict],
        force_config_id: int | None = None,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        idle_timeout: float = 300,
        max_attempts: int = 5,
        retry_wait: Callable = asyncio.sleep,
    ):
        self.configs = copy.deepcopy(configs)
        self.force_config_id = force_config_id
        self.transport, self.idle_timeout = transport, idle_timeout
        self.max_attempts, self.retry_wait = max_attempts, retry_wait

    async def complete(
        self,
        prompt: str = "",
        messages: list[dict] | None = None,
        *,
        phase: str = "prediction",
        previous_response_id: str = "",
        on_attempt: Callable[[dict], None] | None = None,
    ) -> Completion:
        messages = copy.deepcopy(messages) if messages else [{"role": "user", "content": prompt}]
        for message in messages:
            if message.get("role") not in {"assistant", "system"}:
                message["role"] = "user"
        configs, seen = [], set()
        for config in self.configs:
            ident = int(config.get("ID", config.get("id", 0)))
            if config.get("disabled") or ident in seen:
                continue
            if self.force_config_id is not None and ident != self.force_config_id:
                continue
            seen.add(ident)
            configs.append(config)
        if not configs:
            raise ProviderError("configuration", "没有已启用的 AI 模型")
        attempts: list[dict[str, Any]] = []
        errors: list[str] = []
        for fallback, config in enumerate(configs, 1):
            label = str(config.get("name") or config.get("modelName", ""))
            last_error: ProviderError | None = None
            for number in range(1, self.max_attempts + 1):
                started = time.monotonic()
                record: dict[str, Any] = {
                    "id": f"{phase}-{config.get('ID', config.get('id', 0))}-{number}-{time.time_ns()}",
                    "phase": phase,
                    "configId": config.get("ID", config.get("id", 0)),
                    "providerName": label,
                    "modelName": config.get("modelName", ""),
                    "apiProtocol": _protocol(config),
                    "requestTimeoutSeconds": config.get("timeOut") or 300,
                    "inactivityTimeoutSeconds": self.idle_timeout,
                    "fallbackIndex": fallback,
                    "fallbackCount": len(configs),
                    "forcedConfig": self.force_config_id is not None,
                    "previousResponseIdPresent": bool(previous_response_id.strip()),
                    "attempt": number,
                    "maxAttempts": self.max_attempts,
                    "startedAt": _now(),
                    "status": "waiting_response",
                }
                if _protocol(config) == "anthropic_messages":
                    record["maxTokens"] = config.get("maxTokens", 0)
                last_emit = started

                def emit(record=record):
                    if on_attempt:
                        on_attempt(copy.deepcopy(record))

                def activity(event: str, state: str, record=record, started=started, emit=emit):
                    nonlocal last_emit
                    changed = record["status"] != state
                    record.update(lastActivityAt=_now(), lastEventType=event, status=state)
                    if changed or time.monotonic() - last_emit >= 5:
                        record["durationMs"] = int((time.monotonic() - started) * 1000)
                        emit()
                        last_emit = time.monotonic()

                emit()
                try:
                    content, ident, model = await self._provider(
                        config, messages, previous_response_id, activity
                    )
                except asyncio.CancelledError:
                    record.update(
                        status="cancelled",
                        completedAt=_now(),
                        nextAction="stop",
                        errorCategory="cancelled",
                        errorMessage="cancelled",
                    )
                    emit()
                    raise
                except (ProviderError, httpx.HTTPError) as exc:
                    last_error = (
                        exc
                        if isinstance(exc, ProviderError)
                        else ProviderError("network_error", str(exc), retryable=True)
                    )
                    message = _redact(str(last_error), config)
                    retry = last_error.retryable and number < self.max_attempts
                    record.update(
                        status="failed",
                        completedAt=_now(),
                        durationMs=int((time.monotonic() - started) * 1000),
                        httpStatus=last_error.status,
                        errorCategory=last_error.category,
                        errorMessage=message,
                        retryable=last_error.retryable,
                        nextAction="retry_same_model"
                        if retry
                        else ("fallback_next_model" if fallback < len(configs) else "stop"),
                    )
                    if not record.get("lastEventType"):
                        record["lastEventType"] = last_error.last_event
                    attempts.append(copy.deepcopy(record))
                    emit()
                    if retry:
                        await self.retry_wait(min(2 ** (number - 1), 8))
                        continue
                    errors.append(f"{label}/{config.get('modelName', '')}: {message}")
                    break
                else:
                    record.update(
                        status="success",
                        completedAt=_now(),
                        nextAction="complete",
                        durationMs=int((time.monotonic() - started) * 1000),
                    )
                    attempts.append(copy.deepcopy(record))
                    emit()
                    return Completion(content, ident, model, label, attempts)
            if fallback < len(configs) and last_error and last_error.retryable:
                base = str(config.get("baseUrl", "")).strip().rstrip("/").lower()
                next_base = str(configs[fallback].get("baseUrl", "")).strip().rstrip("/").lower()
                if base and base == next_base:
                    await self.retry_wait(8)
        raise ProviderError("all_models_failed", "所有已启用模型均调用失败: " + "; ".join(errors))

    async def _provider(self, config, messages, previous_id, activity):
        path, body, headers = _body(config, messages, previous_id)
        proxy = config.get("httpProxy") if config.get("httpProxyEnabled") else None
        timeout = float(config.get("timeOut") or 300)
        try:
            return await self._request(config, path, body, headers, proxy, timeout, activity)
        except httpx.ConnectError as exc:
            # Preserve the existing explicit-proxy connection-refused fallback.
            refused = "refused" in str(exc).lower() or "10061" in str(exc)
            if not proxy or not refused:
                raise
            return await self._request(config, path, body, headers, None, timeout, activity)

    async def _request(self, config, path, body, headers, proxy, timeout, activity):
        # Bound the response-header wait as well as gaps between valid SSE frames.
        client_timeout = httpx.Timeout(timeout, read=self.idle_timeout)
        async with httpx.AsyncClient(
            transport=self.transport, proxy=proxy, trust_env=False, timeout=client_timeout
        ) as client:
            url = str(config.get("baseUrl", "")).strip().rstrip("/") + path
            async with client.stream("POST", url, json=body, headers=headers) as response:
                if response.is_error:
                    raw = (await response.aread()).decode("utf-8", "replace")
                    try:
                        message = _error_message(json.loads(raw))
                    except ValueError:
                        message = raw[:2048]
                    raise ProviderError(
                        "http_error",
                        message,
                        status=response.status_code,
                        retryable=response.status_code in {408, 429, 500, 502, 503, 504},
                    )
                activity("response_headers", "waiting")
                return await self._consume(response, _protocol(config), config.get("modelName", ""), activity)

    async def _consume(self, response, protocol, model, activity):
        content, ident, terminal, last_event = [], "", False, "response_headers"
        async for frame_type, raw in _frames(response, self.idle_timeout):
            if frame_type == "heartbeat":
                last_event = "heartbeat"
                activity(last_event, "reasoning")
                continue
            if raw.strip() == "[DONE]":
                terminal, last_event = True, "done"
                activity(last_event, "streaming")
                continue
            try:
                event = json.loads(raw)
                if not isinstance(event, dict):
                    raise TypeError("stream event must be an object")
            except (TypeError, ValueError) as exc:
                raise ProviderError("protocol_error", "流事件不是有效 JSON", last_event=last_event) from exc
            if event.get("error"):
                raise ProviderError("stream_error", _error_message(event), last_event=last_event)
            if protocol == "chat_completions":
                ident, model = event.get("id") or ident, event.get("model") or model
                last_event, state = "chat.completion.chunk", "streaming"
                for choice in event.get("choices", []):
                    delta = choice.get("delta") or {}
                    content.append(delta.get("content") or "")
                    if delta.get("reasoning_content"):
                        state = "reasoning"
                    if choice.get("finish_reason"):
                        terminal, last_event = True, "finish_reason:" + choice["finish_reason"]
                activity(last_event, state)
                continue
            last_event = event.get("type") or frame_type
            if not last_event:
                raise ProviderError("protocol_error", "流事件缺少 type")
            nested = event.get("response" if protocol == "openai_responses" else "message") or {}
            ident, model = nested.get("id") or ident, nested.get("model") or model
            delta = event.get("delta") or {}
            reasoning = (
                "reasoning" in last_event
                or last_event in {"ping", "response.created", "response.in_progress"}
                or isinstance(delta, dict)
                and (delta.get("thinking") or "thinking" in delta.get("type", ""))
            )
            activity(last_event, "reasoning" if reasoning else "streaming")
            if last_event in {"response.failed", "response.incomplete", "error"}:
                raise ProviderError("stream_error", _error_message(event), last_event=last_event)
            if protocol == "openai_responses":
                if last_event == "response.output_text.delta":
                    content.append(str(delta) if isinstance(delta, str) else "")
                if last_event == "response.completed":
                    terminal = True
                    if not "".join(content):
                        text = nested.get("output_text") or "\n".join(
                            block.get("text", "")
                            for item in nested.get("output", [])
                            for block in item.get("content", [])
                            if block.get("text")
                        )
                        content.append(text)
            else:
                if last_event == "content_block_delta":
                    content.append(delta.get("text") or "")
                if last_event == "message_stop":
                    terminal = True
        if not terminal:
            raise ProviderError(
                "stream_interrupted", "模型流在完成事件前中断", retryable=True, last_event=last_event
            )
        result = "".join(content).strip()
        if not result:
            raise ProviderError("empty_output", "模型流已完成但没有正文", last_event=last_event)
        return result, ident, model
