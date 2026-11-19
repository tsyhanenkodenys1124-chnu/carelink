-- Догляд: повна схема SQLite. Усі часові значення зберігаються у UTC у форматі RFC3339.
-- Цей файл є єдиним джерелом правди для структури бази: сервер вбудовує його та застосовує під час запуску.
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

-- Помічники: єдині власники облікових записів. Підопічний облікового запису не має.
CREATE TABLE IF NOT EXISTS helpers (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  display_name TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'Europe/Kyiv',
  email_confirmed_at TEXT,
  phone TEXT,
  created_at TEXT NOT NULL,
  deleted_at TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  helper_id TEXT NOT NULL REFERENCES helpers(id),
  token_hash TEXT NOT NULL UNIQUE,
  csrf_token TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  user_agent TEXT,
  revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS sessions_by_helper ON sessions(helper_id, revoked_at);

-- Одноразові коди для підтвердження пошти та відновлення пароля.
CREATE TABLE IF NOT EXISTS email_tokens (
  id TEXT PRIMARY KEY,
  helper_id TEXT NOT NULL REFERENCES helpers(id),
  kind TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  used_at TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS email_tokens_by_helper ON email_tokens(helper_id, kind);

-- Обмеження частоти спроб входу за адресою та за IP.
CREATE TABLE IF NOT EXISTS login_attempts (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  subject TEXT NOT NULL,
  failures INTEGER NOT NULL DEFAULT 0,
  locked_until TEXT,
  updated_at TEXT NOT NULL,
  UNIQUE(scope, subject)
);

CREATE TABLE IF NOT EXISTS wards (
  id TEXT PRIMARY KEY,
  address_name TEXT NOT NULL,
  timezone TEXT NOT NULL,
  photo_path TEXT,
  emergency_phone TEXT,
  pin_hash TEXT,
  pin_failed_attempts INTEGER NOT NULL DEFAULT 0,
  pin_locked_until TEXT,
  wake_lock_enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  deleted_at TEXT
);

CREATE TABLE IF NOT EXISTS care_links (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  helper_id TEXT NOT NULL REFERENCES helpers(id),
  is_primary INTEGER NOT NULL DEFAULT 0,
  can_view INTEGER NOT NULL DEFAULT 1,
  can_edit_tasks INTEGER NOT NULL DEFAULT 0,
  can_edit_settings INTEGER NOT NULL DEFAULT 0,
  can_escalations INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  revoked_at TEXT,
  UNIQUE(ward_id, helper_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS one_primary_helper_per_ward ON care_links(ward_id) WHERE is_primary=1 AND revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS invitations (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  email TEXT,
  token_hash TEXT NOT NULL UNIQUE,
  can_edit_tasks INTEGER NOT NULL DEFAULT 0,
  can_edit_settings INTEGER NOT NULL DEFAULT 0,
  can_escalations INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL,
  accepted_at TEXT,
  revoked_at TEXT,
  created_by TEXT NOT NULL REFERENCES helpers(id),
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  token_hash TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL,
  last_active_at TEXT,
  created_at TEXT NOT NULL,
  revoked_at TEXT
);

CREATE TABLE IF NOT EXISTS pairing_codes (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  code TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  used_at TEXT,
  created_by TEXT NOT NULL REFERENCES helpers(id),
  created_at TEXT NOT NULL
);

-- Тимчасовий режим помічника на пристрої підопічного після введення PIN.
CREATE TABLE IF NOT EXISTS pin_sessions (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  device_id TEXT NOT NULL REFERENCES devices(id),
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  revoked_at TEXT
);

-- Профіль функціональних потреб. Діагнози не збираються.
CREATE TABLE IF NOT EXISTS needs_profiles (
  ward_id TEXT PRIMARY KEY REFERENCES wards(id),
  vision_text TEXT,
  vision_colors TEXT,
  hearing TEXT,
  motor TEXT,
  dyslexia TEXT,
  memory TEXT,
  prefers_audio TEXT,
  updated_at TEXT NOT NULL,
  updated_by TEXT NOT NULL REFERENCES helpers(id)
);

CREATE TABLE IF NOT EXISTS accessibility_settings (
  ward_id TEXT PRIMARY KEY REFERENCES wards(id),
  scale INTEGER NOT NULL DEFAULT 100,
  palette TEXT NOT NULL DEFAULT 'standard',
  magnifier INTEGER NOT NULL DEFAULT 0,
  magnifier_scale INTEGER NOT NULL DEFAULT 2,
  large_controls INTEGER NOT NULL DEFAULT 0,
  dyslexic_font INTEGER NOT NULL DEFAULT 0,
  letter_spacing TEXT NOT NULL DEFAULT '0',
  word_spacing TEXT NOT NULL DEFAULT '0',
  line_height TEXT NOT NULL DEFAULT '1.5',
  dementia_mode INTEGER NOT NULL DEFAULT 0,
  speech_enabled INTEGER NOT NULL DEFAULT 0,
  speech_rate REAL NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL,
  updated_by TEXT NOT NULL REFERENCES helpers(id)
);

CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  title TEXT NOT NULL,
  category TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  critical INTEGER NOT NULL DEFAULT 0,
  local_start_date TEXT NOT NULL,
  local_time TEXT NOT NULL,
  timezone TEXT NOT NULL,
  recurrence_kind TEXT NOT NULL DEFAULT 'once',
  recurrence_days TEXT NOT NULL DEFAULT '[]',
  recurrence_interval INTEGER NOT NULL DEFAULT 1,
  completion_window_minutes INTEGER NOT NULL DEFAULT 120,
  escalation_window_minutes INTEGER NOT NULL DEFAULT 30,
  warning_minutes INTEGER NOT NULL DEFAULT 0,
  reminder_repeats INTEGER NOT NULL DEFAULT 0,
  reminder_interval_minutes INTEGER NOT NULL DEFAULT 5,
  photo_media_id TEXT,
  created_by TEXT NOT NULL REFERENCES helpers(id),
  created_at TEXT NOT NULL,
  deleted_at TEXT
);
CREATE INDEX IF NOT EXISTS tasks_by_ward ON tasks(ward_id, deleted_at);

CREATE TABLE IF NOT EXISTS task_steps (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  position INTEGER NOT NULL,
  text TEXT NOT NULL,
  UNIQUE(task_id, position)
);

CREATE TABLE IF NOT EXISTS media (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  task_id TEXT REFERENCES tasks(id),
  path TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  byte_size INTEGER NOT NULL DEFAULT 0,
  created_by TEXT REFERENCES helpers(id),
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS task_occurrences (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  ward_id TEXT NOT NULL REFERENCES wards(id),
  scheduled_at TEXT NOT NULL,
  completion_deadline TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'planned',
  completed_at TEXT,
  device_time TEXT,
  marked_by_helper_id TEXT REFERENCES helpers(id),
  device_id TEXT REFERENCES devices(id),
  idempotency_key TEXT UNIQUE,
  undo_until TEXT,
  system_closed_at TEXT,
  created_at TEXT NOT NULL,
  UNIQUE(task_id, scheduled_at)
);
CREATE INDEX IF NOT EXISTS occurrences_by_ward_time ON task_occurrences(ward_id, scheduled_at);
CREATE INDEX IF NOT EXISTS occurrences_by_status ON task_occurrences(status, completion_deadline);

CREATE TABLE IF NOT EXISTS reminder_jobs (
  id TEXT PRIMARY KEY,
  occurrence_id TEXT NOT NULL REFERENCES task_occurrences(id),
  kind TEXT NOT NULL,
  due_at TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'created',
  attempt INTEGER NOT NULL DEFAULT 0,
  idempotency_key TEXT NOT NULL UNIQUE,
  locked_until TEXT,
  sent_at TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS reminder_jobs_due ON reminder_jobs(status, due_at);

-- Канали сповіщень помічника: push, telegram, email. Рядок без ward_id є типовим для всіх підопічних.
CREATE TABLE IF NOT EXISTS notification_prefs (
  id TEXT PRIMARY KEY,
  helper_id TEXT NOT NULL REFERENCES helpers(id),
  ward_id TEXT REFERENCES wards(id),
  channel TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  quiet_start TEXT,
  quiet_end TEXT,
  UNIQUE(helper_id, ward_id, channel)
);

CREATE TABLE IF NOT EXISTS push_subscriptions (
  id TEXT PRIMARY KEY,
  helper_id TEXT NOT NULL REFERENCES helpers(id),
  endpoint TEXT NOT NULL UNIQUE,
  p256dh TEXT NOT NULL,
  auth TEXT NOT NULL,
  created_at TEXT NOT NULL,
  revoked_at TEXT
);

CREATE TABLE IF NOT EXISTS telegram_links (
  id TEXT PRIMARY KEY,
  helper_id TEXT NOT NULL REFERENCES helpers(id),
  chat_id TEXT UNIQUE,
  link_code_hash TEXT UNIQUE,
  expires_at TEXT,
  created_at TEXT NOT NULL,
  revoked_at TEXT
);

-- Черга доставки. Кожен рядок — одне повідомлення в один канал.
CREATE TABLE IF NOT EXISTS notifications (
  id TEXT PRIMARY KEY,
  helper_id TEXT REFERENCES helpers(id),
  ward_id TEXT REFERENCES wards(id),
  channel TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT,
  error TEXT,
  created_at TEXT NOT NULL,
  sent_at TEXT,
  delivered_at TEXT
);
CREATE INDEX IF NOT EXISTS notifications_queue ON notifications(status, next_attempt_at);

CREATE TABLE IF NOT EXISTS escalations (
  id TEXT PRIMARY KEY,
  occurrence_id TEXT NOT NULL UNIQUE REFERENCES task_occurrences(id),
  ward_id TEXT NOT NULL REFERENCES wards(id),
  status TEXT NOT NULL DEFAULT 'open',
  taken_by TEXT REFERENCES helpers(id),
  comment TEXT,
  opened_at TEXT NOT NULL,
  repeated_at TEXT,
  closed_at TEXT
);

CREATE TABLE IF NOT EXISTS help_requests (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  status TEXT NOT NULL DEFAULT 'open',
  message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  closed_at TEXT,
  closed_by TEXT REFERENCES helpers(id)
);

CREATE TABLE IF NOT EXISTS help_acknowledgements (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL REFERENCES help_requests(id),
  helper_id TEXT NOT NULL REFERENCES helpers(id),
  created_at TEXT NOT NULL,
  UNIQUE(request_id, helper_id)
);

-- «Я не пам'ятаю»: підопічний питає, чи приймав ліки.
CREATE TABLE IF NOT EXISTS unsure_requests (
  id TEXT PRIMARY KEY,
  occurrence_id TEXT NOT NULL REFERENCES task_occurrences(id),
  ward_id TEXT NOT NULL REFERENCES wards(id),
  status TEXT NOT NULL DEFAULT 'open',
  answer TEXT,
  answered_by TEXT REFERENCES helpers(id),
  created_at TEXT NOT NULL,
  answered_at TEXT
);

CREATE TABLE IF NOT EXISTS adaptation_signals (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  date TEXT NOT NULL,
  viewport_zoom_sessions INTEGER NOT NULL DEFAULT 0,
  magnifier_uses INTEGER NOT NULL DEFAULT 0,
  quick_undos INTEGER NOT NULL DEFAULT 0,
  speech_uses INTEGER NOT NULL DEFAULT 0,
  unsure_uses INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  UNIQUE(ward_id, date)
);

CREATE TABLE IF NOT EXISTS adaptation_suggestions (
  id TEXT PRIMARY KEY,
  ward_id TEXT NOT NULL REFERENCES wards(id),
  task_id TEXT REFERENCES tasks(id),
  rule_code TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'created',
  explanation TEXT NOT NULL,
  proposal_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  decided_at TEXT,
  decided_by TEXT REFERENCES helpers(id),
  effect_before_json TEXT,
  effect_after_json TEXT,
  suppressed_until TEXT
);

CREATE TABLE IF NOT EXISTS audit_log (
  id TEXT PRIMARY KEY,
  ward_id TEXT REFERENCES wards(id),
  helper_id TEXT REFERENCES helpers(id),
  action TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id TEXT,
  details_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_by_ward ON audit_log(ward_id, created_at);
