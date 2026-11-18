#!/usr/bin/env bash
# Derruba todos os processos do comms (bus + listeners) pela porta 8753 / nome do script.
set -e
echo "=> matando bus na porta 8753"
# Windows (git-bash) e Linux: acha quem ouve 8753 e mata
if command -v fuser >/dev/null 2>&1; then fuser -k 8753/tcp 2>/dev/null || true; fi
pkill -f "comms/bus.py" 2>/dev/null || true
pkill -f "comms/agent_listen.py" 2>/dev/null || true
sleep 1
echo "encerrrado."
