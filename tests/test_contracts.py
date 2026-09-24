import copy
import hashlib
import json
import re
from dataclasses import replace
from pathlib import Path

import pytest
from fastapi import FastAPI, WebSocket

from stock_god.config import AppConfig
from stock_god.contracts import (
    ContractError,
    apply_operation_ids,
    check_generated,
    generate_typescript,
    load_spec,
    operations,
    prune_components,
    reachable_components,
    read_spec,
    ts_type,
    validate_app_routes,
    validate_document,
)

ROOT = Path(__file__).resolve().parents[1]


def digest(value):
    encoded = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def small_spec():
    return {
        "openapi": "3.1.0",
        "info": {"title": "fixture", "version": "2.0.0"},
        "paths": {
            "/livez": {
                "get": {
                    "operationId": "getLiveness",
                    "responses": {
                        "200": {
                            "description": "ok",
                            "content": {
                                "application/json": {"schema": {"$ref": "#/components/schemas/Status"}}
                            },
                        }
                    },
                }
            }
        },
        "components": {
            "schemas": {
                "Status": {
                    "type": "object",
                    "required": ["ok"],
                    "properties": {"message": {"type": "string"}, "ok": {"type": "boolean"}},
                }
            }
        },
    }


def test_market_paths_parameters_responses_and_transitive_types_are_unchanged():
    spec = load_spec(ROOT)
    baseline = json.loads((ROOT / "tests/fixtures/retained_contract_market.json").read_text(encoding="utf-8"))
    for path, expected in baseline["paths"].items():
        assert digest(spec["paths"][path]) == expected, path
    for category, values in baseline["components"].items():
        for name, expected in values.items():
            assert digest(spec["components"][category][name]) == expected, category + "." + name


def test_canonical_prediction_names_preserve_database_identity_and_remove_retired_contracts():
    spec = load_spec(ROOT)
    assert spec["info"]["version"] == "2.0.0"
    for path in spec["paths"]:
        assert not re.search(
            r"/api/v1/(research(?:2|-centers)?|watchlist|groups|knowledge|exports)(?:/|$)", path
        )
    schemas = spec["components"]["schemas"]
    assert not any(name.startswith(("Research", "Knowledge", "CreateKnowledge")) for name in schemas)
    assert schemas["PredictionAuditDetail"]["properties"]["ownerType"]["enum"] == ["research2", "replay"]
    assert "sourceOwnerType" not in schemas["CreatePredictionReplayRequest"]["properties"]
    assert spec["paths"]["/api/v1/events/ws"]["get"]["x-websocket"] is True
    assert set(schemas) == reachable_components(spec)["schemas"]
    check_generated(spec, ROOT / "frontend/src/services/api-types.generated.ts")


def test_frontend_imports_and_all_api_path_members_exist_after_recursive_pruning():
    spec = load_spec(ROOT)
    ids = {operation["operationId"] for _, _, operation in operations(spec)}
    names = set(spec["components"]["schemas"]) | {"API_PATHS"}
    for path in (ROOT / "frontend/src").rglob("*"):
        if path.suffix not in (".ts", ".js", ".mjs", ".vue") or path.name == "api-types.generated.ts":
            continue
        source = path.read_text(encoding="utf-8")
        for name in re.findall(r"API_PATHS\.([A-Za-z0-9_]+)", source):
            assert name in ids, f"{path}: {name}"
        for group in re.findall(
            r"import\s+(?:type\s+)?\{([^}]+)\}\s+from\s+['\"][^'\"]*api-types.generated['\"]", source
        ):
            for imported in group.split(","):
                name = imported.strip().split(" as ")[0]
                if name:
                    assert name in names, f"{path}: {name}"


def test_type_generation_is_deterministic_and_preserves_required_null_and_intersection_shapes():
    spec = small_spec()
    first = generate_typescript(spec)
    second = generate_typescript(copy.deepcopy(spec))
    assert first == second
    assert "message?: string" in first and "ok: boolean" in first
    assert ts_type({"oneOf": [{"$ref": "#/components/schemas/Status"}, {"type": "null"}]}) == "Status | null"
    assert (
        ts_type({"type": "array", "items": {"type": "integer", "nullable": True}}) == "Array<number | null>"
    )
    assert (
        ts_type({"allOf": [{"$ref": "#/components/schemas/A"}, {"$ref": "#/components/schemas/B"}]})
        == "A & B"
    )
    assert ts_type({"enum": ["open", "closed"]}) == '"open" | "closed"'
    assert ts_type({"type": "object"}) == "unknown"
    assert ts_type({"const": False}) == "false"


