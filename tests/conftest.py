"""All ordinary tests use disposable resources; live probes require explicit opt-in."""

import pytest


def pytest_addoption(parser):
    parser.addoption("--live", action="store_true", default=False, help="Enable external provider probes")


def pytest_collection_modifyitems(config, items):
    if config.getoption("--live"):
        return
    skip_live = pytest.mark.skip(reason="external probes require --live")
    for item in items:
        if "live" in item.keywords:
            item.add_marker(skip_live)
