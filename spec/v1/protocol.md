# Flows Plugin & Callback Protocol v1

## Stdio NDJSON Frames

Plugins communicate over `stdin` (requests from `flowsd`) and `stdout` (frames from the plugin). Diagnostic logs MUST go to `stderr`.

### Describe Handshake
Request:
```json
{"protocol":1,"request_id":"describe-1","kind":"describe"}
```
Response:
```json
{"protocol":1,"request_id":"describe-1","kind":"manifest","manifest":{"protocol":1,"id":"sample","version":"1.0.0","operations":[{"id":"sample.ping","description":"Ping"}]}}
```

### Invocation & Progress
Request:
```json
{"protocol":1,"request_id":"att-1:req-1","kind":"invoke","operation":"sample.ping","attempt_id":"att-1","run_id":"run-1","callback_socket":"/tmp/flows-.../flowsd.sock","input":{"target":"ok"}}
```
Progress frame:
```json
{"protocol":1,"request_id":"att-1:req-1","kind":"progress","percent":0.5,"message":"checking"}
```
Result frame:
```json
{"protocol":1,"request_id":"att-1:req-1","kind":"result","output":{"reply":"ok"}}
```
Watch observation frame:
```json
{"protocol":1,"request_id":"att-1:req-2","kind":"observation","observation":{"done":false,"cursor":"cur-2","data":{"pending":1}}}
```

## Scoped Callback API
- `POST /v1/attempts/{attempt_id}/notify`
- `POST /v1/attempts/{attempt_id}/questions`
- `POST /v1/attempts/{attempt_id}/artifacts`
- `POST /v1/runs/{run_id}/edits`
