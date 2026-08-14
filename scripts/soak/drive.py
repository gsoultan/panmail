"""Fires N sends at the API concurrently and reports what the API did."""
import json, sys, threading, time, urllib.request, collections

BASE, TOKEN, PROVIDER = sys.argv[1], sys.argv[2], sys.argv[3]
TOTAL, CONC = int(sys.argv[4]), int(sys.argv[5])

codes = collections.Counter()
lock = threading.Lock()
work = list(range(TOTAL))
idx = 0

def post(path, payload):
    req = urllib.request.Request(BASE + path, data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + TOKEN})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status
    except urllib.error.HTTPError as e:
        return e.code
    except Exception:
        return 0

def worker():
    global idx
    while True:
        with lock:
            if idx >= TOTAL: return
            i = idx; idx += 1
        c = post("/panmail.v1.EmailService/SendEmail", {
            "providerId": PROVIDER, "from": "soak@example.com",
            "to": ["r%d@example.com" % i], "subject": "soak %d" % i,
            "body": "<p>soak body %d</p>" % i,
        })
        with lock:
            codes[c] += 1

start = time.time()
threads = [threading.Thread(target=worker) for _ in range(CONC)]
for t in threads: t.start()
for t in threads: t.join()
elapsed = time.time() - start

print(json.dumps({
    "sent": TOTAL, "seconds": round(elapsed, 1),
    "accepts_per_second": round(TOTAL/elapsed, 1),
    "codes": dict(codes),
}))
