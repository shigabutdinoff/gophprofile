#!/bin/sh
# Создаёт index pattern логов в OpenSearch Dashboards, делает его основным
# и заводит монитор, который поднимает алерт на записи уровня ERROR.
set -eu
dashboards=http://opensearch-dashboards:5601
opensearch=http://opensearch:9200
json='Content-Type: application/json'

until curl -fsS -o /dev/null -X POST "$dashboards/api/saved_objects/index-pattern/logs?overwrite=true" \
  -H 'osd-xsrf: true' -H "$json" \
  -d '{"attributes":{"title":"ss4o_logs-*","timeFieldName":"@timestamp"}}'; do
  sleep 5
done
curl -fsS -o /dev/null -X POST "$dashboards/api/opensearch-dashboards/settings" \
  -H 'osd-xsrf: true' -H "$json" -d '{"changes":{"defaultIndex":"logs"}}'

if curl -fsS "$opensearch/_plugins/_alerting/monitors/_search" -H "$json" \
  -d '{"query":{"match_phrase":{"monitor.name":"Ошибки в логах"}}}' | grep -q '"total":{"value":[1-9]'; then
  exit 0
fi
curl -fsS -o /dev/null -X POST "$opensearch/_plugins/_alerting/monitors" -H "$json" -d '{
  "type": "monitor",
  "monitor_type": "query_level_monitor",
  "name": "Ошибки в логах",
  "enabled": true,
  "schedule": {"period": {"interval": 1, "unit": "MINUTES"}},
  "inputs": [{"search": {"indices": ["ss4o_logs-*"], "query": {"size": 0, "query": {"bool": {"filter": [
    {"term": {"severity.text.keyword": "ERROR"}},
    {"range": {"@timestamp": {"gte": "{{period_end}}||-5m", "lte": "{{period_end}}", "format": "epoch_millis"}}}
  ]}}}}}],
  "triggers": [{
    "name": "Есть записи ERROR",
    "severity": "1",
    "condition": {"script": {"source": "ctx.results[0].hits.total.value > 0", "lang": "painless"}},
    "actions": []
  }]
}'
