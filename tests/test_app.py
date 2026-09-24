import copy
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient

from stock_god.app import create_app
from stock_god.cli import process_lock
from stock_god.prediction.core import local
from stock_god.storage.db import Database


class OfflineMarket:
    def __init__(self):
        self.calls = []

    def with_settings(self, config):
        self.calls.append(copy.deepcopy(config))
        return self

    def close(self):
        pass

    def __getattr__(self, name):
        raise AssertionError("unexpected network operation: " + name)


class OfflineAI:
    def __init__(self, configs, force_config_id=None):
        self.configs = configs
        self.model_id = force_config_id

    async def complete(self, prompt):
        assert self.model_id == self.configs[0]["ID"]
        return SimpleNamespace(model="fixture", content="OK")


def client(config, **kwargs):
    app = create_app(
        config, market=OfflineMarket(), ai_factory=OfflineAI, clock=lambda: local("2026-09-24T09:50:10+08:00")
    )
    return TestClient(app, base_url="http://127.0.0.1:34115", client=("127.0.0.1", 45678), **kwargs)


def test_cold_start_ready_static_and_all_24_accounts_without_network(app_config):
    with client(app_config) as web:
        assert web.get("/livez").json() == {"ok": True}
        ready = web.get("/readyz")
        assert ready.status_code == 200
        assert ready.json()["appVersion"] == "6.0.0"
        assert all(ready.json()["readiness"].values())
        assert web.get("/api/v1/system/info").json()["version"] == "6.0.0"
        assert "Stock God" in web.get("/prediction").text
        assert web.get("/assets/missing.js").status_code == 404
        assert len(web.get("/api/v1/prediction/slots").json()) == 24
        for hour, minute in [(9 + value // 60, value % 60) for value in range(30, 150, 5)]:
            response = web.get("/api/v1/prediction/account", params={"slot": f"{hour:02}:{minute:02}"})
            assert response.status_code == 200, response.text
        assert len(web.app.state.market.calls) == 24  # Snapshots only; every provider operation fails above.
    assert not web.app.state.readiness["ready"]


@pytest.mark.parametrize(
    "headers",
    [
        {"Host": "evil.example"},
        {"Origin": "https://evil.example"},
        {"Origin": "http://localhost:34115"},
        {"Origin": "http://127.0.0.1:34115/"},
        {"Origin": "http://127.0.0.1:34115?x=1"},
        {"X-Forwarded-For": "127.0.0.1"},
        {"Forwarded": "for=127.0.0.1"},
    ],
)
def test_local_boundary_rejects_dns_rebinding_and_cross_origin(app_config, headers):
    with client(app_config) as web:
        assert web.get("/api/v1/prediction/settings", headers=headers).status_code == 403


def test_websocket_body_limit_and_retired_routes(app_config):
    with client(app_config) as web:
        with web.websocket_connect("ws://127.0.0.1:34115/api/v1/events/ws") as socket:
            assert socket.receive_json() == {"event": "loadingMsg", "payload": "done"}
        assert web.put("/api/v1/settings", content=b"x" * ((4 << 20) + 1)).status_code == 413
        for path in (
            "/api/v1/research2/slots",
            "/api/v1/research/analysis-runs",
            "/api/v1/knowledge/documents",
            "/api/v1/stocks/followed",
            "/market",
            "/research2",
            "/watchlist",
        ):
            assert web.get(path).status_code == 404, path
        invalid = web.get("/api/v1/prediction/analysis-runs?limit=-1")
        assert invalid.status_code == 400 and "error" in invalid.json()


def test_settings_cas_ai_test_and_retired_data_preserved(app_config):
    database = Database(app_config.main_db)
    with database.connection() as connection:
        retired = connection.execute(
            "SELECT config_json FROM research_settings WHERE center='research1'"
        ).fetchone()[0]
    with client(app_config) as web:
        source = web.get("/api/v1/prediction/settings").json()
        source["aiConfigs"] = [
            {
                "name": "fixture",
                "apiKey": "secret-fixture",
                "baseUrl": "http://fixture",
                "modelName": "fixture",
                "apiProtocol": "chat_completions",
            }
        ]
        saved = web.put("/api/v1/prediction/settings", json=source)
        assert saved.status_code == 200, saved.text
        assert saved.json()["revision"] == source["revision"] + 1
        assert web.put("/api/v1/prediction/settings", json=source).status_code == 409
        model = saved.json()["aiConfigs"][0]
        assert web.post("/api/v1/prediction/ai/configs/test", json={"id": model["ID"]}).json()["success"]
        assert web.post("/api/v1/prediction/ai/configs/test", json={"id": 99999}).status_code == 400
        assert web.put("/api/v1/settings", json={"research2AutoEnabled": False}).status_code == 400
        assert web.put("/api/v1/settings", json={"darkTheme": True}).status_code == 200
        assert web.get("/api/v1/settings").json()["darkTheme"] is True
    with database.connection() as connection:
        assert (
            retired
            == connection.execute(
                "SELECT config_json FROM research_settings WHERE center='research1'"
            ).fetchone()[0]
        )


def test_process_lock_rejects_second_owner_and_releases(tmp_path):
    target = tmp_path / "runtime/web.lock"
    with process_lock(target):
        with pytest.raises((RuntimeError, OSError)), process_lock(target):
            pass
    with process_lock(target):
        pass


def test_fresh_settings_initialize_without_rewriting_existing_settings(app_config):
    database = Database(app_config.main_db)
    with database.transaction() as connection:
        connection.execute("DELETE FROM settings")
    with client(app_config) as web:
        assert web.get("/api/v1/settings").json()["refreshInterval"] == 1
        assert web.put("/api/v1/settings", json={"refreshInterval": 19}).status_code == 200
    with client(app_config) as web:
        assert web.get("/api/v1/settings").json()["refreshInterval"] == 19
