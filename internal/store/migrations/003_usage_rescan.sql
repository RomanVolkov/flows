DELETE FROM usage_offsets
WHERE source_path LIKE '%/.codex/%'
  AND last_input = 0
  AND last_cache_read = 0
  AND last_output = 0
  AND last_reasoning = 0;
