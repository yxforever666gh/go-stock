"""Executable ownership rules for the smaller, Python-only product."""

import ast
import json
import re
import subprocess
import tomllib
from pathlib import Path

import pytest

from stock_god import APP_VERSION, __version__
from stock_god.contracts import load_spec
from stock_god.storage.migrations import FINAL_VERSION

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "src/stock_god"
FORBIDDEN = {
    "prediction": ("app", "cli", "runtime", "market", "storage.historical", "storage.migrations"),
    "market": ("app", "cli", "runtime", "prediction"),
    "storage": ("app", "cli", "runtime", "prediction", "market", "ai", "settings", "audit"),
    "ai": ("app", "cli", "runtime", "prediction", "market", "storage.historical"),
    "config": ("app", "cli", "runtime", "prediction", "market", "ai", "storage", "settings", "audit"),
    "jsonutil": ("app", "cli", "runtime", "prediction", "market", "ai", "storage", "settings", "audit"),
}


def imported_modules(tree, module, package=False):
    base = module.split(".") if package else module.split(".")[:-1]
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                yield node.lineno, alias.name
        elif isinstance(node, ast.ImportFrom):
            prefix = base[: len(base) - node.level + 1] if node.level else []
            target = ".".join(prefix + ([node.module] if node.module else []))
            if target:
                yield node.lineno, target
            for alias in node.names:
                if alias.name != "*":
                    yield node.lineno, target + "." + alias.name
        elif isinstance(node, ast.Call):
            dynamic = isinstance(node.func, ast.Name) and node.func.id == "__import__"
            dynamic |= isinstance(node.func, ast.Attribute) and node.func.attr == "import_module"
            if (
                dynamic
                and node.args
                and isinstance(node.args[0], ast.Constant)
                and isinstance(node.args[0].value, str)
            ):
                yield node.lineno, node.args[0].value


def forbidden_edge(owner, target):
    return any(
        target == "stock_god." + blocked or target.startswith("stock_god." + blocked + ".")
        for blocked in FORBIDDEN.get(owner, ())
    )


def test_python_dependencies_follow_the_business_boundaries():
    failures = []
    for path in SOURCE.rglob("*.py"):
        relative = path.relative_to(ROOT / "src").with_suffix("")
        parts = relative.parts
        package = parts[-1] == "__init__"
        module = ".".join(parts[:-1] if package else parts)
        owner = parts[1] if len(parts) > 1 else ""
        tree = ast.parse(path.read_text(encoding="utf-8-sig"), filename=str(path))
        for line, target in imported_modules(tree, module, package):
            if forbidden_edge(owner, target):
                failures.append(f"{path.relative_to(ROOT)}:{line} imports {target}")
    assert not failures, "\n".join(failures)


@pytest.mark.parametrize(
    "source",
    [
        "from stock_god import market",
        "from ..market import MarketServices",
        "from ..storage import historical",
        "import importlib; importlib.import_module('stock_god.market')",
    ],
)
def test_guard_resolves_absolute_relative_and_dynamic_domain_imports(source):
    modules = imported_modules(ast.parse(source), "stock_god.prediction.service")
    assert any(forbidden_edge("prediction", target) for _, target in modules)


def test_tracked_source_has_no_go_runtime_or_retired_backend_packages():
    result = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, check=True, capture_output=True)
    tracked = result.stdout.decode("utf-8").split("\0")
    retired = [
        path
        for path in tracked
        if path.endswith(".go") or Path(path).name in {"go.mod", "go.sum", "wails.json", ".golangci.yml"}
    ]
    assert not retired, retired
    assert not any(path.startswith(("backend/", "internal/", "cmd/")) for path in tracked)
    assert not (SOURCE / "research").exists() and not (SOURCE / "knowledge").exists()
    for name in tracked:
        if Path(name).suffix.lower() in {".ps1", ".cmd", ".bat"}:
            text = (ROOT / name).read_text(encoding="utf-8-sig")
            assert not re.search(
                r"(?im)^\s*(?:&\s+['\"]?)?(?:go(?:\.exe)?|wails)['\"]?\s+(?:run|build|test|vet|mod)\b", text
            ), name
    # String UUID/owner references to go-stock/research2 are deliberately allowed.
    for path in SOURCE.rglob("*.py"):
        tree = ast.parse(path.read_text(encoding="utf-8-sig"))
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call) or not node.args:
                continue
            argument = node.args[0]
            if isinstance(argument, (ast.List, ast.Tuple)) and argument.elts:
                executable = argument.elts[0]
                if isinstance(executable, ast.Constant) and isinstance(executable.value, str):
                    assert Path(executable.value).name.lower() not in {
                        "go",
                        "go.exe",
                        "wails",
                        "wails.exe",
                    }, str(path)


def test_retired_apis_and_pages_do_not_return_as_hidden_aliases():
    spec = load_spec(ROOT)
    for path in spec["paths"]:
        assert not re.search(r"/api/v1/(research(?:2|-centers)?|watchlist|groups|knowledge)(?:/|$)", path)
    routes = (ROOT / "frontend/src/router/routes.js").read_text(encoding="utf-8")
    paths = re.findall(r"\bpath:\s*['\"]([^'\"]+)['\"]", routes)
    assert set(paths) == {"/", "/prediction", "/settings", "/about", "/:pathMatch(.*)*"}
    views = {path.name for path in (ROOT / "frontend/src/views").glob("*.vue")}
    assert views == {"PredictionView.vue", "SettingsView.vue", "AboutView.vue"}


def test_release_versions_and_schema_targets_agree():
    manifest = json.loads((SOURCE / "release_manifest.json").read_text(encoding="utf-8"))
    project = tomllib.loads((ROOT / "pyproject.toml").read_text(encoding="utf-8"))["project"]
    locked = tomllib.loads((ROOT / "uv.lock").read_text(encoding="utf-8"))
    package = next(item for item in locked["package"] if item["name"] == project["name"])
    assert project["name"] == "stock-god"
    assert manifest["appVersion"] == project["version"] == package["version"] == APP_VERSION == __version__
    assert manifest["mainSchemaVersion"] == FINAL_VERSION["main"]
    assert manifest["minuteSchemaVersion"] == FINAL_VERSION["minute"]
    frontend = json.loads((ROOT / "frontend/package.json").read_text(encoding="utf-8"))
    assert frontend["name"] == project["name"]
    assert re.fullmatch(r"\d+\.\d+\.\d+", (ROOT / ".python-version").read_text().strip())


def test_retained_runtime_resources_and_public_icons_are_present():
    for name in (
        "stock_basic.json",
        "stock_base_info_hk.json",
        "stock_base_info_us.json",
        "finance.txt",
        "sentiment_dict.txt",
        "sentiment_weights.json",
        "user.txt",
        "sensitive_words.txt",
        "stop_words.txt",
    ):
        assert (SOURCE / "market/resources" / name).is_file(), name
    for name in ("app.ico", "appicon.png"):
        assert (ROOT / "frontend/public" / name).stat().st_size > 0
    assert 'href="/appicon.png"' in (ROOT / "frontend/index.html").read_text(encoding="utf-8")
    assert (ROOT / "LICENSE").is_file() and (ROOT / "NOTICE").is_file()
