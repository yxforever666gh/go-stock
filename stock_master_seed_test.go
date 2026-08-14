package main

import "testing"

import "time"

func TestEmbeddedStockMasterSeedIsComplete(t *testing.T) {
	rows, result, err := embeddedDomesticStockMaster()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 5000 || result.SHA256 == "" || !result.UsedSeed {
		t.Fatalf("embedded stock master seed is incomplete: rows=%d result=%+v", len(rows), result)
	}
	if embeddedStockMasterSeedManifest.RowCount != len(rows) || embeddedStockMasterSeedManifest.SHA256 != result.SHA256 {
		t.Fatalf("seed manifest mismatch: manifest=%+v result=%+v", embeddedStockMasterSeedManifest, result)
	}
	if time.Since(embeddedStockMasterSeedManifest.GeneratedAt) > 60*24*time.Hour {
		t.Fatal("embedded seed is too old for release")
	}
}
