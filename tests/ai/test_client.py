import asyncio
import json

import httpx
import pytest

from stock_god.ai import AIClient, ProviderError


def config(**overrides):
    return {
        "ID": 1,
        "name": "fixture",
        "baseUrl": "https://provider.invalid/v1",
        "apiKey": "secret-key",
        "modelName": "fixture-model",
        "apiProtocol": "chat_completions",
        "maxTokens": 64,
        "timeOut": 1,
        **overrides,
    }


@pytest.mark.parametrize(
    "protocol,path,events,ident",
    [
        (
            "chat_completions",
            "/v1/chat/completions",
            [
                {"id": "chat", "choices": [{"delta": {"reasoning_content": "reason"}}]},
                {"id": "chat", "choices": [{"delta": {"content": "OK"}, "finish_reason": "stop"}]},
            ],
            "chat",
        ),
        (
            "openai_responses",
            "/v1/responses",
            [
                {"type": "response.created", "response": {"id": "resp"}},
                {"type": "response.output_text.delta", "delta": "OK"},
                {"type": "response.completed", "response": {"id": "resp"}},
            ],
            "resp",
        ),
        (
            "anthropic_messages",
            "/v1/messages",
            [
                {"type": "message_start", "message": {"id": "msg"}},
                {"type": "content_block_delta", "delta": {"type": "thinking_delta", "thinking": "reason"}},
                {"type": "content_block_delta", "delta": {"type": "text_delta", "text": "OK"}},
                {"type": "message_stop"},
            ],
            "msg",
        ),
    ],
)
async def test_protocols_preserve_provider_defaults_and_activity(protocol, path, events, ident):
    def handler(request):
        assert request.url.path == path
        body = json.loads(request.content)
        assert body["stream"] is True
        assert "temperature" not in body
        assert "max_output_tokens" not in body
        if protocol == "anthropic_messages":
            assert body["max_tokens"] == 64
            assert request.headers["x-api-key"] == "secret-key"
        else:
            assert "max_tokens" not in body
            assert request.headers["authorization"] == "Bearer secret-key"
        if protocol == "openai_responses":
            assert body["previous_response_id"] == "previous"
            assert body["instructions"] == "system"
        stream = ": heartbeat\n\n" + "".join("data: " + json.dumps(event) + "\n\n" for event in events)
        return httpx.Response(200, text=stream, headers={"content-type": "text/event-stream"})

    records = []
    client = AIClient([config(apiProtocol=protocol)], transport=httpx.MockTransport(handler))
    result = await client.complete(
        messages=[{"role": "system", "content": "system"}, {"role": "user", "content": "ping"}],
        previous_response_id="previous",
        on_attempt=records.append,
    )
    assert (result.content, result.response_id, result.model) == ("OK", ident, "fixture-model")
    assert any(item["status"] == "reasoning" for item in records)
    assert records[-1]["status"] == "success"
    assert "secret-key" not in json.dumps(records)


async def test_retry_fallback_order_and_immutable_snapshot():
    calls, delays = [], []

    def handler(request):
        model = json.loads(request.content)["model"]
        calls.append(model)
        if model == "first":
            return httpx.Response(503, json={"error": {"message": "secret-key busy"}})
        return httpx.Response(
            200, text='data: {"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}\n\n'
        )

    async def wait(delay):
        delays.append(delay)

    configs = [
        config(modelName="first"),
        config(ID=2, modelName="disabled", disabled=True),
        config(ID=3, modelName="last"),
    ]
    client = AIClient(configs, transport=httpx.MockTransport(handler), retry_wait=wait)
    configs[0]["modelName"] = "mutated"
    result = await client.complete(prompt="ping")
    assert calls == ["first"] * 5 + ["last"]
    assert delays == [1, 2, 4, 8, 8]
    assert len(result.attempts) == 6
    assert result.attempts[4]["nextAction"] == "fallback_next_model"
    assert "secret-key" not in json.dumps(result.attempts)


async def test_permanent_error_falls_back_without_retry_or_cooldown():
    calls = []

    def handler(request):
        calls.append(json.loads(request.content)["model"])
        if len(calls) == 1:
            return httpx.Response(401, json={"error": {"message": "bad key"}})
        return httpx.Response(
            200, text='data: {"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}\n\n'
        )

    result = await AIClient(
        [config(modelName="one"), config(ID=2, modelName="two")], transport=httpx.MockTransport(handler)
    ).complete(prompt="ping")
    assert calls == ["one", "two"]
    assert result.attempts[0]["retryable"] is False


@pytest.mark.parametrize(
    "stream,category",
    [
        ('data: {"choices":[{"delta":{"content":"partial"}}]}\n\n', "stream_interrupted"),
        ('data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\n', "empty_output"),
        ("data: not-json\n\n", "protocol_error"),
    ],
)
async def test_incomplete_empty_and_malformed_streams_fail(stream, category):
    transport = httpx.MockTransport(lambda _: httpx.Response(200, text=stream))
    records = []
    with pytest.raises(ProviderError):
        await AIClient([config()], transport=transport, max_attempts=1).complete(on_attempt=records.append)
    assert records[-1]["errorCategory"] == category


async def test_forced_replay_model_does_not_fall_back():
    calls = []

    def handler(request):
        calls.append(json.loads(request.content)["model"])
        return httpx.Response(400, json={"error": {"message": "unsupported"}})

    with pytest.raises(ProviderError):
        await AIClient(
            [config(modelName="one"), config(ID=2, modelName="two")],
            force_config_id=2,
            transport=httpx.MockTransport(handler),
        ).complete(prompt="ping")
    assert calls == ["two"]


async def test_idle_watchdog_does_not_treat_unfinished_data_lines_as_activity():
    class SlowStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b"data: "
            await asyncio.sleep(0.05)
            yield b'{"choices":[]}\n\n'

    transport = httpx.MockTransport(lambda _: httpx.Response(200, stream=SlowStream()))
    records = []
    with pytest.raises(ProviderError):
        await AIClient([config()], transport=transport, idle_timeout=0.01, max_attempts=1).complete(
            on_attempt=records.append
        )
    assert records[-1]["errorCategory"] == "idle_timeout"
