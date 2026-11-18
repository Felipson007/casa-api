#!/usr/bin/env python3
"""Watcher que acorda o Hermes quando chega mensagem nova no bus.

RoDa em background. A cada INTERVAL segundos poll GET /recv?agent=hermes&since=<last_id>.
Quando aparece mensagem nova dirigida a 'hermes' ou 'all', imprime:
    WAKE <from> -> <to>: <texto>
e grava o texto em comms/_wake_hermes.txt (append) para o Hermes ler mesmo se o stdout
do processo nao for entregue. Mantem last_id em comms/state-watch-hermes.json.

Resiliente: nunca quebra em erro de rede (continua no proximo ciclo). Para reiniciar do zero,
apague comms/state-watch-hermes.json.

Uso: python comms/watch_hermes.py [--interval 5]
"""
import argparse
import json
import os
import sys
import time
import urllib.request
import urllib.error

HERE = os.path.dirname(os.path.abspath(__file__))
STATE = os.path.join(HERE, "state-watch-hermes.json")
SIGNAL = os.path.join(HERE, "_wake_hermes.txt")
BASE = "http://127.0.0.1:8753"


def load_last():
    try:
        with open(STATE) as f:
            return int(json.load(f).get("last_id", 0))
    except Exception:
        return 0


def save_last(last_id):
    try:
        with open(STATE, "w") as f:
            json.dump({"last_id": last_id}, f)
    except Exception:
        pass


def recv(agent, since):
    url = f"{BASE}/recv?agent={agent}&since={since}"
    try:
        with urllib.request.urlopen(url, timeout=5) as r:
            return json.loads(r.read().decode())
    except Exception:
        return {"messages": []}


def signal(text):
    try:
        with open(SIGNAL, "a", encoding="utf-8") as f:
            f.write(text + "\n")
    except Exception:
        pass


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--interval", type=int, default=5)
    args = ap.parse_args()

    last_id = load_last()
    print(f"[watch-hermes] iniciado (last_id={last_id}, interval={args.interval}s)", flush=True)
    while True:
        try:
            data = recv("hermes", last_id)
            for m in data.get("messages", []):
                mid = m.get("id", 0)
                if mid > last_id:
                    last_id = mid
                    to = m.get("to")
                    if to in ("hermes", "all"):
                        text = (m.get("text") or "").replace("\n", " ")
                        # sinal para o Hermes ler mesmo sem stdout
                        signal(f"WAKE {m.get('from')} -> {to}: {text[:200]}")
                        # tambem imprime no stdout (notificacao do processo)
                        print(f"WAKE {m.get('from')} -> {to}: {text[:200]}", flush=True)
                        sys.stdout.flush()
            save_last(last_id)
        except Exception:
            pass
        time.sleep(args.interval)


if __name__ == "__main__":
    main()
