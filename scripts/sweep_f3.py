import urllib.request, json, datetime

BASE = "http://127.0.0.1:8099"
OWNER = "6d27dba7-8ad1-4af9-b8d7-cf53a8fd7ba9"

def req(method, path, token=OWNER, body=None):
    h = {"Content-Type": "application/json"}
    if token: h["Authorization"] = f"Bearer {token}"
    d = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE+path, data=d, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=20) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()

def jget(path):
    s, t = req("GET", path)
    return s, (json.loads(t) if s == 200 and t.strip() else None)

fails, oks = [], []

def compare(name, post_obj, get_obj):
    diffs = []
    for k, v in post_obj.items():
        if k not in get_obj: continue
        if get_obj[k] != v:
            diffs.append(f"{k}: POST={v!r} GET={get_obj[k]!r}")
    ca = post_obj.get("createdAt")
    if ca and ca.startswith("0001-01-01"):
        diffs.append("createdAt is ZERO VALUE in POST")
    if diffs:
        fails.append((name, diffs)); print(f"[XX] {name}: " + " | ".join(diffs))
    else:
        oks.append(name); print(f"[OK] {name}: 201 body == GET (createdAt={ca})")

def run(name, coll, body, item_path=None):
    s, t = req("POST", coll, body=body)
    if s not in (200, 201):
        fails.append((name, [f"POST {s}: {t[:150]}"])); print(f"[XX] {name}: POST {s} {t[:150]}"); return None
    p = json.loads(t)
    pid = p.get("id")
    gs, g = jget((item_path or coll) + "/" + str(pid))
    if gs != 200:
        # fallback: find in list
        ls, lst = jget(coll)
        g = next((x for x in (lst or []) if x.get("id") == pid), None)
        if g is None:
            fails.append((name, [f"cannot re-read (GET/{{id}}={gs})"])); print(f"[XX] {name}: cannot re-read"); return pid
    compare(name, p, g)
    return pid

print("=== F-3 DELTA SWEEP (POST 201 body vs subsequent GET) ===")

_, people = jget("/api/people")
pid = (people[-1].get("id") or people[-1].get("ID")) if people else None
_, clients = jget("/api/clients")
clid = clients[-1]["id"] if clients else None
_, cats = jget("/api/categories")
catid = cats[-1]["id"] if cats else None
today = datetime.date.today().isoformat()

run("clients", "/api/clients", {"name": "F3 Client", "personId": pid})
ts = today + "T00:00:00Z"
run("expenses", "/api/expenses", {"description": "F3 Expense", "amount": "123.45", "expenseDate": ts, "categoryId": catid})
run("income", "/api/income", {"description": "F3 Income", "amount": "8700.00", "incomeDate": ts})
run("assets", "/api/assets", {"name": "F3 Asset", "type": "VEHICLE", "acquisitionValue": "50000.00"})
run("documents", "/api/documents", {"name": "F3 Doc", "origin": "PERSONAL", "originalFilename": "f3.pdf", "filePath": "/tmp/f3.pdf", "fileType": "PDF"})
run("pets", "/api/pets", {"name": "Luffy F3", "species": "DOG"})
run("notifications", "/api/notifications", {"type": "WEEKLY_DIGEST", "title": "F3", "message": "f3 test"})
run("finance-balance", "/api/finance/balances", {"name": "F3 Cofrinho", "type": "SAVINGS", "currentValue": "1000", "updatedOn": ts})
run("chat", "/api/chat/messages", {"role": "USER", "content": "f3 ping"})
if clid:
    run("loans", "/api/loans", {"clientId": clid, "principalAmount": "1000.00", "monthlyInterestRate": "10", "paymentType": "INSTALLMENT", "installmentCount": 3, "contractDate": "2026-01-31T00:00:00Z", "dueDay": 31})

print("\n=== SUMMARY ===")
print(f"OK: {len(oks)}  FAIL: {len(fails)}")
for n, d in fails:
    print(f"  FAIL {n}: {'; '.join(d)}")
