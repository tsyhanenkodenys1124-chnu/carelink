package store

import (
	"database/sql"
	"io/fs"
	"testing"
)

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("база: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// Повторний запуск сервера не повинен падати на ALTER TABLE із другої міграції.
func TestMigrateIsSafeToRunTwice(t *testing.T) {
	db := openTemp(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("перше застосування: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("повторне застосування: %v", err)
	}

	// Рахуємо від кількості файлів, а не від константи: інакше кожна нова
	// міграція ламала б тест, який насправді перевіряє не число, а те, що
	// повторний запуск нічого не застосовує вдруге.
	expected, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatalf("перелік міграцій: %v", err)
	}
	var applied int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("облік міграцій: %v", err)
	}
	if applied != len(expected) {
		t.Fatalf("очікувалося %d застосованих міграцій, записано %d", len(expected), applied)
	}
}

// Колонки другої міграції мають з'явитися у схемі.
func TestMigrateAddsMetricColumns(t *testing.T) {
	db := openTemp(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("міграції: %v", err)
	}
	if _, err := db.Exec("SELECT taken_at FROM escalations LIMIT 1"); err != nil {
		t.Fatalf("немає колонки escalations.taken_at: %v", err)
	}
	if _, err := db.Exec("SELECT correction_count FROM task_occurrences LIMIT 1"); err != nil {
		t.Fatalf("немає колонки task_occurrences.correction_count: %v", err)
	}
	if _, err := db.Exec("SELECT actor FROM audit_log LIMIT 1"); err != nil {
		t.Fatalf("немає колонки audit_log.actor: %v", err)
	}
}

// TestMigrateUpgradesExistingDatabase перевіряє, що нова міграція лягає на базу,
// яка вже працює: оновлення в бою — це не створення схеми з нуля.
func TestMigrateUpgradesExistingDatabase(t *testing.T) {
	db := openTemp(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("міграції: %v", err)
	}
	// Імітуємо стару базу: відкочуємо позначку про останню міграцію та колонку.
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version=3"); err != nil {
		t.Fatalf("відкат позначки: %v", err)
	}
	if _, err := db.Exec("ALTER TABLE audit_log DROP COLUMN actor"); err != nil {
		t.Fatalf("відкат колонки: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("повторні міграції: %v", err)
	}
	if _, err := db.Exec("SELECT actor FROM audit_log LIMIT 1"); err != nil {
		t.Fatalf("міграція не лягла на наявну базу: %v", err)
	}
}

func TestVersionOfRejectsBadName(t *testing.T) {
	if _, err := versionOf("initial.sql"); err == nil {
		t.Fatal("назва без номера має бути відхилена")
	}
	version, err := versionOf("012_щось.sql")
	if err != nil {
		t.Fatalf("розбір назви: %v", err)
	}
	if version != 12 {
		t.Fatalf("очікувався номер 12, отримано %d", version)
	}
}
