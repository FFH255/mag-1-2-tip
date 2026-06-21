#!/usr/bin/env bash
# deploy.sh — сборка бинарника tasks под Linux и доставка на VPS (ПЗ №15).
#
# Запуск с локальной машины из КОРНЯ репозитория:
#   VPS=user@203.0.113.10 ./deploy/vps/deploy.sh
#
# Что делает:
#   1) кросс-компилирует статический бинарник под linux/amd64;
#   2) копирует его на VPS в /tmp/tasks (scp);
#   3) на VPS атомарно заменяет /opt/tasks/tasks с сохранением старой версии
#      (.old для отката), правит владельца/права и перезапускает systemd.
#
# Перед первым запуском на VPS должны существовать: пользователь tasksuser,
# каталоги /opt/tasks и /etc/tasks/tasks.env, установленный unit tasks.service
# (см. раздел «ПЗ №15» в README.md).
set -euo pipefail

: "${VPS:?Укажите цель SSH: VPS=user@<VPS_IP> ./deploy/vps/deploy.sh}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/services/tasks/bin/tasks"

echo "==> Сборка $OUT (linux/amd64, static, stripped)"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o "$OUT" "$ROOT/services/tasks/cmd/tasks"
sha256sum "$OUT" || shasum -a 256 "$OUT"

echo "==> Копирование на $VPS:/tmp/tasks"
scp "$OUT" "$VPS:/tmp/tasks"

echo "==> Установка на VPS и перезапуск сервиса"
ssh "$VPS" 'bash -se' <<'REMOTE'
set -euo pipefail
sudo systemctl stop tasks || true
# Сохраняем текущую версию для возможного отката.
if [ -f /opt/tasks/tasks ]; then
  sudo mv -f /opt/tasks/tasks /opt/tasks/tasks.old
fi
sudo mv /tmp/tasks /opt/tasks/tasks
sudo chown tasksuser:tasksuser /opt/tasks/tasks
sudo chmod 755 /opt/tasks/tasks
sudo systemctl start tasks
sleep 1
sudo systemctl --no-pager --full status tasks | head -n 15
REMOTE

echo "==> Готово. Логи: ssh $VPS 'journalctl -u tasks -f'"
