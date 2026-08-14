package models

import "time"

// StockMasterRefreshResult is the auditable result of one all-or-nothing
// security-master refresh attempt.
type StockMasterRefreshResult struct {
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetchedAt"`
	RowCount  int       `json:"rowCount"`
	ValidRows int       `json:"validRows"`
	SHA256    string    `json:"sha256"`
	Replaced  bool      `json:"replaced"`
	UsedSeed  bool      `json:"usedSeed"`
	Warnings  []string  `json:"warnings,omitempty"`
}

type StockMasterSeedManifest struct {
	GeneratedAt time.Time `json:"generatedAt"`
	RowCount    int       `json:"rowCount"`
	SHA256      string    `json:"sha256"`
}

// StockMasterRefreshMetadata is the singleton audit record for the last
// accepted security-master snapshot. Its schema is owned by migration 3.
type StockMasterRefreshMetadata struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Source    string    `json:"source" gorm:"size:64;not null"`
	FetchedAt time.Time `json:"fetchedAt" gorm:"not null"`
	RowCount  int       `json:"rowCount" gorm:"not null"`
	ValidRows int       `json:"validRows" gorm:"not null"`
	SHA256    string    `json:"sha256" gorm:"size:64;not null"`
	UsedSeed  bool      `json:"usedSeed" gorm:"not null"`
	Warnings  string    `json:"warnings,omitempty" gorm:"type:text"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (StockMasterRefreshMetadata) TableName() string { return "stock_master_refresh_metadata" }

type StockMasterHealth struct {
	Ready       bool      `json:"ready"`
	RowCount    int64     `json:"rowCount"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	Age         string    `json:"age,omitempty"`
	Warning     string    `json:"warning,omitempty"`
	FailureCode string    `json:"failureCode,omitempty"`
}

type MarketDataPreflightResult struct {
	BenchmarkReady      bool    `json:"benchmarkReady"`
	BenchmarkCode       string  `json:"benchmarkCode"`
	UniverseCount       int64   `json:"universeCount"`
	MasterReadyCount    int64   `json:"masterReadyCount"`
	MasterReadyCoverage float64 `json:"masterReadyCoverage"`
	QuoteReadyCount     int64   `json:"quoteReadyCount"`
	QuoteReadyCoverage  float64 `json:"quoteReadyCoverage"`
	MissingCount        int64   `json:"missingCount"`
	StaleCount          int64   `json:"staleCount"`
	ZeroTurnoverCount   int64   `json:"zeroTurnoverCount"`
	ProviderUnsupported int64   `json:"providerUnsupported"`
	BeijingUnsupported  int64   `json:"beijingUnsupported"`
	FailureCode         string  `json:"failureCode,omitempty"`
	Message             string  `json:"message,omitempty"`
}
