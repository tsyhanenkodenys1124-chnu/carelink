// Пакет store відкриває SQLite та застосовує вбудовану схему.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // драйвер SQLite без залежності від cgo
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open створює каталог бази, відкриває з'єднання у WAL-режимі та застосовує схему.
//
// Пул навмисно обмежений одним з'єднанням: SQLite підтримує лише одного письменника,
// а послідовні запити прибирають клас помилок «database is locked». Через це у коді
// діє правило: не можна виконувати запит усередині циклу за незакритим курсором —
// спершу зчитуємо рядки у зріз, потім працюємо з ними.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("каталог бази: %w", err)
		}
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("відкриття бази: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("з'єднання з базою: %w", err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// OpenMemory відкриває окрему базу в пам'яті. Використовується у тестах.
func OpenMemory(name string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate застосовує ще не застосовані міграції за порядком номерів у назві файла.
//
// Кожна міграція виконується один раз і записується в schema_migrations. Саме це
// дозволяє змінювати схему далі: ALTER TABLE не можна виконувати повторно, тому
// підхід «застосувати всі файли при кожному запуску» працював лише до першої зміни.
func Migrate(db *sql.DB) error {
	// Таблиця обліку має існувати до того, як ми щось питатимемо про застосовані версії.
	if _, err := db.Exec(
		"CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		return fmt.Errorf("таблиця міграцій: %w", err)
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("читання міграцій: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := versionOf(name)
		if err != nil {
			return err
		}
		if applied[version] {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("читання %s: %w", name, err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			return fmt.Errorf("застосування %s: %w", name, err)
		}
		if _, err := db.Exec(
			"INSERT OR REPLACE INTO schema_migrations(version,applied_at) VALUES(?,datetime('now'))", version); err != nil {
			return fmt.Errorf("запис версії %d: %w", version, err)
		}
	}
	return nil
}

// appliedVersions читає номери вже застосованих міграцій.
func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("читання застосованих міграцій: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("читання номера міграції: %w", err)
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// versionOf дістає номер із назви на кшталт 002_research_metrics.sql.
func versionOf(name string) (int, error) {
	digits, _, found := strings.Cut(name, "_")
	if !found {
		return 0, fmt.Errorf("назва міграції %q має починатися з номера та підкреслення", name)
	}
	version, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("номер міграції %q: %w", name, err)
	}
	return version, nil
}
