#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
set -a
. ../../../.env
set +a
: "${DD_API_KEY:?set DD_API_KEY in .env}"
: "${DD_APP_KEY:?set DD_APP_KEY in .env}"
api="https://api.${DD_SITE:-datadoghq.eu}/api/v1"

call() {
	curl -sS -X "$1" "$api$2" \
		-H "Content-Type: application/json" \
		-H "DD-API-KEY: $DD_API_KEY" \
		-H "DD-APPLICATION-KEY: $DD_APP_KEY" \
		--data-binary @-
}

title="$(python3 -c 'import json; print(json.load(open("dashboard.json"))["title"])')"
existing="$(curl -sS "$api/dashboard" -H "DD-API-KEY: $DD_API_KEY" -H "DD-APPLICATION-KEY: $DD_APP_KEY" |
	python3 -c 'import json, sys; t = sys.argv[1]; print(next((d["id"] for d in json.load(sys.stdin).get("dashboards", []) if d["title"] == t), ""))' "$title")"
if [ -n "$existing" ]; then
	call PUT "/dashboard/$existing" < dashboard.json | python3 -c 'import json, sys; d = json.load(sys.stdin); print("dashboard updated:", d.get("url") or d)'
else
	call POST /dashboard < dashboard.json | python3 -c 'import json, sys; d = json.load(sys.stdin); print("dashboard created:", d.get("url") or d)'
fi

existing_monitors="$(curl -sS "$api/monitor?monitor_tags=project:anti-fraud" -H "DD-API-KEY: $DD_API_KEY" -H "DD-APPLICATION-KEY: $DD_APP_KEY")"
python3 - "$existing_monitors" <<'PY' | while IFS=$'\t' read -r method path body; do
import json, sys
existing = {m["name"]: m["id"] for m in json.loads(sys.argv[1])}
for monitor in json.load(open("monitors.json")):
    if monitor["name"] in existing:
        print("PUT", f"/monitor/{existing[monitor['name']]}", json.dumps(monitor), sep="\t")
    else:
        print("POST", "/monitor", json.dumps(monitor), sep="\t")
PY
	printf '%s' "$body" | call "$method" "$path" | python3 -c 'import json, sys; m = json.load(sys.stdin); print("monitor:", m.get("name"), m.get("id"), m.get("errors", ""))'
done
