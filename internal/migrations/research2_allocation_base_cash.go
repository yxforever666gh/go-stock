package migrations

import (
	"errors"

	"go-stock/backend/research2"
	"gorm.io/gorm"
)

func mainMigrationV30Definition() string {
	return "research2_execution_chains.allocation_base_cash REAL NULL; NULL preserves the former dynamic allocation for historical and already-running chains; new winning slot reports persist their starting account cash"
}

func applyResearch2AllocationBaseCash(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if !tx.Migrator().HasTable(&research2.ExecutionChain{}) {
		return errors.New("research2 execution chains are unavailable")
	}
	if !tx.Migrator().HasColumn(&research2.ExecutionChain{}, "AllocationBaseCash") {
		if err := tx.Migrator().AddColumn(&research2.ExecutionChain{}, "AllocationBaseCash"); err != nil {
			return err
		}
	}
	return verifyMainSchema30Runtime(tx)
}

func verifyMainSchema30Runtime(db *gorm.DB) error {
	if db == nil {
		return errors.New("main database is unavailable")
	}
	if !db.Migrator().HasTable(&research2.ExecutionChain{}) || !db.Migrator().HasColumn(&research2.ExecutionChain{}, "AllocationBaseCash") {
		return errors.New("main schema 30 research2 execution-chain allocation base is missing")
	}
	return nil
}
