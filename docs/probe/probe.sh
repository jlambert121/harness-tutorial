#!/usr/bin/env bash
# Probes Vercel AI Gateway Chat Completions behaviors the tutorial depends on.
# Usage: ./probe.sh [model]   (default anthropic/claude-haiku-5.5). Writes raw output to ./out/.
set -euo pipefail
MODEL="${1:-anthropic/claude-haiku-5.5}"
BASE="https://ai-gateway.vercel.sh/v1"
OUT="out/$(echo "$MODEL" | tr '/' '_')"
mkdir -p "$OUT"
post() { curl -sS -N "$BASE/chat/completions" -H "Authorization: Bearer $AI_GATEWAY_API_KEY" -H "Content-Type: application/json" -d "$1"; }

TOOLS='[{"type":"function","function":{"name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}]'

echo "== (a,b,c) streaming tool calls + include_usage + cost"
post "{\"model\":\"$MODEL\",\"stream\":true,\"stream_options\":{\"include_usage\":true},\"tools\":$TOOLS,
  \"messages\":[{\"role\":\"user\",\"content\":\"Read both a.txt and b.txt using the tool. Call the tool twice in one response.\"}]}" > "$OUT/stream_tools.sse"
grep -c '^data:' "$OUT/stream_tools.sse" | sed 's/^/chunks: /'
grep -o '"tool_calls":\[[^]]*\]' "$OUT/stream_tools.sse" | head -6
grep -o '"usage":{[^}]*}[^}]*}' "$OUT/stream_tools.sse" | tail -1
grep -o '"cost":"[^"]*"' "$OUT/stream_tools.sse" | tail -1 || echo "no cost field in stream"
grep -o '"finish_reason":"[^"]*"' "$OUT/stream_tools.sse" | sort | uniq -c

echo "== (d) caching: two identical ~3k token requests with caching:auto"
BIG=$(python3 -c "print(' '.join(['The quick brown fox jumps over the lazy dog number %d.'%i for i in range(400)]))")
BODY=$(python3 -c "
import json,sys
print(json.dumps({'model':sys.argv[1],'max_tokens':20,'providerOptions':{'gateway':{'caching':'auto'}},
 'messages':[{'role':'system','content':sys.argv[2]},{'role':'user','content':'Reply with OK.'}]}))" "$MODEL" "$BIG")
post "$BODY" > "$OUT/cache1.json"; sleep 2; post "$BODY" > "$OUT/cache2.json"
python3 -c "
import json
for f in ['cache1','cache2']:
  d=json.load(open('$OUT/'+f+'.json')); print(f, json.dumps(d.get('usage')))"

echo "== (e) reasoning_details round trip in a tool loop"
R1=$(post "{\"model\":\"$MODEL\",\"reasoning\":{\"effort\":\"medium\"},\"tools\":$TOOLS,\"messages\":[{\"role\":\"user\",\"content\":\"Use the tool to read a.txt, then tell me its contents.\"}]}")
echo "$R1" > "$OUT/reason1.json"
python3 - "$OUT" "$MODEL" "$TOOLS" <<'PY'
import json,sys,subprocess,os
out,model,tools=sys.argv[1],sys.argv[2],json.loads(sys.argv[3])
r=json.load(open(out+'/reason1.json')); m=r['choices'][0]['message']
print('reasoning present:', bool(m.get('reasoning')), 'reasoning_details:', [d.get('type') for d in m.get('reasoning_details') or []])
tc=m.get('tool_calls') or []
print('tool calls:', [t['function']['name'] for t in tc])
if not tc: sys.exit(0)
asst={'role':'assistant','content':m.get('content'),'tool_calls':tc}
if m.get('reasoning_details'): asst['reasoning_details']=m['reasoning_details']
msgs=[{'role':'user','content':'Use the tool to read a.txt, then tell me its contents.'},asst]+[{'role':'tool','tool_call_id':t['id'],'content':'hello from a.txt'} for t in tc]
body=json.dumps({'model':model,'reasoning':{'effort':'medium'},'tools':tools,'messages':msgs})
res=subprocess.run(['curl','-sS','https://ai-gateway.vercel.sh/v1/chat/completions','-H','Authorization: Bearer '+os.environ['AI_GATEWAY_API_KEY'],'-H','Content-Type: application/json','-d',body],capture_output=True,text=True).stdout
open(out+'/reason2.json','w').write(res)
d=json.loads(res)
print('follow-up:', 'error' if 'error' in d else repr(d['choices'][0]['message'].get('content'))[:120])
if 'error' in d: print(json.dumps(d['error'])[:400])
PY
