"""A minimal SMTP sink that counts what it receives.

Not a mail server: it speaks just enough of RFC 5321 to accept a message and
record it. That is all a soak needs, and it removes the real provider from the
measurement — what is being tested is panmail's queue, not someone's SMTP.
"""
import socket, threading, sys, json, time, signal

count = 0
lock = threading.Lock()
first = None
last = None

def handle(conn):
    global count, first, last
    f = conn.makefile('rwb')
    def send(s): f.write((s + "\r\n").encode()); f.flush()
    send("220 sink ESMTP")
    data_mode = False
    try:
        while True:
            line = f.readline()
            if not line:
                return
            s = line.decode('utf-8', 'replace').strip()
            if data_mode:
                if s == ".":
                    data_mode = False
                    with lock:
                        count += 1
                        now = time.time()
                        if first is None: first = now
                        last = now
                    send("250 OK queued")
                continue
            u = s.upper()
            if u.startswith("EHLO") or u.startswith("HELO"):
                # AUTH is advertised because the provider under test has
                # credentials configured, and gsmail refuses to send to a
                # server that cannot take them.
                send("250-sink"); send("250-SIZE 10485760")
                send("250-AUTH PLAIN LOGIN"); send("250 8BITMIME")
            elif u.startswith("AUTH"):
                if "LOGIN" in u and len(s.split()) == 2:
                    send("334 VXNlcm5hbWU6")   # Username:
                    f.readline()
                    send("334 UGFzc3dvcmQ6")   # Password:
                    f.readline()
                send("235 2.7.0 Authentication successful")
            elif u.startswith("MAIL FROM") or u.startswith("RCPT TO"):
                send("250 OK")
            elif u == "DATA":
                send("354 End data with <CRLF>.<CRLF>"); data_mode = True
            elif u == "QUIT":
                send("221 Bye"); return
            elif u.startswith("RSET") or u.startswith("NOOP"):
                send("250 OK")
            else:
                send("250 OK")
    except Exception:
        pass
    finally:
        try: conn.close()
        except Exception: pass

def report(*_):
    with lock:
        rate = 0.0
        if first and last and last > first:
            rate = count / (last - first)
        print(json.dumps({"accepted": count, "seconds": round((last-first) if first and last else 0, 1),
                          "per_second": round(rate, 1)}))
    sys.exit(0)

signal.signal(signal.SIGTERM, report)
signal.signal(signal.SIGINT, report)

srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 2599))
srv.listen(256)
print("sink listening", flush=True)
while True:
    c, _ = srv.accept()
    threading.Thread(target=handle, args=(c,), daemon=True).start()
