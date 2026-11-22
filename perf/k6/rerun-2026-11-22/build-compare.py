#!/usr/bin/env python3
"""Before/after comparison dashboard for the SOS-Allert k6 read mix."""
import json, re, html, sys, os

BEFORE = sys.argv[1] if len(sys.argv) > 1 else "/home/ubuntu/devfest/backend/perf/k6/summary.json"
AFTER = sys.argv[2] if len(sys.argv) > 2 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "summary.json")
OUT = sys.argv[3] if len(sys.argv) > 3 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "compare.html")

CLS = {
    "/api/push/vapid-public-key": ("config", "#4ade80"),
    "/api/location": ("redis", "#fbbf24"),
    "/api/location/nearby": ("redis", "#fbbf24"),
    "/api/users/_current": ("mysql", "#5b9bf8"),
    "/api/contacts": ("mysql", "#5b9bf8"),
    "/api/emergency-contacts": ("mysql", "#5b9bf8"),
    "/api/push/subscriptions": ("mysql", "#5b9bf8"),
    "/api/sos/active": ("mysql", "#5b9bf8"),
    "/api/sos/inbox": ("mysql", "#5b9bf8"),
    "/api/sos/history": ("mysql", "#5b9bf8"),
    "/api/sos/summary": ("mysql", "#5b9bf8"),
}
BG, PANEL, BORDER, GRID = "#0b0d10", "#12161b", "#242a33", "#1c222a"
TXT, TXT2, TXT3 = "#d5dae2", "#7d8794", "#4c5561"
GREEN, RED, BLUE, ORANGE = "#4ade80", "#f87171", "#5b9bf8", "#fb923c"
MONO = '"JetBrains Mono","SF Mono","DejaVu Sans Mono",Menlo,Consolas,monospace'
SANS = '-apple-system,"Segoe UI",Roboto,"DejaVu Sans",sans-serif'

mb = json.load(open(BEFORE))["metrics"]
ma = json.load(open(AFTER))["metrics"]
key = lambda p: "ep_" + re.sub(r"[^a-z0-9]+", "_", p.lstrip("/"))

rows = []
for p, (cls, color) in CLS.items():
    b, a = mb[key(p)]["values"], ma[key(p)]["values"]
    rows.append(dict(path=p, cls=cls, color=color, **{f"b_{k}": b[k] for k in ("count", "avg", "p(95)", "p(99)", "max")},
                     **{f"a_{k}": a[k] for k in ("count", "avg", "p(95)", "p(99)", "max")}))
rows.sort(key=lambda r: r["b_avg"] - r["a_avg"], reverse=True)

ob, oa = mb["http_req_duration"]["values"], ma["http_req_duration"]["values"]
fail_b, fail_a = mb["http_req_failed"]["values"]["rate"], ma["http_req_failed"]["values"]["rate"]
vus_b, vus_a = mb["vus_max"]["values"]["max"], ma["vus_max"]["values"]["max"]


def clsave():
    out = {}
    for c in ("config", "redis", "mysql"):
        for tag, m in (("b", "b"), ("a", "a")):
            n = sum(r[f"{tag}_count"] for r in rows if r["cls"] == c)
            tot = sum(r[f"{tag}_count"] * r[f"{tag}_avg"] for r in rows if r["cls"] == c)
            out[(c, tag)] = (n, tot / n if n else 0)
    return out


CA = clsave()
mx = max(max(r["b_avg"], r["a_avg"]) for r in rows)

# ---- per-route grouped bars
bar_rows = ""
for r in rows:
    wb, wa = r["b_avg"] / mx * 100, r["a_avg"] / mx * 100
    d = (r["a_avg"] - r["b_avg"]) / r["b_avg"] * 100
    dc = GREEN if d < -10 else (ORANGE if d < 0 else RED)
    short = r["path"].replace("/api/", "")
    bar_rows += f"""
    <div class="rrow">
      <div class="rlab"><i style="background:{r['color']}"></i><span>{html.escape(short)}</span></div>
      <div class="rbars">
        <div class="bar b"><span style="width:{wb:.1f}%"></span><em>{r['b_avg']:.0f}</em></div>
        <div class="bar a"><span style="width:{wa:.1f}%"></span><em>{r['a_avg']:.0f}</em></div>
      </div>
      <div class="rdelta" style="color:{dc}">{d:+.0f}%</div>
    </div>"""

