#!/usr/bin/env bash
set -Eeuo pipefail

TARGET_USER="${1:-${SUDO_USER:-$USER}}"

sudo dnf install -y ca-certificates cronie curl git dnf-plugins-core

if ! sudo dnf repolist --all | grep -q '^docker-ce-stable'; then
  sudo dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
fi

sudo dnf install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker
sudo systemctl enable --now crond
sudo usermod -aG docker "$TARGET_USER"

sudo install -d -m 0750 -o "$TARGET_USER" -g "$TARGET_USER" /opt/nova
sudo install -d -m 0700 -o "$TARGET_USER" -g "$TARGET_USER" /opt/nova/backups

if sudo systemctl is-active --quiet firewalld; then
  sudo firewall-cmd --permanent --add-service=http
  sudo firewall-cmd --permanent --add-service=https
  sudo firewall-cmd --reload
fi

sudo docker version
sudo docker compose version

echo "Oracle Linux host is ready. Reconnect over SSH before running Docker as $TARGET_USER."
