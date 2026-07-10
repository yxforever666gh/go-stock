package db

import (
	"bytes"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestDBLoggerDoesNotRenderSecretParameters(t *testing.T) {
	const secret = "sql-log-secret-9f4a"
	var output bytes.Buffer
	logger := newDBLoggerWithWriter(&output).LogMode(gormlogger.Info)
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := database.Exec("CREATE TABLE secrets (value TEXT)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := database.Exec("INSERT INTO secrets(value) VALUES (?)", secret).Error; err != nil {
		t.Fatalf("insert secret: %v", err)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatalf("SQL logger exposed a bound secret: %s", output.String())
	}
}