# ---- percentile table
pt = ""
for lab, kb, ka in (("avg", "avg", "avg"), ("p50", "med", "med"), ("p90", "p(90)", "p(90)"),
                    ("p95", "p(95)", "p(95)"), ("p99", "p(99)", "p(99)"), ("max", "max", "max")):
    vb, va = ob[kb], oa[ka]
    d = (va - vb) / vb * 100
    dc = GREEN if d < -10 else (ORANGE if d < 0 else RED)
    pt += f"<tr><td>{lab}</td><td>{vb:.0f}</td><td>{va:.0f}</td><td style='color:{dc}'>{d:+.0f}%</td></tr>"

# ---- class table
ct = ""
for c, col in (("config", GREEN), ("redis", "#fbbf24"), ("mysql", BLUE)):
    nb, ab = CA[(c, "b")]
    na, aa = CA[(c, "a")]
    d = (aa - ab) / ab * 100
    dc = GREEN if d < -10 else (ORANGE if d < 0 else RED)
    ct += f"""<tr><td><i style="background:{col}"></i>{c}</td><td>{nb:.0f}</td><td>{ab:.0f}</td>
    <td>{na:.0f}</td><td>{aa:.0f}</td><td style="color:{dc}">{d:+.0f}%</td>
    <td>{aa - ab:+.0f} ms</td></tr>"""

# ---- phase table
ph = (("blocked", "http_req_blocked"), ("connecting", "http_req_connecting"),
      ("tls", "http_req_tls_handshaking"), ("sending", "http_req_sending"),
      ("waiting (TTFB)", "http_req_waiting"), ("receiving", "http_req_receiving"))
pht = ""
for lab, k in ph:
    vb = mb.get(k, {}).get("values", {}).get("avg", 0)
    va = ma.get(k, {}).get("values", {}).get("avg", 0)
    pht += f"<tr><td>{lab}</td><td>{vb:.2f}</td><td>{va:.2f}</td><td>{va - vb:+.2f}</td></tr>"

verdict = "PASS" if fail_a < 0.02 and oa["p(95)"] < 2500 and oa["p(99)"] < 4000 else "FAIL"

