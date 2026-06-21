#!/usr/bin/env bash
# setup-vps.sh — однократная подготовка VPS под сервис tasks (ПЗ №15).
# Запускается НА VPS (с правами sudo). Идемпотентен: повторный запуск не ломает
# уже созданные сущности.
#
#   # на VPS, из каталога с этим репозиторием (или скопировав файлы рядом):
#   sudo bash deploy/vps/setup-vps.sh
#
# Делает: системного пользователя tasksuser, каталоги /opt/tasks и /etc/tasks,
# кладёт пример env (если его ещё нет) и устанавливает unit tasks.service.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Пользователь tasksuser (system, без shell, без home)"
id tasksuser >/dev/null 2>&1 || \
  useradd --system --no-create-home --shell /usr/sbin/nologin tasksuser

echo "==> Каталог приложения /opt/tasks"
mkdir -p /opt/tasks
chown -R tasksuser:tasksuser /opt/tasks

echo "==> Каталог и файл конфигурации /etc/tasks/tasks.env"
mkdir -p /etc/tasks
if [ ! -f /etc/tasks/tasks.env ]; then
  install -m 0600 -o root -g root "$HERE/tasks.env.example" /etc/tasks/tasks.env
  echo "    создан /etc/tasks/tasks.env (0600 root:root) — отредактируйте секреты!"
else
  echo "    /etc/tasks/tasks.env уже существует — не трогаю"
fi

echo "==> Установка unit-файла /etc/systemd/system/tasks.service"
install -m 0644 -o root -g root "$HERE/tasks.service" /etc/systemd/system/tasks.service

echo "==> systemctl daemon-reload + enable"
systemctl daemon-reload
systemctl enable tasks

echo "==> Готово. Положите бинарник в /opt/tasks/tasks и выполните: systemctl start tasks"
