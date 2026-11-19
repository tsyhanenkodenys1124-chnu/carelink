#!/usr/bin/env bash
# Запуск «Догляду» однією командою на Linux і macOS.
#
#   ./start.sh            звичайний запуск, база backend/data/doglyad.db
#   ./start.sh --demo     запуск із демонстраційними даними
#   PORT=9000 ./start.sh
#
# Скрипт збирає інтерфейс, збирає сервер і запускає його. Сервер сам роздає
# і статику, і API, тому окремо запускати Vite не потрібно.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
backend="$root/backend"
web="$root/web"
data="$backend/data"
port="${PORT:-8080}"
demo=0

for argument in "$@"; do
  case "$argument" in
    --demo) demo=1 ;;
    *) echo "Невідомий параметр: $argument" >&2; exit 1 ;;
  esac
done

for tool in go npm; do
  command -v "$tool" >/dev/null 2>&1 || { echo "Не знайдено $tool. Встановіть його і повторіть." >&2; exit 1; }
done

mkdir -p "$data"

echo "Збираємо інтерфейс…"
cd "$web"
[ -d node_modules ] || npm ci
npm run build

echo "Збираємо сервер…"
cd "$backend"
go build -o "$data/doglyad" ./cmd/api

database="$data/doglyad.db"
if [ "$demo" -eq 1 ]; then
  database="$data/demo.db"
  echo "Створюємо демонстраційні дані…"
  DATABASE_PATH="$database" go run ./cmd/seed
fi

echo
echo "Застосунок працює: http://127.0.0.1:$port"
if [ "$demo" -eq 1 ]; then
  echo "Демо-вхід: olena.demo@doglyad.local / DemoCare2026!"
fi
echo "Зупинити: Ctrl+C"
echo

exec env \
  ADDR=":$port" \
  DATABASE_PATH="$database" \
  MEDIA_DIR="$data/media" \
  WEB_DIR="$web/dist" \
  PUBLIC_URL="http://127.0.0.1:$port" \
  "$data/doglyad"
