package ports

import "context"

// DatabaseStatus is the CLI-owned view of one SQLite database's migration
// state. Bootstrap maps persistence details into this command contract.
type DatabaseStatus struct {
	Database        string            `json:"database"`
	CurrentVersion  int               `json:"currentVersion"`
	ExpectedVersion int               `json:"expectedVersion"`
	Pending         []int             `json:"pending"`
	Records         []MigrationRecord `json:"records"`
	QuickCheck      string            `json:"quickCheck,omitempty"`
}

type MigrationRecord struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Checksum   string `json:"checksum"`
	AppliedAt  string `json:"appliedAt"`
	AppVersion string `json:"appVersion"`
}

// StorageAdmin owns one explicitly opened main/minute database pair. The CLI
// never receives global database handles or invokes migrations directly.
type StorageAdmin interface {
	Status(context.Context) (DatabaseStatus, DatabaseStatus, error)
	Migrate(context.Context) error
	Verify(context.Context) (DatabaseStatus, DatabaseStatus, error)
	Backup(context.Context, string, string) error
	QuickCheck(context.Context) error
	Close() error
}