doc = f"""<!doctype html><meta charset="utf-8"><title>SOS-Allert k6 — before / after #59, #61</title>
<style>
*{{box-sizing:border-box}}
body{{margin:0;background:{BG};color:{TXT};font-family:{SANS};padding:34px 40px 46px}}
h1{{font-size:21px;font-weight:600;margin:0 0 4px}}
.sub{{color:{TXT2};font-size:12.5px;margin-bottom:22px}}
.meta{{display:flex;flex-wrap:wrap;gap:0;border:1px solid {BORDER};background:{PANEL};margin-bottom:24px}}
.meta div{{padding:11px 16px;border-right:1px solid {BORDER};min-width:132px}}
.meta b{{display:block;font-family:{MONO};font-size:12.5px;margin-top:4px;color:{TXT}}}
.meta span{{color:{TXT3};font-size:10px;letter-spacing:.09em;text-transform:uppercase}}
.grid{{display:grid;grid-template-columns:1fr 1fr;gap:20px;margin-bottom:22px}}
.card{{border:1px solid {BORDER};background:{PANEL};padding:16px 18px}}
.card h2{{font-size:11px;letter-spacing:.11em;text-transform:uppercase;color:{TXT2};margin:0 0 13px;font-weight:600}}
.card.wide{{grid-column:1/-1}}
table{{width:100%;border-collapse:collapse;font-family:{MONO};font-size:12.5px}}
th{{text-align:right;color:{TXT3};font-weight:500;font-size:10px;letter-spacing:.08em;text-transform:uppercase;
   padding:0 0 7px;border-bottom:1px solid {BORDER}}}
th:first-child,td:first-child{{text-align:left}}
td{{padding:6px 0;border-bottom:1px solid {GRID};text-align:right}}
tr:last-child td{{border-bottom:none}}
td i{{display:inline-block;width:7px;height:7px;border-radius:1px;margin-right:7px;vertical-align:middle}}
.rrow{{display:grid;grid-template-columns:184px 1fr 58px;align-items:center;gap:12px;padding:5px 0;border-bottom:1px solid {GRID}}}
.rrow:last-child{{border-bottom:none}}
.rlab{{display:flex;align-items:center;font-family:{MONO};font-size:11.5px;color:{TXT}}}
.rlab i{{width:7px;height:7px;border-radius:1px;margin-right:7px;flex:none}}
.rlab span{{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}}
.bar{{position:relative;height:11px;background:{GRID};margin:2px 0}}
.bar span{{display:block;height:100%}}
.bar.b span{{background:#465060}}
.bar.a span{{background:{GREEN}}}
.bar em{{position:absolute;right:0;top:-3px;transform:translateX(calc(100% + 8px));font-style:normal;
   font-family:{MONO};font-size:10.5px;color:{TXT2}}}
.rbars{{padding-right:34px}}
.rdelta{{text-align:right;font-family:{MONO};font-size:12px}}
.legend{{display:flex;gap:18px;font-family:{MONO};font-size:11px;color:{TXT2};margin-bottom:12px}}
.legend i{{display:inline-block;width:9px;height:9px;border-radius:1px;margin-right:6px;vertical-align:middle}}
.pill{{display:inline-block;padding:3px 10px;border:1px solid {GREEN};color:{GREEN};font-family:{MONO};
   font-size:11px;letter-spacing:.07em}}
.note{{color:{TXT2};font-size:12px;line-height:1.55;margin-top:10px}}
</style>
<h1>SOS-Allert API — read mix, before vs after</h1>
<div class="sub">backend <b style="font-family:{MONO}">#59 / #61</b> — batched detail reads on active &amp; inbox, roster JOIN, viewer resolved once &nbsp;·&nbsp; same script, same 2 → 10 req/s ramp, same account</div>
<div class="meta">
  <div><span>before commit</span><b>0b04ac1</b></div>
  <div><span>after commit</span><b>94d5cf8</b></div>
  <div><span>requests</span><b>{ob['count']:.0f} → {oa['count']:.0f}</b></div>
  <div><span>window</span><b>100 s × 2</b></div>
  <div><span>avg VUs</span><b>{vus_b:.0f} → {vus_a:.0f} max</b></div>
  <div><span>failed</span><b>{fail_b*100:.2f}% → {fail_a*100:.2f}%</b></div>
  <div><span>thresholds</span><b>p95 {oa["p(95)"]:.0f}&lt;2500 · p99 {oa["p(99)"]:.0f}&lt;4000</b><b><span class="pill">{verdict}</span></b></div>
</div>

<div class="card wide">
  <h2>Per-route average latency — before (grey) vs after (green)</h2>
  <div class="legend"><span><i style="background:#465060"></i>0b04ac1</span>
  <span><i style="background:{GREEN}"></i>94d5cf8</span><span style="color:{TXT3}">bar label = avg ms</span><span style="color:{TXT3};margin-left:auto">scale 0 – {mx:.0f} ms</span></div>
  {bar_rows}
</div>

<div class="grid">
  <div class="card">
    <h2>Overall latency (ms)</h2>
    <table><tr><th>stat</th><th>before</th><th>after</th><th>delta</th></tr>{pt}</table>
    <div class="note">All of the drop sits in <b>waiting</b> (time to first byte). Connection setup,
    TLS and receiving are unchanged — the handlers simply answer sooner.</div>
  </div>
  <div class="card">
    <h2>By what the route must touch</h2>
    <table><tr><th>class</th><th>n b</th><th>avg b</th><th>n a</th><th>avg a</th><th>delta</th><th>abs</th></tr>{ct}</table>
    <div class="note">mysql … the class the PR touched: <b>{CA[('mysql','b')][1]:.0f} → {CA[('mysql','a')][1]:.0f} ms</b>
    ({((CA[('mysql','a')][1]-CA[('mysql','b')][1])/CA[('mysql','b')][1]*100):+.0f}%).
    redis routes were not touched and moved only ~{((CA[('redis','a')][1]-CA[('redis','b')][1])/CA[('redis','b')][1]*100):+.0f}%.</div>
  </div>
  <div class="card wide">
    <h2>Where the milliseconds go (avg per request)</h2>
    <table><tr><th>phase</th><th>before</th><th>after</th><th>delta</th></tr>{pht}</table>
  </div>
</div>
"""
open(OUT, "w").write(doc)
print("wrote", OUT, len(doc), "bytes;", len(rows), "routes; verdict", verdict)