def test_recursive_pruning_follows_responses_and_removes_dead_schema_cycles():
    spec = small_spec()
    spec["components"]["responses"] = {"Ready": spec["paths"]["/livez"]["get"]["responses"]["200"]}
    spec["paths"]["/livez"]["get"]["responses"]["200"] = {"$ref": "#/components/responses/Ready"}
    schemas = spec["components"]["schemas"]
    schemas["Status"]["properties"]["child"] = {"$ref": "#/components/schemas/Child"}
    schemas["Child"] = {"type": "string"}
    schemas["DeadA"] = {"$ref": "#/components/schemas/DeadB"}
    schemas["DeadB"] = {"$ref": "#/components/schemas/DeadA"}
    trimmed = prune_components(spec)
    assert set(trimmed["components"]["schemas"]) == {"Status", "Child"}
    assert set(trimmed["components"]["responses"]) == {"Ready"}
    validate_document(trimmed)
    assert "DeadA" in spec["components"]["schemas"]  # The pruning function does not mutate its input.


@pytest.mark.parametrize("broken", ["reference", "external", "duplicate-id", "unused"])
def test_contract_rejects_dangling_references_and_ambiguous_operations(broken):
    spec = small_spec()
    if broken == "reference":
        spec["components"]["schemas"]["Status"]["properties"]["bad"] = {
            "$ref": "#/components/schemas/Missing"
        }
    elif broken == "external":
        spec["components"]["schemas"]["Status"]["properties"]["bad"] = {
            "$ref": "https://example.invalid/schema"
        }
    elif broken == "duplicate-id":
        spec["paths"]["/readyz"] = copy.deepcopy(spec["paths"]["/livez"])
    else:
        spec["components"]["schemas"]["Unused"] = {"type": "string"}
    with pytest.raises(ContractError):
        validate_document(spec)


def test_duplicate_yaml_keys_and_stale_generated_output_are_rejected(tmp_path):
    duplicate = tmp_path / "duplicate.yaml"
    duplicate.write_text("openapi: 3.1.0\nopenapi: 3.1.1\n", encoding="utf-8")
    with pytest.raises(ContractError, match="duplicate"):
        read_spec(duplicate)
    generated = tmp_path / "generated.ts"
    generated.write_text("stale", encoding="utf-8")
    with pytest.raises(ContractError, match="stale"):
        check_generated(small_spec(), generated)


def test_websocket_is_not_an_http_get_and_route_drift_is_rejected():
    spec = small_spec()
    spec["paths"]["/api/v1/events/ws"] = {
        "get": {
            "operationId": "connectEventsWebSocket",
            "x-websocket": True,
            "responses": {"101": {"description": "upgrade"}},
        }
    }
    app = FastAPI()

    @app.get("/livez")
    def liveness():
        return {"ok": True}

    @app.websocket("/api/v1/events/ws")
    async def events(socket: WebSocket):
        await socket.close()

    routes = validate_app_routes(app, spec)
    assert ("GET", "/api/v1/events/ws") not in routes
    apply_operation_ids(app, spec)
    assert routes[("GET", "/livez")].operation_id == "getLiveness"
    app.add_api_route("/api/v1/undocumented", liveness, methods=["POST"])
    with pytest.raises(ContractError, match="undocumented"):
        validate_app_routes(app, spec)


def test_actual_fastapi_assembly_matches_canonical_http_and_websocket_routes(tmp_path, monkeypatch):
    from stock_god.app import create_app

    monkeypatch.delenv("STOCK_GOD_RELEASE_DIR", raising=False)
    config = replace(AppConfig.from_env(tmp_path), scheduler_enabled=False)
    app = create_app(config, market=object())
    spec = load_spec(ROOT)
    actual = validate_app_routes(app, spec)
    apply_operation_ids(app, spec)
    for method, path, operation in operations(spec):
        if not operation.get("x-websocket"):
            assert actual[(method, path)].operation_id == operation["operationId"]
    assert not config.main_db.exists() and not config.minute_db.exists()
    app.state.prediction.database.close()
