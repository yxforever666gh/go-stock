package persistence

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"go-stock/backend/models"
)

func TestGORMOrderEventStoreAppendsWithoutMutatingInput(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}

	signalAt := bundle.OrderEvents[0].EventAt.Add(15 * time.Minute)
	orderAt := signalAt.Add(15 * time.Minute)
	events := []models.OrderEvent{
		appendedOrderEvent(bundle, "store-fill", "fill", 4, orderAt),
		appendedOrderEvent(bundle, "store-signal", "signal", 2, signalAt),
		appendedOrderEvent(bundle, "store-order", "order", 3, orderAt),
	}
	before := append([]models.OrderEvent(nil), events...)

	store := NewGORMOrderEventStore(database)
	if err := store.AppendOrderEvents(context.Background(), bundle.Run.RunID, events); err != nil {
		t.Fatalf("append through store: %v", err)
	}
	if !reflect.DeepEqual(events, before) {
		t.Fatalf("store mutated caller events:\n got: %#v\nwant: %#v", events, before)
	}

	var persisted []models.OrderEvent
	if err := database.Where("run_id = ?", bundle.Run.RunID).Order("sequence ASC").Find(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 4 {
		t.Fatalf("persisted event count = %d, want 4", len(persisted))
	}
	for index, event := range persisted {
		if event.Sequence != index+1 {
			t.Fatalf("persisted sequence[%d] = %d, want %d", index, event.Sequence, index+1)
		}
	}
}

func TestGORMOrderEventStorePreservesErrorsAndAtomicity(t *testing.T) {
	t.Run("immutable conflict", func(t *testing.T) {
		database := openStrategyPersistenceTestDB(t)
		bundle := frozenStrategyBundle()
		if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
			t.Fatal(err)
		}

		store := NewGORMOrderEventStore(database)
		event := appendedOrderEvent(bundle, "store-conflict", "signal", 2, bundle.OrderEvents[0].EventAt.Add(15*time.Minute))
		if err := store.AppendOrderEvents(context.Background(), bundle.Run.RunID, []models.OrderEvent{event}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendOrderEvents(context.Background(), bundle.Run.RunID, []models.OrderEvent{event}); !errors.Is(err, ErrImmutableConflict) {
			t.Fatalf("duplicate append error = %v, want ErrImmutableConflict", err)
		}
	})

	t.Run("invalid batch rollback", func(t *testing.T) {
		database := openStrategyPersistenceTestDB(t)
		bundle := frozenStrategyBundle()
		if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
			t.Fatal(err)
		}

		signalAt := bundle.OrderEvents[0].EventAt.Add(15 * time.Minute)
		valid := appendedOrderEvent(bundle, "store-valid", "signal", 2, signalAt)
		invalid := appendedOrderEvent(bundle, "store-invalid", "order", 3, signalAt.Add(15*time.Minute))
		invalid.StrategyVersion = "invalid-version"
		sealed := []models.OrderEvent{invalid}
		if err := SealStrategyOrderEvents(sealed); err != nil {
			t.Fatal(err)
		}
		invalid = sealed[0]

		store := NewGORMOrderEventStore(database)
		err := store.AppendOrderEvents(context.Background(), bundle.Run.RunID, []models.OrderEvent{valid, invalid})
		if !errors.Is(err, ErrInvalidImmutableRecord) {
			t.Fatalf("invalid append error = %v, want ErrInvalidImmutableRecord", err)
		}

		var count int64
		if err := database.Model(&models.OrderEvent{}).Where("run_id = ?", bundle.Run.RunID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("invalid batch changed event count to %d", count)
		}
	})
}
