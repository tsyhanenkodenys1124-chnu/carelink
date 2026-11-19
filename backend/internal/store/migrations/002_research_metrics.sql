-- Поля для показників, за якими оцінюється робота застосунку.
--
-- taken_at потрібен, щоб рахувати час від появи ескалації до реакції помічника.
-- correction_count показує, скільки разів відмітку довелося виправляти: справа,
-- виконана з першого разу, має нуль.
ALTER TABLE escalations ADD COLUMN taken_at TEXT;
ALTER TABLE task_occurrences ADD COLUMN correction_count INTEGER NOT NULL DEFAULT 0;
