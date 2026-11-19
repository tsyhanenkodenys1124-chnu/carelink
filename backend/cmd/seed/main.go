// Команда seed створює синтетичні демонстраційні дані.
// Вона працює з окремим файлом бази та ніколи не чіпає робочі дані.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"doglyad/backend/internal/api"
	"doglyad/backend/internal/config"
	"doglyad/backend/internal/security"
	"doglyad/backend/internal/store"
)

const (
	demoEmail    = "olena.demo@doglyad.local"
	demoPassword = "DemoCare2026!"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Помилка:", err)
		os.Exit(1)
	}
}

func run() error {
	path := os.Getenv("DATABASE_PATH")
	if path == "" {
		path = "./data/demo.db"
	}
	db, err := store.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()

	var existing int
	if err := db.QueryRow("SELECT COUNT(*) FROM helpers WHERE email=?", demoEmail).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		fmt.Println("Демо-дані вже існують. Базу не змінено.")
		return nil
	}
	if err := seed(db); err != nil {
		return err
	}
	fmt.Println("Демо-дані створено.")
	fmt.Println("Файл бази:", path)
	fmt.Println("Вхід:", demoEmail)
	fmt.Println("Пароль:", demoPassword)
	return nil
}

func seed(db *sql.DB) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	helperID, secondHelperID, wardID := security.ID(), security.ID(), security.ID()

	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO helpers(id,email,password_hash,display_name,timezone,email_confirmed_at,created_at)
		  VALUES(?,?,?,?,?,?,?)`,
			[]any{helperID, demoEmail, security.HashPassword(demoPassword), "Олена Коваль", "Europe/Kyiv", now, now}},
		{`INSERT INTO helpers(id,email,password_hash,display_name,timezone,email_confirmed_at,created_at)
		  VALUES(?,?,?,?,?,?,?)`,
			[]any{secondHelperID, "petro.demo@doglyad.local", security.HashPassword(demoPassword),
				"Петро Коваль", "Europe/Kyiv", now, now}},
		{`INSERT INTO wards(id,address_name,timezone,emergency_phone,created_at) VALUES(?,?,?,?,?)`,
			[]any{wardID, "Маріє Іванівно", "Europe/Kyiv", "103", now}},
		{`INSERT INTO care_links(id,ward_id,helper_id,is_primary,can_view,can_edit_tasks,can_edit_settings,can_escalations,created_at)
		  VALUES(?,?,?,1,1,1,1,1,?)`, []any{security.ID(), wardID, helperID, now}},
		{`INSERT INTO care_links(id,ward_id,helper_id,is_primary,can_view,can_edit_tasks,can_edit_settings,can_escalations,created_at)
		  VALUES(?,?,?,0,1,1,0,1,?)`, []any{security.ID(), wardID, secondHelperID, now}},
		{`INSERT INTO accessibility_settings(ward_id,scale,palette,large_controls,dyslexic_font,dementia_mode,speech_enabled,updated_at,updated_by)
		  VALUES(?,?,?,?,?,?,?,?,?)`,
			[]any{wardID, 150, "high-contrast", 1, 0, 1, 1, now, helperID}},
		{`INSERT INTO needs_profiles(ward_id,vision_text,vision_colors,hearing,motor,dyslexia,memory,prefers_audio,updated_at,updated_by)
		  VALUES(?,?,?,?,?,?,?,?,?,?)`,
			[]any{wardID, "partly", "no", "no", "partly", "no", "yes", "yes", now, helperID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			return fmt.Errorf("демо-дані: %w", err)
		}
	}

	tasks := []struct {
		title       string
		category    string
		clock       string
		critical    bool
		description string
		steps       []string
	}{
		{"Ранкові ліки", "medicine", "08:00", true,
			"Одна таблетка з ранковою їжею.", []string{"Візьміть склянку води", "Прийміть одну таблетку"}},
		{"Обід", "food", "13:00", false, "Розігріти страву з холодильника.", nil},
		{"Вечірні ліки", "medicine", "20:00", true, "Дві таблетки після вечері.", nil},
		{"Прогулянка у дворі", "other", "16:00", false, "Двадцять хвилин на свіжому повітрі.", nil},
	}
	today := time.Now().Format("2006-01-02")
	for _, task := range tasks {
		taskID := security.ID()
		days, err := json.Marshal([]int{})
		if err != nil {
			return err
		}
		critical := 0
		if task.critical {
			critical = 1
		}
		if _, err := db.Exec(`
			INSERT INTO tasks(id,ward_id,title,category,description,critical,local_start_date,local_time,timezone,
				recurrence_kind,recurrence_days,recurrence_interval,completion_window_minutes,escalation_window_minutes,
				warning_minutes,reminder_repeats,reminder_interval_minutes,created_by,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			taskID, wardID, task.title, task.category, task.description, critical, today, task.clock, "Europe/Kyiv",
			"daily", string(days), 1, 120, 30, 10, 2, 10, helperID, now); err != nil {
			return fmt.Errorf("завдання «%s»: %w", task.title, err)
		}
		for position, step := range task.steps {
			if _, err := db.Exec("INSERT INTO task_steps(id,task_id,position,text) VALUES(?,?,?,?)",
				security.ID(), taskID, position, step); err != nil {
				return fmt.Errorf("кроки завдання «%s»: %w", task.title, err)
			}
		}
	}

	// Одразу створюємо розклад, щоб демо показувало справи без очікування планувальника.
	return materialize(db)
}

// materialize повторює роботу планувальника для щойно створених завдань,
// використовуючи той самий код, що й сервер.
func materialize(db *sql.DB) error {
	cfg := config.Load()
	cfg.MediaDir = filepath.Join(os.TempDir(), "doglyad-seed-media")
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	server, err := api.New(db, cfg, log)
	if err != nil {
		return err
	}
	server.Materialize(context.Background())
	return nil
}
