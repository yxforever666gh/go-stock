"""All ordinary tests use disposable resources; live probes require explicit opt-in."""

from pathlib import Path

import pytest

from stock_god.storage.db import Database


def pytest_addoption(parser):
    parser.addoption("--live", action="store_true", default=False, help="Enable external provider probes")


def pytest_collection_modifyitems(config, items):
    if config.getoption("--live"):
        return
    skip_live = pytest.mark.skip(reason="external probes require --live")
    for item in items:
        if "live" in item.keywords:
            item.add_marker(skip_live)


@pytest.fixture
def core_database(tmp_path):
    database = Database(tmp_path / "core.db")
    schema = Path(__file__).parent / "fixtures" / "core_schema.sql"
    with database.connection() as connection:
        connection.executescript(schema.read_text(encoding="utf-8"))
    yield database
    database.close()
