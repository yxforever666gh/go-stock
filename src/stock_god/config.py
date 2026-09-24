"""Explicit application paths and process configuration."""

import os
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class AppConfig:
    root: Path
    main_db: Path
    minute_db: Path
    frontend_dist: Path
    market_data_root: Path
    market_index_dir: Path
    host: str = "127.0.0.1"
    port: int = 34115
    scheduler_enabled: bool = True

    @classmethod
    def from_env(cls, root: Path | None = None) -> "AppConfig":
        base = (root or Path(os.environ.get("STOCK_GOD_ROOT", Path.cwd()))).resolve()

        def path(name: str, default: str) -> Path:
            value = Path(os.environ.get(name, default))
            return (value if value.is_absolute() else base / value).resolve()

        return cls(
            root=base,
            main_db=path("STOCK_GOD_DB_PATH", "data/stock.db"),
            minute_db=path("STOCK_GOD_MINUTE_DB_PATH", "data/minute.db"),
            frontend_dist=path("STOCK_GOD_FRONTEND_DIST", "frontend/dist"),
            market_data_root=path("STOCK_GOD_MARKET_DATA_ROOT", "A股历史分钟线数据包"),
            market_index_dir=path("STOCK_GOD_MARKET_INDEX_DIR", "runtime/minute-index"),
            host=os.environ.get("STOCK_GOD_HOST", "127.0.0.1"),
            port=int(os.environ.get("STOCK_GOD_PORT", "34115")),
            scheduler_enabled=os.environ.get("STOCK_GOD_SCHEDULER", "1") == "1",
        )
