import pytest

from stock_god.storage.db import Database
from stock_god.storage.migrations import _normalize, status


@pytest.mark.parametrize(
    "actual",
    [
        'CREATE INDEX "idx" ON "rows"(first, second)',
        "create index [idx] on [rows] ([first] ASC, [second] asc);",
        "CREATE INDEX IF NOT EXISTS idx ON rows(first,second)",
    ],
)
def test_equivalent_simple_index_identifier_styles(actual):
    assert _normalize(actual) == _normalize("CREATE INDEX `idx` ON `rows`(`first`,`second`)")


@pytest.mark.parametrize(
    "actual",
    [
        "CREATE UNIQUE INDEX idx ON rows(first,second)",
        "CREATE INDEX idx ON different(first,second)",
        "CREATE INDEX idx ON rows(second,first)",
        "CREATE INDEX idx ON rows(first DESC,second)",
        "CREATE INDEX idx ON rows(first,second) WHERE first IS NOT NULL",
        "CREATE INDEX idx ON rows(lower(first),second)",
    ],
)
def test_actual_index_semantic_changes_remain_conflicts(actual):
    assert _normalize(actual) != _normalize("CREATE INDEX idx ON rows(first,second)")


def test_real_legacy_index_styles_verify_without_rewriting_db(app_config):
    with Database(app_config.main_db).transaction() as connection:
        connection.execute("DROP INDEX idx_research2_recommendations_execution_failure_code")
        actual = 'CREATE INDEX "idx_research2_recommendations_execution_failure_code" ON "research2_recommendations"(execution_failure_code)'
        connection.execute(actual)
    assert status(app_config.main_db, app_config.minute_db, verify=True)["main"]["quickCheck"] == "ok"
    with Database(app_config.main_db, read_only=True).connection() as connection:
        assert (
            connection.execute(
                "SELECT sql FROM sqlite_master WHERE name='idx_research2_recommendations_execution_failure_code'"
            ).fetchone()[0]
            == actual
        )
