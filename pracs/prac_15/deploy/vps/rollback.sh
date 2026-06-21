#!/usr/bin/env bash
# rollback.sh — откат tasks на предыдущую версию (ПЗ №15).
#
# Запуск с локальной машины:
#   VPS=user@203.0.113.10 ./deploy/vps/rollback.sh
#
# Возвращает /opt/tasks/tasks.old на место /opt/tasks/tasks и перезапускает
# сервис. Используется, если новая версия не прошла проверку /health.
set -euo pipefail

: "${VPS:?Укажите цель SSH: VPS=user@<VPS_IP> ./deploy/vps/rollback.sh}"

ssh "$VPS" 'bash -se' <<'REMOTE'
set -euo pipefail
if [ ! -f /opt/tasks/tasks.old ]; then
  echo "Нет /opt/tasks/tasks.old — откатываться не на что." >&2
  exit 1
fi
sudo systemctl stop tasks
sudo mv -f /opt/tasks/tasks.old /opt/tasks/tasks
sudo chown tasksuser:tasksuser /opt/tasks/tasks
sudo chmod 755 /opt/tasks/tasks
sudo systemctl start tasks
sleep 1
sudo systemctl --no-pager --full status tasks | head -n 15
REMOTE

echo "==> Откат выполнен."
