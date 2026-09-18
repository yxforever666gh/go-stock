package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/researchaudit"
	"go-stock/backend/researchconfig"
	"gorm.io/gorm"
)

func applyResearch1Freeze(tx *gorm.DB) error {
	for _, field := range []string{"Frozen", "FrozenAt", "FrozenReason"} {
		if !tx.Migrator().HasColumn(&research.SimulatedAccount{}, field) {
			if err := tx.Migrator().AddColumn(&research.SimulatedAccount{}, field); err != nil {
				return err
			}
		}
	}
	if err := research.NewRepository(tx).FreezeAndLiquidate(context.Background(), time.Now().UTC()); err != nil {
		return err
	}
	if err := tx.Model(&researchaudit.RunState{}).Where("owner_type = ? AND status = ?", researchaudit.OwnerResearch1, researchaudit.StatusCapturing).Updates(map[string]any{"status": researchaudit.StatusFailed, "last_error": research.FreezeReason}).Error; err != nil {
		return err
	}
	if err := tx.Model(&researchaudit.Replay{}).Where("source_owner_type = ? AND status IN ?", researchaudit.OwnerResearch1, []string{"queued", "running"}).Updates(map[string]any{"status": "failed", "last_error": research.FreezeReason, "completed_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	var setting researchconfig.Record
	if err := tx.Where("center = ?", researchconfig.Research1).First(&setting).Error; err != nil {
		return err
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(setting.ConfigJSON), &cfg); err != nil {
		return err
	}
	cfg["aiCapitalDeploymentEnabled"] = json.RawMessage("false")
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if string(raw) != setting.ConfigJSON {
		if err = tx.Model(&setting).Updates(map[string]any{"config_json": string(raw), "revision": setting.Revision + 1}).Error; err != nil {
			return err
		}
	}
	return verifyMainSchema29Runtime(tx)
}

func verifyMainSchema29Runtime(db *gorm.DB) error {
	for _, field := range []string{"Frozen", "FrozenAt", "FrozenReason"} {
		if !db.Migrator().HasColumn(&research.SimulatedAccount{}, field) {
			return errors.New("schema 29 missing research1 freeze metadata")
		}
	}
	// Frozen is a persistent operational state, not an immutable schema invariant:
	// a later explicitly authorized unfreeze must not invalidate the database.
	var account research.SimulatedAccount
	if err := db.First(&account, 1).Error; err != nil {
		return err
	}
	if account.Frozen {
		var count int64
		if err := db.Model(&research.Position{}).Where("status = ?", "open").Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errors.New("frozen research1 account still contains open positions")
		}
	}
	return nil
}
