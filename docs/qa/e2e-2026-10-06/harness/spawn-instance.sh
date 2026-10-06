#!/bin/bash
# Usage: spawn-instance.sh <repo-or-worktree-dir> <proxy-port> <admin-port>
# Builds console + binary from the given checkout, starts a fresh instance with its own DB,
# claims it as $QA_USER / $QA_PASS (both required), adds the lmstudio + vllm mock runtimes, and sends a little traffic.
# Re-run to rebuild and restart (the DB is reset each time). Logs: <dir>/.qa-instance/server.log
set -e
: "${QA_USER:?set QA_USER}" "${QA_PASS:?set QA_PASS}"
DIR=$(cd "$1" && pwd); PP=$2; AP=$3
I=$DIR/.qa-instance/$AP; mkdir -p $I
# Stop whatever holds these ports (a previous run of this instance), and wait until they are free.
fuser -k -TERM $PP/tcp $AP/tcp >/dev/null 2>&1 || true
for i in $(seq 1 40); do fuser $PP/tcp $AP/tcp >/dev/null 2>&1 || break; sleep 0.25; done
rm -f $I/darkrouter.db*
[ -n "$SKIP_BUILD" ] || ( cd $DIR/web && [ -d node_modules ] || npm ci --no-audit --no-fund >/dev/null 2>&1; npm run build >/dev/null 2>&1 ) || { echo "web build failed"; (cd $DIR/web && npm run build 2>&1 | tail -20); exit 1; }
if [ -n "$SKIP_BUILD" ]; then cp $DIR/.qa-instance/darkrouter $I/darkrouter; else ( cd $DIR && go build -o $DIR/.qa-instance/darkrouter ./cmd/darkrouter ) && cp $DIR/.qa-instance/darkrouter $I/darkrouter; fi
cd $I && DARKROUTER_MASTER_KEY=$(openssl rand -base64 32) DARKROUTER_PROXY_LISTEN=:$PP DARKROUTER_ADMIN_LISTEN=:$AP DARKROUTER_DB=$I/darkrouter.db setsid nohup ./darkrouter > server.log 2>&1 < /dev/null &
echo $! > $I/pid
for i in $(seq 1 40); do curl -sf localhost:$AP/healthz >/dev/null && break; sleep 0.25; done
B=http://localhost:$AP; C=$I/cookies
curl -s -o /dev/null -H "Origin: $B" -H 'content-type: application/json' -d "{\"username\":\"$QA_USER\",\"password\":\"$QA_PASS\",\"confirm\":\"$QA_PASS\"}" $B/api/auth/setup
CSRF=$(curl -s -c $C -H "Origin: $B" -H 'content-type: application/json' -d "{\"username\":\"$QA_USER\",\"password\":\"$QA_PASS\"}" $B/api/auth/login | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')
for p in lmstudio vllm; do curl -s -o /dev/null -b $C -H "Origin: $B" -H "X-CSRF-Token: $CSRF" -H 'content-type: application/json' -d "{\"id\":\"$p\",\"preset\":\"$p\",\"enabled\":true}" $B/api/providers; done
for m in mock-fast mock-error flaky-1 mock-fast; do curl -s -o /dev/null localhost:$PP/v1/chat/completions -H 'content-type: application/json' -d "{\"model\":\"$m\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"; done
echo "instance up: admin $B  proxy http://localhost:$PP  (pid $(cat $I/pid))"
