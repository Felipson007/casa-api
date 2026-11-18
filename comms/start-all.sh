#!/usr/bin/env bash
# Sobe o chat bus + os dois listeners (Hermes e Claude) de uma vez.
# Uso: bash comms/start-all.sh
# Nota: MSYS_NO_PATHCONV evita dupla-conversao de /c/ no git-bash (Windows).
export MSYS_NO_PATHCONV=1
# caminho absoluto deste script, depois sobe 1 nivel (repo root)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"
echo "root: $ROOT"

if curl -s -o /dev/null -m 1 http://127.0.0.1:8753/ ; then
  echo "bus ja esta rodando em :8753 — abra http://127.0.0.1:8753/ (use stop-all para derrubar)"
  exit 0
fi

echo "=> bus"
python "$SCRIPT_DIR/bus.py" &
echo "=> listener hermes"
python "$SCRIPT_DIR/agent_listen.py" --agent hermes &
echo "=> listener claude"
python "$SCRIPT_DIR/agent_listen.py" --agent claude &

echo "prontos. UI: http://127.0.0.1:8753/  (Ctrl-C encerra todos)"
wait
