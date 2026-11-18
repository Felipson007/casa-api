import urllib.request, json, uuid

BASE = "http://127.0.0.1:8080"
OWNER = "6d27dba7-8ad1-4af9-b8d7-cf53a8fd7ba9"
MEMBER = "0dce48e1-835a-461d-983d-8cb5107cd083"

def req(method, path, token=None, body=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE+path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r, timeout=10) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()

def get_id(obj):
    if isinstance(obj, dict):
        return obj.get("id") or obj.get("ID") or obj.get("Id")
    return None

results = []
def check(name, method, path, token=None, body=None, expect=None):
    status, text = req(method, path, token, body)
    ok = (expect is None) or (status == expect)
    results.append((ok, name, status, text[:90]))
    print(f"[{'OK' if ok else 'XX'}] {name}: {status} {text[:70]}")

print("=== AUTH ===")
check("no-token", "GET", "/api/categories", expect=401)
check("bad-token", "GET", "/api/categories", token="x", expect=401)
check("owner-ok", "GET", "/api/categories", token=OWNER, expect=200)

print("\n=== CATEGORIES ===")
check("create", "POST", "/api/categories", token=OWNER, body={"name":"SweepCat","color":"#fff","active":True}, expect=201)
s,t = req("GET","/api/categories",token=OWNER); cid=get_id(json.loads(t)[-1]) if s==200 else None
check("delete", "DELETE", f"/api/categories/{cid}", token=OWNER, expect=204) if cid else None

print("\n=== PEOPLE ===")
check("list", "GET", "/api/people", token=OWNER, expect=200)
check("create", "POST", "/api/people", token=OWNER, body={"name":"Sweep Person","gender":"M"}, expect=201)
s,t = req("GET","/api/people",token=OWNER); pid=get_id(json.loads(t)[-1]) if s==200 else None

print("\n=== CLIENTS ===")
check("list", "GET", "/api/clients", token=OWNER, expect=200)
check("create", "POST", "/api/clients", token=OWNER, body={"name":"Sweep Client","personId":pid}, expect=201) if pid else None
s,t = req("GET","/api/clients",token=OWNER); clid=get_id(json.loads(t)[-1]) if s==200 else None

print("\n=== LOANS (part 2) ===")
check("list", "GET", "/api/loans", token=OWNER, expect=200)
if clid and pid:
    check("create-installment", "POST", "/api/loans", token=OWNER, body={"clientId":clid,"principalAmount":"1000.00","monthlyInterestRate":"10","paymentType":"INSTALLMENT","installmentCount":3,"contractDate":"2026-08-01T00:00:00Z","dueDay":5}, expect=201)
    s,t = req("GET","/api/loans",token=OWNER); lnid=get_id(json.loads(t)[-1]) if s==200 else None
    check("get-loan", "GET", f"/api/loans/{lnid}", token=OWNER, expect=200) if lnid else None
    check("installments", "GET", f"/api/loans/{lnid}/installments", token=OWNER, expect=200) if lnid else None
    check("transactions", "GET", f"/api/loans/{lnid}/transactions", token=OWNER, expect=200) if lnid else None

print("\n=== EXPENSES ===")
check("create", "POST", "/api/expenses", token=OWNER, body={"amount":"100.00","expenseDate":"2026-08-10T00:00:00Z","description":"sweep","recurring":False}, expect=201)
check("member-create", "POST", "/api/expenses", token=MEMBER, body={"amount":"50.00","expenseDate":"2026-08-10T00:00:00Z","description":"membro","recurring":False}, expect=201)
check("member-list", "GET", "/api/expenses", token=MEMBER, expect=200)

print("\n=== INCOME ===")
check("create", "POST", "/api/income", token=OWNER, body={"amount":"200.00","incomeDate":"2026-08-10T00:00:00Z","description":"sweep","recurring":False}, expect=201)

print("\n=== ASSETS ===")
check("create", "POST", "/api/assets", token=OWNER, body={"personId":pid,"name":"Sweep Asset","categoryId":None,"acquisitionValue":"5000.00","currentEstimatedValue":"5500.00","attributes":[]}, expect=201) if pid else None

print("\n=== DOCUMENTS ===")
check("list", "GET", "/api/documents", token=OWNER, expect=200)

print("\n=== PETS ===")
check("create", "POST", "/api/pets", token=OWNER, body={"name":"SweepPet","species":"DOG","breed":"Vira","gender":"MALE","neutered":False,"weightKg":"10.5"}, expect=201)

print("\n=== HEALTH ===")
check("markers", "GET", "/api/health/markers", token=OWNER, expect=200)
check("records", "GET", "/api/health/records", token=OWNER, expect=200)

print("\n=== NOTIFICATIONS ===")
check("list", "GET", "/api/notifications", token=OWNER, expect=200)
check("config", "GET", "/api/notifications/configuration", token=OWNER, expect=200)

print("\n=== CHAT ===")
check("create", "POST", "/api/chat/messages", token=OWNER, body={"role":"USER","content":"sweep"}, expect=201)
check("list", "GET", "/api/chat/messages", token=OWNER, expect=200)
check("delete", "DELETE", "/api/chat/messages", token=OWNER, expect=204)

print("\n=== AUDIT ===")
check("create", "POST", "/api/audit-logs", token=OWNER, body={"tableName":"people","rowId":str(uuid.uuid4()),"oldData":{"x":1}}, expect=201)
check("list", "GET", "/api/audit-logs", token=OWNER, expect=200)

print("\n=== FINANCE ===")
check("create", "POST", "/api/finance/balances", token=OWNER, body={"name":"SweepBal","type":"FGTS","currentValue":"1000","updatedOn":"2026-08-09T00:00:00Z"}, expect=201)
s,t = req("GET","/api/finance/balances",token=OWNER); bal=get_id(json.loads(t)[-1]) if s==200 else None
check("history", "GET", f"/api/finance/balances/{bal}/history", token=OWNER, expect=200) if bal else None
if bal: req("DELETE", f"/api/finance/balances/{bal}", token=OWNER)

print("\n=== INTEGRATIONS ===")
check("list-external", "GET", "/api/integrations/external", token=OWNER, expect=200)
check("list-system", "GET", "/api/integrations/system", token=OWNER, expect=200)
check("list-ai", "GET", "/api/integrations/ai-configuration", token=OWNER, expect=200)

print("\n=== SUMMARY ===")
fails = [r for r in results if not r[0]]
print(f"Total: {len(results)} | Fails: {len(fails)}")
for f in fails:
    print(f"  FAIL: {f[1]} -> {f[2]} {f[3]}")
