#!/usr/bin/env python3
"""
Agent listener for the local bus — lets an agent HEAR new messages and decide whether to reply.

Each agent runs its own instance:
    python comms/agent_listen.py --agent hermes
    python comms/agent_listen.py --agent claude

What it does:
  - polls GET /recv?agent=<agent>&since=<last_id> every --interval seconds
  - for each NEW message addressed to <agent> or "all", calls on_message(msg)
  - on_message default: prints the message and appends to comms/inbox-<agent>.txt
    (the real agent — Hermes or Claude — reads that inbox and decides to reply via /send)
  - tracks last seen id in comms/state-<agent>.json so restarts don't re-fire

Extending: subclass AgentListener and override on_message(msg) to plug your own
decision/reply logic (e.g. call an LLM, then POST a reply). Keep it side-effect free
except for intentional replies.

Why this exists: the two agents must know when someone spoke to them, without either
editing a shared git file. The bus is the single source of truth; this is the ear.
"""
import argparse
import json
import os
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
BUS = "http://127.0.0.1:8753"
STATE = os.path.join(HERE, "state-{agent}.json")
INBOX = os.path.join(HERE, "inbox-{agent}.txt")


class AgentListener:
    def __init__(self, agent, base=BUS, interval=2.0):
        self.agent = agent
        self.base = base
        self.interval = interval
        self.state_path = STATE.format(agent=agent)
        self.inbox_path = INBOX.format(agent=agent)
        self.last_id = self._load_state()

    def _load_state(self):
        if os.path.exists(self.state_path):
            try:
                return json.load(open(self.state_path, encoding="utf-8")).get("last_id", 0)
            except Exception:
                return 0
        return 0

    def _save_state(self):
        with open(self.state_path, "w", encoding="utf-8") as f:
            json.dump({"last_id": self.last_id}, f)

    def _get(self, path):
        with urllib.request.urlopen(self.base + path, timeout=5) as r:
            return json.loads(r.read().decode("utf-8"))

    def poll_once(self):
        try:
            data = self._get(f"/recv?agent={self.agent}&since={self.last_id}")
        except Exception as e:
            print(f"[{self.agent}] recv error: {e}")
            return []
        msgs = data.get("messages", [])
        for m in msgs:
            if m["id"] > self.last_id:
                self.last_id = m["id"]
            self.on_message(m)
        if msgs:
            self._save_state()
        return msgs

    def on_message(self, msg):
        """Override me. Default: print + append to inbox so the agent knows to reply."""
        line = f"[{msg.get('at','')[:19]}] {msg.get('from')} -> {msg.get('to')}: {msg.get('text')}\n"
        print(line, end="")
        with open(self.inbox_path, "a", encoding="utf-8") as f:
            f.write(line)
        # Heuristic flag: does this need a reply? (very rough; the agent decides for real)
        needs = msg.get("to") == self.agent or msg.get("from") != self.agent and (
            "?" in msg.get("text", "") or any(k in msg.get("text", "").lower()
            for k in ["confirma", "pode", "comecar", "revisa", "responde", "agree", "discorda"])
        )
        if needs:
            with open(self.inbox_path, "a", encoding="utf-8") as f:
                f.write(f"    ^ precisa resposta? SIM (heuristica)\n")

    def run(self):
        print(f"[{self.agent}] listening on {self.base} (last_id={self.last_id})")
        try:
            while True:
                self.poll_once()
                time.sleep(self.interval)
        except KeyboardInterrupt:
            print(f"[{self.agent}] stopped")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--agent", required=True, choices=["hermes", "claude", "user", "sys", "all"])
    ap.add_argument("--base", default=BUS)
    ap.add_argument("--interval", type=float, default=2.0)
    a = ap.parse_args()
    AgentListener(a.agent, base=a.base, interval=a.interval).run()


if __name__ == "__main__":
    main()
