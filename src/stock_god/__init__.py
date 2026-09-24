"""Stock God application package."""

import json
from importlib.resources import files


def release_manifest() -> dict:
    """Read the release identity packaged with this application checkout."""
    return json.loads(files(__name__).joinpath("release_manifest.json").read_text(encoding="utf-8"))


APP_VERSION = release_manifest()["appVersion"]
__version__ = APP_VERSION
