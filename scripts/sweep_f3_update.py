import json, datetime, urllib.request

BASE = "http://127.0.0.1:8099"
OWNER = "6d27dba7-8ad1-4af9-b8d7-cf53a8fd7ba9"

def req(method, path, body=None):
    h = {"Content-Type": "application/json", "Authorization": f"Bearer {OWNER}"}
    d = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE+path, data=d, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=20) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()

today = datetime.date.today().isoformat()
ts = today + "T00:00:00Z"
fails = 0

def put_check(name, coll, pick, body):
    global fails
    s, t = req("GET", coll)
    lst = json.loads(t)
    if isinstance(lst, dict):
        lst = lst.get("items", [])
    item = next((x for x in lst if pick(x)), None)
    if not item:
        print(f"[--] {name}: no fixture found"); return
    pid = item["id"]
    s, t = req("PUT", f"{coll}/{pid}", body=body)
    if s != 200:
        print(f"[XX] {name}: PUT {s} {t[:160]}"); fails += 1; return
    p = json.loads(t)
    gs, gt = req("GET", f"{coll}/{pid}")
    g = json.loads(gt) if gs == 200 else next(x for x in json.loads(req("GET", coll)[1]) if x["id"] == pid)
    diffs = [f"{k}: PUT={v!r} GET={g[k]!r}" for k, v in p.items() if k in g and g[k] != v]
    if diffs:
        print(f"[XX] {name}: " + " | ".join(diffs)); fails += 1
    else:
        print(f"[OK] {name}: PUT body == GET")

put_check("finance-balance", "/api/finance/balances", lambda x: x["name"].startswith("F3"),
          {"name": "F3 Cofrinho upd", "type": "SAVINGS", "currentValue": "2000", "updatedOn": ts})
put_check("expenses", "/api/expenses", lambda x: (x.get("description") or "").startswith("F3"),
          {"amount": "999", "expenseDate": ts, "description": "F3 Expense upd"})
put_check("income", "/api/income", lambda x: (x.get("description") or "").startswith("F3"),
          {"amount": "8800", "incomeDate": ts, "description": "F3 Income upd"})
put_check("assets", "/api/assets", lambda x: x["name"].startswith("F3"),
          {"name": "F3 Asset upd", "type": "VEHICLE", "acquisitionValue": "60000", "status": "ACTIVE"})
print(f"\nUPDATE SWEEP FAILS: {fails}")
