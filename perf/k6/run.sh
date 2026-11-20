#!/usr/bin/env bash
# Run the SOS-Allert read-mix load test and rebuild the dashboard.
#
#   SOS_USER=<account> SOS_PW=<password> ./run.sh   full run (~100s, 2 → 10 req/s)
#   SOS_TOKEN=<session-token> ./run.sh              reuse an existing session
#   SMOKE=1 ./run.sh                                sanity pass (~10s, 2 req/s)
#
# Requires: k6 in PATH, python3, and (for the PNG) headless Chrome.
set -euo pipefail
cd "$(dirname "$0")"

K6=${K6:-k6}
BASE_URL=${BASE_URL:-https://api.allert.my.id}

if [[ -z "${SOS_TOKEN:-}" ]]; then
  : "${SOS_USER:?set SOS_USER or SOS_TOKEN}" "${SOS_PW:?set SOS_PW or SOS_TOKEN}"
  # One login for the whole run: the auth limiter is per-IP (5/min), so the
  # token is fetched once here and shared by every VU.
  SOS_TOKEN=$(BASE_URL="$BASE_URL" SOS_USER="$SOS_USER" SOS_PW="$SOS_PW" python3 - <<'PY'
import json, os, subprocess, sys

base = os.environ["BASE_URL"]
body = json.dumps({"id": os.environ["SOS_USER"], "password": os.environ["SOS_PW"]})
p = subprocess.run(
    ["curl", "-s", "--max-time", "25", "-X", "POST", f"{base}/api/users/_login",
     "-H", "Content-Type: application/json", "--data-binary", body, "-w", "\n%{http_code}"],
    capture_output=True, text=True)
out, _, code = p.stdout.rpartition("\n")
if code != "200":
    sys.exit(f"login failed: HTTP {code}")
print(json.loads(out)["data"]["token"])
PY
)
  export SOS_TOKEN
fi

rm -f results.json summary.json report.html dashboard.png
K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=report.html K6_WEB_DASHBOARD_PERIOD=1s \
  "$K6" run --out json=results.json k6-sos-api.js

python3 build-dashboard.py results.json summary.json dashboard.html
if command -v google-chrome >/dev/null; then
  google-chrome --headless=new --no-sandbox --disable-gpu --hide-scrollbars \
    --window-size=1560,3000 --virtual-time-budget=5000 \
    --screenshot=dashboard.png "file://$PWD/dashboard.html" >/dev/null 2>&1
fi
echo "artifacts: summary.json results.json report.html dashboard.html dashboard.png"
