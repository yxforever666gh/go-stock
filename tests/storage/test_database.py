from concurrent.futures import ThreadPoolExecutor
import sqlite3

import pytest

from stock_god.storage.db import Database
from stock_god.storage.backup import backup_database, verify_database
from stock_god.storage.archive import seal_legacy_archive, verify_archive


def test_write_rollback_and_read_only(tmp_path):
    path=tmp_path/'source.db'
    database=Database(path)
    with database.transaction() as db:
        db.execute('CREATE TABLE accounts(id INTEGER PRIMARY KEY,cash REAL)')
        db.execute('INSERT INTO accounts VALUES(1,10)')
    with pytest.raises(ValueError),database.transaction() as db:
        db.execute('UPDATE accounts SET cash=0')
        raise ValueError('rollback')
    with Database(path,read_only=True).connection() as db:
        assert db.execute('SELECT cash FROM accounts').fetchone()['cash']==10
        with pytest.raises(sqlite3.OperationalError):
            db.execute('UPDATE accounts SET cash=0')


def test_concurrent_transactions_never_lose_cash(tmp_path):
    database=Database(tmp_path/'concurrent.db')
    with database.transaction() as db:
        db.execute('CREATE TABLE accounts(cash INTEGER)')
        db.execute('INSERT INTO accounts VALUES(0)')
    def increment(_):
        with database.transaction() as db:
            cash=db.execute('SELECT cash FROM accounts').fetchone()[0]
            db.execute('UPDATE accounts SET cash=?',(cash+1,))
    with ThreadPoolExecutor(max_workers=4) as executor:
        list(executor.map(increment,range(32)))
    with database.connection() as db:
        assert db.execute('SELECT cash FROM accounts').fetchone()[0]==32


def test_backup_captures_wal_without_overwriting(tmp_path):
    source=tmp_path/'source.db'
    with Database(source).connection() as db:
        db.execute('PRAGMA wal_autocheckpoint=0')
        db.execute('CREATE TABLE records(value BLOB)')
        db.execute('INSERT INTO records VALUES(?)',(b'wal\x00data',))
        destination=tmp_path/'backup.db'
        result=backup_database(source,destination)
        assert result['quickCheck']=='ok'
        with Database(destination,read_only=True).connection() as target:
            assert target.execute('SELECT value FROM records').fetchone()[0]==b'wal\x00data'
        with pytest.raises(FileExistsError):
            backup_database(source,destination)
    assert verify_database(source)['quickCheck']=='ok'


def test_same_database_archive_preserves_dynamic_types_and_rowids(tmp_path):
    database=Database(tmp_path/'archive.db')
    with database.transaction() as db:
        db.execute('CREATE TABLE original(a,b,c)')
        db.execute('INSERT INTO original(rowid,a,b,c) VALUES(8,?,?,?)',(0,'0',b'\x00'))
        db.execute('INSERT INTO original(rowid,a,b,c) VALUES(12,?,?,?)',(None,0.0,''))
        seal_legacy_archive(db,0,{'original'})
        db.execute('DROP TABLE original')
    with database.transaction() as db:
        verify_archive(db)
        seal_legacy_archive(db,0,{'original'})
        archive=db.execute('SELECT archive_table FROM legacy_archive_tables').fetchone()[0]
        values=[tuple(row) for row in db.execute('SELECT * FROM '+archive+' ORDER BY __original_rowid')]
        assert values==[(8,0,'0',b'\x00'),(12,None,0.0,'')]
        assert db.execute('SELECT COUNT(*) FROM legacy_archive_sets').fetchone()[0]==1
        db.execute('UPDATE '+archive+' SET a=3 WHERE __original_rowid=8')
        with pytest.raises(ValueError,match='content conflict'):
            verify_archive(db)
