#!/bin/bash
# Generates a mix of gateway traffic against the mock upstreams.
P=http://localhost:8090
chat() { curl -s -m 15 -o /dev/null -w "%{http_code} $1 $2\n" $P/v1/chat/completions -H 'content-type: application/json' -d "{\"model\":\"$1\",\"stream\":$2,\"messages\":[{\"role\":\"user\",\"content\":\"traffic $RANDOM\"}]}"; }
for i in $(seq 1 ${1:-1}); do
  chat mock-fast false; chat mock-fast true; chat lmstudio/mock-fast false
  chat mock-slow false; chat mock-error false; chat mock-ratelimit false
  chat flaky-1 false; chat flaky-1 true; chat flaky-2 false; chat no-such-model false
  curl -s -m 10 -o /dev/null -w "%{http_code} embed\n" $P/v1/embeddings -H 'content-type: application/json' -d '{"model":"mock-embed","input":"hi"}'
  curl -s -m 10 -o /dev/null -w "%{http_code} anthropic\n" $P/v1/messages -H 'content-type: application/json' -d '{"model":"mock-fast","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}'
done
